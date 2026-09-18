// Package mfacheck 实现「批量查询账号 2FA(TOTP)状态」服务,迁移自 codex-auto
// account_security_service.check_2fa_status。
//
// 与 plancheck(验活+查优惠)同构:用账号已存 accessToken 查 /me 的 mfa_flag_enabled,
// 结合本地是否已有 [FUNC] 判定 totpStatus(enabled/orphaned/dirty),回写账号。
// 无需 recent_auth 重认证,只做只读查询,立即可用。
//
// 并发模型:多账号 WaitGroup + 信号量限流;每号独立 core.Session(一号一会话)。
// 单号失败不影响其它;store 层自带锁/原子写,无数据竞争。
package mfacheck

import (
	"context"
	"math/rand"
	"sync"
	"time"

	"gpt-go/internal/service/signup"
	"gpt-go/internal/service/signup/core"
	"gpt-go/internal/service/signup/mfa"
	"gpt-go/internal/store"
)

// ProxyStore 复用 signup.ProxyStore(租约能力一致)。
type ProxyStore = signup.ProxyStore

// ProxyLease 复用 signup.ProxyLease。
type ProxyLease = signup.ProxyLease

// AccountStore 抽象账号存储(mfacheck 用到的子集,由 store.AccountStore 满足)。
type AccountStore interface {
	Get(ctx context.Context, id string) (*store.AccountDocument, error)
	StoreTotpStatus(ctx context.Context, id string, totpStatus string, mfaEnabled bool) error
}

// Item 是单账号 2FA 状态查询结果。
type Item struct {
	ID             string `json:"id"`
	Status         string `json:"status"` // success / failed / skipped
	TotpStatus     string `json:"totpStatus,omitempty"`
	MfaFlagEnabled *bool  `json:"mfaFlagEnabled,omitempty"`
	ErrorCode      string `json:"errorCode,omitempty"`
}

// Result 是批量 2FA 状态查询汇总。
type Result struct {
	Requested int    `json:"requested"`
	Succeeded int    `json:"succeeded"`
	Failed    int    `json:"failed"`
	Skipped   int    `json:"skipped"`
	Items     []Item `json:"items"`
}

// Service 是批量 2FA 状态查询服务。
type Service struct {
	accounts       AccountStore
	proxies        ProxyStore
	maxConcurrency int
	sessionTimeout time.Duration
	rng            *rand.Rand
	// newSession 可注入替身(测试不发真请求)。返回 mfa.Session + Close。
	newSession func(proxyURL string, timeout time.Duration) (mfaSession, error)
	rlog       *signup.RunLogger
}

// mfaSession 抽象 core.Session:mfa.CheckStatus 的 GetWithHeaders + 资源释放 Close。
type mfaSession interface {
	mfa.Session
	Close()
}

// Option 是可选配置。
type Option func(*Service)

// WithMaxConcurrency 设并发上限(钳到 [1,32])。
func WithMaxConcurrency(n int) Option {
	return func(s *Service) {
		if n < 1 {
			n = 1
		}
		if n > 32 {
			n = 32
		}
		s.maxConcurrency = n
	}
}

// WithSessionFactory 注入会话工厂(测试替身用)。
func WithSessionFactory(f func(proxyURL string, timeout time.Duration) (mfaSession, error)) Option {
	return func(s *Service) { s.newSession = f }
}

// WithRunLogger 注入活动日志句柄。
func WithRunLogger(l *signup.RunLogger) Option {
	return func(s *Service) { s.rlog = l }
}

// New 构造批量 2FA 状态查询服务。
func New(accounts AccountStore, proxies ProxyStore, opts ...Option) *Service {
	s := &Service{
		accounts:       accounts,
		proxies:        proxies,
		maxConcurrency: 8,
		sessionTimeout: 15 * time.Second,
		rng:            rand.New(rand.NewSource(time.Now().UnixNano())),
	}
	for _, o := range opts {
		o(s)
	}
	if s.newSession == nil {
		s.newSession = s.defaultSession
	}
	return s
}

// defaultSession 用随机 identity + 代理构造真 core.Session(与注册同一指纹体系)。
func (s *Service) defaultSession(proxyURL string, timeout time.Duration) (mfaSession, error) {
	id := core.NewIdentity(s.rng, "", "", "")
	return core.NewSession(id, proxyURL, timeout)
}

// CheckBatch 对一批账号查询 2FA 状态并落库。单号失败不影响其它。
func (s *Service) CheckBatch(ctx context.Context, ids []string, proxyID string) (*Result, error) {
	unique := dedup(ids)
	if len(unique) == 0 {
		return &Result{}, nil
	}
	available, _ := s.proxies.CountEligibleProxies(ctx, "", "")
	conc := s.maxConcurrency
	if proxyID != "" {
		conc = 1
	} else if available > 0 && available < conc {
		conc = available
	}
	if conc < 1 {
		conc = 1
	}
	items := make([]Item, len(unique))
	sem := make(chan struct{}, conc)
	var wg sync.WaitGroup
	for i, id := range unique {
		wg.Add(1)
		go func(idx int, accountID string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			items[idx] = s.checkOne(ctx, accountID, proxyID)
		}(i, id)
	}
	wg.Wait()
	res := &Result{Requested: len(items), Items: items}
	for _, it := range items {
		switch it.Status {
		case "success":
			res.Succeeded++
		case "failed":
			res.Failed++
		case "skipped":
			res.Skipped++
		}
	}
	return res, nil
}

// checkOne 查单个账号的 2FA 状态(对齐 check_2fa_status)。
func (s *Service) checkOne(ctx context.Context, accountID, proxyID string) Item {
	src, err := s.accounts.Get(ctx, accountID)
	if err != nil || src == nil {
		return Item{ID: accountID, Status: "skipped", ErrorCode: "account_not_found"}
	}
	if src.AccessToken == "" || src.AccessTokenMissing || !src.AccessTokenConfigured {
		return Item{ID: accountID, Status: "skipped", ErrorCode: "missing_token"}
	}
	country := ""
	if src.RegistrationCountry != nil {
		country = *src.RegistrationCountry
	}
	// 租代理(指定 proxyID 则按 ID,否则按注册国)。
	owner := "mfacheck:" + accountID + ":" + randHex(4)
	var lease *ProxyLease
	if proxyID != "" {
		lease, err = s.proxies.AcquireProxyByID(ctx, proxyID, owner, 120)
	} else {
		lease, err = s.proxies.AcquireProxy(ctx, owner, nil, 120, country, "")
	}
	if err != nil || lease == nil {
		return Item{ID: accountID, Status: "failed", ErrorCode: "no_eligible_proxy"}
	}
	defer func() { _ = s.proxies.ReturnProxy(ctx, lease.ID, owner) }()

	sess, serr := s.newSession(lease.ProxyURL(), s.sessionTimeout)
	if serr != nil {
		return Item{ID: accountID, Status: "failed", ErrorCode: "session_init_failed"}
	}
	defer sess.Close()

	localConfigured := src.TotpSecret != ""
	res, cerr := mfa.CheckStatus(ctx, sess, src.AccessToken, localConfigured)
	if cerr != nil {
		code := "me_check_failed"
		if me, ok := cerr.(*mfa.Error); ok {
			code = me.Code
		}
		return Item{ID: accountID, Status: "failed", ErrorCode: code}
	}
	if err := s.accounts.StoreTotpStatus(ctx, accountID, res.TotpStatus, res.MfaFlagEnabled); err != nil {
		return Item{ID: accountID, Status: "failed", ErrorCode: "store_failed"}
	}
	if s.rlog != nil {
		s.rlog.Emit(signup.LogSuccess, "mfacheck_done", "2FA 状态: "+res.TotpStatus, src.Email,
			map[string]any{"accountId": accountID, "totpStatus": res.TotpStatus})
	}
	return Item{
		ID:             accountID,
		Status:         "success",
		TotpStatus:     res.TotpStatus,
		MfaFlagEnabled: &res.MfaFlagEnabled,
	}
}

// ── 小工具 ──

func dedup(ids []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

const hexDigits = "0123456789abcdef"

func randHex(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = hexDigits[rand.Intn(16)]
	}
	return string(b)
}
