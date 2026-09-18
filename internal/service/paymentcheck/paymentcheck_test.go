package paymentcheck

import (
	"context"
	"strings"
	"testing"
	"time"

	"gpt-go/internal/model"
	"gpt-go/internal/store"
)

// ── routes.go 测试 ──

func TestParseRoutesText_Grouping(t *testing.T) {
	text := `
# 注释行
VN|VND|vi-VN|http://u:p@vna.example:3010
VN|VND|vi-VN|vnb.example:3010:u:p
DE|EUR|de-DE|http://u:p@de.example:3010
VN|VND|vi-VN|DIRECT
`
	routes, err := ParseRoutesText(text)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("应合并为 2 组线路, 得到 %d", len(routes))
	}
	vn := routes[0]
	if vn.Country != "VN" || vn.Currency != "VND" || vn.Locale != "vi-VN" {
		t.Fatalf("VN 线路字段错误: %+v", vn)
	}
	// DIRECT 不加代理条目：两条有效代理。
	if len(vn.Proxies) != 2 {
		t.Fatalf("VN 线路应有 2 个代理（DIRECT 禁用不计）, 得到 %d", len(vn.Proxies))
	}
	if vn.Proxies[1] != "http://u:p@vnb.example:3010" {
		t.Fatalf("host:port:user:pass 应归一化为标准 URL, 得到 %q", vn.Proxies[1])
	}
}

func TestParseRoutesText_Errors(t *testing.T) {
	if _, err := ParseRoutesText("vn|VND|vi-VN|http://x"); err == nil {
		t.Fatal("小写国家码应报错")
	}
	if _, err := ParseRoutesText("VN|VNDD|vi-VN|http://x"); err == nil {
		t.Fatal("4 位币种应报错")
	}
	if _, err := ParseRoutesText("VN|VND|vi-VN"); err == nil {
		t.Fatal("缺代理段应报错")
	}
}

func TestEnabledRoutes_DirectDisabled(t *testing.T) {
	routes, _ := ParseRoutesText("VN|VND|vi-VN|DIRECT\nPH|PHP|en-PH|http://u:p@ph.example:3010")
	enabled := EnabledRoutes(routes)
	if len(enabled) != 1 || enabled[0].Country != "PH" {
		t.Fatalf("DIRECT 线路应被禁用, 得到 %+v", enabled)
	}
}

// ── parse.go 测试 ──

func TestParsePaymentMethods_TopLevelList(t *testing.T) {
	payload := map[string]any{
		"payment_method_types": []any{"card", "momo", "card_payment", "grab_pay"},
	}
	pr := ParsePaymentMethods(payload, "VN")
	want := []string{"card", "momo", "grabpay"} // card_payment→card 去重, grab_pay→grabpay
	if len(pr.Methods) != len(want) {
		t.Fatalf("渠道数错误: %v", pr.Methods)
	}
	for i, w := range want {
		if pr.Methods[i] != w {
			t.Fatalf("第 %d 个渠道错误: got %q want %q", i, pr.Methods[i], w)
		}
	}
}

func TestParsePaymentMethods_NestedSpecs(t *testing.T) {
	payload := map[string]any{
		"elements_options": map[string]any{
			"payment_method_specs": []any{
				map[string]any{"type": "card"},
				map[string]any{"payment_method_type": "momo"},
			},
		},
	}
	pr := ParsePaymentMethods(payload, "VN")
	if len(pr.Methods) != 2 {
		t.Fatalf("嵌套 specs 应解析出 2 个渠道: %v", pr.Methods)
	}
}

func TestParsePaymentMethods_GcashHeuristic(t *testing.T) {
	payload := map[string]any{
		"custom_payment_methods": []any{
			map[string]any{"id": "cpmt_1togstc6h1nxgoi3wuvey2cj"},
			map[string]any{"id": "cpmt_unknown123"},
		},
	}
	// PH 线路：已知 ID → 启发式 gcash（入 inferred，不进 methods）。
	ph := ParsePaymentMethods(payload, "PH")
	if len(ph.Inferred) != 1 || ph.Inferred[0] != "gcash" {
		t.Fatalf("PH 线路应启发式推断 gcash: %+v", ph)
	}
	// VN 线路：不做同类推断（文档 §19.4）。
	vn := ParsePaymentMethods(payload, "VN")
	if len(vn.Inferred) != 0 || len(vn.Methods) != 0 {
		t.Fatalf("VN 线路不应做 gcash 推断: %+v", vn)
	}
}

func TestNormalizeMethod_Aliases(t *testing.T) {
	cases := map[string]string{
		"card_payment": "card",
		"direct_card":  "card",
		"kakao":        "kakao_pay",
		"go_pay":       "gopay",
		"grab_pay":     "grabpay",
		"MoMo":         "momo",
		"google pay":   "google_pay",
	}
	for in, want := range cases {
		if got := normalizeMethod(in); got != want {
			t.Fatalf("normalizeMethod(%q) = %q, want %q", in, got, want)
		}
	}
}

// ── amount.go 测试 ──

func TestJudgeZero(t *testing.T) {
	// 全部 0 → confirmed。
	st, amt := JudgeZero([]AmountObservation{{"a", 0}, {"b", 0}})
	if st != ZeroConfirmed || amt == nil || *amt != 0 {
		t.Fatalf("全 0 应 zero_confirmed: %s", st)
	}
	// 矛盾 → unknown。
	st, amt = JudgeZero([]AmountObservation{{"a", 0}, {"b", 59900}})
	if st != ZeroUnknown || amt != nil {
		t.Fatalf("矛盾应 zero_unknown: %s amt=%v", st, amt)
	}
	// 无观测 → unknown。
	st, _ = JudgeZero(nil)
	if st != ZeroUnknown {
		t.Fatalf("无观测应 zero_unknown: %s", st)
	}
	// 唯一非 0 → not_zero。
	st, amt = JudgeZero([]AmountObservation{{"invoice.amount_due", 59900}, {"b", 59900}})
	if st != ZeroNotZero || amt == nil || *amt != 59900 {
		t.Fatalf("一致非 0 应 not_zero: %s", st)
	}
}

func TestObserveAmounts_Nested(t *testing.T) {
	payload := map[string]any{
		"checkout_session": map[string]any{
			"invoice": map[string]any{"amount_due": float64(0)},
		},
		"total_summary": map[string]any{"due": "0"},
	}
	obs := ObserveAmounts(payload)
	if len(obs) != 2 {
		t.Fatalf("应采集到 2 条观测: %+v", obs)
	}
	st, amt := JudgeZero(obs)
	if st != ZeroConfirmed || amt == nil {
		t.Fatalf("嵌套 0 元应 confirmed: %s", st)
	}
}

// ── checkout.go 单元逻辑测试 ──

func TestExtractSessionID(t *testing.T) {
	payload := map[string]any{"checkout_session_id": "oaics_abc123"}
	if got := extractSessionID(payload); got != "oaics_abc123" {
		t.Fatalf("顶层字段提取失败: %q", got)
	}
	payload = map[string]any{"checkout_session": map[string]any{"id": "cs_live_xyz"}}
	if got := extractSessionID(payload); got != "cs_live_xyz" {
		t.Fatalf("嵌套字段提取失败: %q", got)
	}
	payload = map[string]any{"url": "https://pay.stripe.com/p/cs_test_999"}
	if got := extractSessionID(payload); got != "cs_test_999" {
		t.Fatalf("正则兜底提取失败: %q", got)
	}
}

func TestClassifyCheckoutError(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   string
	}{
		{400, `{"error":"already paid"}`, "already_paid"},
		{400, `{"error":"bad request"}`, "checkout_rejected"},
		{401, ``, "token_invalid"},
		{403, ``, "risk_blocked"},
		{429, ``, "rate_limited"},
		{500, ``, "checkout_failed"},
	}
	for _, c := range cases {
		if got := classifyCheckoutError(c.status, c.body).Status; got != c.want {
			t.Fatalf("classify(%d) = %q, want %q", c.status, got, c.want)
		}
	}
}

func TestSanitizeError(t *testing.T) {
	raw := "Bearer eyJhbGciOiJIUzI1NiIs.eyJzdWIiOiIxMjM0.SflKxwRJSMeKKF2QT4  unexpected \n error"
	got := sanitizeError(raw)
	if len(got) > 280 {
		t.Fatal("应限长 280")
	}
	if got == raw {
		t.Fatal("应脱敏（值应变化）")
	}
	for _, banned := range []string{"eyJhbGciOiJIUzI1NiIs", "Bearer eyJ"} {
		if strings.Contains(got, banned) {
			t.Fatalf("脱敏后仍含 token: %q", got)
		}
	}
}

// ── aggregate.go 测试 ──

func routeResult(country, status, state string, methods []string, zero string) model.PaymentRouteResult {
	return model.PaymentRouteResult{
		Country: country, Status: status, State: state,
		Methods: methods, ZeroStatus: zero, CheckedAt: time.Now().UTC(),
	}
}

func TestAggregate_Available(t *testing.T) {
	routes := []model.PaymentRouteResult{
		routeResult("VN", StatusAvailable, "payment_methods_available", []string{"momo", "card"}, ZeroConfirmed),
		routeResult("PH", StatusAvailable, "payment_methods_available", []string{"card"}, ZeroNotZero),
	}
	s := Aggregate(routes, 2)
	if s.Status != StatusAvailable {
		t.Fatalf("全部完成且有渠道应 available: %s", s.Status)
	}
	if len(s.Methods) != 2 { // momo + card 去重
		t.Fatalf("渠道应去重: %v", s.Methods)
	}
	if len(s.ZeroMethods) != 2 { // 只有 VN 0 元确认
		t.Fatalf("0 元渠道只应来自 VN 线路: %v", s.ZeroMethods)
	}
}

func TestAggregate_Partial(t *testing.T) {
	routes := []model.PaymentRouteResult{
		routeResult("VN", StatusAvailable, "payment_methods_available", []string{"momo"}, ZeroConfirmed),
		routeResult("PH", StatusRateLimited, "rate_limited", nil, ""),
	}
	s := Aggregate(routes, 2)
	if s.Status != StatusPartial {
		t.Fatalf("一线路有渠道一线路未完成应 partial: %s", s.Status)
	}
}

func TestAggregate_TerminalPriority(t *testing.T) {
	routes := []model.PaymentRouteResult{
		routeResult("VN", StatusTokenInvalid, "token_invalid", nil, ""),
	}
	s := Aggregate(routes, 3)
	if s.Status != StatusTokenInvalid || !s.TokenInvalid {
		t.Fatalf("token_invalid 应优先且标记 TokenInvalid: %+v", s)
	}
}

func TestAggregate_NotReturned(t *testing.T) {
	routes := []model.PaymentRouteResult{
		routeResult("VN", StatusNotReturned, "payment_methods_empty", nil, ZeroUnknown),
	}
	s := Aggregate(routes, 1)
	if s.Status != StatusNotReturned {
		t.Fatalf("全部完成但渠道为空应 not_returned: %s", s.Status)
	}
}

// ── store 集成测试（mock store 的 claim/写回/筛选）──

func TestStoreClaimAndSummary(t *testing.T) {
	ms := store.NewMockAccountStore()
	ok, _ := ms.Create(context.Background(), store.AccountDocument{
		ID: "a1", Email: "a@x.com", EmailNormalized: "a@x.com", AccessToken: "tok-abc",
	})
	if !ok {
		t.Fatal("建号失败")
	}
	// claim。
	doc, err := ms.ClaimPaymentCheck(context.Background(), "a1", 5)
	if err != nil || doc == nil {
		t.Fatal("首次认领应成功")
	}
	if doc.AccessToken != "tok-abc" {
		t.Fatal("认领应返回 AT")
	}
	// 重复认领（未卡死）应失败。
	doc2, _ := ms.ClaimPaymentCheck(context.Background(), "a1", 5)
	if doc2 != nil {
		t.Fatal("running 未卡死时不应重复认领")
	}
	// 线路结果落库。
	rr := model.PaymentRouteResult{Country: "VN", Status: "available", Methods: []string{"momo"}, ZeroStatus: ZeroConfirmed, ExitIP: "203.0.113.7"}
	if err := ms.StorePaymentRouteResult(context.Background(), "a1", rr); err != nil {
		t.Fatal(err)
	}
	// 聚合落库 + token 失效联动。
	if err := ms.StorePaymentSummary(context.Background(), "a1", store.PaymentSummaryUpdate{
		Status: "token_invalid", CheckedAt: time.Now().UTC(), TokenInvalid: true,
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := ms.Get(context.Background(), "a1")
	if got.AccessTokenConfigured {
		t.Fatal("tokenInvalid 应置 accessTokenConfigured=false")
	}
	if got.AliveStatus == nil || *got.AliveStatus != "dead" {
		t.Fatal("tokenInvalid 应联动 aliveStatus=dead")
	}
	if got.PaymentRoutes["VN"].ExitIP != "203.0.113.7" {
		t.Fatal("线路明细应含出口 IP")
	}
}

func TestStorePaymentFilters(t *testing.T) {
	ms := store.NewMockAccountStore()
	_, _ = ms.Create(context.Background(), store.AccountDocument{
		ID: "a1", Email: "a@x.com", EmailNormalized: "a@x.com",
		PaymentMethods: []string{"momo", "card"}, PaymentZeroMethods: []string{"momo"},
	})
	_, _ = ms.Create(context.Background(), store.AccountDocument{
		ID: "b1", Email: "b@x.com", EmailNormalized: "b@x.com",
		PaymentMethods: []string{"card"},
	})
	_, total, _ := ms.List(context.Background(), store.AccountQuery{Payment: "momo"}, 1, 20)
	if total != 1 {
		t.Fatalf("payment=momo 应筛出 1 个, got %d", total)
	}
	_, total, _ = ms.List(context.Background(), store.AccountQuery{ZeroPayment: "momo"}, 1, 20)
	if total != 1 {
		t.Fatalf("zero_payment=momo 应筛出 1 个, got %d", total)
	}
	st := "available"
	_, _ = ms.Create(context.Background(), store.AccountDocument{
		ID: "c1", Email: "c@x.com", EmailNormalized: "c@x.com", PaymentStatus: &st,
	})
	_, total, _ = ms.List(context.Background(), store.AccountQuery{PaymentStatus: "unchecked"}, 1, 20)
	if total != 2 {
		t.Fatalf("payment_status=unchecked 应筛出 2 个（a1/b1 无状态）, got %d", total)
	}
}
