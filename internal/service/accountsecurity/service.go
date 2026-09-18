package accountsecurity

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"gpt-go/internal/service/signup"
	"gpt-go/internal/service/signup/authflow"
	"gpt-go/internal/service/signup/core"
	"gpt-go/internal/service/signup/mfa"
	"gpt-go/internal/service/signup/otp"
	"gpt-go/internal/service/signup/sentinel"
	"gpt-go/internal/store"
)

// Flow 抽象登录流程(authflow.Flow 满足;测试可注入替身)。
type Flow interface {
	RunLogin(ctx context.Context, p authflow.LoginParams) (*signup.AuthResult, error)
	// RunBackfill 无密码分支:passwordless 账号补设密码 → 拿 recent_auth AT。
	RunBackfill(ctx context.Context, p authflow.BackfillParams) (*signup.AuthResult, error)
	Boot() *core.Bootstrap
	Close()
}

// ProxyStore 抽象代理租约(补 2FA 登录走代理)。
type ProxyStore interface {
	AcquireProxy(ctx context.Context, owner string, excluded []string, leaseSeconds int, country, group string) (*signup.ProxyLease, error)
	ReleaseProxy(ctx context.Context, proxyID, owner string) error
}

// flowFactory 构造登录 Flow(默认 authflow.New;测试注入替身)。
type flowFactory func(cfg *signup.Config) (Flow, error)

// Service 提供账号安全操作(补 2FA)。
type Service struct {
	accounts   store.AccountStore
	proxies    ProxyStore
	solver     sentinel.Solver
	otpTimeout int
	newFlow    flowFactory
}

// Option 是 Service 的可选配置。
type Option func(*Service)

// WithFlowFactory 注入 Flow 构造替身(测试用;注入后忽略 solver)。
func WithFlowFactory(f flowFactory) Option { return func(s *Service) { s.newFlow = f } }

// WithOTPTimeout 设置 OTP 等待超时(秒)。
func WithOTPTimeout(sec int) Option { return func(s *Service) { s.otpTimeout = sec } }

// New 构造补 2FA 服务。solver 是 Sentinel PoW 求解器(登录的 sentinel 步骤必需,
// 对齐注册链路);nil 时登录会在 sentinel 步骤失败(该号记 failed,不阻断其它号)。
func New(accounts store.AccountStore, proxies ProxyStore, solver sentinel.Solver, opts ...Option) *Service {
	s := &Service{accounts: accounts, proxies: proxies, solver: solver, otpTimeout: 60}
	for _, o := range opts {
		o(s)
	}
	if s.newFlow == nil {
		sv := solver
		s.newFlow = func(cfg *signup.Config) (Flow, error) {
			flow, err := authflow.New(cfg, authflow.WithSolver(sv))
			if err != nil {
				return nil, err
			}
			// sentinel challenge 与主线 TLS 同源(对齐注册装配 wire.go)。
			authflow.InjectCoreFetcher(sv, flow.Boot().Session)
			return flow, nil
		}
	}
	return s
}

// EnsureResult 是单账号补 2FA 的结果。
type EnsureResult struct {
	ID                  string `json:"id"`
	Status              string `json:"status"` // success / failed / skipped
	Error               string `json:"error,omitempty"`
	TotpSecretConfigured bool  `json:"totpSecretConfigured,omitempty"`
}

// OtpProviderFunc 由调用方提供邮箱 OTP provider(补 2FA 登录若走 OTP 分支用)。
// 生产装配层注入(用邮箱 accessUrl 取码);无密码场景需 OTP,有密码场景用不到。
type OtpProviderFunc func(accessURL string) otp.Provider

// EnsureOne 对单账号执行补 2FA:登录拿 recent_auth AT → enroll+activate → 落库。
// proxyID 非空时用指定代理,否则按账号注册国家/分组租临时代理。
// 对齐 codex ensure_totp(有密码分支):账号已有密码时直接登录绑 TOTP。
func (s *Service) EnsureOne(ctx context.Context, accountID, proxyID string, makeOTP OtpProviderFunc) EnsureResult {
	acc, err := s.accounts.Get(ctx, accountID)
	if err != nil {
		return EnsureResult{ID: accountID, Status: "failed", Error: "store:" + err.Error()}
	}
	if acc == nil {
		return EnsureResult{ID: accountID, Status: "skipped", Error: "account_missing"}
	}
	// 跳过条件(对齐 codex ensure_totp):密码与 2FA 都已配置才无需补。
	// 三种需补的情形都要处理:
	//   有密码无2FA → 有密码分支登录后绑 2FA
	//   有2FA无密码 → 无密码分支 backfill 补密码(2FA 预置供 mfa_challenge,不重复 enroll 已有 2FA)
	//   无密码无2FA → 无密码分支 backfill 补密码 + 绑 2FA
	hasPassword := strings.TrimSpace(acc.ChatgptPassword) != ""
	hasTotp := strings.TrimSpace(acc.TotpSecret) != ""
	if hasPassword && hasTotp {
		return EnsureResult{ID: accountID, Status: "skipped", Error: "already_configured", TotpSecretConfigured: true}
	}
	// 租代理:优先指定 proxyID,否则按注册国家/分组租临时。
	owner := "ensure2fa-" + accountID
	var proxyURL string
	var leaseID string
	if s.proxies != nil {
		country, group := "", ""
		if acc.RegistrationCountry != nil {
			country = *acc.RegistrationCountry
		}
		lease, lerr := s.proxies.AcquireProxy(ctx, owner, nil, 300, country, group)
		if lerr == nil && lease != nil {
			proxyURL = lease.ProxyURL()
			leaseID = lease.ID
		}
	}
	if s.proxies != nil && leaseID != "" {
		defer func() { _ = s.proxies.ReleaseProxy(ctx, leaseID, owner) }()
	}

	// flow 保持存活:拿到 recent_auth AT 后用其 session 发 enroll/activate。
	flow, err := s.newFlow(&signup.Config{Proxy: proxyURL})
	if err != nil {
		return EnsureResult{ID: accountID, Status: "failed", Error: "flow_init:" + err.Error()}
	}
	defer flow.Close()

	var mail otp.Provider
	if makeOTP != nil && strings.TrimSpace(acc.EmailAccessURL) != "" {
		mail = makeOTP(acc.EmailAccessURL)
	}

	var result *signup.AuthResult
	newPassword := "" // 无密码分支新生效的密码(落库用)
	if hasPassword {
		// ── 有密码分支:协议登录拿 recent_auth AT(对齐 codex 有密码分支)。
		result, err = flow.RunLogin(ctx, authflow.LoginParams{
			Email:      acc.Email,
			Password:   acc.ChatgptPassword,
			TotpSecret: acc.TotpSecret,
			Mail:       mail,
			OTPTimeout: s.otpTimeout,
		})
		if err != nil {
			return EnsureResult{ID: accountID, Status: "failed", Error: "login:" + sanitizeErr(err)}
		}
	} else {
		// ── 无密码分支:backfill 补设密码 + 拿 recent_auth AT(对齐 codex backfill_password_and_totp)。
		// 走邮箱 OTP 重认证,Mail 必需。
		if mail == nil {
			return EnsureResult{ID: accountID, Status: "skipped", Error: "missing_mailbox_access"}
		}
		result, err = flow.RunBackfill(ctx, authflow.BackfillParams{
			Email:      acc.Email,
			Password:   "", // 空 → 协议层现场生成随机密码,回写 result.Password
			TotpSecret: acc.TotpSecret,
			Mail:       mail,
			OTPTimeout: s.otpTimeout,
		})
		if err != nil {
			// 补密码成功但没拿到 AT(部分成功):密码已生效,落密码后按失败返回(2FA 无法继续)。
			if result != nil && strings.TrimSpace(result.Password) != "" {
				_ = s.accounts.StorePassword(ctx, accountID, store.TotpUpdate{ChatgptPassword: result.Password})
			}
			return EnsureResult{ID: accountID, Status: "failed", Error: "backfill:" + sanitizeErr(err)}
		}
		if result != nil {
			newPassword = strings.TrimSpace(result.Password)
		}
	}
	if result == nil || strings.TrimSpace(result.AccessToken) == "" {
		return EnsureResult{ID: accountID, Status: "failed", Error: "login:no_access_token"}
	}

	// 已有 2FA(有2FA无密码场景):补密码后无需重复 enroll,直接落密码 + 刷新 AT(不碰已有 totp)。
	if hasTotp {
		// newPassword 为空(backfill 未回写密码)属异常:有2FA无密码却补不出密码,按失败处理。
		if newPassword == "" {
			return EnsureResult{ID: accountID, Status: "failed", Error: "backfill:no_password_returned"}
		}
		expiresAt := accessTokenExpiresAt(result.AccessToken)
		if err := s.accounts.StorePassword(ctx, accountID, store.TotpUpdate{
			AccessToken:          result.AccessToken,
			AccessTokenExpiresAt: expiresAt,
			ChatgptPassword:      newPassword,
		}); err != nil {
			return EnsureResult{ID: accountID, Status: "failed", Error: "store_password:" + err.Error()}
		}
		return EnsureResult{ID: accountID, Status: "success", TotpSecretConfigured: true}
	}

	// 无 2FA:enroll + activate(recent_auth AT):复用 signup/mfa 的 EnrollAndActivate(单一实现)。
	SECRET, err := mfa.EnrollAndActivate(ctx, flow.Boot().Session, result.AccessToken)
	if err != nil {
		// 无密码分支:2FA 失败但密码已生效,先把密码落库(不丢已补的密码)。
		// 用 StorePassword(非 StoreTotp):此处 enroll 失败无 secret,StoreTotp 会因空 secret 报错。
		if newPassword != "" {
			_ = s.accounts.StorePassword(ctx, accountID, store.TotpUpdate{ChatgptPassword: newPassword})
		}
		return EnsureResult{ID: accountID, Status: "failed", Error: sanitizeErr(err)}
	}

	// 落库:先落密码(无密码分支新生效的密码;有密码分支为空不动)再落 TOTP + 刷新 AT。
	expiresAt := accessTokenExpiresAt(result.AccessToken)
	if err := s.accounts.StoreTotp(ctx, accountID, store.TotpUpdate{
		TotpSecret:           SECRET,
		AccessToken:          result.AccessToken,
		AccessTokenExpiresAt: expiresAt,
		ChatgptPassword:      newPassword,
	}); err != nil {
		return EnsureResult{ID: accountID, Status: "failed", Error: "store_totp:" + err.Error()}
	}
	return EnsureResult{ID: accountID, Status: "success", TotpSecretConfigured: true}
}

// BatchResult 是批量补 2FA 的汇总。
type BatchResult struct {
	Requested int            `json:"requested"`
	Succeeded int            `json:"succeeded"`
	Failed    int            `json:"failed"`
	Skipped   int            `json:"skipped"`
	Items     []EnsureResult `json:"items"`
}

// EnsureBatch 批量补 2FA(并发限流,单号失败不影响其它)。对齐 codex bulk_ensure_2fa。
func (s *Service) EnsureBatch(ctx context.Context, ids []string, proxyID string, concurrency int, makeOTP OtpProviderFunc) BatchResult {
	if concurrency <= 0 {
		concurrency = 3
	}
	// 去重:同一账号并发跑两个 EnsureOne 会撞代理租约 owner/并发登录/并发写库,必须先按 id 去重。
	seen := make(map[string]struct{}, len(ids))
	unique := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	res := BatchResult{Requested: len(unique), Items: make([]EnsureResult, 0, len(unique))}
	sem := make(chan struct{}, concurrency)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, id := range unique {
		id := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			r := s.EnsureOne(ctx, id, proxyID, makeOTP)
			mu.Lock()
			res.Items = append(res.Items, r)
			switch r.Status {
			case "success":
				res.Succeeded++
			case "skipped":
				res.Skipped++
			default:
				res.Failed++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	return res
}

// ── 小工具 ──

// sanitizeErr 把错误压成单行短串(防协议响应体刷屏)。
func sanitizeErr(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 160 {
		s = s[:160]
	}
	return s
}

// accessTokenExpiresAt 从 JWT exp 解析 AT 过期时间(解析失败回退 now+30d,对齐 codex)。
func accessTokenExpiresAt(at string) *time.Time {
	parts := strings.Split(strings.TrimSpace(at), ".")
	if len(parts) >= 2 {
		payload := parts[1]
		if m := len(payload) % 4; m > 0 {
			payload += strings.Repeat("=", 4-m)
		}
		if data, err := base64.URLEncoding.DecodeString(payload); err == nil {
			var claims struct {
				Exp int64 `json:"exp"`
			}
			if json.Unmarshal(data, &claims) == nil && claims.Exp > 0 {
				t := time.Unix(claims.Exp, 0).UTC()
				return &t
			}
		}
	}
	t := time.Now().UTC().Add(30 * 24 * time.Hour)
	return &t
}
