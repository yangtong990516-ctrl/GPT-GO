// Package model defines the account-pool API models. JSON field names are
// camelCase to match Pydantic (reference: resource_models.py:63-162).
package model

import "time"

// AccountType mirrors AccountType = Literal["plus", "free"].
type AccountType = string

const (
	AccountTypePlus AccountType = "plus" // 付费账号
	AccountTypeFree AccountType = "free" // 免费账号
)

// PlanCheckStatus mirrors PlanCheckStatus = Literal["running", "success", "failed"].
type PlanCheckStatus = string

const (
	PlanCheckRunning PlanCheckStatus = "running"
	PlanCheckSuccess PlanCheckStatus = "success"
	PlanCheckFailed  PlanCheckStatus = "failed"
)

// AliveStatus mirrors the aliveStatus Literal["running","alive","dead","unknown"].
type AliveStatus = string

const (
	AliveRunning AliveStatus = "running" // 检测中
	AliveAlive   AliveStatus = "alive"   // 正常
	AliveDead    AliveStatus = "dead"    // 已失效
	AliveUnknown AliveStatus = "unknown" // 检测异常
)

// PromotionFilter mirrors the promotion query parameter enum used by the
// frontend dropdown (ResourceQuery.promotion).
type PromotionFilter = string

const (
	// PromotionUntriedPlus = 免费试用一个月（未试用 Plus）：
	// accountType=free 且 promotionEligible=true，有试用资格但尚未使用。
	PromotionUntriedPlus PromotionFilter = "untried_plus"
	// PromotionIneligible = 不可试用：promotionEligible=false。
	PromotionIneligible PromotionFilter = "ineligible"
	// PromotionUnchecked = 未查询：promotionEligible 为空（null/不存在）。
	PromotionUnchecked PromotionFilter = "unchecked"
)

// CampaignLabel maps a promotion campaign id to its Chinese display label,
// mirroring the frontend CAMPAIGN_LABELS constant (AccountsView.vue:170-180).
var CampaignLabel = map[string]string{
	"plus-1-month-free":        "免费试用 1 个月",
	"plus-2-months-free":       "免费试用 2 个月",
	"plus-3-months-free":       "免费试用 3 个月",
	"plus-1-month-50-pct-off":  "首月 5 折",
	"plus-2-months-50-pct-off": "前 2 个月 5 折",
	"plus-3-months-50-pct-off": "前 3 个月 5 折",
	"plus-1-month-25-pct-off":  "首月立减 25%",
	"plus-6-months-50-pct-off": "前 6 个月 5 折",
	"plus-1-month-75-pct-off":  "首月立减 75%",
}

// PromotionCampaign mirrors a single element of promotionCampaigns: the four
// keys produced by chatgpt_plan.eligible_campaigns (chatgpt_plan.py:171-176).
type PromotionCampaign struct {
	Plan          string `json:"plan" bson:"plan"`                     // 套餐 key（如 plus）
	ID            string `json:"id" bson:"id"`                         // 活动 id（对应 CampaignLabel）
	PromotionType string `json:"promotion_type" bson:"promotion_type"` // 促销类型
	Title         string `json:"title" bson:"title"`                   // 活动标题（英文）
}

// TrialCountryResult mirrors a single entry of trialScanCountries (types/index.ts:50):
// 试用扫描动作在某个国家的结果（动作结果，用于「试用扫描」列渲染）。
type TrialCountryResult struct {
	Eligible       *bool    `json:"eligible" bson:"eligible"`             // 该国有无试用资格（true有/false无/null未知）
	State          string   `json:"state" bson:"state"`                   // 状态信号
	Signal         string   `json:"signal" bson:"signal"`                 // 信号标识
	PaymentMethods []string `json:"paymentMethods" bson:"paymentMethods"` // 可用支付方式列表
	Error          string   `json:"error" bson:"error"`                   // 该国的错误信息
}

// ── 支付类型检测（paymentcheck 模块）──

// PaymentRouteResult 是一条区域线路的检测结果（对齐 payment-check 设计文档
// PaymentRouteResult，扩展 exitIp/exitPurity/amountDue/zeroStatus）。
// 安全红线：SessionType 只存前缀（oaics_/cs_live_/cs_test_），不存完整会话 ID。
type PaymentRouteResult struct {
	Country         string    `json:"country" bson:"country"`                           // 线路国家（VN）
	Currency        string    `json:"currency" bson:"currency"`                         // 币种（VND）
	Locale          string    `json:"locale" bson:"locale"`                             // 语言（vi-VN）
	Status          string    `json:"status" bson:"status"`                             // available/not_returned/token_invalid/…
	State           string    `json:"state" bson:"state"`                               // payment_methods_available/…
	Methods         []string  `json:"methods" bson:"methods"`                           // 归一化后的支付渠道（momo/card…）
	MethodsInferred []string  `json:"methodsInferred,omitempty" bson:"methodsInferred"` // 启发式推断的渠道（gcash）
	AmountDue       *int64    `json:"amountDue" bson:"amountDue"`                       // 应付金额（minor 单位）；矛盾/未知→null
	ZeroStatus      string    `json:"zeroStatus" bson:"zeroStatus"`                     // zero_confirmed/not_zero/zero_unknown
	ExitIP          string    `json:"exitIp" bson:"exitIp"`                             // 检测实际使用的出口 IP（代理 IP，可展示）
	ExitCountry     string    `json:"exit" bson:"exit"`                                 // 出口国家（cloudflare trace/GeoIP 实测）
	ExitPurity      string    `json:"exitPurity,omitempty" bson:"exitPurity"`           // clean/dirty（sliver 纯度）
	SessionType     string    `json:"sessionType,omitempty" bson:"sessionType"`         // 会话类型前缀 oaics_/cs_live_（不含完整 ID）
	HTTPStatus      int       `json:"httpStatus" bson:"httpStatus"`
	Error           string    `json:"error,omitempty" bson:"error"` // 已脱敏（≤280 字符）
	CheckedAt       time.Time `json:"checkedAt" bson:"checkedAt"`
}

// AccountRecord mirrors resource_models.AccountRecord (basic-information + 展示所需字段).
//
// 字段注释约定：每个字段标注【展示】/【动作】/【动作结果】/【不展示】四类之一，
// 说明该字段是服务于页面表格/卡片渲染，还是服务于操作按钮触发，还是动作完成后
// 写回的结果展示，还是不直接展示（仅存储/导出）。
type AccountRecord struct {
	ID string `json:"id"` // 账号唯一 ID（Mongo _id）；【不展示】仅勾选/操作引用

	// Email 账号登录邮箱；【展示】「邮箱」列，等宽字体显示
	Email string `json:"email"`
	// ChatGPT 登录密码；【不展示】仅存储/导出
	ChatgptPassword string `json:"chatgptPassword"`
	// TOTP 2FA 密钥；【展示】「2FA」列：有值→"已启用"
	TotpSecret string `json:"totpSecret"`
	// TOTP 检测状态；【展示】「2FA」列：totpStatus=="missing" 时显示"缺失"
	TotpStatus *string `json:"totpStatus"`
	// 是否已配置 TOTP（=bool(totpSecret)，推导字段，非独立存储）；【展示】「2FA」列
	TotpSecretConfigured bool `json:"totpSecretConfigured"`
	// 关联邮箱取件 URL（输出时经 direct_mailbox_access_url 做 api798 重写）；【动作】「接码」按钮依赖
	EmailAccessURL string `json:"emailAccessUrl"`
	// 创建时间 UTC；【不展示】仅排序/存储
	CreatedAt time.Time `json:"createdAt"`
	// 账号类型 plus/free；【展示】「账号类型」列：plus→Plus(黄)，free→Free(灰)
	AccountType AccountType `json:"accountType"`
	// 是否已绑定手机；【展示】间接（plus 统计 bound/unbound 依赖）
	PhoneBound *bool `json:"phoneBound"`
	// AT 是否已配置（bool(accessTokenConfigured)，默认 false）；【展示】「AT状态」列核心判断
	AccessTokenConfigured bool `json:"accessTokenConfigured"`
	// AT 过期时间；【展示】「AT状态」列 tooltip："过期时间：xxx"
	AccessTokenExpiresAt *time.Time `json:"accessTokenExpiresAt"`
	// AT 更新时间；【动作结果】刷新/补 AT 后写回
	AccessTokenUpdatedAt *time.Time `json:"accessTokenUpdatedAt"`
	// 刷新令牌；【动作】「刷新 AT」按钮依赖；【不展示】
	RefreshToken *string `json:"refreshToken"`
	// 已建但 AT 未拿到（待补 AT），区别于过期/未配置；【展示】「AT状态」列"缺失"标签
	AccessTokenMissing bool `json:"accessTokenMissing"`
	// 补 AT 状态；【展示】「AT状态」列：failed→"补AT失败"，add_phone_required→"需绑手机"
	AtRefillStatus *string `json:"atRefillStatus"`
	// 补 AT 错误信息；【展示】「AT状态」列 tooltip："补AT失败: xxx"
	AtRefillError *string `json:"atRefillError"`
	// 补 AT 错误时间；【动作结果】补 AT 失败时写回
	AtRefillErrorAt *time.Time `json:"atRefillErrorAt"`
	// 套餐检查状态 running/success/failed；【展示】「优惠资格」列：running→"查询中"
	PlanCheckStatus *string `json:"planCheckStatus"`
	// 套餐检查时间；【展示】「优惠资格」列 tooltip："查询时间：xxx"
	PlanCheckedAt *time.Time `json:"planCheckedAt"`
	// 套餐检查错误码；【展示】「优惠资格」列 tooltip："错误码：xxx"
	PlanCheckErrorCode *string `json:"planCheckErrorCode"`
	// 订阅套餐名；【展示】「优惠资格」列（campaign 相关）
	SubscriptionPlan *string `json:"subscriptionPlan"`
	// 套餐过期时间；【展示】tooltip
	PlanExpiresAt *time.Time `json:"planExpiresAt"`
	// 促销活动列表；【展示】「优惠资格」列：1个→标签，多个→"可试用×N"
	PromotionCampaigns []PromotionCampaign `json:"promotionCampaigns"`
	// 试用扫描时间；【动作结果】「试用扫描」动作完成后写回
	TrialScanCheckedAt *time.Time `json:"trialScanCheckedAt"`
	// 试用扫描各国家结果；【动作结果】「试用扫描」列渲染（空→"未扫描"，有 error→"扫描失败"）
	TrialScanCountries map[string]TrialCountryResult `json:"trialScanCountries"`
	// 试用扫描错误信息；【动作结果】「试用扫描」列：有值→"扫描失败"
	TrialScanError *string `json:"trialScanError"`
	// 注册国家 2 位码；【展示】「国家」列：countryLabel 中文名，空→"-"
	RegistrationCountry *string `json:"registrationCountry"`
	// 注册 IP；【展示】「注册IP」列，等宽字体，空→"-"
	RegistrationIP *string `json:"registrationIp"`
	// 注册 ISP 运营商；【不展示】
	RegistrationIsp *string `json:"registrationIsp"`
	// 存活状态 running/alive/dead/unknown；【展示】「验活」列
	AliveStatus *string `json:"aliveStatus"`
	// 存活检查时间；【展示】「验活」列 tooltip："检测时间：xxx"
	AliveCheckedAt *time.Time `json:"aliveCheckedAt"`
	// 存活检查错误码；【展示】「验活」列 tooltip："结果：xxx"
	AliveErrorCode *string `json:"aliveErrorCode"`
	// 存活检查 HTTP 状态码；【展示】「验活」列 tooltip："HTTP：xxx"
	AliveHTTPStatus *int `json:"aliveHttpStatus"`
	// 换绑状态；【展示】「换绑」列：success→"已换绑"，null→"未换绑"
	RebindStatus *string `json:"rebindStatus"`
	// 换绑前旧邮箱；【展示】tooltip："原邮箱：xxx"
	PreviousEmail *string `json:"previousEmail,omitempty"`
	// 换绑后新邮箱；【展示】tooltip："新邮箱：xxx"
	ReboundEmail *string `json:"reboundEmail,omitempty"`
	// 换绑完成时间；【展示】tooltip："换绑时间：xxx"
	ReboundAt *time.Time `json:"reboundAt,omitempty"`
	// 换绑使用的代理国家；【展示】tooltip："换绑线路：xxx"
	RebindProxyCountry *string `json:"rebindProxyCountry,omitempty"`
	// 换绑错误码；【展示】tooltip："失败原因：xxx"
	RebindError *string `json:"rebindError,omitempty"`
	// 备注；【展示】「备注」列：有值→显示，空→"—"
	Remark *string `json:"remark"`
	// 优惠资格；【展示】「优惠资格」列：true→"有优惠"/活动标签，false→"不可试用"，null→"未查询"
	PromotionEligible *bool `json:"promotionEligible"`

	// ── 支付类型检测（paymentcheck 模块）写回字段 ──
	// PaymentStatus 聚合状态：available/partial/not_returned/already_paid/token_invalid/unknown；
	// 【展示】「支付渠道」列：无渠道时回退显示状态文本
	PaymentStatus *string `json:"paymentStatus"`
	// PaymentMethods 全部线路去重后的支付渠道；【展示】「支付渠道」列标签
	PaymentMethods []string `json:"paymentMethods"`
	// PaymentZeroMethods 0 元确认（zero_confirmed）线路的渠道——运营选号核心字段
	PaymentZeroMethods []string `json:"paymentZeroMethods"`
	// PaymentCheckedAt 检测完成时间；【展示】tooltip："检测时间：xxx"
	PaymentCheckedAt *time.Time `json:"paymentCheckedAt"`
	// PaymentRoutes 每条线路的结构化明细（含出口 IP/金额/0 元状态），供下钻与重检
	PaymentRoutes map[string]PaymentRouteResult `json:"paymentRoutes"`
}

// AccountCreate mirrors resource_models.AccountCreate (basic-information subset).
type AccountCreate struct {
	Email               string      `json:"email"`               // 3-320，strip+lower
	ChatgptPassword     string      `json:"chatgptPassword"`     // 1-1024
	TotpSecret          string      `json:"totpSecret"`          // 1-256
	EmailAccessURL      string      `json:"emailAccessUrl"`      // 1-4096
	AccountType         AccountType `json:"accountType"`         // 默认 free
	PhoneBound          *bool       `json:"phoneBound"`          // 可空
	PromotionEligible   *bool       `json:"promotionEligible"`   // 可空
	SourceEmailID       *string     `json:"sourceEmailId"`       // 关联邮箱池记录（创建后删除该邮箱）
	RegistrationCountry *string     `json:"registrationCountry"` // 2 位国家码
	// ── 注册产物 token（注册成功落库时写入；对齐 codex store_account_access_token）──
	// AccessToken 是 chatgpt 的 Bearer access_token（套餐检查/验活必需）。
	AccessToken string `json:"-"` // 敏感，不回显前端
	// RefreshToken 是 codex RT 换到的 refresh_token（可空）。
	RefreshToken *string `json:"-"` // 敏感，不回显前端
	// AccessTokenExpiresAt 是 access_token 过期时间（JWT exp claim 解析；可空）。
	AccessTokenExpiresAt *time.Time `json:"-"`
}

// AccountListFilter mirrors the list_accounts query parameters (main.py:1228-1235).
type AccountListFilter struct {
	Query     string          // q：按 emailNormalized 模糊匹配
	Promotion PromotionFilter // promotion：untried_plus/ineligible/unchecked
	Country   string          // country：2 位国家码（ZZ=未识别）
	Alive     string          // alive：alive/dead/unknown/unchecked
	// ── 支付类型检测筛选（paymentcheck 扩展）──
	Payment       string // payment：paymentMethods 含该渠道（如 momo）
	ZeroPayment   string // zero_payment：paymentZeroMethods 含该渠道（0 元可走的渠道）
	PaymentStatus string // payment_status：available/…/unchecked（未检测）
}

// AccountPage mirrors Page[AccountRecord] (resource_models.Page).
type AccountPage struct {
	Items    []AccountRecord `json:"items"`
	Total    int             `json:"total"`
	Page     int             `json:"page"`
	PageSize int             `json:"pageSize"`
}

// PlusStats mirrors resource_models.PlusStats (resource_models.py:612).
type PlusStats struct {
	Total   int `json:"total"`   // plus 账号总数
	Bound   int `json:"bound"`   // 已绑定手机（phoneBound=true）
	Unbound int `json:"unbound"` // 未绑定手机（total - bound）
}

// FreeStats mirrors resource_models.FreeStats (resource_models.py:618).
type FreeStats struct {
	Total      int `json:"total"`      // free 账号总数
	Eligible   int `json:"eligible"`   // 有优惠资格（promotionEligible=true）
	Ineligible int `json:"ineligible"` // 无优惠资格（total - eligible）
}

// AccountStats mirrors resource_models.AccountStats (resource_models.py:624).
type AccountStats struct {
	Total        int       `json:"total"`        // 账号总数（Mongo 全部记录）
	Today        int       `json:"today"`        // 今日新增（createdAt >= 今日 UTC 零点）
	TotpComplete int       `json:"totpComplete"` // TOTP 完整（totpSecret 非空）
	Plus         PlusStats `json:"plus"`
	Free         FreeStats `json:"free"`
	// PlusRatio 是 plus 账号占比（plus.total/total*100，1 位小数；total=0 → 0）。
	PlusRatio float64 `json:"plusRatio"`
	// Alive 是验活状态分布（GPT-GO 扩展，codex overview 无此块，为前端大盘准备）。
	Alive AliveStats `json:"alive"`
}

// AliveStats 是验活状态分布（GPT-GO 扩展，为前端大盘准备）。
// aliveStatus 四态 + 未检测（nil/未跑过验活）。
type AliveStats struct {
	Alive     int `json:"alive"`     // aliveStatus=alive（正常）
	Dead      int `json:"dead"`      // aliveStatus=dead（令牌失效）
	Unknown   int `json:"unknown"`   // aliveStatus=unknown（检测失败/超时）
	Unchecked int `json:"unchecked"` // 从未验活（aliveStatus 为空/缺失）
	Running   int `json:"running"`   // aliveStatus=running（检测中）
}

// OverviewStats mirrors resource_models.OverviewStats (resource_models.py:651).
// 本轮仅实现 accounts 块；emails/proxies 块留默认零值（对应模块未接统计）。
type OverviewStats struct {
	Accounts AccountStats `json:"accounts"`
	Emails   EmailStats   `json:"emails"`
	Proxies  ProxyStats   `json:"proxies"`
}

// EmailStats mirrors resource_models.EmailStats (resource_models.py:632).
// 邮箱池统计（本轮暂零值，邮箱池统计接口后续接）。
type EmailStats struct {
	Available   int `json:"available"`
	Reserved    int `json:"reserved"`
	Failed      int `json:"failed"`
	Quarantined int `json:"quarantined"`
	Aliases     int `json:"aliases"`
	Mailcode    int `json:"mailcode"`
	Remail      int `json:"remail"`
}

// ProxyStats mirrors resource_models.ProxyStats (resource_models.py:643).
// 代理池统计（本轮暂零值，代理池未迁移）。
type ProxyStats struct {
	Total       int `json:"total"`
	Enabled     int `json:"enabled"`
	Available   int `json:"available"`
	Used        int `json:"used"`
	Quarantined int `json:"quarantined"`
}
