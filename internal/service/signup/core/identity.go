package core

import (
	"fmt"
	"math/rand"
	"strings"
)

// Identity 是「同一台虚拟浏览器」的完整画像，是 TLS ↔ UA ↔ SO 三路自洽的【单一事实来源】。
//
// 装配原则（对齐 codex-auto 的教训，铁律）：
//   - 一次注册内，Identity 的所有字段【定死不变】。真实浏览器同会话不会中途换 UA/换核心数。
//   - 全部字段从同一个 rng 一次性抽出，杜绝「UA 与 navigator 各自随机导致对不上」。
//   - 任何消费方（HTTP 头、JS 沙箱）都必须从这一个 Identity 取值，【严禁各自再造 UA】。
type Identity struct {
	// ── 传输层（①）入口：httpcloak preset 名 ──
	PresetName string        // 如 "chrome-148-windows"，喂给 httpcloak.NewSession
	Family     BrowserFamily // 家族（决定 Client Hints 是否下发、deviceMemory 是否暴露）

	// ── HTTP 层（②）─
	UserAgent   string   // 完整 UA（同源喂 HTTP 头 + JS 沙箱）
	AcceptLang  string   // Accept-Language 完整串
	LangPrimary string   // 主语言（如 "en-US"）
	Languages   []string // navigator.languages（③，与 AcceptLang 展开一致）

	// Client Hints（仅 Chromium 非空；Safari/Firefox 全空=真实浏览器不发）
	SecChUA                string
	SecChUAFullVersionList string
	SecChUAPlatform        string
	SecChUAPlatformVersion string
	SecChUAMobile          string
	SecChUAArch            string
	SecChUABitness         string
	SecChUAModel           string

	// ── JS 层（③）navigator / 硬件画像 ──
	NavigatorPlatform   string  // navigator.platform
	NavigatorVendor     string  // navigator.vendor
	HardwareConcurrency int     // hardwareConcurrency
	DeviceMemory        *int    // deviceMemory（仅 Chromium 非 nil；nil=JS 侧保持 undefined）
	MaxTouchPoints      int     // maxTouchPoints
	DevicePixelRatio    float64 // devicePixelRatio
	Screen              string  // "WxH"
	Timezone            string  // IANA 时区（来自 GeoIP；喂 JS 的 Intl/Date 对齐）
	CountryCode         string  // 出口 IP 国家码（来自 GeoIP，供审计/语言联动追溯）

	// spec 回指（装配时的随机池来源，仅供内部同族轮换使用）
	spec *PresetSpec
}

// ScreenSize 解析 "WxH" 为宽高像素。
func (id *Identity) ScreenSize() (w, h int) {
	parts := strings.SplitN(id.Screen, "x", 2)
	if len(parts) == 2 {
		fmt.Sscanf(parts[0], "%d", &w)
		fmt.Sscanf(parts[1], "%d", &h)
	}
	if w <= 0 {
		w = 1920
	}
	if h <= 0 {
		h = 1080
	}
	return w, h
}

// IsChromium 报告是否 Chromium 系（决定是否下发 Client Hints / deviceMemory）。
func (id *Identity) IsChromium() bool { return id.Family == FamilyChrome }

// ---------------------------------------------------------------------------
// 家族权重（对齐 codex-auto _BROWSER_WEIGHTS 的精神：模拟真实互联网浏览器分布）
//   chrome 主力、Safari/Firefox 补多样性；iOS 占小头（移动端注册占比本就低）。
//   该权重只影响「家族分布」，与「单个指纹是否够真」无关——风控看分布，不看单科满分。
// ---------------------------------------------------------------------------

var familyWeights = []struct {
	family BrowserFamily
	weight float64
}{
	{FamilyChrome, 55},
	{FamilyMacSafari, 22},
	{FamilyFirefox, 15},
	{FamilyIOSSafari, 8},
}

// NewIdentity 装配一套三路自洽的浏览器身份。
//
// 参数：
//
//	rng:         随机源。传入固定 seed 的 rng 可复现同一身份（测试/同会话一致）。
//	countryCode: 出口 IP 国家码（可来自 GeoIP 或代理自带），驱动语言联动。
//	timezone:    IANA 时区（来自 GeoIP LookupGeo；为空则按国家码退到语言池主时区，再无则 UTC）。
//	vendor:      代理厂商标识，rng 为 nil 时作 seed 盐（对齐 Python vendor seed）。
func NewIdentity(rng *rand.Rand, countryCode, timezone, vendor string) *Identity {
	r := resolveRand(rng, vendor)

	family := pickFamily(r)
	candidates := presetsByFamily(family)
	if len(candidates) == 0 {
		// 家族池为空兜底到 Chrome（绝不让装配失败）。
		candidates = presetsByFamily(FamilyChrome)
		family = FamilyChrome
	}
	spec := candidates[r.Intn(len(candidates))]

	id := &Identity{
		PresetName:             spec.Name,
		Family:                 spec.Family,
		CountryCode:            strings.ToUpper(strings.TrimSpace(countryCode)),
		Timezone:               strings.TrimSpace(timezone),
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

	// UA：Chrome/Firefox 已在 spec 内定死；Safari/iOS 需随机挑一个系统版本装配（对齐 Python）。
	id.UserAgent = id.assembleUserAgent(r)

	// sec-ch-ua-platform-version：仅 Chromium 有意义，且应随机（对齐 codex-auto r.choice）。
	// 钉死某个 OS 版本是可聚类的弱特征；真实 Chrome 同一版本可跑在 Win10/Win11。
	if id.Family == FamilyChrome {
		id.SecChUAPlatformVersion = fmt.Sprintf(`"%s"`, winPlatformVersions[r.Intn(len(winPlatformVersions))])
	}

	// 语言（② + ③ 同源）：按国家码构建 Accept-Language 与 navigator.languages。
	id.LangPrimary, id.AcceptLang, id.Languages = buildAcceptLanguage(r, id.CountryCode)

	// 时区：优先 GeoIP 解出的真实 IANA 时区；为空按国家主语言粗配一个合理时区，再无则 UTC。
	if id.Timezone == "" {
		id.Timezone = fallbackTimezoneForCountry(id.CountryCode)
	}

	// 硬件随机项（③）：从家族池一次性抽定。
	id.HardwareConcurrency = pickInt(r, spec.HardwareChoices, 8)
	if dm := pickIntPtr(r, spec.DeviceMemoryGB); dm != nil {
		id.DeviceMemory = dm
	}
	id.DevicePixelRatio = pickFloat(r, spec.DevicePixelRatios, 1.0)
	id.Screen = pickString(r, spec.Screens, "1920x1080")

	return id
}

// assembleUserAgent 生成与 preset 自洽的 UA。Safari/iOS 需随机挑系统版本（一次注册内定死）。
func (id *Identity) assembleUserAgent(r *rand.Rand) string {
	switch id.Family {
	case FamilyMacSafari:
		// 对齐 Python _gen_mac_safari：随机一个 macOS 版本。
		macosVer := pickString(r, []string{"14_4", "14_5", "15_0", "15_1"}, "15_0")
		return fmt.Sprintf(
			"Mozilla/5.0 (Macintosh; Intel Mac OS X %s) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15",
			macosVer,
		)
	case FamilyIOSSafari:
		safariVer, iosPool := "18.0", []string{"18_0", "18_1", "18_1_1"}
		if strings.Contains(id.PresetName, "safari-17") {
			safariVer, iosPool = "17.2", []string{"17_1_2", "17_2"}
		}
		iosVer := pickString(r, iosPool, iosPool[0])
		return fmt.Sprintf(
			"Mozilla/5.0 (iPhone; CPU iPhone OS %s like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/%s Mobile/15E148 Safari/604.1",
			iosVer, safariVer,
		)
	default:
		return id.spec.UserAgent
	}
}

// ---------------------------------------------------------------------------
// 同版本轮换（对齐 Python _rotate_impersonate_session 的安全语义）
//
// 【2026-09 审计修正】原实现「在同家族 5 个 Chrome 版本里随机换一个」是【错误】的：
// httpcloak 的 TLS 指纹在建会话时已定死（① 不可变），若把 Identity 换成另一个
// Chrome 大版本（如会话是 chrome-148 的 TLS，UA/sec-ch-ua 却换成 chrome-152），
// 就会变成「TLS 握手说 148、UA 说 152」的自相矛盾——这正是 codex-auto 在
// auth_flow.py:1706-1711 警告的同一类坑（UA 与指纹错位），只是从「CH 不同步」
// 退化成「TLS 不同步」，本质一样，都会被 CF 一抓一个准。
//
// 因此轮换只允许「TLS 一致」的候选：同一浏览器主版本、仅平台后缀/地域变体不同
// （这些共享同一 TLS 指纹）。跨大版本一律不可轮换——那种情况只能【换新 session
// 重试】（codex-auto 语义：重试只换出口 IP，不换指纹，见 warmup 注释 1881-1883）。
// ---------------------------------------------------------------------------

// RotateSameFamily 切换到「同一浏览器主版本」的另一个安全候选，返回新 Identity。
// 仅当存在共享同一 TLS 指纹的同版本变体时才轮换；否则返回 nil（调用方应改用
// 「同版本 + 新 session」重试，而不是跨版本换指纹）。
// 屏幕/语言/时区/硬件等会话级属性保持不变（与浏览器版本无关，换了反而破坏「同一台机器」）。
func (id *Identity) RotateSameFamily(r *rand.Rand) *Identity {
	safe := sameVersionCandidates(id.PresetName, id.Family)
	if len(safe) == 0 {
		return nil
	}
	spec := safe[r.Intn(len(safe))]

	// 复制会话级属性，替换为同版本变体的字段（同主版本，故 UA 主版本号不变，
	// sec-ch-ua/notABrand 也一致；仅平台后缀或地域变体差异，无 TLS/UA 错位风险）。
	next := *id
	next.PresetName = spec.Name
	next.spec = spec
	next.SecChUA = spec.SecChUA
	next.SecChUAFullVersionList = spec.SecChUAFullVersionList
	next.SecChUAPlatform = spec.SecChUAPlatform
	next.SecChUAMobile = spec.SecChUAMobile
	next.SecChUAArch = spec.SecChUAArch
	next.SecChUABitness = spec.SecChUABitness
	next.SecChUAModel = spec.SecChUAModel
	// 注：SecChUAPlatformVersion 与 UserAgent 不随变体变化（同主版本同 OS 族），
	// 保持原值即可——它们是「设备/OS 级」属性，与浏览器小变体无关。
	return &next
}

// browserMajorVersion 从 preset 名提取浏览器主版本（"chrome-148-windows"→"148"，
// "firefox-133-windows"→"133"，"safari-18"→"18"，"safari-17-ios"→"17"）。
// 用于「同版本」候选筛选——只有主版本相同才能保证 TLS 指纹一致。
func browserMajorVersion(presetName string) string {
	// 去掉家族前缀。
	s := presetName
	for _, p := range []string{"chrome-", "firefox-", "safari-"} {
		if strings.HasPrefix(s, p) {
			s = s[len(p):]
			break
		}
	}
	// 去掉 "latest"。
	if s == "latest" || strings.HasPrefix(s, "latest-") {
		return "latest"
	}
	// 取首个 '-' 或结尾前的数字段。
	end := len(s)
	for i, c := range s {
		if c < '0' || c > '9' {
			end = i
			break
		}
	}
	return s[:end]
}

// sameVersionCandidates 返回与当前 preset「同主版本、同家族」的安全轮换候选
// （不含自身）。这些候选共享同一 TLS 指纹，轮换不会造成 TLS/UA 错位。
func sameVersionCandidates(presetName string, family BrowserFamily) []*PresetSpec {
	ver := browserMajorVersion(presetName)
	var out []*PresetSpec
	for _, p := range presetPool {
		if p.Name == presetName || p.Family != family {
			continue
		}
		if browserMajorVersion(p.Name) == ver {
			out = append(out, p)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// 内部辅助
// ---------------------------------------------------------------------------

func presetsByFamily(f BrowserFamily) []*PresetSpec {
	var out []*PresetSpec
	for _, p := range presetPool {
		if p.Family == f {
			out = append(out, p)
		}
	}
	return out
}

func resolveRand(rng *rand.Rand, vendor string) *rand.Rand {
	if rng != nil {
		return rng
	}
	v := strings.ToLower(strings.TrimSpace(vendor))
	if v != "" {
		var seed int64
		for _, c := range v {
			seed += int64(c)
		}
		return rand.New(rand.NewSource(seed))
	}
	return rand.New(rand.NewSource(rand.Int63()))
}

func pickFamily(r *rand.Rand) BrowserFamily {
	total := 0.0
	for _, it := range familyWeights {
		total += it.weight
	}
	x := r.Float64() * total
	cum := 0.0
	for _, it := range familyWeights {
		cum += it.weight
		if x <= cum {
			return it.family
		}
	}
	return familyWeights[len(familyWeights)-1].family
}

func pickInt(r *rand.Rand, pool []int, def int) int {
	if len(pool) == 0 {
		return def
	}
	return pool[r.Intn(len(pool))]
}

// pickIntPtr 从设备内存池取值；池为空返回 nil（Safari/Firefox 不暴露 deviceMemory）。
func pickIntPtr(r *rand.Rand, pool []int) *int {
	if len(pool) == 0 {
		return nil
	}
	v := pool[r.Intn(len(pool))]
	return &v
}

func pickFloat(r *rand.Rand, pool []float64, def float64) float64 {
	if len(pool) == 0 {
		return def
	}
	return pool[r.Intn(len(pool))]
}

func pickString(r *rand.Rand, pool []string, def string) string {
	if len(pool) == 0 {
		return def
	}
	return pool[r.Intn(len(pool))]
}

// fallbackTimezoneForCountry 在 GeoIP 缺数据时，按国家主时区给一个合理兜底。
// 正常路径用不到（GeoIP 直接给真实时区），仅作健壮性兜底。
func fallbackTimezoneForCountry(countryCode string) string {
	switch strings.ToUpper(strings.TrimSpace(countryCode)) {
	case "US":
		return "America/New_York"
	case "CA":
		return "America/Toronto"
	case "JP":
		return "Asia/Tokyo"
	case "CN", "HK", "TW", "SG":
		return "Asia/Shanghai"
	case "KR":
		return "Asia/Seoul"
	case "GB":
		return "Europe/London"
	case "DE", "FR", "IT", "ES", "NL", "BE", "CH", "SE", "NO", "DK", "FI", "PL", "AT", "CZ", "PT", "GR":
		return "Europe/Berlin"
	case "RU":
		return "Europe/Moscow"
	case "AU":
		return "Australia/Sydney"
	case "NZ":
		return "Pacific/Auckland"
	case "BR":
		return "America/Sao_Paulo"
	case "MX":
		return "America/Mexico_City"
	case "IN":
		return "Asia/Kolkata"
	case "AE", "SA":
		return "Asia/Dubai"
	case "TR":
		return "Europe/Istanbul"
	case "IL":
		return "Asia/Jerusalem"
	}
	return "UTC"
}
