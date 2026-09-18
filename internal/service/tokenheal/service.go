// Package tokenheal 实现「Session Cookie 落库 + 轻量续期」(docs/SESSION-TOKEN-HEAL.md):
// 用注册时落库的 __Secure-next-auth.session-token cookie(约 3 个月)回灌会话,
// 单次只读 GET /api/auth/session 直接换新 access_token —— 无需密码/OTP/2FA、
// 无需重走 warmup/sentinel/authorize 等高风险协议步骤,Cloudflare 风控暴露面骤降。
//
// 与 accountsecurity.EnsureOne / rebind 的区别:那些要 recent_auth AT(必须 RunLogin
// 全链路);token-heal 只管「只读验活 + AT 续期」,只要 session cookie 活着就能换新 bearer。
package tokenheal

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"gpt-go/internal/service/signup"
	"gpt-go/internal/service/signup/core"
	"gpt-go/internal/store"
)

// ProxyStore 抽象代理租约(续期走注册国代理,保持出口自洽)。
type ProxyStore interface {
	AcquireProxy(ctx context.Context, owner string, excluded []string, leaseSeconds int, country, group string) (*signup.ProxyLease, error)
	ReleaseProxy(ctx context.Context, proxyID, owner string) error
}

// Session 抽象本包需要的最小会话能力(*core.Session 满足;测试可注入替身)。
type Session interface {
	SetCookie(name, value, domain, path string)
	CookieValue(name string) string
	Get(ctx context.Context, url, referer string) (*core.Response, error)
	Close()
}

// sessionFactory 构造带代理/指纹的会话(默认走 core.NewBootstrap;测试注入替身)。
type sessionFactory func(proxyURL, countryHint string) (Session, error)

// Service 提供 token-heal 续期。
type Service struct {
	accounts  store.AccountStore
	proxies   ProxyStore
	newSession sessionFactory
}

// Option 是 Service 的可选配置。
type Option func(*Service)

// WithSessionFactory 注入会话构造替身(测试用)。
func WithSessionFactory(f sessionFactory) Option { return func(s *Service) { s.newSession = f } }

// New 构造 token-heal 服务。
func New(accounts store.AccountStore, proxies ProxyStore, opts ...Option) *Service {
	s := &Service{accounts: accounts, proxies: proxies}
	for _, o := range opts {
		o(s)
	}
	if s.newSession == nil {
		s.newSession = func(proxyURL, countryHint string) (Session, error) {
			boot, err := core.NewBootstrap(core.BootstrapOptions{
				Proxy:       proxyURL,
				Timeout:     30 * time.Second,
				CountryHint: countryHint,
			})
			if err != nil {
				return nil, err
			}
			return boot.Session, nil
		}
	}
	return s
}

// HealResult 是单账号续期结果。
type HealResult struct {
	ID     string `json:"id"`
	Status string `json:"status"` // success / failed / skipped
	Error  string `json:"error,omitempty"`
}

// sessionAPI 是 chatgpt 换 AT 的轻量只读端点。
const sessionAPI = "https://chatgpt.com/api/auth/session"

// Heal 用 session_token cookie 给单账号续 access_token。
// proxyID 非空时用指定代理,否则按账号注册国家租临时代理。
func (s *Service) Heal(ctx context.Context, accountID, proxyID string) HealResult {
	acc, err := s.accounts.Get(ctx, accountID)
	if err != nil {
		return HealResult{ID: accountID, Status: "failed", Error: "store:" + err.Error()}
	}
	if acc == nil {
		return HealResult{ID: accountID, Status: "skipped", Error: "account_missing"}
	}
	sessionToken := strings.TrimSpace(acc.SessionToken)
	if sessionToken == "" {
		// 无 session cookie(老数据/未落库):无法轻量续期,需走完整 RunLogin。
		return HealResult{ID: accountID, Status: "skipped", Error: "no_session_token"}
	}

	// 1) 租代理(按注册国,保持出口自洽)。
	owner := "tokenheal-" + accountID
	var proxyURL, leaseID string
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

	// 2) 建会话(注册国指纹)。
	country := ""
	if acc.RegistrationCountry != nil {
		country = *acc.RegistrationCountry
	}
	sess, err := s.newSession(proxyURL, country)
	if err != nil {
		return HealResult{ID: accountID, Status: "failed", Error: "session_init:" + err.Error()}
	}
	defer sess.Close()

	// 3) 回灌 cookie:session-token + CookieHeader 里的 oai-did 等指纹(三路自洽)。
	replantCookies(sess, acc.CookieHeader, sessionToken)

	// 4) GET /api/auth/session → mint 新 AT + 滚动 session cookie。
	resp, err := sess.Get(ctx, sessionAPI, "https://chatgpt.com/")
	if err != nil {
		return HealResult{ID: accountID, Status: "failed", Error: "session_get:" + err.Error()}
	}
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		// session cookie 已失效(约 3 个月到期或被风控):需完整 RunLogin 重建。
		return HealResult{ID: accountID, Status: "failed", Error: "session_expired"}
	}
	if resp.StatusCode != 200 {
		return HealResult{ID: accountID, Status: "failed", Error: fmt.Sprintf("session_get:HTTP %d", resp.StatusCode)}
	}
	var body struct {
		AccessToken string `json:"accessToken"`
	}
	_ = json.Unmarshal(resp.Bytes(), &body)
	newAT := strings.TrimSpace(body.AccessToken)
	if newAT == "" {
		return HealResult{ID: accountID, Status: "failed", Error: "no_access_token"}
	}

	// 5) 抓滚动后的新 session cookie(rolling session,越用越新鲜)。
	newSessionToken := sess.CookieValue("__Secure-next-auth.session-token")

	// 6) 落库:新 AT + 滚动 session cookie + 复活标记。
	if err := s.accounts.StoreSessionHeal(ctx, accountID, store.SessionHealUpdate{
		AccessToken:          newAT,
		AccessTokenExpiresAt: accessTokenExpiresAt(newAT),
		SessionToken:         newSessionToken,
	}); err != nil {
		return HealResult{ID: accountID, Status: "failed", Error: "store_heal:" + err.Error()}
	}
	return HealResult{ID: accountID, Status: "success"}
}

// replantCookies 把落库的 CookieHeader(name=value; ...)回灌进会话,
// 并强制覆盖 session-token 为最新落库值(防止 CookieHeader 里是旧值)。
func replantCookies(sess Session, cookieHeader, sessionToken string) {
	for _, kv := range strings.Split(cookieHeader, "; ") {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || name == "" {
			continue
		}
		domain := ".chatgpt.com"
		if name == "oai-did" || strings.HasPrefix(name, "__Host-") {
			domain = "" // oai-did / __Host- 类按当前主机
		}
		sess.SetCookie(name, value, domain, "/")
	}
	// session-token 用落库的最新值(可能在 .chatgpt.com 域)。
	sess.SetCookie("__Secure-next-auth.session-token", sessionToken, ".chatgpt.com", "/")
}

// accessTokenExpiresAt 解析 JWT exp claim(与 resource_adapter 同逻辑)。
func accessTokenExpiresAt(token string) *time.Time {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil
	}
	payload, err := base64URLDecode(parts[1])
	if err != nil {
		return nil
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.Exp == 0 {
		return nil
	}
	t := time.Unix(claims.Exp, 0).UTC()
	return &t
}

func base64URLDecode(s string) ([]byte, error) {
	// JWT 用 base64url(RawURLEncoding,无 padding)。
	return base64.RawURLEncoding.DecodeString(s)
}

// BatchResult 是批量续期的汇总。
type BatchResult struct {
	Requested int          `json:"requested"`
	Succeeded int          `json:"succeeded"`
	Failed    int          `json:"failed"`
	Skipped   int          `json:"skipped"`
	Items     []HealResult `json:"items"`
}

// HealBatch 批量续 AT(并发限流,单号失败不影响其它)。对齐 bulk-heal。
func (s *Service) HealBatch(ctx context.Context, ids []string, proxyID string, concurrency int) BatchResult {
	if concurrency <= 0 {
		concurrency = 3
	}
	// 去重:同一账号并发跑两个 Heal 会撞代理租约 owner/并发写库。
	seen := make(map[string]struct{}, len(ids))
	unique := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	res := BatchResult{Requested: len(unique), Items: make([]HealResult, 0, len(unique))}
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
			r := s.Heal(ctx, id, proxyID)
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
