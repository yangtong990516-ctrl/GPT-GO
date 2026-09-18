// signup_wiring_test.go 注册引擎装配的 mock 测试（不联网）。
//
// 核心验证「邮箱池 SourceType → otp provider kind」映射（修复并发串号 + 多源识别）：
//   - remail 邮箱（accessUrl=/v1/pickup，返回 verificationCode JSON）→ jsoncode provider
//   - mailcode/其它邮箱（accessUrl=/api/mail，返回邮件原文）→ mailcode provider
//     确保 remail 邮箱不再被误判为 mailcode（导致拿 JSON 走邮件正则、取不到码）。
package apiserver

import (
	"context"
	"testing"

	"gpt-go/internal/service/signup/otp"
)

// TestOtpKindForSource 验证 SourceType → otp kind 的映射规则。
func TestOtpKindForSource(t *testing.T) {
	cases := map[string]string{
		"remail":        "jsoncode", // 接码平台，取 verificationCode 字段
		"mailcode":      "mailcode", // 邮件原文正则提码
		"mailcom_alias": "mailcode",
		"standard":      "mailcode",
		"manual":        "mailcode",
		"":              "mailcode", // 空 source 默认 mailcode
		"unknown":       "mailcode",
	}
	for src, want := range cases {
		if got := otpKindForSource(src); got != want {
			t.Fatalf("otpKindForSource(%q)=%q, want %q", src, got, want)
		}
	}
}

// TestOtpNewWithPoolAccessURL 验证两种 kind 都能用邮箱池的 accessUrl 构造出 provider。
//
// 这正是「修复 remail 误判」的端到端断言：remail 的 /v1/pickup accessUrl
// 经 jsoncode 工厂能构造成功（不会被 mailcode 正则路径吞掉）。
func TestOtpNewWithPoolAccessURL(t *testing.T) {
	ctx := context.Background()

	// remail：accessUrl 指向 /v1/pickup（JSON 接码）。jsoncode 工厂应接受 accessUrl。
	remailAcct := map[string]any{
		"email":     "user@remail.example",
		"accessUrl": "https://remail.example/v1/pickup?email=user@remail.example&token=tok",
	}
	p1, err := otp.New(ctx, "jsoncode", nil, remailAcct)
	if err != nil || p1 == nil {
		t.Fatalf("jsoncode 用 remail accessUrl 构造失败: %v", err)
	}
	if p1.Caps().Kind != "jsoncode" {
		t.Fatalf("应为 jsoncode provider, got %q", p1.Caps().Kind)
	}

	// mailcode：accessUrl 指向 /api/mail（邮件原文）。mailcode 工厂应接受 accessUrl。
	mailcodeAcct := map[string]any{
		"email":     "user@mail.example",
		"accessUrl": "https://mail.example/api/mail?email=user@mail.example",
	}
	p2, err := otp.New(ctx, "mailcode", nil, mailcodeAcct)
	if err != nil || p2 == nil {
		t.Fatalf("mailcode 用 accessUrl 构造失败: %v", err)
	}
	if p2.Caps().Kind != "mailcode" {
		t.Fatalf("应为 mailcode provider, got %q", p2.Caps().Kind)
	}
}

// TestOtpKindRegistered 确认两个 kind 都已注册进 otp 工厂（装配前提）。
func TestOtpKindRegistered(t *testing.T) {
	kinds := map[string]bool{}
	for _, k := range otp.ListKinds() {
		kinds[k] = true
	}
	if !kinds["mailcode"] || !kinds["jsoncode"] {
		t.Fatalf("otp 工厂缺 kind: mailcode=%v jsoncode=%v (kinds=%v)",
			kinds["mailcode"], kinds["jsoncode"], otp.ListKinds())
	}
}
