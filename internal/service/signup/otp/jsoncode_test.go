// jsoncode_test.go 通用接码平台 Provider 的 mock 测试（不联网，用 httptest）。
//
// 覆盖用户点名的识别要求：各种 JSON 返回格式里，能取出【正确的 code】：
//   - {"verificationCode": "123456"}      → 取 verificationCode
//   - {"code": "123456"}                  → 兜底取 code
//   - {"verificationCode": "", "code": "123456"} → 空则降级到 code
//   - {"data": {"verificationCode": "..."}} → 点路径（自定义 codeFields）
//   - 平台未出码（字段空）→ 轮询直到出码
//   - 取消 / 超时 / 非 JSON 的处理
package otp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// jsoncodeTestServer 起一个在 path 上返回给定 JSON body 的 mock 接码平台。
func jsoncodeTestServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// fastProvider 用极短轮询间隔构造 provider（测试提速）。
func fastProvider(url string, opts ...JSONCodeOption) *JSONCodeProvider {
	base := []JSONCodeOption{WithJSONCodePollInterval(5 * time.Millisecond)}
	return NewJSONCodeProvider(url, append(base, opts...)...)
}

// TestJSONCodeVerificationCodeField 验证标准格式：取 verificationCode 字段。
func TestJSONCodeVerificationCodeField(t *testing.T) {
	srv := jsoncodeTestServer(t, `{"verificationCode": "654321", "status": "ok"}`)
	p := fastProvider(srv.URL)
	code, err := p.WaitForOTP(context.Background(), "user@example.com", 5, 0)
	if err != nil {
		t.Fatalf("WaitForOTP err: %v", err)
	}
	if code != "654321" {
		t.Fatalf("应取 verificationCode=654321, got %q", code)
	}
}

// TestJSONCodeFallbackToCode 验证兜底：无 verificationCode 时取 code 字段（对齐 bridge.py）。
func TestJSONCodeFallbackToCode(t *testing.T) {
	srv := jsoncodeTestServer(t, `{"code": "112233"}`)
	p := fastProvider(srv.URL)
	code, err := p.WaitForOTP(context.Background(), "user@example.com", 5, 0)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if code != "112233" {
		t.Fatalf("应兜底取 code=112233, got %q", code)
	}
}

// TestJSONCodeEmptyVerificationFallsToCode 验证 verificationCode 为空串时降级到 code。
func TestJSONCodeEmptyVerificationFallsToCode(t *testing.T) {
	srv := jsoncodeTestServer(t, `{"verificationCode": "", "code": "998877"}`)
	p := fastProvider(srv.URL)
	code, err := p.WaitForOTP(context.Background(), "user@example.com", 5, 0)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if code != "998877" {
		t.Fatalf("verificationCode 空应降级 code=998877, got %q", code)
	}
}

// TestJSONCodeNestedDotPath 验证嵌套 JSON + 自定义点路径 codeFields（"data.code"）。
func TestJSONCodeNestedDotPath(t *testing.T) {
	srv := jsoncodeTestServer(t, `{"data": {"sms": {"code": "445566"}}}`)
	p := fastProvider(srv.URL, WithJSONCodeFields("data.sms.code"))
	code, err := p.WaitForOTP(context.Background(), "user@example.com", 5, 0)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if code != "445566" {
		t.Fatalf("点路径应取 data.sms.code=445566, got %q", code)
	}
}

// TestJSONCodePollUntilReady 验证平台未出码时持续轮询，出码后即取到（不提前致死）。
func TestJSONCodePollUntilReady(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		if n < 3 {
			_, _ = w.Write([]byte(`{"verificationCode": ""}`)) // 前 2 次未出码
			return
		}
		_, _ = w.Write([]byte(`{"verificationCode": "777888"}`)) // 第 3 次出码
	}))
	defer srv.Close()
	p := fastProvider(srv.URL)
	code, err := p.WaitForOTP(context.Background(), "user@example.com", 5, 0)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if code != "777888" {
		t.Fatalf("轮询后应取到 777888, got %q", code)
	}
	if atomic.LoadInt32(&calls) < 3 {
		t.Fatalf("应轮询至少 3 次, got %d", calls)
	}
}

// TestJSONCodeCancel 验证 ctx 取消 → 返回 cancelled 错误（不致死）。
func TestJSONCodeCancel(t *testing.T) {
	srv := jsoncodeTestServer(t, `{"verificationCode": ""}`) // 永不出码
	p := fastProvider(srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	_, err := p.WaitForOTP(ctx, "user@example.com", 30, 0)
	if err == nil {
		t.Fatal("取消应返回错误")
	}
	var oe *Error
	if !asOTPError(err, &oe) || oe.Kind != "cancelled" {
		t.Fatalf("应为 cancelled 错误, got %v", err)
	}
}

// TestJSONCodeTimeout 验证超时 → 返回 timeout 错误 + 号标记 dead。
func TestJSONCodeTimeout(t *testing.T) {
	srv := jsoncodeTestServer(t, `{"verificationCode": ""}`)
	p := fastProvider(srv.URL)
	_, err := p.WaitForOTP(context.Background(), "user@example.com", 1, 0)
	if err == nil {
		t.Fatal("超时应返回错误")
	}
	var oe *Error
	if !asOTPError(err, &oe) || oe.Kind != "timeout" {
		t.Fatalf("应为 timeout 错误, got %v", err)
	}
	if !p.Exhausted() {
		t.Fatal("超时后号应标记 dead（Pooled 语义）")
	}
}

// TestJSONCodeMissingURL 验证缺轮询 URL → 致命错误（不静默回退）。
func TestJSONCodeMissingURL(t *testing.T) {
	p := fastProvider("")
	_, err := p.WaitForOTP(context.Background(), "user@example.com", 5, 0)
	var oe *Error
	if !asOTPError(err, &oe) || oe.Kind != "missing_poll_url" || !oe.Fatal {
		t.Fatalf("缺 URL 应为致命 missing_poll_url, got %v", err)
	}
}

// TestJSONCodePeek 验证 PeekOTP 预读：有码返回、失败返回 "",nil（不抛错）。
func TestJSONCodePeek(t *testing.T) {
	srv := jsoncodeTestServer(t, `{"verificationCode": "101010"}`)
	p := fastProvider(srv.URL)
	code, err := p.PeekOTP(context.Background(), "user@example.com", 0, 0)
	if err != nil || code != "101010" {
		t.Fatalf("Peek 应取到 101010, got %q err=%v", code, err)
	}
}

// TestJSONCodeCaps 验证能力声明（Pooled 接码号、非 Ephemeral）。
func TestJSONCodeCaps(t *testing.T) {
	p := NewJSONCodeProvider("http://x")
	c := p.Caps()
	if c.Kind != jsoncodeKind || !c.Pooled || c.Ephemeral {
		t.Fatalf("能力声明错误: %+v", c)
	}
}

// TestJSONCodeRegistered 验证 jsoncode 已注册进全局工厂（otp.New 可构造）。
func TestJSONCodeRegistered(t *testing.T) {
	found := false
	for _, k := range ListKinds() {
		if k == jsoncodeKind {
			found = true
		}
	}
	if !found {
		t.Fatalf("jsoncode 未注册, kinds=%v", ListKinds())
	}
	// 工厂构造：account 带 accessUrl。
	prov, err := New(context.Background(), jsoncodeKind, nil, map[string]any{"accessUrl": "http://x/poll"})
	if err != nil || prov == nil {
		t.Fatalf("otp.New(jsoncode) 失败: %v", err)
	}
}

// ── remail 取件形态（{items:[{receivedAt, verificationCode}]}）专项测试 ──
// 依据 remail 官方 OpenAPI 文档：GET /v1/pickup 返回 {items:[MailMessage], fetch:FetchState}，
// MailMessage 含 receivedAt(date-time) + verificationCode。防串号必须按 receivedAt 过滤。

// TestRemailItemsTakesLatest 验证：多封邮件里取 receivedAt 最新一封的 code。
func TestRemailItemsTakesLatest(t *testing.T) {
	body := `{"items":[
		{"sender":"a@x.com","receivedAt":"2026-01-01T00:00:10Z","verificationCode":"111111"},
		{"sender":"a@x.com","receivedAt":"2026-01-01T00:00:30Z","verificationCode":"333333"},
		{"sender":"a@x.com","receivedAt":"2026-01-01T00:00:20Z","verificationCode":"222222"}
	],"fetch":{"lastStatus":"normal"}}`
	srv := jsoncodeTestServer(t, body)
	p := fastProvider(srv.URL)
	// issuedAfter=0：全收 → 取最新（receivedAt=00:00:30 的 333333）。
	code, err := p.WaitForOTP(context.Background(), "u@example.com", 5, 0)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if code != "333333" {
		t.Fatalf("应取 receivedAt 最新的 333333, got %q", code)
	}
}

// TestRemailItemsFiltersOldCode 验证防串号：receivedAt 早于 issuedAfter 的旧码被排除。
func TestRemailItemsFiltersOldCode(t *testing.T) {
	// 窗口起点 = 2026-01-01T00:00:15Z（unix）。
	windowStart := time.Date(2026, 1, 1, 0, 0, 15, 0, time.UTC).Unix()
	body := `{"items":[
		{"sender":"a@x.com","receivedAt":"2026-01-01T00:00:10Z","verificationCode":"OLD111"},
		{"sender":"a@x.com","receivedAt":"2026-01-01T00:00:30Z","verificationCode":"NEW999"}
	],"fetch":{"lastStatus":"normal"}}`
	srv := jsoncodeTestServer(t, body)
	p := fastProvider(srv.URL)
	code, err := p.WaitForOTP(context.Background(), "u@example.com", 5, windowStart)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if code != "NEW999" {
		t.Fatalf("应排除旧码 OLD111, 取窗口内 NEW999, got %q", code)
	}
}

// TestRemailItemsAllOldKeepsPolling 验证：所有邮件都在窗口前（全是旧码）→ 不返回，继续轮询到超时。
func TestRemailItemsAllOldKeepsPolling(t *testing.T) {
	windowStart := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC).Unix() // 远晚于邮件时间
	body := `{"items":[
		{"sender":"a@x.com","receivedAt":"2026-01-01T00:00:10Z","verificationCode":"OLD111"}
	],"fetch":{"lastStatus":"normal"}}`
	srv := jsoncodeTestServer(t, body)
	p := fastProvider(srv.URL)
	// 超时 1s：窗口内无新码 → 应超时而不是误取 OLD111。
	_, err := p.WaitForOTP(context.Background(), "u@example.com", 1, windowStart)
	var oe *Error
	if !asOTPError(err, &oe) || oe.Kind != "timeout" {
		t.Fatalf("窗口内无新码应 timeout(不取旧码), got err=%v", err)
	}
}

// TestRemailItemsMissingReceivedAtIgnored 验证：receivedAt 缺失/无法解析的邮件被忽略
// （宁可等下一封，也不拿无法确认时效的码——对齐「务必尊重 issued_after」）。
func TestRemailItemsMissingReceivedAtIgnored(t *testing.T) {
	body := `{"items":[
		{"sender":"a@x.com","verificationCode":"NO_TIME"},
		{"sender":"a@x.com","receivedAt":"2026-01-01T00:00:30Z","verificationCode":"HAS_TIME"}
	],"fetch":{"lastStatus":"normal"}}`
	srv := jsoncodeTestServer(t, body)
	p := fastProvider(srv.URL)
	code, err := p.WaitForOTP(context.Background(), "u@example.com", 5, 0)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if code != "HAS_TIME" {
		t.Fatalf("应忽略无 receivedAt 的码, 取 HAS_TIME, got %q", code)
	}
}

// TestRemailItemsDoesNotFallToTopLevel 验证：响应含 items 结构时，即使窗口内无新码，
// 也不退化到顶层字段（remail 顶层本就没 code；若有则是别的平台，不应混用）。
func TestRemailItemsDoesNotFallToTopLevel(t *testing.T) {
	windowStart := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC).Unix()
	// items 里全是旧码，但顶层有个 code——形态A命中后不应取顶层 code。
	body := `{"items":[{"receivedAt":"2026-01-01T00:00:10Z","verificationCode":"OLD"}],"code":"TOP_LEVEL"}`
	srv := jsoncodeTestServer(t, body)
	p := fastProvider(srv.URL)
	_, err := p.WaitForOTP(context.Background(), "u@example.com", 1, windowStart)
	var oe *Error
	if !asOTPError(err, &oe) || oe.Kind != "timeout" {
		t.Fatalf("形态A命中后不应退化取顶层 code, 应 timeout, got err=%v", err)
	}
}

// TestRemailEmptyItemsPolls 验证：items 为空数组（平台还没出码）→ 继续轮询。
func TestRemailEmptyItemsPolls(t *testing.T) {
	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		n := atomic.AddInt64(&calls, 1)
		if n < 3 {
			_, _ = w.Write([]byte(`{"items":[],"fetch":{"lastStatus":"pending"}}`))
			return
		}
		// 第3次出新码。
		_, _ = w.Write([]byte(`{"items":[{"receivedAt":"2026-01-01T00:00:30Z","verificationCode":"777777"}]}`))
	}))
	t.Cleanup(srv.Close)
	p := fastProvider(srv.URL)
	code, err := p.WaitForOTP(context.Background(), "u@example.com", 5, 0)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if code != "777777" {
		t.Fatalf("空 items 后轮询应取到新码 777777, got %q", code)
	}
}

// asOTPError 是 errors.As 的本地小封装（避免引 errors 只为这一处）。
func asOTPError(err error, target **Error) bool {
	for err != nil {
		if oe, ok := err.(*Error); ok {
			*target = oe
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
