// codex_rt_test.go Codex refresh_token 换取的 mock 单测（不联网）。
//
// 覆盖（对齐 Python 行为 + 本实现的优化点）：
//   - PKCE 对生成：verifier 43~128、challenge == b64url(sha256(verifier))、S256；
//   - buildCodexAuthorize：URL 含 client_id/redirect_uri/scope(offline_access)/state/PKCE/prompt；
//   - callbackHasCode：命中 redirect_uri 且带 code 才 true（不消费 code 的判据）；
//   - dropQueryKeys：no-prompt 兜底去 prompt；
//   - state 硬校验：exchangeCodexCallbackCode 对 state 不匹配直接拒（CSRF 防护）。
package authflow

import (
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"strings"
	"testing"
)

// TestBuildPKCEPair 验证 PKCE 对符合 RFC 7636：verifier 长度 43~128、
// challenge 是 verifier 的 SHA-256 再 b64url-no-pad。
func TestBuildPKCEPair(t *testing.T) {
	verifier, challenge := buildPKCEPair()
	if len(verifier) < 43 || len(verifier) > 128 {
		t.Fatalf("verifier 长度须在 43~128, got %d", len(verifier))
	}
	sum := sha256.Sum256([]byte(verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if challenge != want {
		t.Fatalf("challenge 应= b64url(sha256(verifier)), got %q want %q", challenge, want)
	}
	// challenge 不得含 padding / 非 url-safe 字符。
	if strings.ContainsAny(challenge, "=+/") {
		t.Fatalf("challenge 含非 url-safe 字符: %q", challenge)
	}
}

// codexAuthorizeQuery 解析授权 URL 的 query（占位域名含空格，net/url 无法 parse host，
// 故截取 "?" 后的 query 串解析，避开 host 校验）。
func codexAuthorizeQuery(t *testing.T, authURL string) url.Values {
	t.Helper()
	i := strings.Index(authURL, "?")
	if i < 0 {
		t.Fatalf("授权 URL 无 query: %s", authURL)
	}
	q, err := url.ParseQuery(authURL[i+1:])
	if err != nil {
		t.Fatalf("query 解析失败: %v", err)
	}
	return q
}

// TestBuildCodexAuthorize 验证授权 URL 携带全部必需参数（离线 RT 的关键是 scope 含 offline_access）。
func TestBuildCodexAuthorize(t *testing.T) {
	authURL, state, verifier := buildCodexAuthorize("login")
	if state == "" || verifier == "" {
		t.Fatal("state/verifier 不能为空")
	}
	if !strings.Contains(authURL, "/oauth/authorize?") {
		t.Fatalf("授权端点路径错误: %s", authURL)
	}
	q := codexAuthorizeQuery(t, authURL)
	cases := map[string]string{
		"client_id":             codexClientID,
		"response_type":         "code",
		"redirect_uri":          codexRedirectURI,
		"code_challenge_method": "S256",
		"prompt":                "login",
	}
	for k, want := range cases {
		if got := q.Get(k); got != want {
			t.Fatalf("query[%s]=%q, want %q", k, got, want)
		}
	}
	// scope 必须含 offline_access（否则服务端不下发 refresh_token）。
	if !strings.Contains(q.Get("scope"), "offline_access") {
		t.Fatalf("scope 缺 offline_access: %q", q.Get("scope"))
	}
	if q.Get("state") != state {
		t.Fatalf("URL state 与返回 state 不一致")
	}
	if q.Get("code_challenge") == "" {
		t.Fatal("缺 code_challenge")
	}
}

// TestBuildCodexAuthorizeNoPrompt 验证空 prompt 时 URL 不带 prompt 键（no-prompt 形态）。
func TestBuildCodexAuthorizeNoPrompt(t *testing.T) {
	authURL, _, _ := buildCodexAuthorize("")
	q := codexAuthorizeQuery(t, authURL)
	if _, has := q["prompt"]; has {
		t.Fatal("空 prompt 不应带 prompt 键")
	}
}

// TestCallbackHasCode 验证「命中 redirect_uri 且带 code」的判据（不消费 code 的捕获条件）。
func TestCallbackHasCode(t *testing.T) {
	const ru = "http://localhost:1455/auth/callback"
	cases := []struct {
		url  string
		want bool
	}{
		{"http://localhost:1455/auth/callback?code=abc123&state=xyz", true},
		{"http://localhost:1455/auth/callback/?code=abc", true},          // 尾斜杠容忍
		{"http://localhost:1455/auth/callback?code=&state=xyz", false},   // code 空
		{"http://localhost:1455/auth/callback?state=xyz", false},         // 无 code
		{"http://localhost:1455/other?code=abc", false},                  // 路径不符
		{"https://auth.AI Platform.com/oauth/authorize?code=abc", false}, // host 不符
		{"", false},
	}
	for _, c := range cases {
		if got := callbackHasCode(c.url, ru); got != c.want {
			t.Fatalf("callbackHasCode(%q)=%v, want %v", c.url, got, c.want)
		}
	}
}

// TestDropQueryKeys 验证 no-prompt 兜底去掉 prompt 键、其它键保留。
// 用无空格的合法 host（生产真实域名是 auth.openai.com；含空格的占位域名 net/url 无法 parse）。
func TestDropQueryKeys(t *testing.T) {
	u := "https://auth.openai.com/oauth/authorize?client_id=x&prompt=login&state=s"
	got := dropQueryKeys(u, "prompt")
	q, err := url.Parse(got)
	if err != nil {
		t.Fatalf("dropQueryKeys 返回非合法 URL: %v", err)
	}
	if _, has := q.Query()["prompt"]; has {
		t.Fatal("prompt 键应被去除")
	}
	if q.Query().Get("client_id") != "x" || q.Query().Get("state") != "s" {
		t.Fatalf("其它键应保留: %s", got)
	}
	// 空 url 返回空。
	if dropQueryKeys("", "prompt") != "" {
		t.Fatal("空 url 应返回空")
	}
}

// TestExchangeStateHardCheck 验证 exchange 的 state 硬校验：
// 双方带 state 且不一致时，必须在发任何请求前就拒（CSRF 防护，不发请求）。
// 用空 Flow（无 Session）调用——若校验生效，会在访问 Session 前 return false，不会 panic。
func TestExchangeStateHardCheck(t *testing.T) {
	f := &Flow{} // 无 Session：若误发请求会 nil panic；正确实现应在 state 校验处提前返回
	bad := "http://localhost:1455/auth/callback?code=abc&state=WRONG"
	if f.exchangeCodexCallbackCode(nil, bad, "EXPECTED", "verifier") {
		t.Fatal("state 不匹配必须返回 false（且不得发起请求）")
	}
	// code 缺失也必须在发请求前拒。
	noCode := "http://localhost:1455/auth/callback?state=EXPECTED"
	if f.exchangeCodexCallbackCode(nil, noCode, "EXPECTED", "verifier") {
		t.Fatal("缺 code 必须返回 false")
	}
}

// TestDefaultCodexRTOptions 验证默认选项对齐 Python 默认行为。
func TestDefaultCodexRTOptions(t *testing.T) {
	o := defaultCodexRTOptions()
	if !o.Enabled || o.Prompt != "login" || !o.NoPromptFallback || o.MaxHops != 12 {
		t.Fatalf("默认选项不符 Python 默认: %+v", o)
	}
}
