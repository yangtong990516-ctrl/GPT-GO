package core

import (
	"math/rand"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// ② HTTP 头注入（网络无关，纯构造逻辑）
// ---------------------------------------------------------------------------

// newTestIdentity 构造一个确定家族的 Identity（跳过随机，直取预设）。
func newTestIdentity(presetName string) *Identity {
	spec := lookupPreset(presetName)
	if spec == nil {
		return nil
	}
	r := rand.New(rand.NewSource(1))
	id := &Identity{
		PresetName:             spec.Name,
		Family:                 spec.Family,
		CountryCode:            "US",
		Timezone:               "America/New_York",
		spec:                   spec,
		SecChUA:                spec.SecChUA,
		SecChUAFullVersionList: spec.SecChUAFullVersionList,
		SecChUAPlatform:        spec.SecChUAPlatform,
		SecChUAPlatformVersion: spec.SecChUAPlatformVersion,
		SecChUAMobile:          spec.SecChUAMobile,
		SecChUAArch:            spec.SecChUAArch,
		SecChUABitness:         spec.SecChUABitness,
		SecChUAModel:           spec.SecChUAModel,
		NavigatorPlatform:      spec.NavigatorPlatform,
		NavigatorVendor:        spec.NavigatorVendor,
		MaxTouchPoints:         spec.MaxTouchPoints,
	}
	id.UserAgent = id.assembleUserAgent(r)
	id.LangPrimary, id.AcceptLang, id.Languages = buildAcceptLanguage(r, "US")
	id.HardwareConcurrency = 8
	id.DevicePixelRatio = 1.0
	id.Screen = "1920x1080"
	if spec.IsChromium() {
		dm := 8
		id.DeviceMemory = &dm
	}
	return id
}

// TestCommonHeadersChromium 验证 Chrome 的 XHR 头：UA + 全套 Client Hints + Sec-Fetch(cors)。
func TestCommonHeadersChromium(t *testing.T) {
	id := newTestIdentity("chrome-148-windows")
	s := &Session{identity: id, retries: 2}
	h := s.commonHeaders("https://auth.openai.com/create-account")

	if got := h["User-Agent"][0]; !strings.Contains(got, "Chrome/148.0.0.0") {
		t.Fatalf("UA 应含 Chrome/148.0.0.0: %s", got)
	}
	// Origin 必须与 referer 同源。
	if got := h["Origin"][0]; got != "https://auth.openai.com" {
		t.Fatalf("Origin 应与 referer 同源: %s", got)
	}
	// Client Hints 全套。
	for _, k := range []string{"sec-ch-ua", "sec-ch-ua-mobile", "sec-ch-ua-platform", "sec-ch-ua-full-version-list", "sec-ch-ua-arch", "sec-ch-ua-bitness", "sec-ch-ua-model", "sec-ch-ua-platform-version"} {
		if len(h[k]) == 0 {
			t.Fatalf("Chrome 应下发 %s", k)
		}
	}
	// sec-ch-ua 版本与 UA 一致。
	if !strings.Contains(h["sec-ch-ua"][0], `"Google Chrome";v="148"`) {
		t.Fatalf("sec-ch-ua 应与 UA 版本一致: %s", h["sec-ch-ua"][0])
	}
	// XHR 形态。
	if h["Sec-Fetch-Mode"][0] != "cors" || h["Sec-Fetch-Dest"][0] != "empty" {
		t.Fatalf("XHR 头 Sec-Fetch 形态错误: %v / %v", h["Sec-Fetch-Mode"], h["Sec-Fetch-Dest"])
	}
}

// TestCommonHeadersSafariNoClientHints 验证 Safari 不发任何 Client Hints（真实浏览器行为）。
func TestCommonHeadersSafariNoClientHints(t *testing.T) {
	id := newTestIdentity("safari-18")
	s := &Session{identity: id, retries: 2}
	h := s.commonHeaders("https://chatgpt.com/auth/login")

	for _, k := range []string{"sec-ch-ua", "sec-ch-ua-mobile", "sec-ch-ua-platform", "sec-ch-ua-full-version-list"} {
		if _, exists := h[k]; exists {
			t.Fatalf("Safari 不应下发 %s", k)
		}
	}
	if !strings.Contains(h["User-Agent"][0], "Macintosh") {
		t.Fatalf("Safari UA 应含 Macintosh: %s", h["User-Agent"][0])
	}
}

// TestNavigationHeadersForm 验证导航头 Sec-Fetch 形态与 Client Hints 一致性。
func TestNavigationHeadersForm(t *testing.T) {
	id := newTestIdentity("chrome-148-windows")
	s := &Session{identity: id, retries: 2}
	h := s.navigationHeaders()

	if h["Sec-Fetch-Mode"][0] != "navigate" || h["Sec-Fetch-Dest"][0] != "document" || h["Sec-Fetch-Site"][0] != "none" {
		t.Fatalf("导航头 Sec-Fetch 形态错误")
	}
	if h["Sec-Fetch-User"][0] != "?1" || h["Upgrade-Insecure-Requests"][0] != "1" {
		t.Fatalf("导航头缺 sec-fetch-user / UIR")
	}
	// Client Hints 与 commonHeaders 同源（都从 Identity 取）。
	if h["sec-ch-ua"][0] != id.SecChUA {
		t.Fatalf("导航头 sec-ch-ua 应与 Identity 同源")
	}
}

// ---------------------------------------------------------------------------
// 代理标准化（socks5 → socks5h）
// ---------------------------------------------------------------------------

func TestNormalizeProxyScheme(t *testing.T) {
	cases := map[string]string{
		"socks5://u:p@1.2.3.4:1080":  "socks5h://u:p@1.2.3.4:1080",
		"socks5h://u:p@1.2.3.4:1080": "socks5h://u:p@1.2.3.4:1080",
		"http://1.2.3.4:8080":        "http://1.2.3.4:8080",
		"":                           "",
		"  socks5://h:1  ":           "socks5h://h:1",
	}
	for in, want := range cases {
		if got := normalizeProxyScheme(in); got != want {
			t.Fatalf("normalizeProxyScheme(%q)=%q, want %q", in, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// 构造期校验（未知 preset 必须在构造期就报错，不放到首次请求）
// ---------------------------------------------------------------------------

func TestNewSessionRejectsUnknownPreset(t *testing.T) {
	bad := &Identity{PresetName: "chrome-999-windows", Family: FamilyChrome}
	if _, err := NewSession(bad, "", 0); err == nil {
		t.Fatal("未知 preset 应在 NewSession 时报错")
	}
	if _, err := NewSession(nil, "", 0); err == nil {
		t.Fatal("nil identity 应报错")
	}
}

// TestNewSessionValidPreset 验证合法 preset 能成功建会话（不发网，仅构造）。
func TestNewSessionValidPreset(t *testing.T) {
	id := newTestIdentity("chrome-148-windows")
	s, err := NewSession(id, "socks5://u:p@127.0.0.1:1080", 0)
	if err != nil {
		t.Fatalf("合法 preset 建会话失败: %v", err)
	}
	defer s.Close()
	if s.Identity() != id {
		t.Fatal("Session 应绑定同一 Identity")
	}
}

// ---------------------------------------------------------------------------
// originOf（Origin 与 referer 同源，auth.openai.com 状态机的关键）
// ---------------------------------------------------------------------------

func TestOriginOf(t *testing.T) {
	cases := map[string]string{
		"https://auth.openai.com/create-account": "https://auth.openai.com",
		"https://chatgpt.com/auth/login":         "https://chatgpt.com",
		"https://chatgpt.com/":                   "https://chatgpt.com",
		"https://chatgpt.com":                    "https://chatgpt.com",
		"":                                       "https://chatgpt.com",
		"not-a-url":                              "https://chatgpt.com",
	}
	for ref, want := range cases {
		if got := originOf(ref, "https://chatgpt.com"); got != want {
			t.Fatalf("originOf(%q)=%q, want %q", ref, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// TLS 瞬断判定
// ---------------------------------------------------------------------------

func TestIsTLSHandshakeErr(t *testing.T) {
	tlsErrs := []string{
		"curl: (35) TLS connect error",
		"tls: handshake failure",
		"ssl error",
		"unexpected EOF",
		"connection reset by peer",
	}
	for _, m := range tlsErrs {
		if !isTLSHandshakeErr(errString(m)) {
			t.Fatalf("%q 应判为 TLS 瞬断", m)
		}
	}
	nonTLS := []string{"context deadline exceeded", "403 Forbidden", "invalid_state", "dns resolve failed"}
	for _, m := range nonTLS {
		if isTLSHandshakeErr(errString(m)) {
			t.Fatalf("%q 不应判为 TLS 瞬断", m)
		}
	}
}

type errString string

func (e errString) Error() string { return string(e) }
