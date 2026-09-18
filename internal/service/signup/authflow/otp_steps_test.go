// otp_steps_test.go 认证动作单测：kickoff 决策、challenge_id 提取、mfa 判断、默认密码规则。
package authflow

import "testing"

// TestIsExistingFlow 验证 resend vs send 的判定（防 state 破坏的命门）。
func TestIsExistingFlow(t *testing.T) {
	cases := []struct {
		name     string
		flow     *Flow
		modeHint string
		want     bool
	}{
		{"新注册(默认)", &Flow{}, "new_register", false},
		{"modeHint含existing", &Flow{}, "existing_login", true},
		{"modeHint含passwordless_login", &Flow{}, "passwordless_login", true},
		{"modeHint含passwordless_signup", &Flow{}, "passwordless_signup", true},
		{"探测到passwordless_signup", &Flow{existingVerificationMode: "passwordless_signup"}, "", true},
		{"isExistingAccount已标记", &Flow{isExistingAccount: true}, "", true},
		{"新注册空hint", &Flow{}, "", false},
	}
	for _, c := range cases {
		if got := c.flow.isExistingFlow(c.modeHint); got != c.want {
			t.Fatalf("%s: isExistingFlow(%q)=%v, want %v", c.name, c.modeHint, got, c.want)
		}
	}
}

// TestExtractMfaChallengeID 验证从 continue_url 提取 mfa challenge ID。
func TestExtractMfaChallengeID(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"https://auth.openai.com/mfa-challenge/6a76f2e8-abc", "6a76f2e8-abc"},
		{"https://auth.openai.com/mfa-challenge/6a76f2e8?foo=bar", "6a76f2e8"},
		{"https://auth.openai.com/mfa-challenge/6a76f2e8/", "6a76f2e8"},
		{"https://auth.openai.com/email-verification", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := extractMfaChallengeID(c.url); got != c.want {
			t.Fatalf("extractMfaChallengeID(%q)=%q, want %q", c.url, got, c.want)
		}
	}
}

// TestIsMfaChallengeState 验证 mfa-challenge 状态判断（page.type 或 continue_url）。
func TestIsMfaChallengeState(t *testing.T) {
	cases := []struct {
		pageType, continueURL string
		want                  bool
	}{
		{"mfa_challenge", "", true},
		{"MFA_CHALLENGE", "", true}, // 大小写不敏感
		{"", "https://x/mfa-challenge/abc", true},
		{"login_password", "", false},
		{"email_otp_verification", "https://x/email-verification", false},
		{"", "", false},
	}
	for _, c := range cases {
		if got := isMfaChallengeState(c.pageType, c.continueURL); got != c.want {
			t.Fatalf("isMfaChallengeState(%q,%q)=%v, want %v", c.pageType, c.continueURL, got, c.want)
		}
	}
}

// TestDefaultPasswordFromEmail 验证默认猜密码规则（对齐 Python：去@，不足8位补 2026OpenAI）。
func TestDefaultPasswordFromEmail(t *testing.T) {
	cases := []struct {
		email string
		want  string
	}{
		{"abcdef@x.com", "abcdefx.com"},   // 去@，长度≥8 不补
		{"ab@x.com", "abx.com2026OpenAI"}, // 去@后 <8 位补后缀
		{"longusername@y.com", "longusernamey.com"},
	}
	for _, c := range cases {
		if got := defaultPasswordFromEmail(c.email); got != c.want {
			t.Fatalf("defaultPasswordFromEmail(%q)=%q, want %q", c.email, got, c.want)
		}
	}
}

// TestPageTypeAndContinueURLExtract 验证 page.type / continue_url 提取辅助。
func TestPageTypeAndContinueURLExtract(t *testing.T) {
	data := map[string]any{
		"page":         map[string]any{"type": "login_password", "payload": map[string]any{"email_verification_mode": "passwordless_login"}},
		"continue_url": "https://auth.openai.com/log-in/password",
	}
	if got := pageTypeOf(data); got != "login_password" {
		t.Fatalf("pageTypeOf=%q, want login_password", got)
	}
	if got := continueURLOf(data); got != "https://auth.openai.com/log-in/password" {
		t.Fatalf("continueURLOf=%q", got)
	}
	if got := nestedStrOf(data, "page", "payload", "email_verification_mode"); got != "passwordless_login" {
		t.Fatalf("nestedStrOf(mode)=%q, want passwordless_login", got)
	}
}
