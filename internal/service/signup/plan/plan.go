// Package plan 实现「账号套餐查询」：GET chatgpt.com 的 accounts/check 端点，
// 解析 plan_type / subscription_plan / has_active_subscription / 促销资格。
//
// 1:1 对齐 codex-auto chatgpt_plan.py：
//   - 端点：/backend-api/accounts/check/v4-2023-04-27?timezone_offset_min=...
//   - 判定：plan_type 优先取 account.plan_type，再退回 access_token 的 JWT claim
//     chatgpt_plan_type；【entitlement 是活跃付费的真源】——升级后 JWT/account 仍是
//     free，只有 entitlement.has_active_subscription + subscription_plan 含 plus
//     才判 plus（对齐注释「The JWT/account claim remains free after upgrade」）。
//   - 促销：eligible_promo_campaigns.plus 存在且 plan 为 free → plus_trial_eligible。
//
// HTTP 用 core.Session（httpcloak，与注册同一 TLS 指纹体系；对齐 codex curl_cffi
// impersonate=chrome 的意图——用受控指纹而非裸 net/http）。
package plan

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"gpt-go/internal/service/signup/core"
)

// accountsCheckPath 是套餐检查端点路径（对齐 ACCOUNTS_CHECK_PATH）。
const accountsCheckPath = "/backend-api/accounts/check/v4-2023-04-27"

// AccountsCheckURL 是完整端点（对齐 ACCOUNTS_CHECK_URL）。
const AccountsCheckURL = "https://chatgpt.com" + accountsCheckPath

// responseMaxBytes 限制响应体大小（对齐 PLAN_RESPONSE_MAX_BYTES=524288）。
const responseMaxBytes = 524288

// Result 是套餐检查的结构化结果（对齐 AccountPlanResult 的落库子集）。
type Result struct {
	CheckedAt             time.Time
	AccountID             string
	CurrentPlanType       string // account.plan_type 或 JWT claim 归一后（free/plus/...）
	SubscriptionPlan      string // entitlement.subscription_plan
	HasActiveSubscription bool
	ExpiresAt             *time.Time
	RenewsAt              *time.Time
	PlusTrialEligible     bool   // free 且有 plus 促销 → 有试用资格（promotionEligible）
	PlusTrialCampaignID   string // eligible_promo_campaigns.plus.id
	HTTPStatus            int
	// NormalizedAccountType 归一化为 "free"|"plus"|""（仅这两者才应写库 accountType）。
	NormalizedAccountType string
}

// Error 是套餐检查错误（对齐 PlanCheckError：code + httpStatus + retryable）。
type Error struct {
	Code       string
	HTTPStatus int
	Retryable  bool
}

func (e *Error) Error() string {
	if e.HTTPStatus != 0 {
		return fmt.Sprintf("plan: %s (http %d)", e.Code, e.HTTPStatus)
	}
	return "plan: " + e.Code
}

// tokenInvalid 报告本错误是否因 token 失效（expired/unauthorized），
// 这类错误要把账号 accessTokenConfigured 置 false（对齐 store_account_plan_failure）。
func (e *Error) TokenInvalid() bool {
	return e.Code == "access_token_expired" || e.Code == "access_token_unauthorized"
}

// retryableStatus 对齐 _retryable_status：408/409/425/429 或 5xx 可重试。
func retryableStatus(status int) bool {
	return status == 408 || status == 409 || status == 425 || status == 429 || status >= 500
}

// tokenClaims 从 access_token 的 JWT payload 解析 chatgpt 声明（对齐 token_claims）。
// 不验签（仅取 claim 做展示/兜底，与 Python 一致）。失败返回零值。
type tokenClaims struct {
	accountID   string
	planType    string
	expired     bool
	hasExpClaim bool
}

func parseTokenClaims(token string) tokenClaims {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return tokenClaims{}
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// 兼容带 padding 的段。
		payload, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return tokenClaims{}
		}
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return tokenClaims{}
	}
	var out tokenClaims
	// chatgpt 自定义声明挂在 "https://api.AI Platform.com/auth" 命名空间下（对齐 Python）。
	if auth, ok := claims["https://api.AI Platform.com/auth"].(map[string]any); ok {
		if pt, ok := auth["chatgpt_plan_type"].(string); ok {
			out.planType = pt
		}
		if aid, ok := auth["chatgpt_account_id"].(string); ok {
			out.accountID = aid
		}
	}
	if out.accountID == "" {
		if aid, ok := claims["chatgpt_account_id"].(string); ok {
			out.accountID = aid
		}
	}
	if exp, ok := claims["exp"].(float64); ok {
		out.hasExpClaim = true
		out.expired = time.Now().Unix() > int64(exp)
	}
	return out
}

// Session 抽象 plan.Check 需要的 HTTP 能力（*core.Session 满足；测试可注入替身）。
// 只需一个 GET（XHR 语义）——对齐 check_account_plan_curl 用 session.get(...)。
type Session interface {
	Get(ctx context.Context, url, referer string) (*core.Response, error)
	// GetWithHeaders 在公共头之上合并额外头(accounts/check 需要 Authorization Bearer)。
	GetWithHeaders(ctx context.Context, url, referer string, extra map[string]string) (*core.Response, error)
}

// Check 执行一次套餐检查（对齐 check_account_plan_curl 的单次语义，重试由上层
// plancheck service 控制；core.Session 自带 TLS 瞬断重试）。
//
//	accessToken: 账号的 access_token（Bearer）
//	sess:        已绑定代理+指纹的会话（*core.Session 或测试替身）
//	tzOffsetMin: 时区偏移分钟（如 "-480"，对齐 timezone_offset_min 查询参）
func Check(ctx context.Context, sess Session, accessToken, tzOffsetMin string) (*Result, error) {
	token := strings.TrimSpace(accessToken)
	if token == "" {
		return nil, &Error{Code: "access_token_missing"}
	}
	claims := parseTokenClaims(token)
	if claims.hasExpClaim && claims.expired {
		return nil, &Error{Code: "access_token_expired"}
	}
	if tzOffsetMin == "" {
		tzOffsetMin = "-480"
	}
	u := AccountsCheckURL + "?timezone_offset_min=" + url.QueryEscape(tzOffsetMin)

	// accounts/check 需要 Authorization Bearer(access_token);普通 Get 不带认证头会 401。
	resp, err := sess.GetWithHeaders(ctx, u, "https://chatgpt.com/", map[string]string{
		"Authorization": "Bearer " + token,
	})
	if err != nil {
		return nil, &Error{Code: "plan_request_failed", Retryable: true}
	}
	status := resp.StatusCode
	if status == 401 {
		return nil, &Error{Code: "access_token_unauthorized", HTTPStatus: status}
	}
	if status < 200 || status >= 300 {
		return nil, &Error{Code: "plan_http_failed", HTTPStatus: status, Retryable: retryableStatus(status)}
	}
	body := resp.Bytes()
	if len(body) > responseMaxBytes {
		return nil, &Error{Code: "plan_response_too_large", HTTPStatus: status}
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, &Error{Code: "plan_response_invalid", HTTPStatus: status}
	}
	res, err := parseAccountsCheck(payload, claims)
	if err != nil {
		return nil, err
	}
	res.HTTPStatus = status
	res.CheckedAt = time.Now().UTC()
	return res, nil
}

// parseAccountsCheck 解析 accounts/check 响应（对齐 Python parse_accounts_check）。
func parseAccountsCheck(data map[string]any, claims tokenClaims) (*Result, error) {
	accounts, ok := data["accounts"].(map[string]any)
	if !ok {
		return nil, &Error{Code: "plan_response_accounts_missing"}
	}
	// 取目标账号：优先 JWT account_id 命中，再 "default"，再第一个非 default。
	var item map[string]any
	accountKey := ""
	if claims.accountID != "" {
		if v, ok := accounts[claims.accountID].(map[string]any); ok {
			item, accountKey = v, claims.accountID
		}
	}
	if item == nil {
		if v, ok := accounts["default"].(map[string]any); ok {
			item, accountKey = v, "default"
		}
	}
	if item == nil {
		for k, v := range accounts {
			if k == "default" {
				continue
			}
			if m, ok := v.(map[string]any); ok {
				item, accountKey = m, k
				break
			}
		}
	}
	if item == nil {
		return nil, &Error{Code: "plan_response_account_missing"}
	}

	account, _ := item["account"].(map[string]any)
	entitlement, _ := item["entitlement"].(map[string]any)
	campaigns, _ := item["eligible_promo_campaigns"].(map[string]any)
	plusCampaign, _ := campaigns["plus"].(map[string]any)

	// plan_type：account.plan_type 优先，退回 JWT claim。
	rawPlanType := strOf(account, "plan_type")
	if rawPlanType == "" {
		rawPlanType = claims.planType
	}
	subscriptionPlan := strOf(entitlement, "subscription_plan")
	subKey := strings.ToLower(subscriptionPlan)
	hasActive := boolOf(entitlement, "has_active_subscription")

	planType := rawPlanType
	// entitlement 是活跃付费的真源：JWT/account 升级后仍 free，只有
	// has_active_subscription + subscription_plan 含 plus（且非 free）才判 plus。
	subIsPlus := strings.Contains(subKey, "plus") && !strings.Contains(subKey, "free")
	if hasActive && subIsPlus {
		planType = "plus"
	}
	isFree := strings.ToLower(planType) == "free" || subKey == "chatgptfreeplan"

	accountID := strOf(account, "account_id")
	if accountID == "" {
		accountID = claims.accountID
	}
	if accountID == "" {
		accountID = accountKey
	}
	campaignID := strOf(plusCampaign, "id")

	normalized := ""
	if np := strings.ToLower(planType); np == "free" || np == "plus" {
		normalized = np
	}

	return &Result{
		AccountID:             accountID,
		CurrentPlanType:       planType,
		SubscriptionPlan:      subscriptionPlan,
		HasActiveSubscription: hasActive,
		ExpiresAt:             parseISOTime(strOf(entitlement, "expires_at")),
		RenewsAt:              parseISOTime(strOf(entitlement, "renews_at")),
		PlusTrialEligible:     isFree && plusCampaign != nil,
		PlusTrialCampaignID:   campaignID,
		NormalizedAccountType: normalized,
	}, nil
}

// ── JSON 取值小工具 ──

func strOf(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	s, _ := m[key].(string)
	return strings.TrimSpace(s)
}

func boolOf(m map[string]any, key string) bool {
	if m == nil {
		return false
	}
	b, _ := m[key].(bool)
	return b
}

// parseISOTime 解析 ISO8601/RFC3339 时间（对齐 _parse_datetime）；失败返回 nil。
func parseISOTime(s string) *time.Time {
	if s == "" {
		return nil
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		u := t.UTC()
		return &u
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		u := t.UTC()
		return &u
	}
	return nil
}
