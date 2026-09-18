package signup

import (
	"context"
	crand "crypto/rand"
	"time"

	"gpt-go/internal/service/signup/sentinel"
)

// Service 是纯协议注册服务，对齐 Python service.py 的 run_protocol_registration。
type Service struct {
	proxy       ProxyStore
	res         ResourceStore
	mailFactory MailFactory      // 按预留邮箱现场造专属 OTP provider（防并发串号）
	solver      sentinel.Solver  // sentinel PoW 求解器（P4 注入）
	dialer      *SessionDialer   // 会话型拨号器（模型 B：通道→新出口 IP）
	adapter     *ResourceAdapter // 成功落库（account.Create + 邮箱 consume）
	flowRunner  FlowRunner       // 协议注册执行器（装配层注入，解耦 authflow）
	// postRegister 注册成功落库后的后台钩子（套餐检查+验活，对齐 _spawn_plan_check_async）。
	postRegister func(ctx context.Context, accountID, email string)
}

// ServiceOption 是 Service 的构造选项。
type ServiceOption func(*Service)

// WithSolver 注入 Sentinel 求解器（P4 那条线）。
func WithSolver(sv sentinel.Solver) ServiceOption {
	return func(s *Service) { s.solver = sv }
}

// WithDialer 注入会话型拨号器（模型 B）。不传则注册时退回静态租约直连。
func WithDialer(d *SessionDialer) ServiceOption {
	return func(s *Service) { s.dialer = d }
}

// WithResourceAdapter 注入落库适配器（成功时 account.Create + 邮箱 consume）。
func WithResourceAdapter(a *ResourceAdapter) ServiceOption {
	return func(s *Service) { s.adapter = a }
}

// WithPostRegister 注入「注册成功落库后」的后台钩子（对齐 codex-auto
// _spawn_plan_check_async：注册流程不再等待套餐检查/验活，改为后台任务）。
//
// 钩子在后台 goroutine 执行，【不阻塞注册返回】。装配层注入「套餐检查+验活」
// （plancheck.CheckCombined）。传 nil 则注册后不做后台检查。
//
// ctx 语义：钩子收到的是【与注册请求脱钩的后台 ctx】（不因注册请求结束而取消），
// 由装配层提供（挂服务生命周期）。
func WithPostRegister(hook func(ctx context.Context, accountID, email string)) ServiceOption {
	return func(s *Service) { s.postRegister = hook }
}

// NewService 创建注册服务。
//
// mailFactory 按预留邮箱现场构造专属 OTP provider（对齐 codex-auto 每 reserved_email
// 一个 MailBridgeProvider）。批量并发注册时，各协程用绑定自己邮箱 accessUrl 的
// provider，互不串号。传 nil 则注册时在取 OTP 步骤报「未配置邮箱工厂」错误。
func NewService(proxy ProxyStore, res ResourceStore, mailFactory MailFactory, opts ...ServiceOption) *Service {
	s := &Service{
		proxy:       proxy,
		res:         res,
		mailFactory: mailFactory,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// RunParams 是单次注册的参数，对齐 Python run_protocol_registration 的关键字参数。
//
// 配置贯穿（来自 ExecutionSettings，由装配层在构造 BatchParams/RunParams 时填入）：
//   - TaskTimeoutSeconds：单账号注册整体超时（>0 时派生 ctx deadline，超时取消）；
//   - MaxRegistrationsPerExitIP：同出口 IP 并发上限（拨号器 exitIPCap；见 dialer.go）。
//
// 这两个字段是 settings 的「执行参数」在注册链路的落点（对齐 codex-auto 的贯穿）。
type RunParams struct {
	Country            string
	Group              string
	EmailSource        string
	RunID              string
	AllowExistingLogin bool
	LeaseSeconds       int
	OTPTimeout         int
	Step               StepLogger

	// Log 可选：本批共享的运行日志写入端（runs 触发点按 runID 取一个传入）。
	// 关键节点（租代理/预留邮箱/拨号/落库/成败）实时写入，供 SSE 推送 + 历史查询。
	// 接口类型（RunEventLogger）：可直接传 *RunLogger，也可传 *RunLogAggregator
	// （大批次下聚合高频过程日志，防刷屏）。nil 时仅走 Step 回调（无结构化事件）。
	Log RunEventLogger

	// TaskTimeoutSeconds 单账号注册整体超时（秒）。>0 时本次 Run 派生带 deadline 的
	// ctx，超时则取消（对齐 ExecutionSettings.taskTimeoutSeconds；0 = 不限）。
	TaskTimeoutSeconds int

	// EnableRegistrationSecurity 密码+2FA 合一开关（对齐 ExecutionSettings.enableRegistrationSecurity）。
	// false(默认):纯 OTP 注册——不生成密码、不设密码、不绑 2FA、落库不含 password/totp。
	// true:注册成功后尝试设密码(若服务端走密码分支)并自动绑 2FA(TOTP),落库含两者。
	EnableRegistrationSecurity bool

	// Dialer 可选：本批共享的会话型拨号器（按 settings.maxRegistrationsPerExitIp
	// 构造 exitIPCap）。A 方案下由 runs 触发点按 settings 现场 new 一个传入——
	// 每批一个干净的 exitIPRegistry（对齐「批次内出口 IP 去重」），且并发安全
	// （各协程只读它，不写）。为 nil 时退回 Service 构造时的 s.dialer。
	Dialer *SessionDialer
}

// Run 执行一次纯协议注册并落库，对齐 Python run_protocol_registration。
//
// 5 步流程：
//
//  1. 租代理（耗尽兜底 + 注册前纯净度复检 + 脏代理换下一个）
//  2. 预留邮箱
//  3. 跑 AuthFlow（注册状态机）
//  4. 落库（含可选套餐检查）
//  5. 清理（代理归还/隔离、邮箱释放/丢弃）
//
// 骨架阶段：编排结构已就绪，AuthFlow 具体实现未接入（返回未实现）。
func (s *Service) Run(ctx context.Context, params RunParams) (*RegistrationResult, error) {
	step := params.Step
	if step == nil {
		step = func(string) {}
	}

	// 0) 单账号整体超时（settings.taskTimeoutSeconds）：>0 派生带 deadline 的 ctx。
	//    超时则取消整个注册（拨号/warmup/OTP 等待全部受影响），并对齐 Python 的
	//    「任务级超时」语义；0 = 不限（默认）。cancel 必须调用以防 ctx 泄漏（契约 6.5.6）。
	if params.TaskTimeoutSeconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(params.TaskTimeoutSeconds)*time.Second)
		defer cancel()
	}

	// log 为 nil 接口时所有写入跳过(仅 step 文本)。注意:接口持有 typed-nil 指针时
	// 仍安全(RunLogger.Emit / Aggregator.Emit 都有 nil 接收者保护),故仅判 nil 接口。
	log := params.Log
	if log == nil {
		log = noopEventLogger{} // 空实现:省去后续每处 Emit 的 nil 判断
	}

	// 1) 租代理
	step("步骤1/5 租用代理 ...")
	lease, err := s.acquireProxy(ctx, params)
	if err != nil {
		log.Emit(LogError, "proxy_acquire_failed", "租用代理失败: "+err.Error(), "", nil)
		return nil, err
	}
	log.Emit(LogInfo, "proxy_acquired", "租用代理成功", "", map[string]any{
		"scheme": lease.Scheme, "country": lease.Country, "group": lease.Group,
	})
	// 归还租约【唯一归还点】：defer 兜底所有 return 路径（成功/失败/中途退出统一在此
	// 归还，owner=RunID 与 acquire 配对——对齐 codex release_proxy_owner 的配对语义）。
	// cleanupOnSuccess/cleanupOnFailure 不再重复归还代理（避免双重释放/计数错）。
	defer s.safeReturnProxy(ctx, lease, params.RunID)

	// 2) 预留邮箱
	step("步骤2/5 预留邮箱 ...")
	reserved, err := s.reserveEmail(ctx, params)
	if err != nil {
		log.Emit(LogError, "email_reserve_failed", "预留邮箱失败: "+err.Error(), "", nil)
		return nil, err
	}
	log.Emit(LogInfo, "email_reserved", "预留邮箱成功", reserved.Email, map[string]any{
		"source": reserved.Source,
	})

	// 3) 拨号（模型 B：通道 → 新出口 IP）+ 跑 AuthFlow
	step("步骤3/5 拨号 + 协议注册状态机 ...")
	if s.flowRunner == nil {
		return nil, NewRegistrationError("protocol_failed", reservedEmailOf(reserved), errNoFlowRunner())
	}

	// 3a) 拨号：租到的通道现场拨出一个「目标国家 + 本批次未用过」的出口 IP。
	//     国家动态实测（不锁死）；无拨号器时退回静态租约直连。
	dial, err := s.dialForRun(ctx, lease, params.Country, params)
	if err != nil {
		log.Emit(LogError, "dial_failed", "拨号失败: "+err.Error(), reserved.Email, nil)
		return nil, NewRegistrationError("no_eligible_proxy", reservedEmailOf(reserved), err)
	}
	step("拨号成功: 出口IP=" + dial.EgressIP + " 国家=" + dial.Country)
	log.Emit(LogInfo, "dial_succeeded", "拨号成功 出口IP="+dial.EgressIP+" 国家="+dial.Country, reserved.Email, map[string]any{
		"country": dial.Country, "timezone": dial.TimezoneID,
	})
	// 注册结束（无论成败）释放出口 IP 的并发额度（对齐 codex-auto _release_exit_ip）。
	// 即时回收额度，让后续账号可复用该 IP（cap>1 时），不必等 TTL 自然过期。
	// 用与拨号同一个 dialer（params.Dialer 优先，否则 s.dialer），保证 claim/release 配对。
	if d := params.Dialer; d != nil {
		defer d.ReleaseEgress(dial.EgressIP)
	} else if s.dialer != nil {
		defer s.dialer.ReleaseEgress(dial.EgressIP)
	}

	// 3b) 按预留邮箱现场造专属 OTP provider（防并发串号，对齐 codex-auto MailBridgeProvider）。
	//     每个协程绑定自己邮箱的 accessUrl；并发批量注册时互不干扰。
	if s.mailFactory == nil {
		return nil, NewRegistrationError("mailbox_unconfigured", reserved.Email, errNoMailFactory())
	}
	mailbox, err := s.mailFactory(ctx, *reserved)
	if err != nil {
		return nil, NewRegistrationError("mailbox_unconfigured", reserved.Email, err)
	}

	// 3c) 跑 AuthFlow（密码由 service 层生成，便于落盘回调前确定）。
	log.Emit(LogInfo, "protocol_started", "开始协议注册状态机", reserved.Email, nil)
	password := ""
	if params.EnableRegistrationSecurity {
		password = generatePassword()
	}
	authRes, err := s.flowRunner(ctx, FlowRequest{
		ProxyURL:   dial.ProxyURL,
		EgressIP:   dial.EgressIP,
		Email:      reserved.Email,
		Password:   password,
		Mail:       mailbox,
		OTPTimeout: params.OTPTimeout,
		Solver:     s.solver,
		OnPassword: nil, // 落盘在 PersistAccount 统一做
	})
	if err != nil {
		// 失败清理：按错误码分流邮箱（可复用归还 / 不可复用标记）；代理租约由
		// defer safeReturnProxy 统一归还。
		log.Emit(LogError, "protocol_failed", "协议注册失败: "+err.Error(), reserved.Email, map[string]any{
			"code": string(ErrorCode(err)),
		})
		s.cleanupOnFailure(ctx, reserved, err)
		return nil, err
	}

	// 3d) auth_session 结果落日志:是否拿到 AT / session_cookie / cookie_header / 密码。
	//     让前端实时看到「auth/session 是否取到 AT」「passwordless 还是设密码」。
	log.Emit(LogInfo, "auth_session_ok", "auth/session 取凭证完成", reserved.Email, map[string]any{
		"accessToken":  authRes.AccessToken != "",
		"sessionToken": authRes.SessionToken != "",
		"cookieHeader": authRes.CookieHeader != "",
		"password":     authRes.Password != "",
		"refreshToken": authRes.RefreshToken != "",
		"country":      dial.Country,
		"exitIP":       dial.EgressIP,
	})

	// 4) 落库（成功 → account.Create + 邮箱 consume）
	step("步骤4/5 落库 ...")
	accountID, perr := s.persistOnSuccess(ctx, authRes, reserved, dial)
	if perr != nil {
		log.Emit(LogError, "persist_failed", "落库失败: "+perr.Error(), reserved.Email, nil)
		return nil, NewRegistrationError("persist_failed", reserved.Email, perr)
	}
	log.Emit(LogSuccess, "account_persisted", "账号落库成功", reserved.Email, map[string]any{
		"accountId": accountID, "country": dial.Country,
	})

	// 5) 清理：邮箱已 consume；代理租约由 defer safeReturnProxy 统一归还（唯一归还点）。
	step("步骤5/5 清理 ...")

	// 6) 注册成功落库后：后台触发套餐检查+验活（对齐 _spawn_plan_check_async）。
	//    后台 goroutine 执行，不阻塞注册返回；ctx 与注册请求脱钩（注册请求结束后
	//    后台任务仍能跑完），由装配层提供挂服务生命周期的 ctx。
	if s.postRegister != nil {
		bg := s.bgCtx(ctx)
		accountID, email := accountID, reserved.Email
		go func() { s.postRegister(bg, accountID, email) }()
	}

	step("注册成功: " + reserved.Email)
	log.Emit(LogSuccess, "account_succeeded", "注册成功", reserved.Email, map[string]any{
		"accountId": accountID, "country": dial.Country,
	})
	return &RegistrationResult{
		OK:           true,
		AccountID:    accountID,
		Email:        reserved.Email,
		Password:     password,
		AccessToken:  authRes.AccessToken,
		SessionToken: authRes.SessionToken,
		DeviceID:     authRes.DeviceID,
		ProxyCountry: dial.Country,
	}, nil
}

func (s *Service) acquireProxy(ctx context.Context, params RunParams) (*ProxyLease, error) {
	if s.proxy == nil {
		return nil, NewRegistrationError("no_eligible_proxy", "", errNoProxyStore())
	}
	// 租约互斥由 proxy store 保证（leaseOwner + leaseUntil）。国家只是粗筛提示
	//（会话型通道的真实国家每次拨号实测，见 dialForRun）。
	// excluded 传 nil：同批次不重复靠「出口 IP 去重注册表」（拨号器内），而非代理条目级
	//（同一条通道能拨出不同 IP，不该被条目级排除）。耗尽兜底见下。
	lease, err := s.proxy.AcquireProxy(ctx, params.RunID, nil, params.LeaseSeconds, params.Country, params.Group)
	if err != nil {
		return nil, NewRegistrationError("no_eligible_proxy", "", err)
	}
	if lease == nil {
		// 耗尽兜底：把 used 代理恢复为 available 再抽一次（对齐 Python restore_used_proxies）。
		if restored, rerr := s.res.RestoreUsedProxies(ctx, params.Country, params.Group); rerr == nil && restored > 0 {
			lease, err = s.proxy.AcquireProxy(ctx, params.RunID, nil, params.LeaseSeconds, params.Country, params.Group)
			if err != nil {
				return nil, NewRegistrationError("no_eligible_proxy", "", err)
			}
		}
	}
	if lease == nil {
		return nil, NewRegistrationError("no_eligible_proxy", "", errNoEligibleProxy())
	}
	return lease, nil
}

// dialForRun 用租到的通道拨出一个新出口 IP（模型 B）。无拨号器时退回静态直连。
//
// 拨号器选择：优先 params.Dialer（A 方案每批按 settings 构造，exitIPCap 正确 +
// 干净 registry），否则退回 Service 构造时的 s.dialer。
func (s *Service) dialForRun(ctx context.Context, lease *ProxyLease, country string, params RunParams) (*DialResult, error) {
	d := params.Dialer
	if d == nil {
		d = s.dialer
	}
	if d != nil {
		return d.Dial(ctx, lease, country)
	}
	// 退回：静态租约直连（不实测出口，EgressIP 为空——GeoIP 时区在 warmup 后由 BindEgress 补）。
	return &DialResult{
		ProxyURL:  lease.ProxyURL(),
		ChannelID: lease.ID,
		Country:   normalizeCountry(country),
	}, nil
}

// persistOnSuccess 成功落库（account.Create + 邮箱 consume）。
func (s *Service) persistOnSuccess(ctx context.Context, authRes *AuthResultLike, reserved *ReservedEmail, dial *DialResult) (string, error) {
	if s.adapter != nil {
		return s.adapter.PersistAccount(ctx, authRes, reserved.ID, dial.Country)
	}
	// 无适配器：退化为只 consume 邮箱（不落账号），返回空 ID（骨架兜底）。
	_ = s.res.DiscardReservedEmail(ctx, reserved.ID, "signup")
	return "", nil
}

// cleanupOnFailure 失败清理：按错误码分流邮箱。代理租约由 defer safeReturnProxy
// 统一归还（唯一归还点，owner=RunID 配对），此处不再归还（避免双重释放/计数错）。
//
// 代理语义（defer 统一处理）：归还租约回 available 池复用（ReturnProxy→ReleaseProxy）。
// 会话型通道不因单次失败而 consume/隔离——它只是这次拨了个坏出口 IP，通道本身还能换
// 新 session 重拨。代理健康度（连续失败→隔离）由 proxy pool 的 RecordProxyTest/
// quarantine 阈值独立管理（对齐 codex consecutiveFailures 阈值模型），不在注册失败路径做。
func (s *Service) cleanupOnFailure(ctx context.Context, reserved *ReservedEmail, runErr error) {
	// 邮箱： warmup/网络类失败可复用（归还）；OTP 超时/已有账号标记（避免重复踩）。
	if reserved != nil && s.res != nil {
		code := ErrorCode(runErr)
		switch code {
		case "existing_account", "otp_fetch_failed", "otp_verify_failed":
			_ = s.res.SetEmailStatus(ctx, reserved.ID, "failed", string(code), string(code))
		default:
			_ = s.res.ReleaseEmail(ctx, reserved.ID, "signup")
		}
	}
}

func (s *Service) reserveEmail(ctx context.Context, params RunParams) (*ReservedEmail, error) {
	if s.res == nil {
		return nil, NewRegistrationError("reserve_email_failed", "", errNoResourceStore())
	}
	source := params.EmailSource
	if source == "" {
		source = "mailcode"
	}
	list, err := s.res.ReserveEmails(ctx, 1, params.RunID, source)
	if err != nil || len(list) == 0 {
		if err == nil {
			err = errNoAvailableEmail()
		}
		return nil, NewRegistrationError("no_available_email", "", err)
	}
	return &list[0], nil
}

// safeReturnProxy 归还代理租约（唯一归还点，由 Run 的 defer 调用）。
// owner 必须与 acquire 时一致（params.RunID）——proxy store 按 (proxyID, owner)
// 配对释放；owner 不匹配会导致租约不释放（泄漏）或误释放他人租约。
func (s *Service) safeReturnProxy(ctx context.Context, lease *ProxyLease, owner string) {
	if lease == nil || s.proxy == nil {
		return
	}
	_ = s.proxy.ReturnProxy(ctx, lease.ID, owner)
}

func reservedEmailOf(r *ReservedEmail) string {
	if r == nil {
		return ""
	}
	return r.Email
}

type serviceError struct{ msg string }

func (e *serviceError) Error() string { return e.msg }

func errNoProxyStore() error    { return &serviceError{msg: "signup: proxy store 未配置"} }
func errNoResourceStore() error { return &serviceError{msg: "signup: resource store 未配置"} }
func errNoEligibleProxy() error { return &serviceError{msg: "signup: 代理池无可用通道"} }
func errNoFlowRunner() error {
	return &serviceError{msg: "signup: FlowRunner 未注入（装配层需 WithFlowRunner）"}
}
func errNoAvailableEmail() error {
	return &serviceError{msg: "signup: 邮箱池无可用邮箱"}
}
func errNoMailFactory() error {
	return &serviceError{msg: "signup: MailFactory 未注入（装配层需传入按邮箱造 OTP provider 的工厂）"}
}

// bgCtx 派生一个「与注册请求脱钩取消」的后台 ctx（对齐 codex 后台任务不因注册返回而停）。
// context.WithoutCancel 保留父 ctx 的值（trace 等）但不受其取消影响——注册请求结束后，
// 后台套餐检查/验活仍能跑完。
func (s *Service) bgCtx(ctx context.Context) context.Context {
	return context.WithoutCancel(ctx)
}

// ErrorCode 从错误链提取 RegistrationError.Code（失败清理按错误码分流用）。
func ErrorCode(err error) string {
	if err == nil {
		return ""
	}
	if re, ok := err.(*RegistrationError); ok {
		return re.Code
	}
	return ""
}

// generatePassword 生成注册用密码（16 位，大小写+数字+符号，对齐 codex-auto 密码规则）。
// service 层生成（而非 authflow），便于落盘回调前确定、且失败时可丢弃。
func generatePassword() string {
	const (
		lower = "abcdefghijkmnopqrstuvwxyz"
		upper = "ABCDEFGHJKLMNPQRSTUVWXYZ"
		digit = "23456789"
		sym   = "!@#$%^&*"
	)
	all := lower + upper + digit + sym
	n := 16
	buf := make([]byte, n)
	rnd := make([]byte, n)
	if _, err := crand.Read(rnd); err != nil {
		for i := range buf {
			buf[i] = all[i%len(all)]
		}
		return string(buf)
	}
	// 保证至少各含一类（前 4 位固定类别，其余随机）。
	classes := []string{lower, upper, digit, sym}
	for i := 0; i < n; i++ {
		set := all
		if i < len(classes) {
			set = classes[i]
		}
		buf[i] = set[int(rnd[i])%len(set)]
	}
	// 简单打乱（Fisher-Yates 用 rnd 高位）。
	for i := n - 1; i > 0; i-- {
		j := int(rnd[i]) % (i + 1)
		buf[i], buf[j] = buf[j], buf[i]
	}
	return string(buf)
}

// ── FlowRunner：解耦 service → authflow（避免包循环 import）────────────────
//
// service 在 signup 根包，authflow import signup（用 Config/AuthResult/RegistrationError），
// 故 signup 不能反向 import authflow。用函数注入：装配层（main）把 authflow.New+RunRegister
// 包成 FlowRunner 注入进来（见 WithFlowRunner）。

// FlowRequest 是单次注册的输入（拨号结果 + 邮箱 + 求解器）。
type FlowRequest struct {
	ProxyURL   string          // 拨号产出的临时代理 URL（session 已随机替换）
	EgressIP   string          // 实测出口 IP（→ GeoIP 解国家+时区，国家动态实测）
	Email      string          // 预留邮箱
	Password   string          // 注册用密码（service 层生成）
	Mail       MailProvider    // 取 OTP
	OTPTimeout int             // OTP 等待超时（秒）
	Solver     sentinel.Solver // Sentinel PoW 求解器（P4）
	OnPassword func(email, password string)
}

// FlowRunner 执行一次协议注册，返回最小结果（落库用）。由装配层注入。
type FlowRunner func(ctx context.Context, req FlowRequest) (*AuthResultLike, error)

// WithFlowRunner 注入协议注册执行器（装配层把 authflow 包成 FlowRunner）。
func WithFlowRunner(r FlowRunner) ServiceOption {
	return func(s *Service) { s.flowRunner = r }
}
