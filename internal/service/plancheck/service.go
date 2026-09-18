// Package plancheck 实现「验活 + 套餐检查」合并服务（对齐 codex-auto
// AccountPlanCheckService.check_combined）。
//
// 核心设计（对齐 codex「验活 + 查询优惠收敛为一次请求」）：
//
//	一次 plan.Check（accounts/check 端点）同时得到 http_status（验活）与
//	plus_trial_eligible（优惠），并一次写 alive + plan 两组字段——比分开两次请求
//	省一半配额，且同 IP/同 device 会话更不易触发风控。
//
// 并发模型（充分利用 Go）：
//   - 多账号用 errgroup 风格的 WaitGroup + 信号量 channel 并发，限流对齐 codex
//     max_concurrency（且不超过可用代理数，避免租不到空转）；
//   - 每号独立：claim（原子置 running，防并发重复/卡死恢复）→ 租代理 →
//     独立 core.Session（httpcloak 非并发安全，一号一会话）→ plan.Check → 落库。
//   - 账号间零共享可变状态；store 层（MockAccountStore）自带锁，无数据竞争。
package plancheck

import (
	"context"
	"math/rand"
	"sync"
	"time"

	"gpt-go/internal/service/signup"
	"gpt-go/internal/service/tokenheal"
	"gpt-go/internal/service/signup/core"
	"gpt-go/internal/model"
	"gpt-go/internal/service/signup/plan"
	"gpt-go/internal/store"
)

// ProxyStore 复用 signup.ProxyStore（租约能力一致，避免重复定义）。
type ProxyStore = signup.ProxyStore

// ProxyLease 复用 signup.ProxyLease。
type ProxyLease = signup.ProxyLease

// AccountStore 抽象账号存储（plancheck 用到的子集，由 store.AccountStore 满足）。
type AccountStore interface {
	Get(ctx context.Context, id string) (*store.AccountDocument, error)
	ClaimPlanCheck(ctx context.Context, id string, staleMin int) (*store.AccountDocument, error)
	ClaimAliveCheck(ctx context.Context, id string, staleMin int) (*store.AccountDocument, error)
	StoreCombinedResult(ctx context.Context, id string, r store.PlanResultUpdate) error
	StoreCombinedFailure(ctx context.Context, id, errCode string, httpStatus *int, dead, tokenInvalid bool) error
}

// Item 是单账号合并检查的结果（对齐 AccountCombinedCheckItem）。
type Item struct {
	ID         string
	Status     string // alive / dead / failed / skipped
	PlanStatus string // success / failed / skipped
	ErrorCode  string
	HTTPStatus *int
}

// Result 是批量合并检查的汇总（对齐 AccountCombinedCheckResult）。
type Result struct {
	Requested int
	Alive     int
	Dead      int
	Failed    int
	Skipped   int
	Items     []Item
}

// Service 是合并检查服务。
type Service struct {
	accounts AccountStore
	proxies  ProxyStore
	// maxConcurrency 并发上限（对齐 codex max_concurrency，默认 8）。
	maxConcurrency int
	// staleMinutes 认领卡死恢复分钟数（对齐 codex 5min）。
	staleMinutes int
	// sessionTimeout 单号 core.Session 超时。
	sessionTimeout time.Duration
	// rng 造 identity 用（可注入固定种子便于测试）。
	rng *rand.Rand
	// newSession 可注入替身（测试时不发真请求）。返回 plan.Session（Get）+ Close。
	newSession func(proxyURL string, timeout time.Duration) (planSession, error)
	// rlog 可选：套餐检查活动日志句柄（系统级流 "plancheck"）。nil 不写日志。
	rlog *signup.RunLogger
	// healer 可选:AT 过期(TokenInvalid)时先用 session cookie 续期再重试一次(docs/SESSION-TOKEN-HEAL.md)。
	// nil 时保持原行为(AT 过期直接判 dead)。
	healer SessionHealer
}

// SessionHealer 抽象 token-heal 续期(tokenheal.Service 满足;避免循环依赖定义最小接口)。
type SessionHealer interface {
	Heal(ctx context.Context, accountID, proxyID string) tokenheal.HealResult
}

// planSession 抽象 core.Session：plan.Check 的 Get + 资源释放 Close。
type planSession interface {
	plan.Session
	Close()
}

// Option 是可选配置。
type Option func(*Service)

// WithMaxConcurrency 设并发上限（钳到 [1, 32]）。
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

// WithSessionFactory 注入会话工厂（测试替身用）。
func WithSessionFactory(f func(proxyURL string, timeout time.Duration) (planSession, error)) Option {
	return func(s *Service) { s.newSession = f }
}

// WithRunLogger 注入套餐检查活动日志句柄（系统级流，供 SSE/历史展示）。
func WithRunLogger(l *signup.RunLogger) Option {
	return func(s *Service) { s.rlog = l }
}

// WithHealer 注入 token-heal 续期服务(AT 过期先续期再重试)。
func WithHealer(h SessionHealer) Option {
	return func(s *Service) { s.healer = h }
}

// New 构造合并检查服务。
func New(accounts AccountStore, proxies ProxyStore, opts ...Option) *Service {
	s := &Service{
		accounts:       accounts,
		proxies:        proxies,
		maxConcurrency: 8,
		staleMinutes:   5,
		sessionTimeout: 15 * time.Second, // 对齐 codex timeout_seconds=15
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

// defaultSession 用随机 identity + 代理构造真 core.Session（与注册同一指纹体系，
// 对齐 codex impersonate=chrome）。core.Session 同时实现 plan.Session 与 Close。
func (s *Service) defaultSession(proxyURL string, timeout time.Duration) (planSession, error) {
	id := core.NewIdentity(s.rng, "", "", "")
	return core.NewSession(id, proxyURL, timeout)
}

// CheckCombined 对一批账号做「验活 + 套餐检查」（对齐 check_combined）。
//
// 并发：每号一个 goroutine，信号量限流到 min(maxConcurrency, 可用代理数)。
// 单号失败不影响其它（对齐 codex「单项失败不丢弃其他成功项」）。
func (s *Service) CheckCombined(ctx context.Context, ids []string, proxyID string) (*Result, error) {
	// 去重（对齐 codex dict.fromkeys）。
	unique := dedup(ids)
	if len(unique) == 0 {
		return &Result{}, nil
	}
	// 限流：不超过可用代理数（proxyID 指定时退化为串行 1，对齐 codex）。
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
		case "alive":
			res.Alive++
		case "dead":
			res.Dead++
		case "failed":
			res.Failed++
		case "skipped":
			res.Skipped++
		}
	}
	return res, nil
}

// checkOne 检查单个账号（对齐 _check_combined_one）。
// CheckCombinedWithProxyURL 用【调用方给的完整代理 URL】(含注册时重写的 session,
// 拨出与注册一致的出口 IP)做套餐+验活,不重新租代理。对齐 codex「查资格用注册时
// 的同一代理(lease 期内)」——出口 IP 与注册一致,避免 OpenAI 因「注册IP≠查询IP」
// 判定异常而拒发试用资格。注册成功后由 service 传 dial.ProxyURL 调用。
func (s *Service) CheckCombinedWithProxyURL(ctx context.Context, ids []string, proxyURL string) (*Result, error) {
	unique := dedup(ids)
	if len(unique) == 0 {
		return &Result{}, nil
	}
	items := make([]Item, len(unique))
	// 串行(同一出口 IP,不能并发撞)。
	for i, id := range unique {
		items[i] = s.checkOne(ctx, id, "", proxyURL)
	}
	res := &Result{Requested: len(items), Items: items}
	for _, it := range items {
		switch it.Status {
		case "alive":
			res.Alive++
		case "dead":
			res.Dead++
		case "failed":
			res.Failed++
		case "skipped":
			res.Skipped++
		}
	}
	return res, nil
}

// checkOne 查单号套餐+验活。directProxyURL 非空时直接用它建会话(不租代理,
// 出口 IP 与注册一致);否则按 proxyID/注册国租代理。
func (s *Service) checkOne(ctx context.Context, accountID, proxyID string, directProxyURL ...string) Item {
	// 1) 原子认领：套餐 + 验活双 running（任一无 token/被认领则跳过）。
	src, err := s.accounts.ClaimPlanCheck(ctx, accountID, s.staleMinutes)
	if err != nil || src == nil {
		s.rlog.Emit(signup.LogWarning, "plancheck_skipped", "套餐检查跳过(无token或被认领)", "", map[string]any{"accountId": accountID})
		return Item{ID: accountID, Status: "skipped", PlanStatus: "skipped", ErrorCode: "account_missing_token_or_busy"}
	}
	if _, err := s.accounts.ClaimAliveCheck(ctx, accountID, s.staleMinutes); err != nil {
		// 套餐已认领成功但验活认领失败：仍继续（combined 以套餐为主，验活失败不阻断）。
	}
	country := ""
	if src.RegistrationCountry != nil {
		country = *src.RegistrationCountry
	}

	// 2) 代理:优先用调用方给的完整 URL(directProxyURL,出口 IP 与注册一致);
	//    否则指定 proxyID 按 ID 租,或按注册国租(对齐 codex 分支)。
	var proxyURL string
	if len(directProxyURL) > 0 && directProxyURL[0] != "" {
		proxyURL = directProxyURL[0] // 注册时的同一代理 URL,不租代理
	} else {
		owner := "combined:" + accountID + ":" + randHex(4)
		var lease *ProxyLease
		if proxyID != "" {
			lease, err = s.proxies.AcquireProxyByID(ctx, proxyID, owner, 120)
		} else {
			lease, err = s.proxies.AcquireProxy(ctx, owner, nil, 120, country, "")
		}
		if err != nil || lease == nil {
			_ = s.accounts.StoreCombinedFailure(ctx, accountID, "no_eligible_proxy", nil, false, false)
			return Item{ID: accountID, Status: "failed", PlanStatus: "failed", ErrorCode: "no_eligible_proxy"}
		}
		defer func() { _ = s.proxies.ReturnProxy(ctx, lease.ID, owner) }()
		proxyURL = lease.ProxyURL()
	}

	// 3) 独立 core.Session（httpcloak 非并发安全，一号一会话）+ plan.Check。
	sess, serr := s.newSession(proxyURL, s.sessionTimeout)
	if serr != nil {
		_ = s.accounts.StoreCombinedFailure(ctx, accountID, "plan_request_failed", nil, false, false)
		return Item{ID: accountID, Status: "failed", PlanStatus: "failed", ErrorCode: "plan_request_failed"}
	}
	defer sess.Close()

	res, perr := plan.Check(ctx, sess, src.AccessToken, timezoneOffsetForCountry(country))
	// ── token-heal 自愈:AT 过期(TokenInvalid)且配了 healer → 先用 session cookie 续期,
	//    续成则用新 AT 重试一次 plan.Check;续不成才按死号/失败落库(docs/SESSION-TOKEN-HEAL.md)。
	if perr != nil && s.healer != nil {
		if pe, ok := perr.(*plan.Error); ok && pe.TokenInvalid() {
			s.rlog.Emit(signup.LogInfo, "plancheck_heal_try", "AT 过期,尝试 session 续期", src.Email, map[string]any{"accountId": accountID})
			hr := s.healer.Heal(ctx, accountID, "")
			if hr.Status == "success" {
				// 续期成功:重新读账号拿新 AT,重试一次 plan.Check。
				if refreshed, gerr := s.accounts.Get(ctx, accountID); gerr == nil && refreshed != nil && refreshed.AccessToken != "" {
					s.rlog.Emit(signup.LogInfo, "plancheck_heal_ok", "session 续期成功,重试套餐检查", src.Email, map[string]any{"accountId": accountID})
					res, perr = plan.Check(ctx, sess, refreshed.AccessToken, timezoneOffsetForCountry(country))
				}
			} else {
				s.rlog.Emit(signup.LogWarning, "plancheck_heal_fail", "session 续期失败: "+hr.Error, src.Email, map[string]any{"accountId": accountID, "err": hr.Error})
			}
		}
	}
	if perr != nil {
		pe, ok := perr.(*plan.Error)
		code, httpSt, dead, tokenInvalid := "plan_request_failed", (*int)(nil), false, false
		if ok {
			code = pe.Code
			if pe.HTTPStatus != 0 {
				hs := pe.HTTPStatus
				httpSt = &hs
			}
			dead = pe.TokenInvalid() // token 失效 → 死号
			tokenInvalid = pe.TokenInvalid()
		}
		_ = s.accounts.StoreCombinedFailure(ctx, accountID, code, httpSt, dead, tokenInvalid)
		st := "failed"
		lvl := signup.LogError
		if dead {
			st = "dead"
			lvl = signup.LogWarning
		}
		s.rlog.Emit(lvl, "plancheck_"+st, "套餐检查失败: "+code, src.Email, map[string]any{"accountId": accountID, "code": code})
		return Item{ID: accountID, Status: st, PlanStatus: "failed", ErrorCode: code, HTTPStatus: httpSt}
	}

	// 4) 成功：一次写 alive + plan 两组字段。
	upd := store.PlanResultUpdate{
		CheckedAt:             res.CheckedAt,
		HTTPStatus:            intPtrOrNil(res.HTTPStatus),
		AccountID:             strPtrOrNil2(res.AccountID),
		SubscriptionPlan:      strPtrOrNil2(res.SubscriptionPlan),
		HasActiveSubscription: boolPtr(res.HasActiveSubscription),
		ExpiresAt:             res.ExpiresAt,
		RenewsAt:              res.RenewsAt,
		PromotionEligible:     boolPtr(res.PlusTrialEligible),
		PromotionCampaignID:   strPtrOrNil2(res.PlusTrialCampaignID),
		PromotionCampaigns:    toModelCampaigns(res.EligibleCampaigns),
		AccountType:           res.NormalizedAccountType,
	}
	if err := s.accounts.StoreCombinedResult(ctx, accountID, upd); err != nil {
		_ = s.accounts.StoreCombinedFailure(ctx, accountID, "plan_result_store_failed", nil, false, false)
		return Item{ID: accountID, Status: "failed", PlanStatus: "failed", ErrorCode: "plan_result_store_failed"}
	}
	s.rlog.Emit(signup.LogSuccess, "plancheck_alive", "套餐检查存活 套餐="+res.NormalizedAccountType, src.Email, map[string]any{
		"accountId": accountID, "accountType": res.NormalizedAccountType,
	})
	return Item{ID: accountID, Status: "alive", PlanStatus: "success", HTTPStatus: upd.HTTPStatus}
}

// ── 小工具 ──

func dedup(ids []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func randHex(n int) string {
	const hexd = "0123456789abcdef"
	b := make([]byte, n)
	for i := range b {
		b[i] = hexd[rand.Intn(16)]
	}
	return string(b)
}

func intPtrOrNil(v int) *int {
	if v == 0 {
		return nil
	}
	return &v
}

func strPtrOrNil2(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func boolPtr(b bool) *bool { return &b }

// toModelCampaigns 把 plan.Campaign 转 model.PromotionCampaign（落库含 title 人话文案）。
func toModelCampaigns(cs []plan.Campaign) []model.PromotionCampaign {
	if len(cs) == 0 {
		return nil
	}
	out := make([]model.PromotionCampaign, 0, len(cs))
	for _, c := range cs {
		out = append(out, model.PromotionCampaign{
			Plan:          c.Plan,
			ID:            c.ID,
			PromotionType: c.PromotionType,
			Title:         c.Title,
		})
	}
	return out
}

// timezoneOffsetForCountry 对齐 codex timezone_offset_for_country：
// JavaScript Date.getTimezoneOffset 语义（UTC - local，单位分钟）。
func timezoneOffsetForCountry(country string) string {
	switch country {
	case "JP", "KR":
		return "-540"
	case "TR":
		return "-180"
	case "SG", "HK", "TW", "PH":
		return "-480"
	case "GB":
		return "0"
	case "DE", "FR":
		return "-60"
	case "US", "CA":
		return "300"
	case "BR":
		return "180"
	case "AU":
		return "-600"
	default:
		return "0"
	}
}
