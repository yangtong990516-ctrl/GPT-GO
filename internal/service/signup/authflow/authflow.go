// Package authflow 实现注册/登录协议状态机，对齐 codex-auto _runtime/auth_flow.py。
//
// AuthFlow 是"注册一个 ChatGPT 账号"的 10 步流程：
//
//	check_proxy → warmup(oai-did) → csrf → signin(auth_url) → oauth_init(device_id)
//	→ sentinel(PoW) → authorize/continue(探测+signup) → user/register(设密码)
//	→ email-otp/send → 邮箱取码 → email-otp/validate → create_account
//	→ callback → auth/session → oauth/token
//
// 架构：
//   - 地基用 core.Bootstrap（Identity 三路自洽 + Session httpcloak + GeoIP 时区）。
//   - sentinel 步骤通过 sentinel.Solver 接口调用（P4 那条线实现），本包只负责
//     从 core.Identity.SentinelEnv 取三路自洽画像喂给它 + 用 core.Session 当
//     ChallengeFetcher 发 /sentinel/req。本包不感知 v8go 的实现细节。
//
// 红线（codex-auto 血泪教训，本包严格遵守）：
//  1. device_id 三处同源：oai-did cookie == oai-device-id 头 == SentinelEnv.DeviceID。
//  2. 重定向链导航用 GetNavigationFollowRedirect（去掉 sec-fetch-user）。
//  3. 中后段（已种 oai-did/csrf）TLS 瞬断只原 session 重试，绝不重建（会丢 cookie→409）。
//  4. sentinel so_token 只在「本 flow 服务端要求」时带，不拿别的 flow 的 SO 凑数。
package authflow

import (
	"context"
	"fmt"

	"gpt-go/internal/service/signup"
	"gpt-go/internal/service/signup/core"
	"gpt-go/internal/service/signup/otp"
	"gpt-go/internal/service/signup/sentinel"
)

// Flow 是注册协议状态机，对齐 Python AuthFlow。
type Flow struct {
	config *signup.Config
	boot   *core.Bootstrap // 地基：Identity + Session(httpcloak) + GeoIP

	// sentinel 求解器（P4 注入；为 nil 时 sentinel 步骤返回明确错误）。
	solver sentinel.Solver

	result *signup.AuthResult

	// 回调（对齐 Python AuthFlow 的构造回调）
	onPassword     func(email, password string)         // 密码生效即回调，调用方负责落盘
	onSessionReady func(flow *Flow, accessToken string) // 拿到 session 后、Codex 授权前的钩子

	// ── 状态（对齐 Python AuthFlow 的实例字段）──
	deviceID    string // oai-did（warmup/oauth_init 种）
	csrfToken   string
	countryCode string
	exitIP      string
	authURL     string // signin 拿到的 authorize 地址

	// sentinel token 缓存（对齐 Python _last_sentinel_token / _last_sentinel_so_token）
	lastSentinelToken   string
	lastSentinelSoToken string

	// 分支状态（对齐 Python signup 的探测结果）
	isExistingAccount        bool
	existingVerificationMode string // passwordless_signup / ...（OTP 分支用）
	existingPageType         string
	isExistingAccountSet     bool
	accountCreated           bool // create_account 200 时置 true（补登录判断用）

	// codexRT 是第 13 步 Codex refresh_token 换取的选项（默认对齐 Python；见 codex_rt.go）。
	codexRT CodexRTOptions
}

// Option 是 Flow 的构造选项。
type Option func(*Flow)

// WithOnPassword 设置密码回调（对齐 Python on_password）。
func WithOnPassword(cb func(email, password string)) Option {
	return func(f *Flow) { f.onPassword = cb }
}

// WithOnSessionReady 设置 session 就绪回调（对齐 Python on_session_ready）。
func WithOnSessionReady(cb func(*Flow, string)) Option {
	return func(f *Flow) { f.onSessionReady = cb }
}

// WithSolver 注入 Sentinel 求解器（P4 那条线的实现）。
func WithSolver(s sentinel.Solver) Option {
	return func(f *Flow) { f.solver = s }
}

// New 创建注册状态机：装配 core 地基（三路自洽 + httpcloak 会话 + GeoIP 时区）。
//
// 代理与国家/时区的来源（重要，别搞反）：
//   - 代理：cfg.Proxy 是拨号器产出的【临时代理 URL】（session 已随机替换）。
//   - 国家/时区：【不从 Config 拿】（Config 只有 Proxy）。它们在 service 层拨号后
//     由实测出口 IP 决定——用 WithBootstrap 直接注入已绑定 GeoIP 的 Bootstrap，
//     或在这里用 EgressIP 触发现场 GeoIP 查询（国家动态实测，不锁死）。
//
// 推荐用法：service 层拨号拿到 DialResult{ProxyURL, EgressIP, Country} 后，
// 调 NewWithDial(cfg, dial, opts...) 一步到位（见下）。
func New(cfg *signup.Config, opts ...Option) (*Flow, error) {
	if cfg == nil {
		return nil, fmt.Errorf("authflow: config 不能为空")
	}
	return NewWithEgress(cfg, "", opts...)
}

// NewWithEgress 用已知出口 IP 创建（service 层拨号后调用）。
// egressIP 非空时，core 会立即用 GeoIP 解出真实 CountryCode+TimezoneID（国家动态实测）。
func NewWithEgress(cfg *signup.Config, egressIP string, opts ...Option) (*Flow, error) {
	if cfg == nil {
		return nil, fmt.Errorf("authflow: config 不能为空")
	}
	boot, err := core.NewBootstrap(core.BootstrapOptions{
		Proxy:    cfg.Proxy,
		EgressIP: egressIP, // 实测出口 IP → GeoIP 解国家+时区（不锁死）
	})
	if err != nil {
		return nil, fmt.Errorf("authflow: 装配地基失败: %w", err)
	}

	f := &Flow{
		config:  cfg,
		boot:    boot,
		result:  &signup.AuthResult{},
		codexRT: defaultCodexRTOptions(), // 默认对齐 Python；WithCodexRT 可覆盖
	}
	for _, opt := range opts {
		opt(f)
	}
	return f, nil
}

// Result 返回当前认证结果。
func (f *Flow) Result() *signup.AuthResult { return f.result }

// Boot 暴露地基（供 service 层在补登录/落库时读 Identity/Geo）。
func (f *Flow) Boot() *core.Bootstrap { return f.boot }

// Close 释放会话连接。
func (f *Flow) Close() {
	if f.boot != nil && f.boot.Session != nil {
		f.boot.Session.Close()
	}
}

// RunRegister 执行完整注册流程，对齐 Python run_register。
//
// 主干（新账号路径；已有账号/号池 fast-fail 的分支由 service 层按 isNew/mode 决策）：
//
//	check_proxy → warmup(oai-did) → csrf → signin(auth_url) → oauth_init(device_id)
//	→ sentinel(authorize_continue) → signup(探测+提交) → [新账号] register_password
//	→ send_otp → wait OTP → verify_otp → create_account → redirect_chain
//	→ consume_callback → auth_session → oauth(Codex RT, 可选)
//
// mail 用于取 OTP 验证码；password 由调用方（service 层）生成传入（便于落盘回调前确定）。
// 返回 *signup.AuthResult（含 email/password/session_token/access_token/device_id/exit_ip）。
func (f *Flow) RunRegister(ctx context.Context, email, password string, mail otp.Provider, otpTimeoutSeconds int) (*signup.AuthResult, error) {
	f.result.Email = email

	// 0) 网络预检（出口 IP 已在拨号阶段实测绑进 Bootstrap，这里仅复确认）。
	f.checkProxy(ctx)

	// 1) warmup 拿 oai-did【硬门槛：失败=后面必 409，直接返回让上层换 IP/换通道】。
	if !f.warmup(ctx) {
		return nil, signup.NewRegistrationError("warmup_failed", email,
			fmt.Errorf("warmup 4 次均未种到 oai-did（继续必 409 invalid_state，多为出口 IP 不通或被 CF 拦）"))
	}

	// 2) csrf
	csrf, err := f.getCSRFToken(ctx)
	if err != nil {
		return nil, signup.NewRegistrationError("csrf_failed", email, err)
	}

	// 3) signin 拿 authorize 地址
	authURL, err := f.getAuthURL(ctx, csrf, email)
	if err != nil {
		return nil, signup.NewRegistrationError("signin_failed", email, err)
	}

	// 4) oauth_init 建 state + 种 oai-did（device_id 三处同源源头）
	deviceID, err := f.authOAuthInit(ctx, authURL)
	if err != nil {
		return nil, signup.NewRegistrationError("oauth_init_failed", email, err)
	}

	// 5) sentinel 解 PoW（flow=authorize_continue）
	sentinelToken, err := f.getSentinelToken(ctx, deviceID, "authorize_continue")
	if err != nil {
		return nil, signup.NewRegistrationError("sentinel_failed", email, err)
	}

	// 6) signup 提交邮箱（探测 + 提交），判三分支
	isNew, err := f.signup(ctx, email, sentinelToken)
	if err != nil {
		return nil, signup.NewRegistrationError("signup_failed", email, err)
	}

	// 已有账号（非 passwordless_signup）→ 交给 service 层决策（fast-fail 换号 / OTP 登录）。
	// 本方法只实现「新账号」全链路；已有账号返回明确错误码让上层走对应分支。
	if f.isExistingAccount && f.existingVerificationMode != "passwordless_signup" {
		return nil, signup.NewRegistrationError("existing_account", email,
			fmt.Errorf("OpenAI 识别为已有账号（page_type=%s）", f.existingPageType))
	}

	var continueURL string

	// 新账号路径：标准新注册(isNew=true) 或 passwordless 新注册(isNew=false 但非已有账号)。
	// 对齐 Python: if is_new or mode=="passwordless_signup" —— 两者都要走 OTP 验证 + create_account。
	// 【审计修正 P1】原条件只写 if isNew,会把 passwordless 新号整个跳过(不验证 OTP、不建号)。
	isNewAccountFlow := isNew || f.existingVerificationMode == "passwordless_signup"
	isPasswordless := f.existingVerificationMode == "passwordless_signup"

	if isNewAccountFlow {
		// 7) 设密码（passwordless 跳过：服务端对此 state 必 400，跳过无损失）
		if !isPasswordless {
			if _, err := f.registerPassword(ctx, email, password); err != nil {
				return nil, signup.NewRegistrationError("register_password_failed", email, err)
			}
		}
		// 8) 发码（对齐 Python kickoff_otp_delivery，防 state 破坏）：
		//    passwordless：signup 已在服务端触发首封 → kickoff 只 resend（不 send，否则旧码失效）。
		//    新密码分支：register 成功后服务端切流程，原 OTP 失效 → 必须重发（kickoff 走 send/resend）。
		//    发码时间戳在发码【之前】取（否则晚于邮件到达时间，被 issuedAfter 过滤而收不到）。
		otpSentAt := nowUnix()
		if isPasswordless {
			// passwordless：等服务端已发的首封（不主动发，撞频控会 silent-drop）。
			// 窗口短：首封 3-30s 内到，60s 没到基本被 drop，早 resend 换新码更划算。
			f.kickoffOTPDelivery(ctx, "passwordless_signup")
		} else {
			if err := f.sendOTP(ctx, "https://auth.AI Platform.com/create-account/password"); err != nil {
				// send 失败回退 kickoff（resend 兜底），不直接判死。
				if !f.kickoffOTPDelivery(ctx, "new_register") {
					return nil, signup.NewRegistrationError("send_otp_failed", email, err)
				}
			}
		}
		// 9) 取码（先 peek 命服务端抢跑发的码，再 wait；传 otpSentAt 防串号）+ 校验
		if otpTimeoutSeconds <= 0 {
			otpTimeoutSeconds = 60
		}
		code, err := f.fetchOTPWithPeek(ctx, mail, email, otpTimeoutSeconds, otpSentAt)
		if err != nil {
			return nil, signup.NewRegistrationError("otp_fetch_failed", email, err)
		}
		if _, err := f.verifyOTP(ctx, code); err != nil {
			return nil, signup.NewRegistrationError("otp_verify_failed", email, err)
		}
		// 10) create_account
		continueURL, err = f.createAccount(ctx)
		if err != nil {
			return nil, signup.NewRegistrationError("create_account_failed", email, err)
		}
	}

	// 11) 重定向链 → 捕获 callback（不消费 code）
	if continueURL != "" {
		callbackURL, _ := f.followRedirectChain(ctx, continueURL)
		// 12) 先消费 callback 让 NextAuth 种 session-token，再拿 session（顺序是硬约束）。
		if callbackURL != "" {
			f.consumeCallbackForSession(ctx, callbackURL)
		}
	}
	if err := f.getAuthSession(ctx); err != nil {
		return nil, signup.NewRegistrationError("auth_session_failed", email, err)
	}

	// 13) Codex refresh_token（独立 authorize 链）。【用户决策：只试一次，拿不到就算了】
	//     - 拿不到 RT 是大概率事件，且【失败绝不阻断注册】：oauthCodexRTExchange 只返回
	//       bool，失败不进入 error 分支，主链路继续走 IsValid 校验（只要 session_token+
	//       access_token，不要求 RT）。
	//     - 放在 getAuthSession 之后：此链用独立 client_id/redirect_uri，与 chatgpt.com
	//       的 NextAuth callback 不抢 code（顺序安全）。拿到则 result.RefreshToken 落库。
	if f.codexRT.Enabled {
		if f.oauthCodexRTExchange(ctx, mail) {
			// 拿到 RT（result.RefreshToken 已写）；最终再拉一次 session 同步 access_token。
			_ = f.getAuthSession(ctx)
		}
	}

	// 结果校验：session_token + access_token 都要有。
	if !f.result.IsValid() {
		return nil, signup.NewRegistrationError("invalid_result", email,
			fmt.Errorf("注册完成但未获取有效凭证（session_token/access_token 缺失）"))
	}
	f.result.ExitIP = f.exitIP
	return f.result, nil
}
