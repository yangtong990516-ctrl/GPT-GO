// Account storage contract and in-memory mock. Mirrors resource_service.py
// list_accounts / create_account / delete_accounts against the accounts collection.
package store

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"gpt-go/internal/model"
)

// AccountDocument is the persisted account shape (Mongo document). It holds the
// raw stored fields; the JSON projection happens in the service layer.
type AccountDocument struct {
	ID                    string                              `bson:"_id"`
	Email                 string                              `bson:"email"`
	EmailNormalized       string                              `bson:"emailNormalized"`
	ChatgptPassword       string                              `bson:"chatgptPassword"`
	// PasswordConfiguredAt 补密码分支新密码生效时间(对齐 codex passwordConfiguredAt);nil=未补过密码。
	PasswordConfiguredAt  *time.Time                          `bson:"passwordConfiguredAt"`
	TotpSecret            string                              `bson:"totpSecret"`
	TotpStatus            *string                             `bson:"totpStatus"`
	EmailAccessURL        string                              `bson:"emailAccessUrl"`
	CreatedAt             time.Time                           `bson:"createdAt"`
	AccountType           string                              `bson:"accountType"`
	PhoneBound            *bool                               `bson:"phoneBound"`
	PromotionEligible     *bool                               `bson:"promotionEligible"`
	AccessTokenConfigured bool                                `bson:"accessTokenConfigured"`
	AccessTokenExpiresAt  *time.Time                          `bson:"accessTokenExpiresAt"`
	AccessTokenUpdatedAt  *time.Time                          `bson:"accessTokenUpdatedAt"`
	RefreshToken          *string                             `bson:"refreshToken"`
	AccessTokenMissing    bool                                `bson:"accessTokenMissing"`
	AtRefillStatus        *string                             `bson:"atRefillStatus"`
	AtRefillError         *string                             `bson:"atRefillError"`
	AtRefillErrorAt       *time.Time                          `bson:"atRefillErrorAt"`
	PlanCheckStatus       *string                             `bson:"planCheckStatus"`
	PlanCheckedAt         *time.Time                          `bson:"planCheckedAt"`
	PlanCheckErrorCode    *string                             `bson:"planCheckErrorCode"`
	SubscriptionPlan      *string                             `bson:"subscriptionPlan"`
	PlanExpiresAt         *time.Time                          `bson:"planExpiresAt"`
	PromotionCampaigns    []model.PromotionCampaign           `bson:"promotionCampaigns"`
	TrialScanCheckedAt    *time.Time                          `bson:"trialScanCheckedAt"`
	TrialScanCountries    map[string]model.TrialCountryResult `bson:"trialScanCountries"`
	TrialScanError        *string                             `bson:"trialScanError"`
	RegistrationCountry   *string                             `bson:"registrationCountry"`
	RegistrationIP        *string                             `bson:"registrationIp"`
	RegistrationIsp       *string                             `bson:"registrationIsp"`
	AliveStatus           *string                             `bson:"aliveStatus"`
	AliveCheckedAt        *time.Time                          `bson:"aliveCheckedAt"`
	AliveErrorCode        *string                             `bson:"aliveErrorCode"`
	AliveHTTPStatus       *int                                `bson:"aliveHttpStatus"`
	RebindStatus          *string                             `bson:"rebindStatus"`
	PreviousEmail         *string                             `bson:"previousEmail"`
	ReboundEmail          *string                             `bson:"reboundEmail"`
	ReboundAt             *time.Time                          `bson:"reboundAt"`
	RebindProxyCountry    *string                             `bson:"rebindProxyCountry"`
	RebindError           *string                             `bson:"rebindError"`
	Remark                *string                             `bson:"remark"`

	// ── 2FA(TOTP)状态检测(mfa 模块)字段 ──
	// MfaFlagEnabled 服务端 mfa_flag_enabled(查 /me 所得);nil=未检测。
	MfaFlagEnabled *bool `bson:"mfaFlagEnabled"`
	// TotpCheckedAt 最近一次 2FA 状态检测时间。
	TotpCheckedAt *time.Time `bson:"totpCheckedAt"`

	// ── 支付类型检测（paymentcheck 模块）字段 ──
	PaymentStatus      *string                             `bson:"paymentStatus"`
	PaymentMethods     []string                            `bson:"paymentMethods"`
	PaymentZeroMethods []string                            `bson:"paymentZeroMethods"`
	PaymentCheckedAt   *time.Time                          `bson:"paymentCheckedAt"`
	PaymentRoutes      map[string]model.PaymentRouteResult `bson:"paymentRoutes"`
	// 认领/卡死恢复：running 状态 + 起始时间（对齐 PlanCheckStartedAt 语义）。
	PaymentCheckStartedAt *time.Time `bson:"paymentCheckStartedAt"`

	// ── 套餐检查/验活（plancheck 模块）补充字段，对齐 codex-auto resource_service ──
	// AccessToken 裸 token（check_account_plan_curl 的 Bearer；注册落库时写入）。
	// 注意：仅存于 store 层（敏感），不进 model.AccountRecord 的对外 JSON。
	AccessToken string `bson:"accessToken"`
	// 认领/卡死恢复：套餐检查与验活各一对「running 状态 + 起始时间」，
	// 超过 claimStaleMinutes（默认 5min）仍 running 视为卡死可被重新认领。
	PlanCheckStartedAt  *time.Time `bson:"planCheckStartedAt"`
	AliveCheckStartedAt *time.Time `bson:"aliveCheckStartedAt"`
	// 套餐检查附加结果（store_account_plan_result 写入）。
	PlanCheckHTTPStatus   *int       `bson:"planCheckHttpStatus"`
	PlanAccountID         *string    `bson:"planAccountId"`
	HasActiveSubscription *bool      `bson:"hasActiveSubscription"`
	PlanRenewsAt          *time.Time `bson:"planRenewsAt"`
	PromotionCampaignID   *string    `bson:"promotionCampaignId"` // plus 试用活动 id（eligible_promo_campaigns.plus.id）

	// ── token-heal（session cookie 续期）字段 ──
	// SessionToken 是 __Secure-next-auth.session-token cookie（约 3 个月，续期凭证；敏感）。
	SessionToken string `bson:"sessionToken"`
	// CookieHeader 是注册时 Cookie 头（含 oai-did 指纹，heal 回灌三路自洽；敏感）。
	CookieHeader string `bson:"cookieHeader"`
	// SessionUpdatedAt 是 session cookie 滚动轮换后的回写时间。
	SessionUpdatedAt *time.Time `bson:"sessionUpdatedAt"`
}

// AccountQuery mirrors the list_accounts mongo_query filter.
type AccountQuery struct {
	Query     string // q: regex on emailNormalized (escaped, lowercased)
	Promotion string // untried_plus / ineligible / unchecked
	Country   string // 2-letter code, "ZZ" = unmatched
	Alive     string // alive / dead / unknown / unchecked
	// ── 支付类型检测筛选（paymentcheck 扩展）──
	Payment       string // paymentMethods 含该渠道
	ZeroPayment   string // paymentZeroMethods 含该渠道
	PaymentStatus string // available/…/unchecked
}

// AccountStore is the account storage contract.
type AccountStore interface {
	// List returns a page (ordered createdAt desc) plus total count.
	List(ctx context.Context, q AccountQuery, page, pageSize int) ([]AccountDocument, int, error)
	// Create inserts a document; returns false if emailNormalized already exists.
	Create(ctx context.Context, d AccountDocument) (bool, error)
	// Delete removes documents by id; returns deleted count.
	Delete(ctx context.Context, ids []string) (int, error)
	// Count returns the number of documents matching pred.
	Count(ctx context.Context, pred func(AccountDocument) bool) (int, error)

	// ── 套餐检查/验活（plancheck 模块）──

	// Get 按 id 取单个账号（不存在返回 nil,nil）。
	Get(ctx context.Context, id string) (*AccountDocument, error)
	// ClaimPlanCheck 原子认领套餐检查：仅当 accessToken 非空且未在 running（或 running
	// 已卡死超过 staleMin 分钟）时，置 planCheckStatus=running 并返回该账号（含 AccessToken）。
	// 否则返回 nil（已被别的任务认领 / 无 token 不可查）。对齐 codex claim_account_plan_check。
	ClaimPlanCheck(ctx context.Context, id string, staleMin int) (*AccountDocument, error)
	// ClaimAliveCheck 同 ClaimPlanCheck，认领验活（置 aliveStatus=running）。
	ClaimAliveCheck(ctx context.Context, id string, staleMin int) (*AccountDocument, error)
	// StorePlanResult 套餐检查成功落库（对齐 store_account_plan_result）：
	// planCheckStatus=success + subscription/hasActive/promotionEligible/campaignId/accountType。
	StorePlanResult(ctx context.Context, id string, r PlanResultUpdate) error
	// StorePlanFailure 套餐检查失败落库（planCheckStatus=failed + errorCode/httpStatus；
	// token 失效类错误同时置 accessTokenConfigured=false）。
	StorePlanFailure(ctx context.Context, id string, errCode string, httpStatus *int, tokenInvalid bool) error
	// StoreAliveResult 验活落库：aliveStatus=alive/dead + aliveCheckedAt + httpStatus。
	StoreAliveResult(ctx context.Context, id string, alive bool, httpStatus *int) error
	// StoreAliveFailure 验活异常落库：aliveStatus=unknown + errorCode。
	StoreAliveFailure(ctx context.Context, id string, errCode string, httpStatus *int) error
	// StoreCombinedResult 验活+套餐成功时一次写两组字段（对齐 store_account_combined_result）：
	// aliveStatus=alive + planCheckStatus=success + subscription/promotion/accountType。
	StoreCombinedResult(ctx context.Context, id string, r PlanResultUpdate) error
	// StoreCombinedFailure 验活+套餐失败时一次写两组（对齐 store_account_combined_failure）：
	// aliveStatus=dead/unknown + planCheckStatus=failed；tokenInvalid 时置 accessTokenConfigured=false。
	StoreCombinedFailure(ctx context.Context, id, errCode string, httpStatus *int, dead, tokenInvalid bool) error

	// ── 2FA(TOTP)状态检测(mfa 模块)──

	// StoreTotpStatus 2FA 状态检测落库(对齐 check_2fa_status 的回写):
	// totpStatus(enabled/orphaned/dirty/"") + mfaFlagEnabled + totpCheckedAt。
	StoreTotpStatus(ctx context.Context, id string, totpStatus string, mfaEnabled bool) error

	// StoreTotp 补 2FA(enroll+activate)成功后落库(对齐 codex store_account_totp):
	// totpSecret + totpStatus=enabled + accessToken(enroll 时的 recent_auth AT,非空才写)
	// + accessTokenConfigured + accessTokenExpiresAt + accessTokenUpdatedAt。
	// totpSecret 为空属调用方逻辑错误,返回参数错误。
	StoreTotp(ctx context.Context, id string, u TotpUpdate) error

	// StorePassword 仅写密码 + AT(「有2FA无密码」场景:backfill 补密码成功后落库,
	// 不碰已有 totp 字段)。password 为空属调用方逻辑错误,返回参数错误。
	StorePassword(ctx context.Context, id string, u TotpUpdate) error

	// ── 支付类型检测（paymentcheck 模块）──

	// ClaimPaymentCheck 原子认领支付检测：accessToken 非空且未在 running（或已卡死
	// 超过 staleMin 分钟）时，置 paymentStatus=running 并返回账号（含 AccessToken）。
	ClaimPaymentCheck(ctx context.Context, id string, staleMin int) (*AccountDocument, error)
	// StorePaymentRouteResult 单条线路结果落库（流式：一条线路完成即写一条）。
	StorePaymentRouteResult(ctx context.Context, id string, r model.PaymentRouteResult) error
	// StorePaymentSummary 账号全部线路完成后的聚落库：status/methods/zeroMethods/checkedAt；
	// tokenInvalid 时联动置 accessTokenConfigured=false + aliveStatus=dead。
	StorePaymentSummary(ctx context.Context, id string, s PaymentSummaryUpdate) error

	// ── 换绑（rebind 模块）──

	// ClaimRebind 原子认领换绑：仅当账号未被认领（rebindStatus != running 或
	// running 已卡死超过 staleMin 分钟）时置 rebindStatus=running 并返回账号。
	ClaimRebind(ctx context.Context, id string, staleMin int) (*AccountDocument, error)
	// MarkRebindEmailChanged 远端换绑已不可逆时立即落库：email/emailNormalized/
	// emailAccessUrl 切到新邮箱 + rebindStatus=email_changed_token_pending +
	// previousEmail/reboundEmail/reboundAt；清 rebindError。
	MarkRebindEmailChanged(ctx context.Context, id string, u RebindEmailChangedUpdate) error
	// MarkRebindSuccess 新邮箱重登确认 + 新 AT 落库：MarkRebindEmailChanged 全集 +
	// accessToken/accessTokenExpiresAt + rebindStatus=success。
	MarkRebindSuccess(ctx context.Context, id string, u RebindSuccessUpdate) error
	// MarkRebindFailure 换绑失败落库：rebindStatus=failed + rebindError；保留原邮箱。
	MarkRebindFailure(ctx context.Context, id, errCode string) error

	// ── token-heal（session cookie 续期）──

	// StoreSessionHeal 续期成功落库：新 accessToken + 过期/更新时间 + 滚动后的 sessionToken
	// + sessionUpdatedAt；同时清 accessTokenMissing/atRefillStatus/atRefillError（账号复活）。
	// sessionToken 非空才回写（滚动轮换抓到新值才更新，否则保留旧 cookie）。
	StoreSessionHeal(ctx context.Context, id string, u SessionHealUpdate) error
}

// SessionHealUpdate 是 StoreSessionHeal 的入参（token-heal 续期成功的写入集）。
type SessionHealUpdate struct {
	AccessToken          string     // 新 access_token（/api/auth/session mint）
	AccessTokenExpiresAt *time.Time // 新 AT 过期时间（JWT exp 解析）
	SessionToken         string     // 滚动后的新 session cookie（非空才回写）
}

// RebindEmailChangedUpdate 是 MarkRebindEmailChanged 的入参。
type RebindEmailChangedUpdate struct {
	NewEmail      string
	NewAccessURL  string
	PreviousEmail string
	ProxyCountry  string
	ReboundAt     time.Time
}

// RebindSuccessUpdate 是 MarkRebindSuccess 的入参。
type RebindSuccessUpdate struct {
	RebindEmailChangedUpdate
	AccessToken          string
	AccessTokenExpiresAt *time.Time
}

// TotpUpdate 是 StoreTotp 的入参(对齐 codex store_account_totp 的写入集)。
type TotpUpdate struct {
	// TotpSecret 是 enroll 拿到的规范化 TOTP 密钥(base32),必填。
	TotpSecret string
	// AccessToken 是本次协议登录拿到的 recent_auth AT(非空时一并刷新 token 字段)。
	AccessToken string
	// AccessTokenExpiresAt 是 AT 过期时间(对齐 codex now+30d);nil 时不改。
	AccessTokenExpiresAt *time.Time
	// ChatgptPassword 是补密码分支新生效的密码(非空时一并加密落库 chatgptPassword +
	// passwordConfiguredAt;对齐 codex 无密码分支「先落密码再落 TOTP」)。有密码分支留空。
	ChatgptPassword string
}

// AccountInvalidUpdateError 表示落库入参非法(如空 totpSecret)。
type AccountInvalidUpdateError struct {
	ID     string
	Reason string
}

func (e *AccountInvalidUpdateError) Error() string {
	return "账号落库入参非法(" + e.ID + "): " + e.Reason
}

// PaymentSummaryUpdate 是 StorePaymentSummary 的入参（对齐支付检测设计文档聚合规则）。
type PaymentSummaryUpdate struct {
	Status       string   // available/partial/not_returned/already_paid/token_invalid/…
	Methods      []string // 全部线路去重渠道
	ZeroMethods  []string // 0 元确认线路的渠道
	CheckedAt    time.Time
	TokenInvalid bool // true → 联动置 accessTokenConfigured=false + aliveStatus=dead
}

// PlanResultUpdate 是 StorePlanResult 的入参（对齐 codex AccountPlanResult 的落库子集）。
type PlanResultUpdate struct {
	CheckedAt             time.Time
	HTTPStatus            *int
	AccountID             *string
	SubscriptionPlan      *string
	HasActiveSubscription *bool
	ExpiresAt             *time.Time
	RenewsAt              *time.Time
	PromotionEligible     *bool
	PromotionCampaignID   *string
	// AccountType 归一化后的 "free"|"plus"（空串=不更新，对齐 codex 仅 free/plus 才写）。
	AccountType string
}

// MockAccountStore is an in-memory AccountStore.
//
// 并发安全：plancheck 后台任务会并发 Claim/Store（多号同时套餐检查+验活），
// 故所有读写 m.docs 的方法都持 mu（对齐项目「并发独立、无数据竞争」约束）。
type MockAccountStore struct {
	mu   sync.RWMutex
	docs []AccountDocument
}

// NewMockAccountStore returns an empty in-memory account store.
func NewMockAccountStore() *MockAccountStore {
	return &MockAccountStore{}
}

// List filters and pages documents (ordered createdAt desc), mirroring
// list_accounts (resource_service.py:476-572).
func (m *MockAccountStore) List(_ context.Context, q AccountQuery, page, pageSize int) ([]AccountDocument, int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	filtered := make([]AccountDocument, 0, len(m.docs))
	for _, d := range m.docs {
		if !matchAccount(d, q) {
			continue
		}
		filtered = append(filtered, d)
	}
	// createdAt descending (stable).
	sort.SliceStable(filtered, func(i, j int) bool {
		return filtered[i].CreatedAt.After(filtered[j].CreatedAt)
	})
	total := len(filtered)
	start := (page - 1) * pageSize
	if start < 0 {
		start = 0
	}
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	return filtered[start:end], total, nil
}

// matchAccount mirrors the mongo_query predicates in list_accounts.
func matchAccount(d AccountDocument, q AccountQuery) bool {
	if q.Query != "" && !strings.Contains(d.EmailNormalized, q.Query) {
		return false
	}
	if q.Country != "" {
		if q.Country == "ZZ" {
			// unmatched: registrationCountry null/absent
			if d.RegistrationCountry != nil {
				return false
			}
		} else if d.RegistrationCountry == nil || *d.RegistrationCountry != q.Country {
			return false
		}
	}
	if q.Promotion != "" {
		switch q.Promotion {
		case model.PromotionUntriedPlus:
			// accountType=free && promotionEligible=true
			if d.AccountType != model.AccountTypeFree || d.PromotionEligible == nil || !*d.PromotionEligible {
				return false
			}
		case model.PromotionIneligible:
			if d.PromotionEligible == nil || *d.PromotionEligible {
				return false
			}
		case model.PromotionUnchecked:
			if d.PromotionEligible != nil {
				return false
			}
		}
	}
	if q.Alive != "" {
		switch q.Alive {
		case model.AliveAlive, model.AliveDead, model.AliveUnknown:
			if d.AliveStatus == nil || *d.AliveStatus != q.Alive {
				return false
			}
		case "unchecked":
			if d.AliveStatus != nil {
				return false
			}
		}
	}
	if q.Payment != "" && !stringSliceContains(d.PaymentMethods, q.Payment) {
		return false
	}
	if q.ZeroPayment != "" && !stringSliceContains(d.PaymentZeroMethods, q.ZeroPayment) {
		return false
	}
	if q.PaymentStatus != "" {
		if q.PaymentStatus == "unchecked" {
			if d.PaymentStatus != nil {
				return false
			}
		} else if d.PaymentStatus == nil || *d.PaymentStatus != q.PaymentStatus {
			return false
		}
	}
	return true
}

// stringSliceContains 判断切片是否包含目标值（支付渠道筛选用）。
func stringSliceContains(xs []string, target string) bool {
	for _, x := range xs {
		if x == target {
			return true
		}
	}
	return false
}

// Create inserts a document; returns false if a doc with the same emailNormalized
// already exists (mirrors DuplicateKeyError -> DuplicateResourceError).
func (m *MockAccountStore) Create(_ context.Context, d AccountDocument) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.docs {
		if existing.EmailNormalized == d.EmailNormalized {
			return false, nil
		}
	}
	m.docs = append(m.docs, d)
	return true, nil
}

// Delete removes documents by id; returns deleted count.
func (m *MockAccountStore) Delete(_ context.Context, ids []string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	idSet := make(map[string]bool, len(ids))
	for _, id := range ids {
		idSet[id] = true
	}
	kept := m.docs[:0]
	deleted := 0
	for _, d := range m.docs {
		if idSet[d.ID] {
			deleted++
			continue
		}
		kept = append(kept, d)
	}
	m.docs = kept
	return deleted, nil
}

// Count returns the number of documents matching pred.
func (m *MockAccountStore) Count(_ context.Context, pred func(AccountDocument) bool) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n := 0
	for _, d := range m.docs {
		if pred == nil || pred(d) {
			n++
		}
	}
	return n, nil
}

// ── 套餐检查/验活（plancheck 模块）Mock 实现 ──

// findIdx 返回 id 对应 doc 的下标；不存在返回 -1。调用方须已持锁。
func (m *MockAccountStore) findIdx(id string) int {
	for i, d := range m.docs {
		if d.ID == id {
			return i
		}
	}
	return -1
}

// Get 按 id 取账号（拷贝返回，避免外部改动共享内部态）。
func (m *MockAccountStore) Get(_ context.Context, id string) (*AccountDocument, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if i := m.findIdx(id); i >= 0 {
		d := m.docs[i]
		return &d, nil
	}
	return nil, nil
}

// claimCheck 是 ClaimPlanCheck/ClaimAliveCheck 的公共原子认领逻辑。
// kind: "plan" | "alive"。对齐 codex claim_*：accessToken 非空 + 未 running(或卡死)。
func (m *MockAccountStore) claimCheck(id, kind string, staleMin int) (*AccountDocument, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	i := m.findIdx(id)
	if i < 0 {
		return nil, nil
	}
	d := &m.docs[i]
	if d.AccessToken == "" {
		return nil, nil // 无 token 不可查
	}
	if staleMin <= 0 {
		staleMin = 5
	}
	now := time.Now().UTC()
	staleBefore := now.Add(-time.Duration(staleMin) * time.Minute)
	if kind == "plan" {
		// 已 running 且未卡死 → 被别的任务认领，返回 nil。
		if d.PlanCheckStatus != nil && *d.PlanCheckStatus == "running" &&
			d.PlanCheckStartedAt != nil && d.PlanCheckStartedAt.After(staleBefore) {
			return nil, nil
		}
		running := "running"
		d.PlanCheckStatus = &running
		d.PlanCheckStartedAt = &now
		d.PlanCheckErrorCode = nil
		d.PlanCheckHTTPStatus = nil
	} else { // alive
		if d.AliveStatus != nil && *d.AliveStatus == "running" &&
			d.AliveCheckStartedAt != nil && d.AliveCheckStartedAt.After(staleBefore) {
			return nil, nil
		}
		running := "running"
		d.AliveStatus = &running
		d.AliveCheckStartedAt = &now
		d.AliveErrorCode = nil
		d.AliveHTTPStatus = nil
	}
	out := *d
	return &out, nil
}

// ClaimPlanCheck 原子认领套餐检查（置 running + 返回含 token 的账号）。
func (m *MockAccountStore) ClaimPlanCheck(_ context.Context, id string, staleMin int) (*AccountDocument, error) {
	return m.claimCheck(id, "plan", staleMin)
}

// ClaimAliveCheck 原子认领验活（置 running + 返回含 token 的账号）。
func (m *MockAccountStore) ClaimAliveCheck(_ context.Context, id string, staleMin int) (*AccountDocument, error) {
	return m.claimCheck(id, "alive", staleMin)
}

// StorePlanResult 套餐检查成功落库（对齐 store_account_plan_result）。
func (m *MockAccountStore) StorePlanResult(_ context.Context, id string, r PlanResultUpdate) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	i := m.findIdx(id)
	if i < 0 {
		return &AccountNotFoundError{ID: id}
	}
	d := &m.docs[i]
	success := "success"
	d.PlanCheckStatus = &success
	d.PlanCheckedAt = &r.CheckedAt
	d.PlanCheckErrorCode = nil
	d.PlanCheckHTTPStatus = r.HTTPStatus
	d.PlanAccountID = r.AccountID
	d.SubscriptionPlan = r.SubscriptionPlan
	d.HasActiveSubscription = r.HasActiveSubscription
	d.PlanExpiresAt = r.ExpiresAt
	d.PlanRenewsAt = r.RenewsAt
	d.PromotionEligible = r.PromotionEligible
	d.PromotionCampaignID = r.PromotionCampaignID
	// 仅归一化为 free/plus 才写 accountType（对齐 codex normalized_plan in {free,plus}）。
	if r.AccountType == "free" || r.AccountType == "plus" {
		d.AccountType = r.AccountType
	}
	return nil
}

// StorePlanFailure 套餐检查失败落库；tokenInvalid 时同时置 accessTokenConfigured=false。
func (m *MockAccountStore) StorePlanFailure(_ context.Context, id, errCode string, httpStatus *int, tokenInvalid bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	i := m.findIdx(id)
	if i < 0 {
		return &AccountNotFoundError{ID: id}
	}
	d := &m.docs[i]
	failed := "failed"
	now := time.Now().UTC()
	d.PlanCheckStatus = &failed
	d.PlanCheckedAt = &now
	d.PlanCheckErrorCode = strPtrOrNil(errCode)
	d.PlanCheckHTTPStatus = httpStatus
	if tokenInvalid {
		d.AccessTokenConfigured = false
	}
	return nil
}

// StoreAliveResult 验活落库：aliveStatus=alive/dead。
func (m *MockAccountStore) StoreAliveResult(_ context.Context, id string, alive bool, httpStatus *int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	i := m.findIdx(id)
	if i < 0 {
		return &AccountNotFoundError{ID: id}
	}
	d := &m.docs[i]
	st := "dead"
	if alive {
		st = "alive"
	}
	now := time.Now().UTC()
	d.AliveStatus = &st
	d.AliveCheckedAt = &now
	d.AliveHTTPStatus = httpStatus
	if alive {
		d.AliveErrorCode = nil
	}
	return nil
}

// StoreAliveFailure 验活异常落库：aliveStatus=unknown + errorCode。
func (m *MockAccountStore) StoreAliveFailure(_ context.Context, id, errCode string, httpStatus *int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	i := m.findIdx(id)
	if i < 0 {
		return &AccountNotFoundError{ID: id}
	}
	d := &m.docs[i]
	unknown := "unknown"
	now := time.Now().UTC()
	d.AliveStatus = &unknown
	d.AliveCheckedAt = &now
	d.AliveErrorCode = strPtrOrNil(errCode)
	d.AliveHTTPStatus = httpStatus
	return nil
}

// StoreCombinedResult 验活+套餐成功时一次写两组字段（对齐 store_account_combined_result）。
func (m *MockAccountStore) StoreCombinedResult(_ context.Context, id string, r PlanResultUpdate) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	i := m.findIdx(id)
	if i < 0 {
		return &AccountNotFoundError{ID: id}
	}
	d := &m.docs[i]
	// 验活组。
	alive := "alive"
	d.AliveStatus = &alive
	d.AliveCheckedAt = &r.CheckedAt
	d.AliveErrorCode = nil
	d.AliveHTTPStatus = r.HTTPStatus
	// 套餐组。
	success := "success"
	d.PlanCheckStatus = &success
	d.PlanCheckedAt = &r.CheckedAt
	d.PlanCheckErrorCode = nil
	d.PlanCheckHTTPStatus = r.HTTPStatus
	d.PlanAccountID = r.AccountID
	d.SubscriptionPlan = r.SubscriptionPlan
	d.HasActiveSubscription = r.HasActiveSubscription
	d.PlanExpiresAt = r.ExpiresAt
	d.PlanRenewsAt = r.RenewsAt
	d.PromotionEligible = r.PromotionEligible
	d.PromotionCampaignID = r.PromotionCampaignID
	if r.AccountType == "free" || r.AccountType == "plus" {
		d.AccountType = r.AccountType
	}
	return nil
}

// StoreCombinedFailure 验活+套餐失败时一次写两组（对齐 store_account_combined_failure）。
func (m *MockAccountStore) StoreCombinedFailure(_ context.Context, id, errCode string, httpStatus *int, dead, tokenInvalid bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	i := m.findIdx(id)
	if i < 0 {
		return &AccountNotFoundError{ID: id}
	}
	d := &m.docs[i]
	now := time.Now().UTC()
	// 验活组：dead→dead，否则 unknown。
	aliveSt := "unknown"
	if dead {
		aliveSt = "dead"
	}
	d.AliveStatus = &aliveSt
	d.AliveCheckedAt = &now
	d.AliveErrorCode = strPtrOrNil(errCode)
	d.AliveHTTPStatus = httpStatus
	// 套餐组。
	failed := "failed"
	d.PlanCheckStatus = &failed
	d.PlanCheckedAt = &now
	d.PlanCheckErrorCode = strPtrOrNil(errCode)
	d.PlanCheckHTTPStatus = httpStatus
	if tokenInvalid {
		d.AccessTokenConfigured = false
	}
	return nil
}

// StoreTotpStatus 2FA 状态检测落库(Mock)。
func (m *MockAccountStore) StoreTotpStatus(_ context.Context, id string, totpStatus string, mfaEnabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	i := m.findIdx(id)
	if i < 0 {
		return &AccountNotFoundError{ID: id}
	}
	d := &m.docs[i]
	now := time.Now().UTC()
	d.TotpStatus = strPtrOrNil(totpStatus)
	d.MfaFlagEnabled = &mfaEnabled
	d.TotpCheckedAt = &now
	return nil
}

// StoreTotp 补 2FA 成功落库(Mock,对齐 codex store_account_totp)。
func (m *MockAccountStore) StoreTotp(_ context.Context, id string, u TotpUpdate) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	i := m.findIdx(id)
	if i < 0 {
		return &AccountNotFoundError{ID: id}
	}
	if strings.TrimSpace(u.TotpSecret) == "" {
		return &AccountInvalidUpdateError{ID: id, Reason: "totpSecret 不能为空"}
	}
	d := &m.docs[i]
	d.TotpSecret = strings.TrimSpace(u.TotpSecret)
	enabled := "enabled"
	d.TotpStatus = &enabled
	mfa := true
	d.MfaFlagEnabled = &mfa
	now := time.Now().UTC()
	d.TotpCheckedAt = &now
	// enroll 时的 recent_auth AT 一并刷新 token 字段(对齐 codex store_account_totp)。
	if tok := strings.TrimSpace(u.AccessToken); tok != "" {
		d.AccessToken = tok
		d.AccessTokenConfigured = true
		d.AccessTokenUpdatedAt = &now
		if u.AccessTokenExpiresAt != nil {
			d.AccessTokenExpiresAt = u.AccessTokenExpiresAt
		}
	}
	// 补密码分支:新生效密码一并落库(对齐 codex 无密码分支 chatgptPassword + passwordConfiguredAt)。
	if pwd := strings.TrimSpace(u.ChatgptPassword); pwd != "" {
		d.ChatgptPassword = pwd
		d.PasswordConfiguredAt = &now
	}
	return nil
}

// StorePassword 仅写密码 + AT(Mock,不碰已有 totp 字段)。
func (m *MockAccountStore) StorePassword(_ context.Context, id string, u TotpUpdate) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	i := m.findIdx(id)
	if i < 0 {
		return &AccountNotFoundError{ID: id}
	}
	if strings.TrimSpace(u.ChatgptPassword) == "" {
		return &AccountInvalidUpdateError{ID: id, Reason: "chatgptPassword 不能为空"}
	}
	d := &m.docs[i]
	now := time.Now().UTC()
	d.ChatgptPassword = strings.TrimSpace(u.ChatgptPassword)
	d.PasswordConfiguredAt = &now
	if tok := strings.TrimSpace(u.AccessToken); tok != "" {
		d.AccessToken = tok
		d.AccessTokenConfigured = true
		d.AccessTokenUpdatedAt = &now
		if u.AccessTokenExpiresAt != nil {
			d.AccessTokenExpiresAt = u.AccessTokenExpiresAt
		}
	}
	return nil
}

// StoreSessionHeal 续期成功落库(Mock):新 AT + 滚动 session cookie,清 AT 失效标记。
func (m *MockAccountStore) StoreSessionHeal(_ context.Context, id string, u SessionHealUpdate) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	i := m.findIdx(id)
	if i < 0 {
		return &AccountNotFoundError{ID: id}
	}
	tok := strings.TrimSpace(u.AccessToken)
	if tok == "" {
		return &AccountInvalidUpdateError{ID: id, Reason: "accessToken 不能为空"}
	}
	d := &m.docs[i]
	now := time.Now().UTC()
	d.AccessToken = tok
	d.AccessTokenConfigured = true
	d.AccessTokenUpdatedAt = &now
	d.AccessTokenMissing = false
	d.AtRefillStatus = nil
	d.AtRefillError = nil
	d.AtRefillErrorAt = nil
	if u.AccessTokenExpiresAt != nil {
		d.AccessTokenExpiresAt = u.AccessTokenExpiresAt
	}
	if st := strings.TrimSpace(u.SessionToken); st != "" {
		d.SessionToken = st
		d.SessionUpdatedAt = &now
	}
	return nil
}

// ── 支付类型检测（paymentcheck 模块）Mock 实现 ──

// ClaimPaymentCheck 原子认领支付检测（置 running + 返回含 token 的账号）。
func (m *MockAccountStore) ClaimPaymentCheck(_ context.Context, id string, staleMin int) (*AccountDocument, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	i := m.findIdx(id)
	if i < 0 {
		return nil, nil
	}
	d := &m.docs[i]
	if d.AccessToken == "" {
		return nil, nil // 无 token 不可测
	}
	if staleMin <= 0 {
		staleMin = 5
	}
	now := time.Now().UTC()
	staleBefore := now.Add(-time.Duration(staleMin) * time.Minute)
	// 已 running 且未卡死 → 被别的任务认领，返回 nil。
	if d.PaymentStatus != nil && *d.PaymentStatus == "running" &&
		d.PaymentCheckStartedAt != nil && d.PaymentCheckStartedAt.After(staleBefore) {
		return nil, nil
	}
	running := "running"
	d.PaymentStatus = &running
	d.PaymentCheckStartedAt = &now
	out := *d
	return &out, nil
}

// StorePaymentRouteResult 单条线路结果落库（按 country 键覆盖/新增）。
func (m *MockAccountStore) StorePaymentRouteResult(_ context.Context, id string, r model.PaymentRouteResult) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	i := m.findIdx(id)
	if i < 0 {
		return &AccountNotFoundError{ID: id}
	}
	d := &m.docs[i]
	if d.PaymentRoutes == nil {
		d.PaymentRoutes = map[string]model.PaymentRouteResult{}
	}
	d.PaymentRoutes[r.Country] = r
	return nil
}

// StorePaymentSummary 支付检测聚合落库；tokenInvalid 时联动置死（对齐设计文档熔断语义）。
func (m *MockAccountStore) StorePaymentSummary(_ context.Context, id string, s PaymentSummaryUpdate) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	i := m.findIdx(id)
	if i < 0 {
		return &AccountNotFoundError{ID: id}
	}
	d := &m.docs[i]
	d.PaymentStatus = strPtrOrNil(s.Status)
	d.PaymentMethods = s.Methods
	d.PaymentZeroMethods = s.ZeroMethods
	d.PaymentCheckedAt = &s.CheckedAt
	d.PaymentCheckStartedAt = nil
	if s.TokenInvalid {
		d.AccessTokenConfigured = false
		dead := "dead"
		now := time.Now().UTC()
		d.AliveStatus = &dead
		d.AliveCheckedAt = &now
	}
	return nil
}

// strPtrOrNil 空串转 nil（对齐 codex 把 errorCode 清为 None）。
func strPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// AccountNotFoundError 表示目标账号不存在（对齐 codex ResourceNotFoundError）。
type AccountNotFoundError struct{ ID string }

func (e *AccountNotFoundError) Error() string { return "账号不存在: " + e.ID }

// ── 换绑（rebind 模块）Mock 实现 ──

func (m *MockAccountStore) ClaimRebind(_ context.Context, id string, staleMin int) (*AccountDocument, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	i := m.findIdx(id)
	if i < 0 {
		return nil, nil
	}
	d := &m.docs[i]
	// 已被认领且未卡死 → 拒绝。
	if d.RebindStatus != nil && *d.RebindStatus == "running" {
		if d.ReboundAt != nil && staleMin > 0 && time.Since(*d.ReboundAt) < time.Duration(staleMin)*time.Minute {
			return nil, nil
		}
		if d.ReboundAt == nil {
			return nil, nil
		}
	}
	running := "running"
	now := time.Now().UTC()
	d.RebindStatus = &running
	d.ReboundAt = &now // 复用 ReboundAt 做认领时间戳（卡死判断用）
	cp := *d
	return &cp, nil
}

func (m *MockAccountStore) MarkRebindEmailChanged(_ context.Context, id string, u RebindEmailChangedUpdate) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	i := m.findIdx(id)
	if i < 0 {
		return &AccountNotFoundError{ID: id}
	}
	d := &m.docs[i]
	norm := strings.ToLower(strings.TrimSpace(u.NewEmail))
	d.Email = norm
	d.EmailNormalized = norm
	d.EmailAccessURL = strings.TrimSpace(u.NewAccessURL)
	changed := "email_changed_token_pending"
	d.RebindStatus = &changed
	prev := strings.ToLower(strings.TrimSpace(u.PreviousEmail))
	d.PreviousEmail = &prev
	d.ReboundEmail = &norm
	d.ReboundAt = &u.ReboundAt
	if u.ProxyCountry != "" {
		d.RebindProxyCountry = &u.ProxyCountry
	}
	d.RebindError = nil
	return nil
}

func (m *MockAccountStore) MarkRebindSuccess(_ context.Context, id string, u RebindSuccessUpdate) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	i := m.findIdx(id)
	if i < 0 {
		return &AccountNotFoundError{ID: id}
	}
	d := &m.docs[i]
	norm := strings.ToLower(strings.TrimSpace(u.NewEmail))
	d.Email = norm
	d.EmailNormalized = norm
	d.EmailAccessURL = strings.TrimSpace(u.NewAccessURL)
	success := "success"
	d.RebindStatus = &success
	prev := strings.ToLower(strings.TrimSpace(u.PreviousEmail))
	d.PreviousEmail = &prev
	d.ReboundEmail = &norm
	d.ReboundAt = &u.ReboundAt
	if u.ProxyCountry != "" {
		d.RebindProxyCountry = &u.ProxyCountry
	}
	d.AccessToken = u.AccessToken
	d.AccessTokenConfigured = u.AccessToken != ""
	d.AccessTokenExpiresAt = u.AccessTokenExpiresAt
	now := time.Now().UTC()
	d.AccessTokenUpdatedAt = &now
	d.RebindError = nil
	return nil
}

func (m *MockAccountStore) MarkRebindFailure(_ context.Context, id, errCode string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	i := m.findIdx(id)
	if i < 0 {
		return &AccountNotFoundError{ID: id}
	}
	d := &m.docs[i]
	failed := "failed"
	d.RebindStatus = &failed
	d.RebindError = strPtrOrNil(errCode)
	return nil
}
