// plan_test.go 套餐检查解析逻辑的单元测试（不联网，直接喂 parseAccountsCheck）。
//
// 逐分支对齐 codex-auto chatgpt_plan.py parse_accounts_check 的判定：
//   - plan_type 优先 account.plan_type，退回 JWT claim；
//   - entitlement.has_active_subscription + subscription_plan 含 plus → 判 plus（真源）；
//   - free + eligible_promo_campaigns.plus → plus_trial_eligible（有试用资格）。
package plan

import (
	"testing"
	"time"
)

// accountsPayload 构造一个 accounts/check 响应体（map 形态，等同服务端 JSON）。
func accountsPayload(account map[string]any, entitlement map[string]any, campaigns map[string]any) map[string]any {
	item := map[string]any{}
	if account != nil {
		item["account"] = account
	}
	if entitlement != nil {
		item["entitlement"] = entitlement
	}
	if campaigns != nil {
		item["eligible_promo_campaigns"] = campaigns
	}
	return map[string]any{"accounts": map[string]any{"default": item}}
}

// TestParseFreeWithPlusPromo 验证：free 账号 + plus 促销 → 有试用资格 + 活动 id。
func TestParseFreeWithPlusPromo(t *testing.T) {
	data := accountsPayload(
		map[string]any{"plan_type": "free", "account_id": "acc-1"},
		map[string]any{"subscription_plan": "chatgptfreeplan", "has_active_subscription": false},
		map[string]any{"plus": map[string]any{"id": "plus-1-month-free"}},
	)
	res, err := parseAccountsCheck(data, tokenClaims{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.NormalizedAccountType != "free" {
		t.Fatalf("accountType 应 free, got %q", res.NormalizedAccountType)
	}
	if !res.PlusTrialEligible {
		t.Fatal("free+plus促销 应 plus_trial_eligible=true")
	}
	if res.PlusTrialCampaignID != "plus-1-month-free" {
		t.Fatalf("campaign id 错误: %q", res.PlusTrialCampaignID)
	}
	if res.AccountID != "acc-1" {
		t.Fatalf("accountID 应 acc-1, got %q", res.AccountID)
	}
}

// TestParseFreeNoPromo 验证：free 无促销 → 无试用资格。
func TestParseFreeNoPromo(t *testing.T) {
	data := accountsPayload(
		map[string]any{"plan_type": "free"},
		map[string]any{"subscription_plan": "chatgptfreeplan"},
		nil,
	)
	res, err := parseAccountsCheck(data, tokenClaims{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.PlusTrialEligible {
		t.Fatal("free 无促销 应 plus_trial_eligible=false")
	}
}

// TestParsePlusViaEntitlement 验证关键分支：JWT/account 是 free，但 entitlement
// has_active_subscription + subscription_plan 含 plus → 判 plus（付费真源）。
func TestParsePlusViaEntitlement(t *testing.T) {
	data := accountsPayload(
		map[string]any{"plan_type": "free"}, // 升级后 account claim 仍 free
		map[string]any{"subscription_plan": "chatgptplusplan", "has_active_subscription": true},
		nil,
	)
	res, err := parseAccountsCheck(data, tokenClaims{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.NormalizedAccountType != "plus" {
		t.Fatalf("entitlement 活跃 plus 订阅应判 plus, got %q", res.NormalizedAccountType)
	}
	if !res.HasActiveSubscription {
		t.Fatal("has_active_subscription 应 true")
	}
	// 已付费 plus 不再是「试用资格」。
	if res.PlusTrialEligible {
		t.Fatal("已付费 plus 不应再判试用资格")
	}
}

// TestParseSubscriptionFreeNotPlus 验证：subscription_plan 含 free 不算 plus。
func TestParseSubscriptionFreeNotPlus(t *testing.T) {
	data := accountsPayload(
		map[string]any{"plan_type": "free"},
		map[string]any{"subscription_plan": "chatgptfreeplan", "has_active_subscription": true},
		nil,
	)
	res, err := parseAccountsCheck(data, tokenClaims{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.NormalizedAccountType == "plus" {
		t.Fatal("subscription 含 free 不应误判 plus")
	}
}

// TestParseExpiresRenews 验证：entitlement 的 expires_at/renews_at 被解析为时间。
func TestParseExpiresRenews(t *testing.T) {
	data := accountsPayload(
		map[string]any{"plan_type": "plus"},
		map[string]any{
			"subscription_plan":       "chatgptplusplan",
			"has_active_subscription": true,
			"expires_at":              "2026-12-01T00:00:00Z",
			"renews_at":               "2026-11-01T00:00:00Z",
		},
		nil,
	)
	res, err := parseAccountsCheck(data, tokenClaims{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.ExpiresAt == nil || res.ExpiresAt.Month() != time.December {
		t.Fatalf("expires_at 解析错误: %+v", res.ExpiresAt)
	}
	if res.RenewsAt == nil || res.RenewsAt.Month() != time.November {
		t.Fatalf("renews_at 解析错误: %+v", res.RenewsAt)
	}
}

// TestParseMissingAccounts 验证：缺 accounts 字段 → plan_response_accounts_missing。
func TestParseMissingAccounts(t *testing.T) {
	_, err := parseAccountsCheck(map[string]any{}, tokenClaims{})
	pe, ok := err.(*Error)
	if !ok || pe.Code != "plan_response_accounts_missing" {
		t.Fatalf("应报 accounts_missing, got %v", err)
	}
}

// TestParseAccountIDFallback 验证：account_id 缺省时用 default key 兜底。
func TestParseAccountIDFallback(t *testing.T) {
	data := accountsPayload(map[string]any{"plan_type": "free"}, nil, nil)
	res, err := parseAccountsCheck(data, tokenClaims{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.AccountID != "default" {
		t.Fatalf("accountID 应兜底 default, got %q", res.AccountID)
	}
}

// TestErrorTokenInvalid 验证 token 失效类错误的判定（用于置 accessTokenConfigured=false）。
func TestErrorTokenInvalid(t *testing.T) {
	for _, code := range []string{"access_token_expired", "access_token_unauthorized"} {
		if !(&Error{Code: code}).TokenInvalid() {
			t.Fatalf("%s 应 TokenInvalid", code)
		}
	}
	if (&Error{Code: "plan_http_failed"}).TokenInvalid() {
		t.Fatal("plan_http_failed 不应 TokenInvalid")
	}
}
