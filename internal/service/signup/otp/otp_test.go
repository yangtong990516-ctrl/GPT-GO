// otp_test.go 验证：extract_otp 防误判、mailcode provider 轮询/防串号/peek/致命判定。
package otp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// ── ExtractOTP 防误判 ──

func TestExtractOTPPrefersSpan(t *testing.T) {
	raw := "Subject: code\r\n\r\n<html><span> 123456 </span></html> 999999"
	if got := ExtractOTP(raw, nil); got != "123456" {
		t.Fatalf("应优先取 <span> 里的码, got %q", got)
	}
}

func TestExtractOTPStripsEmailAndTimestamp(t *testing.T) {
	// 邮箱地址里的 6 位 + 时间戳里的 6 位都不能误判；真正的码在正文。
	raw := "From: user123456@x.com\r\nDate: t=1234567890\r\n\r\nYour code is 654321 m=+111111.0"
	if got := ExtractOTP(raw, nil); got != "654321" {
		t.Fatalf("应剔除邮箱/时间戳取正文码, got %q", got)
	}
}

func TestExtractOTPSkipsMIMEHeader(t *testing.T) {
	// header 里的 6 位（如 Message-ID 数字段）不该被取；取 body 里的。
	raw := "Message-ID: <111222@x>\r\n\r\ncode: 777888"
	if got := ExtractOTP(raw, nil); got != "777888" {
		t.Fatalf("应跳过 header 取 body 码, got %q", got)
	}
}

func TestExtractOTPNotFound(t *testing.T) {
	if got := ExtractOTP("no digits here", nil); got != "" {
		t.Fatalf("无码应返回空, got %q", got)
	}
}

// ── mailcode provider：轮询 + 防串号 ──

// fakeMailServer 模拟 mailcode-api：前 N 次无码/旧码，之后给新码。
func fakeMailServer(t *testing.T, hits *int32, giveAfter int, code string, ts int64) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(hits, 1)
		if int(n) < giveAfter {
			fmt.Fprint(w, `{"verificationCode":""}`)
			return
		}
		fmt.Fprintf(w, `{"verificationCode":%q,"receivedAt":%d}`, code, ts)
	}))
}

func TestMailcodeWaitForOTPPolls(t *testing.T) {
	var hits int32
	srv := fakeMailServer(t, &hits, 3, "246810", 0)
	defer srv.Close()

	p := NewMailcodeProvider(srv.URL+"/api/mail?email=a@x.com",
		WithMailcodePollInterval(5*time.Millisecond))
	code, err := p.WaitForOTP(context.Background(), "a@x.com", 5, 0)
	if err != nil {
		t.Fatalf("WaitForOTP err: %v", err)
	}
	if code != "246810" {
		t.Fatalf("应轮询到码 246810, got %q", code)
	}
	if atomic.LoadInt32(&hits) < 3 {
		t.Fatalf("应轮询多次, 实际 %d 次", hits)
	}
}

func TestMailcodeIssuedAfterFiltersOldCode(t *testing.T) {
	// 服务端返回的旧码 receivedAt=100，但 issuedAfter=200 → 应被过滤（等不到→超时）。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"verificationCode":"111111","receivedAt":100}`)
	}))
	defer srv.Close()
	p := NewMailcodeProvider(srv.URL, WithMailcodePollInterval(5*time.Millisecond))
	_, err := p.WaitForOTP(context.Background(), "a@x.com", 1, 200)
	if err == nil {
		t.Fatal("旧码(时间窗外)应被过滤导致超时, 不应取到")
	}
	if e, ok := err.(*Error); !ok || e.Kind != "timeout" {
		t.Fatalf("应返回 timeout 类错误, got %v", err)
	}
}

func TestMailcodeIssuedAfterAcceptsNewCode(t *testing.T) {
	// receivedAt=300 > issuedAfter=200 → 应接受。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"verificationCode":"222222","receivedAt":300}`)
	}))
	defer srv.Close()
	p := NewMailcodeProvider(srv.URL, WithMailcodePollInterval(5*time.Millisecond))
	code, err := p.WaitForOTP(context.Background(), "a@x.com", 2, 200)
	if err != nil || code != "222222" {
		t.Fatalf("时间窗内新码应被接受, code=%q err=%v", code, err)
	}
}

func TestMailcodePeekOTPNonDestructive(t *testing.T) {
	var hits int32
	srv := fakeMailServer(t, &hits, 1, "135790", 0)
	defer srv.Close()
	p := NewMailcodeProvider(srv.URL+"/api/mail", WithMailcodePollInterval(5*time.Millisecond))
	// peek 命中
	code, err := p.PeekOTP(context.Background(), "a@x.com", 0, 0)
	if err != nil || code != "135790" {
		t.Fatalf("peek 应命中, code=%q err=%v", code, err)
	}
	// peek 是单次非破坏的（不记 seen），紧接着再 peek 仍能拿到（同一封没被消费）。
	code2, _ := p.PeekOTP(context.Background(), "a@x.com", 0, 0)
	if code2 != "135790" {
		t.Fatalf("peek 非破坏性: 再次 peek 应仍拿到, got %q", code2)
	}
}

func TestMailcodePeekNoCodeReturnsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"verificationCode":""}`)
	}))
	defer srv.Close()
	p := NewMailcodeProvider(srv.URL)
	code, err := p.PeekOTP(context.Background(), "a@x.com", 0, 0)
	if err != nil || code != "" {
		t.Fatalf("peek 无码应返回空且不报错, code=%q err=%v", code, err)
	}
}

func TestMailcodeFatalOnAuthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	p := NewMailcodeProvider(srv.URL)
	_, err := p.WaitForOTP(context.Background(), "a@x.com", 2, 0)
	if !IsFatal(err) {
		t.Fatalf("401 应判致命(号废), got %v", err)
	}
	if !p.Exhausted() {
		t.Fatal("致命错误后 Exhausted 应为 true")
	}
}

func TestMailcodeExtractsFromBody(t *testing.T) {
	// 服务端返回原始邮件正文（无已解析验证码字段），应本地 ExtractOTP 抠码。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"body":"Your verification code is 555777. Valid 10 min."}`)
	}))
	defer srv.Close()
	p := NewMailcodeProvider(srv.URL, WithMailcodePollInterval(5*time.Millisecond))
	code, err := p.WaitForOTP(context.Background(), "a@x.com", 5, 0)
	if err != nil || code != "555777" {
		t.Fatalf("应从正文抠码 555777, code=%q err=%v", code, err)
	}
}

// ── 注册表 ──

func TestRegistryNewAndUnknown(t *testing.T) {
	p, err := New(context.Background(), "mailcode", nil, map[string]any{"accessUrl": "http://x/api/mail?email=a@x.com"})
	if err != nil {
		t.Fatalf("New(mailcode) err: %v", err)
	}
	if p.Caps().Kind != "mailcode" {
		t.Fatalf("kind 应=mailcode, got %q", p.Caps().Kind)
	}
	// 未知 kind 应返回致命错误（不静默回退）。
	_, err = New(context.Background(), "nonexistent", nil, nil)
	if err == nil || !IsFatal(err) {
		t.Fatalf("未知 kind 应返回致命错误, got %v", err)
	}
}

func TestMailcodeFromConfigBuildsURL(t *testing.T) {
	// 无 accessUrl 时，由 baseURL+email 现拼。
	p, err := New(context.Background(), "mailcode",
		map[string]any{"baseURL": "https://mail.example.com"},
		map[string]any{"email": "a@x.com"})
	if err != nil {
		t.Fatalf("from_config err: %v", err)
	}
	if p == nil {
		t.Fatal("应能由 baseURL+email 构造")
	}
}
