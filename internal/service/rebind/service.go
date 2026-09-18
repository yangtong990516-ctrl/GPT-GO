// Package rebind 实现「邮箱换绑」服务：用注册协议登录拿 access_token，
// 走 change_email 三端点把账号邮箱切到新邮箱，新邮箱重登确认后写回账号池。
//
// 架构（对齐 codex-auto account_rebind_service + GPT-GO paymentcheck 模式）：
//
//	三层并发：批次全局互斥 → 账号级并发（信号量，上限 min(maxConcurrency, 可用代理数)）
//	  → 单账号内串行（登录→eligibility→begin→接码→verify→重登确认）；
//	  换绑是账号级写操作，同账号绝不并发。
//
//	单账号流程：认领账号 → 预留新邮箱（原子，防并发重复分配）→ 租代理 →
//	  authflow.RunLogin 旧邮箱登录 → CheckEligibility → BeginChangeEmail（发码）→
//	  AccessURLProvider 接码 → VerifyChangeEmail（不可逆点）→ MarkRebindEmailChanged
//	  （立即落库新邮箱）→ 新邮箱重登确认 → MarkRebindSuccess（新 AT 落库）→
//	  ConsumeRebindEmail（邮箱置 used）。
//
// 边界红线：任一失败释放邮箱预留（回 available）；换绑已不可逆但重登失败时
// 保留 email_changed_token_pending 状态供人工补 AT；错误脱敏 ≤120 字符。
package rebind

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	crand "crypto/rand"
	"time"

	"gpt-go/internal/service/signup"
	"gpt-go/internal/service/signup/authflow"
	"gpt-go/internal/service/signup/core"
	"gpt-go/internal/service/signup/otp"
	"gpt-go/internal/store"
)

// ErrBusy 表示已有换绑批次在运行（全局互斥）。
var ErrBusy = errors.New("已有换绑批次在运行")

// ErrNoAccounts 表示批次账号列表为空。
var ErrNoAccounts = errors.New("换绑账号列表为空")

// AccountStore 抽象账号存储（store.AccountStore 满足）。
type AccountStore interface {
	ClaimRebind(ctx context.Context, id string, staleMin int) (*store.AccountDocument, error)
	MarkRebindEmailChanged(ctx context.Context, id string, u store.RebindEmailChangedUpdate) error
	MarkRebindSuccess(ctx context.Context, id string, u store.RebindSuccessUpdate) error
	MarkRebindFailure(ctx context.Context, id, errCode string) error
}

// EmailStore 抽象邮箱存储（store.EmailStore 满足）。
type EmailStore interface {
	ReserveForRebind(ctx context.Context, runID string) (*store.EmailDocument, error)
	ReserveForRebindByID(ctx context.Context, emailID, runID string) (*store.EmailDocument, error)
	ConsumeRebindEmail(ctx context.Context, emailID, runID string) (bool, error)
	ReleaseRebindReservation(ctx context.Context, emailID, runID string) (bool, error)
}

// ProxyStore 抽象代理租约（signup.ProxyServiceAdapter 满足）。
type ProxyStore interface {
	CountEligibleProxies(ctx context.Context, country, group string) (int, error)
	AcquireProxy(ctx context.Context, owner string, excludedIDs []string, leaseSeconds int, country, group string) (*signup.ProxyLease, error)
	ReturnProxy(ctx context.Context, proxyID, owner string) error
}

// RunLogger 抽象活动日志（复用 signup.RunLogger）。
type RunLogger interface {
	Emit(level signup.LogLevel, event, message, email string, details map[string]any)
}

// flowFactory 抽象 authflow 构造（测试注入替身）。
type flowFactory func(cfg *signup.Config) (loginFlow, error)

// loginFlow 抽象登录流程（authflow.Flow 满足）。
type loginFlow interface {
	RunLogin(ctx context.Context, p authflow.LoginParams) (*signup.AuthResult, error)
	Boot() *core.Bootstrap
	Close()
}

// Item 是单账号换绑完成后的结果项。
type Item struct {
	ID          string
	OldEmail    string
	NewEmail    string
	Status      string // success/failed/canceled
	Error       string
	Proxy       string // 脱敏后的代理
	StartedAt   time.Time
	CompletedAt time.Time
}

// Batch 是批次实时状态（前端轮询用）。
type Batch struct {
	ID         string            `json:"batchId"`
	Total      int               `json:"total"`
	Done       int               `json:"done"`
	Success    int               `json:"success"`
	Failed     int               `json:"failed"`
	Running    bool              `json:"running"`
	Canceled   bool              `json:"canceled"`
	Accounts   map[string]string `json:"accounts"` // accountID → pending/running/done/failed/skipped
	Items      map[string]*Item  `json:"-"`
	StartedAt  time.Time         `json:"startedAt"`
	FinishedAt *time.Time        `json:"finishedAt,omitempty"`
	rlog       RunLogger         // 批次专属日志流（runID=batch.ID）
}

// Service 是换绑服务。
type Service struct {
	accounts AccountStore
	emails   EmailStore
	proxies  ProxyStore
	rlog     RunLogger // 全局兜底日志流（runID="rebind"）
	logHub   *signup.RunLogHub

	maxConcurrency int
	staleMinutes   int
	sessionTimeout time.Duration
	otpTimeout     int

	mu       sync.Mutex
	active   bool
	batch    *Batch
	cancelFn context.CancelFunc

	newFlow flowFactory
}

// Option 是 Service 的可选配置。
type Option func(*Service)

// WithRunLogger 注入活动日志（复用 signup logHub）。
func WithRunLogger(l RunLogger) Option {
	return func(s *Service) { s.rlog = l }
}

// WithLogHub 注入日志中心（每批次独立流用）。
func WithLogHub(h *signup.RunLogHub) Option {
	return func(s *Service) { s.logHub = h }
}

// WithFlowFactory 注入 authflow 构造替身（测试用）。
func WithFlowFactory(f flowFactory) Option {
	return func(s *Service) { s.newFlow = f }
}

// New 构造换绑服务。
func New(accounts AccountStore, emails EmailStore, proxies ProxyStore, opts ...Option) *Service {
	s := &Service{
		accounts:       accounts,
		emails:         emails,
		proxies:        proxies,
		maxConcurrency: 2, // 换绑是账号级写操作，默认并发 2（对齐 codex account_rebind_concurrency）
		staleMinutes:   5,
		sessionTimeout: 45 * time.Second,
		otpTimeout:     240, // 换绑接码超时（对齐 Python timeout=240）
	}
	for _, o := range opts {
		o(s)
	}
	if s.newFlow == nil {
		s.newFlow = s.defaultFlow
	}
	return s
}

// RunParams 是批次入参。
type RunParams struct {
	AccountIDs []string
	Country    string            // 代理国家筛选（空 = 任意）
	Group      string            // 代理分组筛选（空 = 任意）
	// Pairs 是前端配对关系（accountID → emailID）；有配对时按 ID 预留，
	// 无配对时回退自动分配（ReserveForRebind）。
	Pairs map[string]string
}

// Run 启动换绑批次（异步：批次在后台 goroutine 执行，立即返回批次 ID）。
func (s *Service) Run(ctx context.Context, p RunParams) (string, error) {
	s.mu.Lock()
	if s.active {
		s.mu.Unlock()
		return "", ErrBusy
	}
	ids := dedupStrings(p.AccountIDs)
	if len(ids) == 0 {
		s.mu.Unlock()
		return "", ErrNoAccounts
	}
	s.active = true
	batch := &Batch{
		ID:        fmt.Sprintf("rb-%d", time.Now().UnixNano()),
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
	// 每批次独立日志流（runID=batch.ID，前端可按批次查日志）。
	if s.logHub != nil {
		batch.rlog = s.logHub.Logger(batch.ID)
	}
	s.mu.Unlock()

	go s.runBatch(batchCtx, batch, ids, p.Country, p.Group, p.Pairs)
	return batch.ID, nil
}

// Status 返回当前批次快照（深拷贝，无批次返回 nil）。
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

// Cancel 取消当前批次（进行中的账号跑完当前步骤后停止）。
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

// BatchItems 返回完成账号的明细（深拷贝，前端结果矩阵用）。
func (s *Service) BatchItems() map[string]Item {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]Item)
	if s.batch == nil {
		return out
	}
	for k, v := range s.batch.Items {
		if v != nil {
			out[k] = *v
		}
	}
	return out
}

// runBatch 批次主循环（后台 goroutine）。
func (s *Service) runBatch(ctx context.Context, batch *Batch, ids []string, country, group string, pairs map[string]string) {
	defer func() {
		s.mu.Lock()
		now := time.Now().UTC()
		batch.Running = false
		batch.FinishedAt = &now
		s.active = false
		s.cancelFn = nil
		s.mu.Unlock()
	}()

	available, _ := s.proxies.CountEligibleProxies(ctx, country, group)
	conc := s.maxConcurrency
	if available > 0 && available < conc {
		conc = available
	}
	if conc < 1 {
		conc = 1
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, conc)
	for _, id := range ids {
		wg.Add(1)
		go func(accountID string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			s.mu.Lock()
			batch.Accounts[accountID] = "running"
			s.mu.Unlock()

			item := s.rebindOne(ctx, accountID, country, group, pairs[accountID])
			s.mu.Lock()
			batch.Items[accountID] = item
			switch item.Status {
			case "success":
				batch.Success++
				batch.Accounts[accountID] = "done"
			case "canceled":
				batch.Accounts[accountID] = "skipped"
			default:
				batch.Failed++
				batch.Accounts[accountID] = "failed"
			}
			batch.Done++
			s.mu.Unlock()
		}(id)
	}
	wg.Wait()
}

// rebindOne 单账号换绑全流程（认领→预留邮箱→租代理→登录→换绑→确认→落库）。
func (s *Service) rebindOne(ctx context.Context, accountID, country, group, preferredEmailID string) *Item {
	item := &Item{ID: accountID, StartedAt: time.Now().UTC()}
	defer func() { item.CompletedAt = time.Now().UTC() }()

	// 1) 认领账号（原子：rebindStatus=running，卡死 5min 可重认领）。
	account, err := s.accounts.ClaimRebind(ctx, accountID, s.staleMinutes)
	if err != nil || account == nil {
		item.Status = "canceled"
		item.Error = "account_busy_or_missing"
		return item
	}
	item.OldEmail = account.Email

	// 失败兜底：未成功前任何返回都落 rebindStatus=failed + rebindError。
	success := false
	defer func() {
		if !success && item.Status != "canceled" {
			_ = s.accounts.MarkRebindFailure(context.Background(), accountID, item.Error)
		}
	}()

	// 2) 预留新邮箱（优先按前端配对 ID 预留；无配对回退自动分配）。
	runID := fmt.Sprintf("rebind-%s-%s", accountID, randHexStr(6))
	var mailbox *store.EmailDocument
	if preferredEmailID != "" {
		mailbox, err = s.emails.ReserveForRebindByID(ctx, preferredEmailID, runID)
		if err != nil || mailbox == nil {
			item.Status = "failed"
			item.Error = "paired_mailbox_unavailable"
			s.emit(signup.LogWarning, "rebind_paired_mailbox_unavailable", "配对邮箱已被占用或不存在", account.Email, map[string]any{"pairedEmailId": preferredEmailID})
			return item
		}
	} else {
		mailbox, err = s.emails.ReserveForRebind(ctx, runID)
		if err != nil || mailbox == nil {
			item.Status = "failed"
			item.Error = "no_available_mailbox"
			s.emit(signup.LogWarning, "rebind_no_mailbox", "邮箱池无可用邮箱", account.Email, nil)
			return item
		}
	}
	item.NewEmail = mailbox.Email
	mailboxReserved := true
	defer func() {
		// 未消费前任何返回都释放预留（回 available）。
		if mailboxReserved {
			_, _ = s.emails.ReleaseRebindReservation(context.Background(), mailbox.ID, runID)
		}
	}()

	// 3) 租代理（优先批次指定的国家/分组）。
	owner := "rebind:" + accountID + ":" + randHexStr(4)
	lease, err := s.proxies.AcquireProxy(ctx, owner, nil, 300, country, group)
	if err != nil || lease == nil {
		item.Status = "failed"
		item.Error = "no_eligible_proxy"
		s.emit(signup.LogWarning, "rebind_no_proxy", "无可用代理", account.Email, nil)
		return item
	}
	defer func() { _ = s.proxies.ReturnProxy(context.Background(), lease.ID, owner) }()
	proxyURL := lease.ProxyURL()
	item.Proxy = maskProxy(proxyURL)

	// 4) 旧邮箱登录（authflow 完整链路拿 access_token + cookie + device_id）。
	s.emit(signup.LogInfo, "rebind_login_old", "正在登录旧邮箱", account.Email, nil)
	oldOTP := NewAccessURLProvider(account.EmailAccessURL, 30*time.Second)
	oldFlow, oldResult, err := s.loginAccount(ctx, account.Email, account.ChatgptPassword, account.TotpSecret, oldOTP, proxyURL)
	if err != nil {
		item.Status = "failed"
		item.Error = "old_login:" + sanitizeErr(err)
		s.emit(signup.LogError, "rebind_old_login_failed", "旧邮箱登录失败", account.Email, map[string]any{"error": item.Error})
		return item
	}
	defer oldFlow.Close()
	if !oldResult.IsValid() {
		item.Status = "failed"
		item.Error = "old_session_invalid"
		return item
	}

	// 5) 换绑三端点：eligibility → begin → 接码 → verify。
	sess := oldFlow.Boot().Session
	sessionID := randHexStr(16)
	accountIDFromJWT := DecodeAccountID(oldResult.AccessToken)

	s.emit(signup.LogInfo, "rebind_eligibility", "检查换绑资格", account.Email, nil)
	if err := CheckEligibility(ctx, sess, oldResult.AccessToken, oldResult.DeviceID, sessionID, accountIDFromJWT, oldResult.CookieHeader); err != nil {
		item.Status = "failed"
		item.Error = err.Error()
		s.emit(signup.LogError, "rebind_ineligible", "换绑资格检查失败", account.Email, map[string]any{"error": item.Error})
		return item
	}

	s.emit(signup.LogInfo, "rebind_begin", "向新邮箱发送验证码", mailbox.Email, nil)
	issuedAt, err := BeginChangeEmail(ctx, sess, oldResult.AccessToken, oldResult.DeviceID, sessionID, accountIDFromJWT, oldResult.CookieHeader, mailbox.Email)
	if err != nil {
		item.Status = "failed"
		item.Error = err.Error()
		s.emit(signup.LogError, "rebind_begin_failed", "发送换绑验证码失败", account.Email, map[string]any{"error": item.Error})
		return item
	}

	// 6) 新邮箱接码（AccessURLProvider 带 issuedAfter 时间窗防旧码）。
	s.emit(signup.LogInfo, "rebind_wait_code", "等待新邮箱验证码", mailbox.Email, nil)
	newOTP := NewAccessURLProvider(mailbox.AccessURL, 30*time.Second)
	code, err := newOTP.WaitForOTP(ctx, mailbox.Email, s.otpTimeout, issuedAt)
	if err != nil {
		item.Status = "failed"
		item.Error = "otp:" + sanitizeErr(err)
		s.emit(signup.LogError, "rebind_otp_failed", "接收换绑验证码失败", mailbox.Email, map[string]any{"error": item.Error})
		return item
	}

	// 7) verify（不可逆点）：远端邮箱已切换。
	s.emit(signup.LogInfo, "rebind_verify", "确认换绑验证码", mailbox.Email, nil)
	if err := VerifyChangeEmail(ctx, sess, oldResult.AccessToken, oldResult.DeviceID, sessionID, accountIDFromJWT, oldResult.CookieHeader, mailbox.Email, code); err != nil {
		item.Status = "failed"
		item.Error = err.Error()
		s.emit(signup.LogError, "rebind_verify_failed", "确认换绑验证码失败", mailbox.Email, map[string]any{"error": item.Error})
		return item
	}
	Logout(ctx, sess)

	// 8) 立即落库新邮箱（对齐 Python mark_rebind_email_changed：不可逆点先落库再确认）。
	reboundAt := time.Now().UTC()
	_ = s.accounts.MarkRebindEmailChanged(ctx, accountID, store.RebindEmailChangedUpdate{
		NewEmail:      mailbox.Email,
		NewAccessURL:  mailbox.AccessURL,
		PreviousEmail: account.Email,
		ProxyCountry:  lease.Country,
		ReboundAt:     reboundAt,
	})
	s.emit(signup.LogInfo, "rebind_changed", "新邮箱已写入账号池", mailbox.Email, nil)

	// 9) 新邮箱重登确认 + 刷新 AT。
	s.emit(signup.LogInfo, "rebind_confirm_login", "新邮箱重登确认", mailbox.Email, nil)
	newFlow, newResult, err := s.loginAccount(ctx, mailbox.Email, account.ChatgptPassword, account.TotpSecret, newOTP, proxyURL)
	if err != nil {
		// 换绑已不可逆，但重登失败：保留 email_changed_token_pending 供人工补 AT。
		item.Status = "failed"
		item.Error = "confirm_login:" + sanitizeErr(err)
		s.emit(signup.LogError, "rebind_confirm_failed", "新邮箱重登失败（换绑已生效，需人工补 AT）", mailbox.Email, map[string]any{"error": item.Error})
		return item
	}
	defer newFlow.Close()
	if !newResult.IsValid() {
		item.Status = "failed"
		item.Error = "confirm_session_invalid"
		return item
	}
	confirmedEmail := strings.ToLower(strings.TrimSpace(newResult.Email))
	if confirmedEmail != "" && confirmedEmail != strings.ToLower(mailbox.Email) {
		item.Status = "failed"
		item.Error = "rebind_confirmation_email_mismatch"
		return item
	}

	// 10) 成功落库：新 AT + rebindStatus=success。
	_ = s.accounts.MarkRebindSuccess(ctx, accountID, store.RebindSuccessUpdate{
		RebindEmailChangedUpdate: store.RebindEmailChangedUpdate{
			NewEmail:      mailbox.Email,
			NewAccessURL:  mailbox.AccessURL,
			PreviousEmail: account.Email,
			ProxyCountry:  lease.Country,
			ReboundAt:     reboundAt,
		},
		AccessToken:          newResult.AccessToken,
		AccessTokenExpiresAt: AccessTokenExpiry(newResult.AccessToken),
	})

	// 11) 消费邮箱（置 used，防二次分配）。
	consumed, _ := s.emails.ConsumeRebindEmail(ctx, mailbox.ID, runID)
	if consumed {
		mailboxReserved = false // 已消费，defer 不再释放
	}

	item.Status = "success"
	success = true
	s.emit(signup.LogInfo, "rebind_success", fmt.Sprintf("换绑成功：%s → %s", account.Email, mailbox.Email), mailbox.Email, nil)
	return item
}

// loginAccount 用 authflow 登录账号拿 access_token（对齐 _login_via_authflow）。
func (s *Service) loginAccount(ctx context.Context, email, password, totpSecret string, mail otp.Provider, proxyURL string) (loginFlow, *signup.AuthResult, error) {
	cfg := &signup.Config{Proxy: proxyURL}
	flow, err := s.newFlow(cfg)
	if err != nil {
		return nil, nil, err
	}
	result, err := flow.RunLogin(ctx, authflow.LoginParams{
		Email:      email,
		Password:   password,
		TotpSecret: totpSecret,
		Mail:       mail,
		OTPTimeout: s.otpTimeout,
	})
	if err != nil {
		flow.Close()
		return nil, nil, err
	}
	return flow, result, nil
}

// defaultFlow 默认 authflow 构造（生产路径）。
func (s *Service) defaultFlow(cfg *signup.Config) (loginFlow, error) {
	return authflow.New(cfg)
}

// emit 写活动日志（优先走批次专属流，无批次时走全局流；nil 安全）。
func (s *Service) emit(level signup.LogLevel, event, message, email string, details map[string]any) {
	s.mu.Lock()
	b := s.batch
	s.mu.Unlock()
	if b != nil && b.rlog != nil {
		b.rlog.Emit(level, event, message, email, details)
		return
	}
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
	raw := make([]byte, n)
	if _, err := crand.Read(raw); err != nil {
		// 兜底：纳秒扰动（生产不应走到）。
		for i := range raw {
			raw[i] = byte(time.Now().UnixNano() >> (i % 8))
		}
	}
	b := make([]byte, n)
	for i := range b {
		b[i] = hexd[raw[i]%16]
	}
	return string(b)
}

// maskProxy 脱敏代理 URL（对齐 Python _masked_proxy：隐去认证信息）。
func maskProxy(proxyURL string) string {
	if idx := strings.Index(proxyURL, "@"); idx > 0 {
		scheme := ""
		if sidx := strings.Index(proxyURL, "://"); sidx > 0 {
			scheme = proxyURL[:sidx+3]
		}
		return scheme + "***@" + proxyURL[idx+1:]
	}
	return proxyURL
}
