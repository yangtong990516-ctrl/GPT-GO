// Package paymentcheck 实现「支付类型检测」服务：按区域线路创建优惠
// Custom Checkout（只建单不付款），读取 Stripe/OAICS 初始化状态中的
// 应付金额与支付渠道列表，并写回账号池。
//
// 架构（对齐支付类型检测实现说明 + GPT-GO plancheck 的成熟模式）：
//
//	三层并发：批次全局互斥 → 账号级并发（errgroup + 信号量，上限 min(8, 可用代理数)）
//	  → 单账号内线路串行（同 AT 同时对多国建单是风控高危动作）；
//	  全部线路启动共享全局速率槽（3s + 0~500ms 抖动）。
//
//	单线路流程：租代理 → 出口校验（cdn-cgi/trace，国家不符熔断）→
//	  独立 core.Session（每线路新 Device/Session ID）→ Sentinel best-effort →
//	  建单（promo_campaign=plus-1-month-free）→ oaics_/cs_live_ 分流读取 →
//	  归一化 + 金额矛盾判定 → 流式写回。
//
// 边界红线：不确认 Checkout、不创建 PaymentMethod、不提交支付信息、
// 不绑定支付方式、不扣款；完整会话 ID 不落库（仅存前缀）；错误脱敏 ≤280 字符。
package paymentcheck

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"gpt-go/internal/model"
	"gpt-go/internal/service/signup"
	"gpt-go/internal/service/signup/core"
	signupsentinel "gpt-go/internal/service/signup/sentinel"
	"gpt-go/internal/store"
)

// ErrBusy 表示已有支付检测批次在运行（全局互斥，对齐文档 §4）。
var ErrBusy = errors.New("已有支付检测批次在运行")

// ErrNoRoutes 表示没有有效区域线路（对齐文档 §3.1「完全没有有效线路时拒绝执行」）。
var ErrNoRoutes = errors.New("没有有效的支付检测线路（代理池为空）")

// ProxyStore 抽象代理租约（由 signup.ProxyServiceAdapter 满足）。
type ProxyStore interface {
	CountEligibleProxies(ctx context.Context, country, group string) (int, error)
	AcquireProxy(ctx context.Context, owner string, excludedIDs []string, leaseSeconds int, country, group string) (*signup.ProxyLease, error)
	ReleaseProxy(ctx context.Context, proxyID, owner string) error
	ReturnProxy(ctx context.Context, proxyID, owner string) error
}

// AccountStore 抽象账号存储（store.AccountStore 满足）。
type AccountStore interface {
	ClaimPaymentCheck(ctx context.Context, id string, staleMin int) (*store.AccountDocument, error)
	StorePaymentRouteResult(ctx context.Context, id string, r model.PaymentRouteResult) error
	StorePaymentSummary(ctx context.Context, id string, s store.PaymentSummaryUpdate) error
}

// sessionFactory 抽象 core.Session 构造（测试注入替身）。
type sessionFactory func(proxyURL string, timeout time.Duration) (checkoutSession, error)

// SentinelSolver 抽象 Sentinel 求解（复用 signup/sentinel.V8Solver；nil 时跳过）。
type SentinelSolver interface {
	GetToken(ctx context.Context, env signupsentinel.EnvPayload, flow signupsentinel.Flow) (*signupsentinel.Result, error)
}

// Item 是单账号检测完成后的汇总项（供 API/批次状态）。
type Item struct {
	ID          string
	Email       string
	Status      string // available/partial/not_returned/already_paid/token_invalid/failed/skipped
	Methods     []string
	ZeroMethods []string
	Routes      map[string]model.PaymentRouteResult
	Error       string
}

// Batch 是批次实时状态（前端轮询用）。
type Batch struct {
	ID        string             `json:"batchId"`
	Total     int                `json:"total"`
	Done      int                `json:"done"`
	Skipped   int                `json:"skipped"`
	ZeroCount int                `json:"zeroCount"` // 已产出「0 元有渠道」的账号数
	Running   bool               `json:"running"`
	Canceled  bool               `json:"canceled"`
	Accounts  map[string]string  `json:"accounts"` // accountID → pending/running/done/failed/skipped
	Items     map[string]*Item   `json:"-"`        // 完成账号的明细（routes 接口返回）
	StartedAt time.Time          `json:"startedAt"`
	FinishedAt *time.Time        `json:"finishedAt,omitempty"`
}

// Service 是支付类型检测服务。
type Service struct {
	accounts AccountStore
	proxies  ProxyStore
	solver   SentinelSolver // nil → Sentinel best-effort 跳过
	prober   ExitProber

	maxConcurrency int
	staleMinutes   int
	sessionTimeout time.Duration
	routeInterval  time.Duration // 线路启动最小间隔（默认 3s）
	routeJitter    time.Duration // 抖动上限（默认 500ms）
	rng            *rand.Rand
	newSession     sessionFactory
	rlog           *signup.RunLogger

	mu       sync.Mutex
	active   bool            // 全局互斥
	batch    *Batch          // 当前批次实时状态
	cancelFn context.CancelFunc
}

// Option 是可选配置。
type Option func(*Service)

// WithMaxConcurrency 设账号级并发上限（钳到 [1, 32]）。
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
func WithSessionFactory(f sessionFactory) Option {
	return func(s *Service) { s.newSession = f }
}

// WithExitProber 注入出口探测器（测试替身用）。
func WithExitProber(p ExitProber) Option {
	return func(s *Service) { s.prober = p }
}

// WithSolver 注入 Sentinel 求解器（nil 安全：跳过风控头）。
func WithSolver(sv SentinelSolver) Option {
	return func(s *Service) { s.solver = sv }
}

// WithRunLogger 注入活动日志句柄（系统级流 "paymentcheck"）。
func WithRunLogger(l *signup.RunLogger) Option {
	return func(s *Service) { s.rlog = l }
}

// WithRoutePacing 设线路速率槽（最小间隔 + 抖动）。
func WithRoutePacing(interval, jitter time.Duration) Option {
	return func(s *Service) {
		if interval >= 0 {
			s.routeInterval = interval
		}
		if jitter >= 0 {
			s.routeJitter = jitter
		}
	}
}

// New 构造支付检测服务。
func New(accounts AccountStore, proxies ProxyStore, opts ...Option) *Service {
	s := &Service{
		accounts:       accounts,
		proxies:        proxies,
		prober:         NewCDNExitProber(),
		maxConcurrency: 8,
		staleMinutes:   5,
		sessionTimeout: 45 * time.Second, // 对齐文档 §5 超时默认 45s
		routeInterval:  3 * time.Second,  // 对齐文档 §4 最小间隔 3000ms
		routeJitter:    500 * time.Millisecond,
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

// defaultSession 用随机 identity + 代理构造真 core.Session（与注册同一指纹体系）。
func (s *Service) defaultSession(proxyURL string, timeout time.Duration) (checkoutSession, error) {
	id := core.NewIdentity(s.rng, "", "", "")
	sess, err := core.NewSession(id, proxyURL, timeout)
	if err != nil {
		return nil, err
	}
	return &sessionAdapter{sess}, nil
}

// sessionAdapter 包装 *core.Session 满足 checkoutSession（Close 不暴露在接口里）。
type sessionAdapter struct{ *core.Session }

// TokenInput 是粘贴 AT 模式的一条输入（djblook 式：只给 token，不落库）。
type TokenInput struct {
	AccessToken string
	Label       string // 展示名（可空，默认 token 前 12 位掩码）
}

// RunParams 是批次入参。
type RunParams struct {
	AccountIDs []string     // 账号池模式
	Tokens     []TokenInput // 粘贴 AT 模式（优先于 AccountIDs；WriteBack 被忽略）
	Routes     []Route
	WriteBack  bool // true → 结果写回账号池；false → 仅返回不落库
}

// Run 启动一个支付检测批次（异步：批次在后台 goroutine 执行，立即返回批次 ID）。
//
// 全局互斥：同时只允许一个批次（对齐文档 §4）；批次内账号并发、账号内线路串行。
func (s *Service) Run(ctx context.Context, p RunParams) (string, error) {
	s.mu.Lock()
	if s.active {
		s.mu.Unlock()
		return "", ErrBusy
	}
	routes := EnabledRoutes(p.Routes)
	if len(routes) == 0 {
		s.mu.Unlock()
		return "", ErrNoRoutes
	}
	tokenMode := len(p.Tokens) > 0
	// 单次过滤：id 与 token 在这里一次性配对，下游不再二次过滤（消除双重实现）。
	type workItem struct {
		id    string
		token TokenInput // tokenMode 时有效
	}
	var work []workItem
	if tokenMode {
		for _, t := range p.Tokens {
			t.AccessToken = strings.TrimSpace(t.AccessToken)
			if t.AccessToken == "" {
				continue
			}
			work = append(work, workItem{id: fmt.Sprintf("tok-%d", len(work)), token: t})
		}
	} else {
		for _, id := range dedupStrings(p.AccountIDs) {
			work = append(work, workItem{id: id})
		}
	}
	if len(work) == 0 {
		s.mu.Unlock()
		return "", errors.New("账号列表为空")
	}
	ids := make([]string, len(work))
	for i, w := range work {
		ids[i] = w.id
	}
	s.active = true
	batch := &Batch{
		ID:        fmt.Sprintf("pc-%d", time.Now().UnixNano()),
		Total:     len(ids),
		Running:   true,
		Accounts:  map[string]string{},
		Items:     map[string]*Item{},
		StartedAt: time.Now().UTC(),
	}
	for _, id := range ids {
		batch.Accounts[id] = "pending"
	}
	s.batch = batch
	batchCtx, cancel := context.WithCancel(context.Background())
	s.cancelFn = cancel
	s.mu.Unlock()

	if tokenMode {
		tokens := make([]TokenInput, len(work))
		for i, w := range work {
			tokens[i] = w.token
		}
		go s.runBatchTokens(batchCtx, batch, ids, tokens, routes)
	} else {
		go s.runBatch(batchCtx, batch, ids, routes, p.WriteBack)
	}
	return batch.ID, nil
}

// Status 返回当前批次实时状态。
func (s *Service) Status() *Batch {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.batch == nil {
		return nil
	}
	b := *s.batch
	accts := make(map[string]string, len(b.Accounts))
	for k, v := range b.Accounts {
		accts[k] = v
	}
	b.Accounts = accts
	return &b
}

// Cancel 取消当前批次（进行中的线路跑完或标记 canceled，已写回结果保留）。
func (s *Service) Cancel() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancelFn == nil || s.batch == nil || !s.batch.Running {
		return false
	}
	s.batch.Canceled = true
	s.cancelFn()
	return true
}

// AccountRoutes 返回某账号的线路明细（已完成账号）。
func (s *Service) AccountRoutes(accountID string) (map[string]model.PaymentRouteResult, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.batch == nil {
		return nil, false
	}
	item, ok := s.batch.Items[accountID]
	if !ok || item == nil {
		return nil, false
	}
	return item.Routes, true
}

// BatchItems 返回当前批次全部完成账号的明细（前端结果矩阵拉取用）。
// 返回的是深拷贝值（非指针），避免与批次 goroutine 并发读写同一 map。
func (s *Service) BatchItems() map[string]Item {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]Item)
	if s.batch == nil {
		return out
	}
	for k, v := range s.batch.Items {
		if v == nil {
			continue
		}
		// 深拷贝 Routes map，防止前端 JSON 序列化时后端仍在写。
		cp := *v
		routes := make(map[string]model.PaymentRouteResult, len(v.Routes))
		for rk, rv := range v.Routes {
			routes[rk] = rv
		}
		cp.Routes = routes
		out[k] = cp
	}
	return out
}

// runBatch 是批次主循环（后台 goroutine）。
func (s *Service) runBatch(ctx context.Context, batch *Batch, ids []string, routes []Route, writeBack bool) {
	defer func() {
		s.mu.Lock()
		now := time.Now().UTC()
		batch.Running = false
		batch.FinishedAt = &now
		s.active = false
		s.cancelFn = nil
		s.mu.Unlock()
	}()

	// 账号级并发上限：min(maxConcurrency, 可用代理数)（对齐 plancheck 自适应）。
	available, _ := s.proxies.CountEligibleProxies(ctx, "", "")
	conc := s.maxConcurrency
	if available > 0 && available < conc {
		conc = available
	}
	if conc < 1 {
		conc = 1
	}

	// 全局速率槽：所有账号的线路启动共享该通道（对齐文档 §4 的 minimum interval）。
	rateCh := make(chan struct{}, 1)
	rateCh <- struct{}{} // 首个令牌立即放行

	var wg sync.WaitGroup
	sem := make(chan struct{}, conc)
	for _, id := range ids {
		wg.Add(1)
		go func(accountID string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			// 标记 running，让前端进度条区分"排队中"与"检测中"。
			s.mu.Lock()
			batch.Accounts[accountID] = "running"
			s.mu.Unlock()

			item := s.checkOne(ctx, accountID, routes, writeBack, rateCh)
			s.mu.Lock()
			batch.Items[accountID] = item
			switch item.Status {
			case "skipped":
				batch.Skipped++
				batch.Accounts[accountID] = "skipped"
			case "failed":
				batch.Accounts[accountID] = "failed"
			default:
				batch.Accounts[accountID] = "done"
			}
			batch.Done++
			if len(item.ZeroMethods) > 0 {
				batch.ZeroCount++
			}
			s.mu.Unlock()
		}(id)
	}
	wg.Wait()
}

// runBatchTokens 是粘贴 AT 模式的批次主循环（不落库、不 claim、不写回）。
func (s *Service) runBatchTokens(ctx context.Context, batch *Batch, ids []string, tokens []TokenInput, routes []Route) {
	defer func() {
		s.mu.Lock()
		now := time.Now().UTC()
		batch.Running = false
		batch.FinishedAt = &now
		s.active = false
		s.cancelFn = nil
		s.mu.Unlock()
	}()

	available, _ := s.proxies.CountEligibleProxies(ctx, "", "")
	conc := s.maxConcurrency
	if available > 0 && available < conc {
		conc = available
	}
	if conc < 1 {
		conc = 1
	}

	rateCh := make(chan struct{}, 1)
	rateCh <- struct{}{}

	// ids[i] ↔ tokens[i] 已在 Run() 单次过滤配对，这里直接用索引（不再二次过滤）。
	var wg sync.WaitGroup
	sem := make(chan struct{}, conc)
	for i, id := range ids {
		if i >= len(tokens) {
			break
		}
		wg.Add(1)
		go func(accountID string, tok TokenInput) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			// 标记 running（进度条区分排队/检测中）。
			s.mu.Lock()
			batch.Accounts[accountID] = "running"
			s.mu.Unlock()

			item := s.checkOneToken(ctx, accountID, tok, routes, rateCh)
			s.mu.Lock()
			batch.Items[accountID] = item
			batch.Accounts[accountID] = "done"
			batch.Done++
			if len(item.ZeroMethods) > 0 {
				batch.ZeroCount++
			}
			s.mu.Unlock()
		}(id, tokens[i])
	}
	wg.Wait()
}

// checkOneToken 检测一条粘贴的 AT（不 claim、不写回、不落库）。
func (s *Service) checkOneToken(ctx context.Context, id string, tok TokenInput, routes []Route, rateCh chan struct{}) *Item {
	label := tok.Label
	if label == "" {
		raw := strings.TrimSpace(tok.AccessToken)
		if len(raw) > 16 {
			label = raw[:8] + "…" + raw[len(raw)-4:]
		} else {
			label = raw
		}
	}
	item := &Item{ID: id, Email: label, Routes: map[string]model.PaymentRouteResult{}}

	for _, route := range routes {
		if ctx.Err() != nil {
			item.Routes[route.Country] = model.PaymentRouteResult{
				Country:   route.Country,
				Currency:  route.Currency,
				Locale:    route.Locale,
				Status:    StatusCanceled,
				State:     "batch_canceled",
				CheckedAt: time.Now().UTC(),
			}
			continue
		}
		s.waitRateSlot(ctx, rateCh)
		if ctx.Err() != nil {
			item.Routes[route.Country] = model.PaymentRouteResult{
				Country:   route.Country,
				Currency:  route.Currency,
				Locale:    route.Locale,
				Status:    StatusCanceled,
				State:     "batch_canceled",
				CheckedAt: time.Now().UTC(),
			}
			continue
		}
		rr := s.checkRouteToken(ctx, id, tok.AccessToken, route)
		item.Routes[route.Country] = rr
		if IsTerminalStatus(rr.Status) {
			break
		}
	}
	summary := Aggregate(routeResultsOf(item.Routes), len(routes))
	item.Status = summary.Status
	item.Methods = summary.Methods
	item.ZeroMethods = summary.ZeroMethods
	s.emit(signup.LogInfo, "paymentcheck_token_done",
		fmt.Sprintf("粘贴AT检测完成 status=%s methods=%v zero=%v", summary.Status, summary.Methods, summary.ZeroMethods),
		label, map[string]any{"status": summary.Status})
	return item
}

// checkRouteToken 对裸 token 执行单条线路（与 checkRoute 同流程，来源是 TokenInput）。
func (s *Service) checkRouteToken(ctx context.Context, ownerID, accessToken string, route Route) model.PaymentRouteResult {
	result := model.PaymentRouteResult{
		Country:   route.Country,
		Currency:  route.Currency,
		Locale:    route.Locale,
		CheckedAt: time.Now().UTC(),
	}

	owner := "paymentcheck:" + ownerID + ":" + randHexStr(4)
	lease, err := s.proxies.AcquireProxy(ctx, owner, nil, 180, route.Country, "")
	if err != nil || lease == nil {
		result.Status = StatusProxyUnavail
		result.State = "no_eligible_proxy"
		result.Error = "该国线路无可用代理"
		return result
	}
	defer func() { _ = s.proxies.ReturnProxy(ctx, lease.ID, owner) }()
	proxyURL := lease.ProxyURL()

	exit, err := s.prober.ProbeExit(ctx, proxyURL, 8)
	if err != nil || exit == nil {
		result.Status = StatusProxyUnavail
		result.State = "exit_probe_failed"
		result.Error = "出口检测失败"
		return result
	}
	result.ExitIP = exit.IP
	result.ExitCountry = exit.Country
	result.ExitPurity = exit.Purity
	if exit.Country != route.Country {
		result.Status = StatusProxyMismatch
		result.State = "proxy_country_mismatch"
		result.Error = fmt.Sprintf("出口国家 %s ≠ 线路国家 %s", exit.Country, route.Country)
		return result
	}

	sess, serr := s.newSession(proxyURL, s.sessionTimeout)
	if serr != nil {
		result.Status = StatusCheckoutFailed
		result.State = "session_failed"
		result.Error = sanitizeError(serr.Error())
		return result
	}
	if closer, ok := sess.(interface{ Close() }); ok {
		defer closer.Close()
	}

	out, cerr := runCheckout(ctx, sess, accessToken, route, s.sentinelProvider(), randHexStr(16))
	if cerr != nil {
		result.Status = cerr.Status
		result.State = cerr.Status
		result.HTTPStatus = cerr.HTTPStatus
		result.Error = cerr.Message
		return result
	}

	result.SessionType = out.SessionType
	result.ProcessorEntity = out.ProcessorEntity
	result.Methods = out.Methods
	result.MethodsInferred = out.MethodsInferred
	result.AmountDue = out.AmountDue
	result.ZeroStatus = out.ZeroStatus
	result.HTTPStatus = out.HTTPStatus
	if len(out.Methods) > 0 || len(out.MethodsInferred) > 0 {
		result.Status = StatusAvailable
		result.State = "payment_methods_available"
	} else {
		result.Status = StatusNotReturned
		result.State = "payment_methods_empty"
	}
	return result
}

// checkOne 检测单个账号（claim → 逐线路串行 → 聚合写回）。
func (s *Service) checkOne(ctx context.Context, accountID string, routes []Route, writeBack bool, rateCh chan struct{}) *Item {
	src, err := s.accounts.ClaimPaymentCheck(ctx, accountID, s.staleMinutes)
	if err != nil || src == nil {
		s.emit(signup.LogWarning, "paymentcheck_skipped", "支付检测跳过(无token或被认领)", "", map[string]any{"accountId": accountID})
		return &Item{ID: accountID, Status: "skipped", Error: "account_missing_token_or_busy"}
	}
	item := &Item{ID: accountID, Email: src.Email, Routes: map[string]model.PaymentRouteResult{}}

	for _, route := range routes {
		// 批次取消：剩余线路标记 canceled（对齐文档 §4 Context 取消语义）。
		if ctx.Err() != nil {
			item.Routes[route.Country] = model.PaymentRouteResult{
				Country:   route.Country,
				Currency:  route.Currency,
				Locale:    route.Locale,
				Status:    StatusCanceled,
				State:     "batch_canceled",
				CheckedAt: time.Now().UTC(),
			}
			continue
		}
		// 全局速率槽：等待令牌 → 间隔+抖动后放行下一个。
		s.waitRateSlot(ctx, rateCh)
		if ctx.Err() != nil {
			item.Routes[route.Country] = model.PaymentRouteResult{
				Country:   route.Country,
				Currency:  route.Currency,
				Locale:    route.Locale,
				Status:    StatusCanceled,
				State:     "batch_canceled",
				CheckedAt: time.Now().UTC(),
			}
			continue
		}

		rr := s.checkRoute(ctx, src, route)
		item.Routes[route.Country] = rr
		if writeBack {
			_ = s.accounts.StorePaymentRouteResult(ctx, accountID, rr)
		}

		// 账号级熔断：token_invalid / already_paid 终止剩余线路（对齐文档 §14）。
		if IsTerminalStatus(rr.Status) {
			break
		}
	}

	summary := Aggregate(routeResultsOf(item.Routes), len(routes))
	item.Status = summary.Status
	item.Methods = summary.Methods
	item.ZeroMethods = summary.ZeroMethods

	if writeBack {
		_ = s.accounts.StorePaymentSummary(ctx, accountID, store.PaymentSummaryUpdate{
			Status:       summary.Status,
			Methods:      summary.Methods,
			ZeroMethods:  summary.ZeroMethods,
			CheckedAt:    summary.CheckedAt,
			TokenInvalid: summary.TokenInvalid,
		})
	}
	s.emit(signup.LogInfo, "paymentcheck_done",
		fmt.Sprintf("支付检测完成 status=%s methods=%v zero=%v", summary.Status, summary.Methods, summary.ZeroMethods),
		src.Email, map[string]any{"accountId": accountID, "status": summary.Status})
	return item
}

// checkRoute 执行单条线路：租代理 → 出口校验 → 建单读取。
func (s *Service) checkRoute(ctx context.Context, src *store.AccountDocument, route Route) model.PaymentRouteResult {
	result := model.PaymentRouteResult{
		Country:  route.Country,
		Currency: route.Currency,
		Locale:   route.Locale,
		CheckedAt: time.Now().UTC(),
	}

	// 1) 租代理：优先线路国家（代理池按国家分组），租不到 → 线路失败。
	owner := "paymentcheck:" + src.ID + ":" + randHexStr(4)
	lease, err := s.proxies.AcquireProxy(ctx, owner, nil, 180, route.Country, "")
	if err != nil || lease == nil {
		result.Status = StatusProxyUnavail
		result.State = "no_eligible_proxy"
		result.Error = "该国线路无可用代理"
		return result
	}
	defer func() { _ = s.proxies.ReturnProxy(ctx, lease.ID, owner) }()
	proxyURL := lease.ProxyURL()

	// 2) 出口校验：国家不符 → 熔断该线路（文档 §6.1 红线）。
	exit, err := s.prober.ProbeExit(ctx, proxyURL, 8)
	if err != nil || exit == nil {
		result.Status = StatusProxyUnavail
		result.State = "exit_probe_failed"
		result.Error = "出口检测失败"
		return result
	}
	result.ExitIP = exit.IP
	result.ExitCountry = exit.Country
	result.ExitPurity = exit.Purity
	if exit.Country != route.Country {
		result.Status = StatusProxyMismatch
		result.State = "proxy_country_mismatch"
		result.Error = fmt.Sprintf("出口国家 %s ≠ 线路国家 %s", exit.Country, route.Country)
		return result
	}

	// 3) 独立 core.Session（每线路新指纹）→ 建单 + 读取。
	sess, serr := s.newSession(proxyURL, s.sessionTimeout)
	if serr != nil {
		result.Status = StatusCheckoutFailed
		result.State = "session_failed"
		result.Error = sanitizeError(serr.Error())
		return result
	}
	if closer, ok := sess.(interface{ Close() }); ok {
		defer closer.Close()
	}

	deviceID := randHexStr(16)
	out, cerr := runCheckout(ctx, sess, src.AccessToken, route, s.sentinelProvider(), deviceID)
	if cerr != nil {
		result.Status = cerr.Status
		result.State = cerr.Status
		result.HTTPStatus = cerr.HTTPStatus
		result.Error = cerr.Message
		return result
	}

	result.SessionType = out.SessionType
	result.ProcessorEntity = out.ProcessorEntity
	result.Methods = out.Methods
	result.MethodsInferred = out.MethodsInferred
	result.AmountDue = out.AmountDue
	result.ZeroStatus = out.ZeroStatus
	result.HTTPStatus = out.HTTPStatus
	if len(out.Methods) > 0 || len(out.MethodsInferred) > 0 {
		result.Status = StatusAvailable
		result.State = "payment_methods_available"
	} else {
		result.Status = StatusNotReturned
		result.State = "payment_methods_empty"
	}
	return result
}

// sentinelProvider 把注入的 solver 适配为 sentinelHeaderProvider。
func (s *Service) sentinelProvider() sentinelHeaderProvider {
	if s.solver == nil {
		return nil
	}
	return &solverHeaderProvider{solver: s.solver}
}

// solverHeaderProvider 用 V8Solver 产出 AI Platform-sentinel-token（best-effort）。
type solverHeaderProvider struct{ solver SentinelSolver }

func (p *solverHeaderProvider) CheckoutHeaders(ctx context.Context, env signupsentinel.EnvPayload) map[string]string {
	res, err := p.solver.GetToken(ctx, env, signupsentinel.FlowAuthorizeContinue)
	if err != nil || res == nil || res.Token == "" {
		return nil
	}
	h := map[string]string{"AI Platform-sentinel-token": res.Token}
	if res.SOToken != "" {
		h["AI Platform-sentinel-so-token"] = res.SOToken
	}
	return h
}

// waitRateSlot 全局速率槽：取令牌 → 间隔+抖动后把令牌传给下一个等待者。
// 关键不变量：无论走哪条路径返回，令牌必须回到通道（否则后续线路永久阻塞）。
func (s *Service) waitRateSlot(ctx context.Context, rateCh chan struct{}) {
	select {
	case <-ctx.Done():
		return
	case <-rateCh:
	}
	// 已持有令牌：无论计时器完成还是 ctx 取消，都要把令牌放回。
	defer func() {
		select {
		case rateCh <- struct{}{}:
		default:
		}
	}()
	wait := s.routeInterval
	if s.routeJitter > 0 {
		wait += time.Duration(s.rng.Int63n(int64(s.routeJitter)))
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

// emit 写活动日志（nil 安全）。
func (s *Service) emit(level signup.LogLevel, event, message, email string, details map[string]any) {
	if s.rlog != nil {
		s.rlog.Emit(level, event, message, email, details)
	}
}

// ── 小工具 ──

func dedupStrings(ids []string) []string {
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

func randHexStr(n int) string {
	const hexd = "0123456789abcdef"
	b := make([]byte, n)
	for i := range b {
		b[i] = hexd[rand.Intn(16)]
	}
	return string(b)
}

func routeResultsOf(m map[string]model.PaymentRouteResult) []model.PaymentRouteResult {
	out := make([]model.PaymentRouteResult, 0, len(m))
	for _, r := range m {
		out = append(out, r)
	}
	return out
}