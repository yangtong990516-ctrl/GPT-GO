// signup_wiring.go 装配「纯协议注册引擎」（signup.Service）到 HTTP 服务。
//
// 这是注册链路的「点火开关」：把地基（core/httpcloak）、sentinel solver、协议状态机
// （authflow）、资源/代理轮转、拨号、OTP 取件组装成一个可调 RunBatch 的 Service，
// 并挂到 Server 上供 apiserver/runs 触发。
//
// 依赖来源（全部复用 server.go 已装配的 service，不重复造）：
//   - proxySvc    → ProxyStore（租约/归还/隔离）
//   - emailStore + accountSvc → ResourceStore（预留邮箱/落账号池）
//   - mailcodeSvc → mailFactory 兜底拼 accessUrl 的 baseURL
//   - buildSolver → sentinel.Solver（v8 真 V8；见 solver_v8.go / solver_nov8.go）
//
// 并发/内存（用户两个硬保证，见各层实现）：
//   - 每次 Run 由 NewFlowRunner 现场建独立 Flow（deviceID/csrf/sentinel 全在实例字段），
//     跑完 defer flow.Close() 释放 httpcloak 会话 —— 协程间零共享可变状态。
//   - mailbox 用 MailFactory 按预留邮箱现场造专属 provider（accessUrl 绑定该邮箱），
//     杜绝并发批量注册「共享 mailbox 串号」。
//   - 共享的只有 solver：V8Solver 自带 Isolate 池 + sync.Mutex + WaitGroup，并发安全。
package apiserver

import (
	"context"

	"go.uber.org/zap"

	"gpt-go/internal/service/accountsecurity"
	"gpt-go/internal/service/mfacheck"
	"gpt-go/internal/service/paymentcheck"
	"gpt-go/internal/service/plancheck"
	"gpt-go/internal/service/rebind"
	"gpt-go/internal/service/signup"
	"gpt-go/internal/service/signup/authflow"
	"gpt-go/internal/service/signup/otp"
	"gpt-go/internal/service/signup/sentinel"
	"gpt-go/internal/util"
)

// signupEngine 是装配好的注册引擎（Service + 它持有的可关闭资源）。
type signupEngine struct {
	// Svc 是可调 Run/RunBatch 的注册服务。
	Svc *signup.Service
	// solver 是共享的 Sentinel 求解器（并发安全）；服务关停时 Close 释放 V8 Isolate。
	solver sentinel.Solver
	// newDialer 按 settings 的 maxRegistrationsPerExitIp 现场建拨号器（每批一个干净
	// exitIPRegistry，对齐「批次内出口 IP 去重」语义）。见 runs 触发点。
	prober signup.EgressProber
}

// Close 释放引擎持有的资源（V8 Isolate 等）。服务关停时调用。
//
// Solver 接口本身无 Close（v8go 的 V8Solver 才有）；用接口断言探测，非 V8 solver
// （或 nil）安全跳过。
func (e *signupEngine) Close() {
	if e == nil || e.solver == nil {
		return
	}
	if c, ok := e.solver.(interface{ Close() error }); ok {
		if err := c.Close(); err != nil {
			util.Logger().Warn("关闭 V8 solver 失败", zap.Error(err))
		}
	}
}

// wireSignup 装配注册引擎并挂到 Server（在 NewServer 里调用）。
//
// 只做「一次性、与单次注册无关」的装配：Service/适配器/prober/solver。
// 与单次注册相关的（dialer 的 exitIPCap、batch 的 concurrency/timeout）在 runs 触发点
// 按 settings 实时注入（A 方案）——见 internal/apiserver/runs。
func (s *Server) wireSignup(ctx context.Context) {
	solver := buildSolver(ctx)
	prober := signup.NewCDNProber()
	engine := &signupEngine{solver: solver, prober: prober}

	// mailFactory：按预留邮箱现场造专属 OTP provider（防并发串号）。
	mailFactory := s.buildMailFactory()

	// 适配器桥接（复用已装配的 service）。
	proxyStore := signup.NewProxyServiceAdapter(s.proxySvc)
	resourceStore := signup.NewResourceAdapter(s.emailStore, s.accountSvc)

	// flowRunner：拨号结果 → core.Bootstrap → authflow.RunRegister（solver 注入，
	// challenge 与主线 TLS 同源，见 authflow/wire.go）。
	flowRunner := authflow.NewFlowRunner(solver)

	// 套餐检查+验活服务（合并一次请求，对齐 codex check_combined）。
	// 复用 accountStore（套餐/验活字段落库）+ proxyStore（租代理）。默认开启：
	// 注册成功落库后挂后台任务触发（对齐 _spawn_plan_check_async，不阻塞注册）。
	// 套餐检查活动日志（系统级流 "plancheck"）：检查过程写入 runlogs，SSE/历史可查。
	var plancheckLog *signup.RunLogger
	if s.logHub != nil {
		plancheckLog = s.logHub.Logger("plancheck")
	}
	planChecker := plancheck.New(s.accountStore, proxyStore, plancheck.WithRunLogger(plancheckLog))
	// 存到 server,供 /api/accounts/check-combined 批量「验活+查优惠」复用同一实例。
	s.planChecker = planChecker

	// 批量 2FA 状态查询服务(/api/accounts/check-2fa,迁移自 codex check_2fa_status):
	// 复用 accountStore(totpStatus 落库)+ proxyStore(租代理);无需重认证,只读 /me。
	var mfacheckLog *signup.RunLogger
	if s.logHub != nil {
		mfacheckLog = s.logHub.Logger("mfacheck")
	}
	s.mfaChecker = mfacheck.New(s.accountStore, proxyStore, mfacheck.WithRunLogger(mfacheckLog))

	// 补 2FA(开通 TOTP)服务(/api/accounts/{id}/ensure-2fa + /bulk-ensure-2fa):
	// 复用 accountStore(StoreTotp 落库)+ proxyStore(租代理)+ solver(登录 sentinel 步骤必需)。
	// 对齐 codex ensure_totp 有密码分支:协议登录拿 recent_auth AT → enroll+activate → 落库。
	s.security = accountsecurity.New(s.accountStore, proxyStore, solver)

	// 支付类型检测服务（独立功能：/api/payment-check）。
	// 复用 accountStore（支付字段落库）+ proxyStore（租代理）+ solver（Sentinel best-effort）。
	// 活动日志流 "paymentcheck"：检测过程写入 runlogs，SSE/历史可查。
	var paymentcheckLog *signup.RunLogger
	if s.logHub != nil {
		paymentcheckLog = s.logHub.Logger("paymentcheck")
	}
	var paymentSolver paymentcheck.SentinelSolver
	if solver != nil {
		paymentSolver = solver
	}
	s.paymentChecker = paymentcheck.New(s.accountStore, proxyStore,
		paymentcheck.WithRunLogger(paymentcheckLog),
		paymentcheck.WithSolver(paymentSolver),
	)

	// 换绑服务（邮箱换绑）：复用 accountStore（换绑字段落库）+ emailStore（预留邮箱）
	// + proxyStore（租代理）+ authflow（登录链路）+ logHub 流 "rebind"。
	var rebindLog *signup.RunLogger
	if s.logHub != nil {
		rebindLog = s.logHub.Logger("rebind")
	}
	s.rebinder = rebind.New(s.accountStore, s.emailStore, proxyStore,
		rebind.WithRunLogger(rebindLog),
		rebind.WithLogHub(s.logHub),
	)
	postRegister := func(ctx context.Context, accountID, email string) {
		res, err := planChecker.CheckCombined(ctx, []string{accountID}, "")
		if err != nil {
			util.Logger().Warn("注册后套餐/验活检查失败", zap.String("email", email), zap.Error(err))
		} else if len(res.Items) > 0 {
			it := res.Items[0]
			util.Logger().Info("注册后套餐/验活完成",
				zap.String("email", email),
				zap.String("alive", it.Status),
				zap.String("plan", it.PlanStatus),
				zap.String("errCode", it.ErrorCode))
		}

		// 密码+2FA 合一开关开启时:注册成功后后台补绑 2FA(重认证+enroll+activate,
		// 消耗一封邮箱 OTP)。开关关闭则完全跳过(不生成密码、不碰 2FA)。
		if s.settingsSvc != nil {
			if st, serr := s.settingsSvc.Get(ctx); serr == nil && st.EnableRegistrationSecurity && s.security != nil {
				secRes := s.security.EnsureOne(ctx, accountID, "", s.buildEnsure2FAOTPFactory())
				if secRes.Status == "success" {
					util.Logger().Info("注册后自动绑 2FA 完成", zap.String("email", email))
				} else {
					util.Logger().Warn("注册后自动绑 2FA 失败", zap.String("email", email), zap.String("err", secRes.Error))
				}
			}
		}
	}

	engine.Svc = signup.NewService(
		proxyStore,
		resourceStore,
		mailFactory,
		signup.WithSolver(solver),
		signup.WithResourceAdapter(signup.NewResourceAdapter(s.emailStore, s.accountSvc)),
		signup.WithFlowRunner(flowRunner),
		signup.WithPostRegister(postRegister),
	)
	s.signup = engine
	util.Logger().Info("注册引擎已装配", zap.Bool("solverReady", solver != nil))
}

// buildMailFactory 返回 MailFactory：按预留邮箱的 AccessURL + EmailSource 调 otp.New
// 构造专属 provider。
//
// 取件入口优先级（对齐 otp/mailcode.go 的工厂）：
//  1. 邮箱池记录自带的 AccessURL（最准，各源 service 导入时已生成并绑定该邮箱）；
//  2. 兜底：mailcode 配置的 baseURL + email 现拼（记录缺 accessUrl 时，仅 mailcode 适用）。
//
// OTP provider 选择（按邮箱池 SourceType 映射，见 otpKindForSource）：
//   - remail → jsoncode：accessUrl 指向 /v1/pickup，返回 JSON 含 verificationCode/code
//     字段，取字段即用（不做邮件正则）；
//   - mailcode / mailcom_alias / standard 等 → mailcode：accessUrl 返回邮件原文
//     （HTML/文本），用 extract.go 五步防误判正则提码。
//   - cloudmail 已移除（用户决策：去掉 Cloudflare 相关邮件）。
func (s *Server) buildMailFactory() signup.MailFactory {
	return func(ctx context.Context, email signup.ReservedEmail) (signup.MailProvider, error) {
		kind := otpKindForSource(email.Source)
		// settings：给 mailcode 兜底拼 accessUrl 用的 baseURL（jsoncode 用不到，但传了无害）。
		settings := map[string]any{}
		if s.mailcodeSvc != nil {
			if cfg, err := s.mailcodeSvc.GetConfig(ctx); err == nil && cfg.BaseURL != "" {
				settings["baseURL"] = cfg.BaseURL
			}
		}
		account := map[string]any{
			"email":     email.Email,
			"accessUrl": email.AccessURL, // 邮箱池记录自带（mailcode/jsoncode 工厂都优先用它）
		}
		return otp.New(ctx, kind, settings, account)
	}
}

// otpKindForSource 把邮箱池 SourceType 映射到 otp 工厂注册的 provider kind。
//
// 邮箱池 SourceType（store.EmailDocument.SourceType）与 otp kind 是两套标识：
//
//	邮箱池: mailcode / remail / mailcom_alias / standard（manual）(cloudmail 已按决策移除)
//	otp  : mailcode（邮件原文正则提码）/ jsoncode（接码平台取 verificationCode 字段）
//
// 映射规则（按 accessUrl 返回的内容形态归类）：
//   - remail        → jsoncode（/v1/pickup 返回 JSON，含 verificationCode/code）
//   - 其余（含空）   → mailcode（邮件原文，走 extract.go 正则）
//
// 注意：otp 的 kind 常量未导出（包内小写），这里用字面量与 otp 注册保持一致
// （otp/mailcode.go 注册 "mailcode"、otp/jsoncode.go 注册 "jsoncode"）。
func otpKindForSource(sourceType string) string {
	if sourceType == "remail" {
		return "jsoncode"
	}
	return "mailcode"
}
