package core

import (
	"math/rand"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// TLS ↔ UA ↔ SO 三路一致性（本包存在的核心理由）
// ---------------------------------------------------------------------------

// TestTLSUASOCoherence 验证：任一 Identity，其 preset 家族、UA 字符串、navigator 三者必须同源。
// 这是「UA 说 X、sec-ch-ua 说 Y、navigator.platform 说 Z」自相矛盾的反向断言。
func TestTLSUASOCoherence(t *testing.T) {
	for seed := int64(0); seed < 200; seed++ {
		r := rand.New(rand.NewSource(seed))
		id := NewIdentity(r, "US", "America/New_York", "")

		ua := id.UserAgent
		switch id.Family {
		case FamilyChrome:
			// UA 必须含 Chrome/<ver>，且 navigator 必须是 Win32 + Google Inc.
			if !strings.Contains(ua, "Chrome/") {
				t.Fatalf("seed=%d chrome 但 UA 无 Chrome/: %s", seed, ua)
			}
			if id.NavigatorPlatform != "Win32" || id.NavigatorVendor != "Google Inc." {
				t.Fatalf("seed=%d chrome navigator 不自洽: platform=%s vendor=%s", seed, id.NavigatorPlatform, id.NavigatorVendor)
			}
			// Chromium 必须发 Client Hints，且 sec-ch-ua 里的版本要与 UA 里的 Chrome 主版本一致。
			if id.SecChUA == "" {
				t.Fatalf("seed=%d chrome 但 SecChUA 为空", seed)
			}
			major := chromeMajorFromUA(ua)
			if major == "" || !strings.Contains(id.SecChUA, `"Google Chrome";v="`+major+`"`) {
				t.Fatalf("seed=%d UA 主版本(%s) 与 sec-ch-ua(%s) 不一致", seed, major, id.SecChUA)
			}
			// Chromium 才暴露 deviceMemory。
			if id.DeviceMemory == nil {
				t.Fatalf("seed=%d chrome 应暴露 deviceMemory", seed)
			}
		case FamilyMacSafari:
			if !strings.Contains(ua, "Macintosh") || !strings.Contains(ua, "Safari/") {
				t.Fatalf("seed=%d mac_safari UA 异常: %s", seed, ua)
			}
			if id.NavigatorPlatform != "MacIntel" || id.NavigatorVendor != "Apple Computer, Inc." {
				t.Fatalf("seed=%d mac_safari navigator 不自洽", seed)
			}
			// Safari 不发 Client Hints（真实浏览器行为）。
			if id.SecChUA != "" {
				t.Fatalf("seed=%d mac_safari 不应下发 sec-ch-ua", seed)
			}
			// Safari 不暴露 deviceMemory。
			if id.DeviceMemory != nil {
				t.Fatalf("seed=%d mac_safari 不应暴露 deviceMemory", seed)
			}
			// Retina 像素比必定 2.0。
			if id.DevicePixelRatio != 2.0 {
				t.Fatalf("seed=%d mac_safari DPR 应=2.0, got %v", seed, id.DevicePixelRatio)
			}
		case FamilyIOSSafari:
			if !strings.Contains(ua, "iPhone") {
				t.Fatalf("seed=%d ios_safari UA 应含 iPhone: %s", seed, ua)
			}
			if id.NavigatorPlatform != "iPhone" || id.MaxTouchPoints != 5 {
				t.Fatalf("seed=%d ios_safari navigator 不自洽: platform=%s touch=%d", seed, id.NavigatorPlatform, id.MaxTouchPoints)
			}
			if id.DeviceMemory != nil {
				t.Fatalf("seed=%d ios_safari 不应暴露 deviceMemory", seed)
			}
		case FamilyFirefox:
			if !strings.Contains(ua, "Firefox/") {
				t.Fatalf("seed=%d firefox UA 应含 Firefox/: %s", seed, ua)
			}
			// Firefox navigator.vendor 为空串（不是 undefined）。
			if id.NavigatorVendor != "" {
				t.Fatalf("seed=%d firefox vendor 应为空串, got %q", seed, id.NavigatorVendor)
			}
			if id.SecChUA != "" {
				t.Fatalf("seed=%d firefox 不应下发 sec-ch-ua", seed)
			}
			if id.DeviceMemory != nil {
				t.Fatalf("seed=%d firefox 不应暴露 deviceMemory", seed)
			}
		default:
			t.Fatalf("seed=%d 未知家族 %s", seed, id.Family)
		}

		// UA ↔ navigator.platform 的平台必须一致（Win UA 不能配 MacIntel）。
		assertPlatformConsistent(t, seed, id)
	}
}

// assertPlatformConsistent 校验 UA 里的平台与 navigator.platform 严格一致。
func assertPlatformConsistent(t *testing.T, seed int64, id *Identity) {
	ua := id.UserAgent
	wantPlatform := id.NavigatorPlatform
	switch wantPlatform {
	case "Win32":
		if !strings.Contains(ua, "Windows NT") {
			t.Fatalf("seed=%d navigator=Win32 但 UA 无 Windows NT: %s", seed, ua)
		}
	case "MacIntel":
		if !strings.Contains(ua, "Macintosh") {
			t.Fatalf("seed=%d navigator=MacIntel 但 UA 无 Macintosh: %s", seed, ua)
		}
	case "iPhone":
		if !strings.Contains(ua, "iPhone") {
			t.Fatalf("seed=%d navigator=iPhone 但 UA 无 iPhone: %s", seed, ua)
		}
	}
}

// chromeMajorFromUA 从 UA 提取 Chrome 主版本号（"Chrome/148.0.0.0" → "148"）。
func chromeMajorFromUA(ua string) string {
	i := strings.Index(ua, "Chrome/")
	if i < 0 {
		return ""
	}
	rest := ua[i+len("Chrome/"):]
	j := strings.IndexByte(rest, '.')
	if j < 0 {
		return ""
	}
	return rest[:j]
}

// ---------------------------------------------------------------------------
// 时区 / 语言联动（git-lfs GeoIP 数据源）
// ---------------------------------------------------------------------------

// TestIdentityTimezonePassthrough 验证：传入的 GeoIP 时区被原样写进 Identity（喂 JS 沙箱）。
func TestIdentityTimezonePassthrough(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	id := NewIdentity(r, "JP", "Asia/Tokyo", "")
	if id.Timezone != "Asia/Tokyo" {
		t.Fatalf("Timezone 应=Asia/Tokyo, got %s", id.Timezone)
	}
	if id.CountryCode != "JP" {
		t.Fatalf("CountryCode 应=JP, got %s", id.CountryCode)
	}
}

// TestAcceptLanguageByCountry 验证：国家码驱动语言联动，主语言正确。
func TestAcceptLanguageByCountry(t *testing.T) {
	cases := map[string]string{
		"JP": "ja-JP",
		"US": "en-US",
		"DE": "de-DE",
		"FR": "fr-FR",
		"BR": "pt-BR",
	}
	for cc, wantPrimary := range cases {
		r := rand.New(rand.NewSource(7))
		primary, full, list := buildAcceptLanguage(r, cc)
		if primary != wantPrimary {
			t.Fatalf("cc=%s 主语言应=%s, got %s", cc, wantPrimary, primary)
		}
		// Accept-Language 必须以主语言开头（无 q）。
		if !strings.HasPrefix(full, wantPrimary) {
			t.Fatalf("cc=%s Accept-Language 应以主语言开头: %s", cc, full)
		}
		// navigator.languages 第一个必须是主语言。
		if len(list) == 0 || list[0] != wantPrimary {
			t.Fatalf("cc=%s languages[0] 应=%s: %v", cc, wantPrimary, list)
		}
		// 长度在 3~5。
		if len(list) < 3 || len(list) > 5 {
			t.Fatalf("cc=%s languages 长度应在 3~5: %d", cc, len(list))
		}
	}
}

// TestGeoIPLookup 验证：git-lfs 的 GeoLite2 库可用（若 DB 缺失则跳过，不阻断）。
func TestGeoIPLookup(t *testing.T) {
	if defaultGeoResolver.path == "" {
		t.Skip("GeoLite2 mmdb 未找到（git-lfs 未拉取），跳过")
	}
	// 8.8.8.8 是 Google 公共 DNS（US），GeoLite2 应解出非空国家码。
	g := LookupGeo("8.8.8.8")
	if g == nil {
		t.Skip("GeoIP 查询返回 nil（DB 不可用），跳过")
	}
	if g.CountryCode == "" {
		t.Fatalf("GeoIP 应解出国家码, got %+v", g)
	}
	t.Logf("GeoIP 8.8.8.8 → country=%s tz=%s", g.CountryCode, g.TimezoneID)
}

// ---------------------------------------------------------------------------
// 同族轮换（对齐 fingerprint_for_impersonate 的一致性约束）
// ---------------------------------------------------------------------------

// TestRotateSameFamily 验证：轮换只在同家族内，且 UA/CH 同步、会话级属性（屏幕/时区/语言/硬件）不变。
func TestRotateSameFamily(t *testing.T) {
	// 构造一个确定是 Chrome 且有同版本变体的身份（chrome-148-windows 有 chrome-148 变体）。
	id := newTestIdentity("chrome-148-windows")

	origScreen := id.Screen
	origTZ := id.Timezone
	origLang := id.AcceptLang
	origHW := id.HardwareConcurrency
	origPreset := id.PresetName

	r := rand.New(rand.NewSource(1))
	next := id.RotateSameFamily(r)
	// chrome-148 族在池内有同版本变体（chrome-148 / chrome-148-windows 等），应可轮换。
	if next == nil {
		t.Fatal("chrome-148-windows 应有同版本变体可轮换")
	}
	// 家族不变、主版本不变（仅变体差异，TLS 一致）。
	if next.Family != FamilyChrome {
		t.Fatalf("轮换后家族应仍=chrome, got %s", next.Family)
	}
	if browserMajorVersion(next.PresetName) != browserMajorVersion(origPreset) {
		t.Fatalf("轮换不得跨主版本: %s → %s", origPreset, next.PresetName)
	}
	if next.PresetName == origPreset {
		t.Fatalf("轮换应换 preset 变体, 仍=%s", next.PresetName)
	}
	// 会话级属性保持不变（同一台机器）。
	if next.Screen != origScreen || next.Timezone != origTZ || next.AcceptLang != origLang || next.HardwareConcurrency != origHW {
		t.Fatalf("轮换不应改变会话级属性: screen %s→%s tz %s→%s lang %s→%s hw %d→%d",
			origScreen, next.Screen, origTZ, next.Timezone, origLang, next.AcceptLang, origHW, next.HardwareConcurrency)
	}
	// 同主版本，故 UA 主版本号与 sec-ch-ua 仍一致（无 TLS/UA 错位）。
	major := chromeMajorFromUA(next.UserAgent)
	if major == "" || !strings.Contains(next.SecChUA, `"Google Chrome";v="`+major+`"`) {
		t.Fatalf("轮换后 UA(%s) 与 sec-ch-ua(%s) 不一致", next.UserAgent, next.SecChUA)
	}
}

// TestRotateNeverCrossesMajorVersion 验证：任何身份轮换后绝不跨主版本（TLS 一致性红线）。
func TestRotateNeverCrossesMajorVersion(t *testing.T) {
	for _, name := range []string{"chrome-148-windows", "chrome-143-windows", "firefox-148-windows", "safari-18"} {
		id := newTestIdentity(name)
		r := rand.New(rand.NewSource(1))
		next := id.RotateSameFamily(r)
		if next == nil {
			continue // 无同版本变体属正常（如 safari-18 唯一版本）
		}
		if browserMajorVersion(next.PresetName) != browserMajorVersion(name) {
			t.Fatalf("%s 轮换跨主版本到 %s（破坏 TLS 一致）", name, next.PresetName)
		}
	}
}

// TestRotateSameFamilyNoOther 验证：无同版本变体时返回 nil（Safari 桌面池只有 1 个版本）。
func TestRotateSameFamilyNoOther(t *testing.T) {
	id := &Identity{Family: FamilyMacSafari, PresetName: "safari-18", spec: lookupPreset("safari-18")}
	r := rand.New(rand.NewSource(1))
	if next := id.RotateSameFamily(r); next != nil {
		t.Fatalf("safari-18 唯一版本应无同版本候选, got %v", next.PresetName)
	}
}

// TestPlatformVersionRandomized 验证：Chrome 的 sec-ch-ua-platform-version 在
// {10.0.19045, 15.0.0} 间随机（对齐 codex-auto r.choice），不随 Chrome 版本钉死。
func TestPlatformVersionRandomized(t *testing.T) {
	seen := map[string]int{}
	for seed := int64(0); seed < 400; seed++ {
		r := rand.New(rand.NewSource(seed))
		id := NewIdentity(r, "US", "America/New_York", "")
		if id.Family != FamilyChrome {
			continue
		}
		seen[id.SecChUAPlatformVersion]++
	}
	if len(seen) < 2 {
		t.Fatalf("platform-version 应随机分布（≥2 种），实际只见到: %v", seen)
	}
	for v := range seen {
		if v != `"10.0.19045"` && v != `"15.0.0"` {
			t.Fatalf("非法 platform-version: %s", v)
		}
	}
	t.Logf("platform-version 分布: %v", seen)
}

// TestBrowserMajorVersion 验证主版本提取（同版本轮换的依据）。
func TestBrowserMajorVersion(t *testing.T) {
	cases := map[string]string{
		"chrome-148-windows":  "148",
		"chrome-143":          "143",
		"firefox-133-windows": "133",
		"safari-18":           "18",
		"safari-17-ios":       "17",
		"chrome-latest":       "latest",
	}
	for name, want := range cases {
		if got := browserMajorVersion(name); got != want {
			t.Fatalf("browserMajorVersion(%q)=%q, want %q", name, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// 指纹多样性（轮换的根基：家族分布合理，不死磕单一版本）
// ---------------------------------------------------------------------------

// TestFamilyDistribution 验证：大量抽样下四家族都出现，且 Chrome 占比最高。
func TestFamilyDistribution(t *testing.T) {
	counts := map[BrowserFamily]int{}
	const n = 2000
	for i := 0; i < n; i++ {
		r := rand.New(rand.NewSource(int64(i)))
		id := NewIdentity(r, "US", "America/New_York", "")
		counts[id.Family]++
	}
	for _, fam := range []BrowserFamily{FamilyChrome, FamilyMacSafari, FamilyFirefox, FamilyIOSSafari} {
		if counts[fam] == 0 {
			t.Fatalf("家族 %s 在 %d 次抽样中一次未出现", fam, n)
		}
	}
	if counts[FamilyChrome] <= counts[FamilyMacSafari] || counts[FamilyChrome] <= counts[FamilyFirefox] {
		t.Fatalf("Chrome 应为占比最高家族: %v", counts)
	}
	t.Logf("家族分布(n=%d): %v", n, counts)
}

// ---------------------------------------------------------------------------
// SentinelEnv 导出（③ 出口与 ② 同源）
// ---------------------------------------------------------------------------

// TestSentinelEnvMatchesIdentity 验证：喂给沙箱的 UA/navigator 与 Identity（即 HTTP 层）完全一致。
func TestSentinelEnvMatchesIdentity(t *testing.T) {
	r := rand.New(rand.NewSource(99))
	id := NewIdentity(r, "DE", "Europe/Berlin", "")
	env := id.SentinelEnv("did-123", "authorize_continue")

	if env.UserAgent != id.UserAgent {
		t.Fatalf("SentinelEnv.UserAgent 与 Identity 不同源")
	}
	if env.Platform != id.NavigatorPlatform || env.Vendor != id.NavigatorVendor {
		t.Fatalf("SentinelEnv navigator 与 Identity 不同源")
	}
	if env.Timezone != "Europe/Berlin" {
		t.Fatalf("SentinelEnv.Timezone 应=Europe/Berlin, got %s", env.Timezone)
	}
	if env.DeviceID != "did-123" || env.Flow != "authorize_continue" {
		t.Fatalf("SentinelEnv device/flow 透传错误")
	}
	// deviceMemory 一致性：Chromium 非 nil，其余 nil。
	if id.IsChromium() && env.DeviceMemory == nil {
		t.Fatalf("Chromium 的 SentinelEnv 应有 deviceMemory")
	}
	if !id.IsChromium() && env.DeviceMemory != nil {
		t.Fatalf("非 Chromium 的 SentinelEnv 不应有 deviceMemory")
	}
}
