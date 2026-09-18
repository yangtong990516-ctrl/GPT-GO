// Package core 是注册的「基础骨架」权威实现（对齐 codex-auto 的 _runtime 三件套：
// fingerprint.py + http_client.py + sentinel 的指纹喂入部分）。
//
// 本包的核心职责只有一句话：产出并维系「TLS ↔ UA ↔ SO(navigator) 三路自洽」的
// 同一个虚拟浏览器身份（Identity），并用 httpcloak 承载其传输层。
//
// 三路一致性（本包存在的全部理由）：
//
//	① TLS  层：TCP/TLS 握手的 JA3/JA4 指纹，由 httpcloak 的 preset（如 chrome-148）保证。
//	② HTTP 层：User-Agent / sec-ch-ua* / Accept-Language / 头顺序，由本包按 preset 同步生成。
//	③ JS   层：sdk.js 在沙箱里读到的 navigator.* / screen / timezone，由本包注入。
//
// 三路必须描述「同一台机器」。任何两者对不上（UA 说 Chrome/148、sec-ch-ua 说 v=152、
// navigator.platform 却是 MacIntel），风控一眼假——这正是 Python 版反复踩过的坑。
//
// 架构定位：本包是「地基」，被 authflow（协议状态机）调用；本包不感知业务、不碰邮箱/账号。
package core

import (
	"fmt"
	"strings"
)

// BrowserFamily 是浏览器家族（对齐 Python browser_type）。
type BrowserFamily string

const (
	FamilyChrome    BrowserFamily = "chrome"
	FamilyMacSafari BrowserFamily = "mac_safari"
	FamilyIOSSafari BrowserFamily = "ios_safari"
	FamilyFirefox   BrowserFamily = "firefox"
)

// PresetSpec 描述一个 httpcloak 传输层预设，以及与之三路自洽的 HTTP/JS 层常量。
//
// 注意分工（与 httpcloak 的边界，必须清楚）：
//   - TLS/H2/H3 指纹：由 httpcloak preset 内部固定，本包【不参与、也无法修改】。
//   - UA / sec-ch-ua：httpcloak 会按 preset 自动生成；但本包必须知道「它生成的是什么」，
//     以便把【同一个 UA】喂给 JS 沙箱，否则 ②③ 两路对不上。
//   - navigator/screen/时区/语言：httpcloak 【完全不管】，全部本包负责。
type PresetSpec struct {
	// Name 是 httpcloak 的 preset 名（如 "chrome-148-windows" / "safari-18" / "firefox-148-windows"）。
	Name string
	// Family 是该预设所属的浏览器家族。
	Family BrowserFamily

	// ── HTTP 层（②）：与 httpcloak preset 自洽的 UA / Client Hints ──
	UserAgent              string // 完整 UA 串（会同时喂给 HTTP 头与 JS 沙箱，保证同源）
	SecChUA                string // sec-ch-ua（仅 Chrome 非空；Safari/Firefox 为空串=真实浏览器不发）
	SecChUAFullVersionList string // sec-ch-ua-full-version-list（仅 Chrome）
	SecChUAPlatform        string // sec-ch-ua-platform（仅 Chrome，含引号，如 "Windows"）
	SecChUAPlatformVersion string // sec-ch-ua-platform-version（仅 Chrome）
	SecChUAMobile          string // "?0"（桌面） / "?1"（移动）；非 Chrome 为空
	SecChUAArch            string // 仅 Chrome
	SecChUABitness         string // 仅 Chrome
	SecChUAModel           string // 仅 Chrome（桌面为空串 `""`）

	// ── JS 层（③）：喂给 sdk.js 沙箱的 navigator / 硬件画像常量 ──
	// 这些只随「家族 + 平台」变，不随具体小版本变；随机项（核心数/内存/屏幕）在 Identity 装配时抽。
	NavigatorPlatform string    // navigator.platform：Win32 / MacIntel / iPhone
	NavigatorVendor   string    // navigator.vendor：Google Inc. / Apple Computer, Inc. / ""(Firefox)
	HardwareChoices   []int     // hardwareConcurrency 候选池
	DeviceMemoryGB    []int     // deviceMemory 候选池（仅 Chromium 有值；空=不暴露，JS 侧保持 undefined）
	MaxTouchPoints    int       // navigator.maxTouchPoints：iOS=5，桌面=0
	DevicePixelRatios []float64 // devicePixelRatio 候选池
	Screens           []string  // 屏幕分辨率候选池 "WxH"
}

// IsChromium 报告该预设是否 Chromium 系（决定是否下发 Client Hints 与 deviceMemory）。
func (p *PresetSpec) IsChromium() bool { return p.Family == FamilyChrome }

// ---------------------------------------------------------------------------
// 预设池（传输层固定，来自 httpcloak v1.7.2 实测 100 个预设中精选的注册可用集）
//
// 选池原则（对齐 codex-auto fingerprint.py 的版本档 + 你确认的「httpcloak 全家桶」决策）：
//   - Chrome 桌面 Windows 为主力（真实互联网占比最高），覆盖 143/146/148/150/152 多版本。
//   - Safari 桌面 + iOS、Firefox 桌面补足家族多样性（风控看的是家族分布，不是单科满分）。
//   - 每个版本档的 notABrand / fullVer / platform_version 必须与真机一致，这是 CF 的高频检测点。
// ---------------------------------------------------------------------------

// notABrand 串随 Chrome 大版本变化（136→"Not.A/Brand";v="99"，142→"Not/A)Brand";v="8"，
// 146→"Not?A_Brand";v="99"，143+/148/152 主流为 "Not A(Brand";v="24"）。
// 这里按 httpcloak 实测与真实抓取对齐到各版本，严禁张冠李戴。

var presetPool = []*PresetSpec{
	// ── Chrome (Windows 桌面) ────────────────────────────────────────────
	// 每个主版本放「-windows」与「无后缀」两个变体：二者共享同一 TLS 指纹，
	// 构成 RotateSameFamily 的合法安全候选（同主版本，见 identity.go）。
	chromeDesktop("chrome-143-windows", "143", "143.0.0.0", `"Not A(Brand";v="24"`, "10.0.19045"),
	chromeDesktop("chrome-143", "143", "143.0.0.0", `"Not A(Brand";v="24"`, "10.0.19045"),
	chromeDesktop("chrome-146-windows", "146", "146.0.0.0", `"Not?A_Brand";v="99"`, "10.0.19045"),
	chromeDesktop("chrome-146", "146", "146.0.0.0", `"Not?A_Brand";v="99"`, "10.0.19045"),
	chromeDesktop("chrome-148-windows", "148", "148.0.0.0", `"Not A(Brand";v="24"`, "15.0.0"),
	chromeDesktop("chrome-148", "148", "148.0.0.0", `"Not A(Brand";v="24"`, "15.0.0"),
	chromeDesktop("chrome-150-windows", "150", "150.0.0.0", `"Not A(Brand";v="24"`, "15.0.0"),
	chromeDesktop("chrome-152-windows", "152", "152.0.0.0", `"Not A(Brand";v="24"`, "15.0.0"),

	// ── Safari (macOS 桌面) ─────────────────────────────────────────────
	safariDesktop("safari-18", "18.0", "605.1.15", []string{"14_4", "14_5", "15_0", "15_1"}),

	// ── Safari (iOS) ────────────────────────────────────────────────────
	safariIOS("safari-18-ios", "18.0", "605.1.15", []string{"18_0", "18_1", "18_1_1"}),
	safariIOS("safari-17-ios", "17.2", "605.1.15", []string{"17_1_2", "17_2"}),

	// ── Firefox (Windows 桌面) ──────────────────────────────────────────
	firefoxDesktop("firefox-133-windows", "133.0"),
	firefoxDesktop("firefox-148-windows", "148.0"),
}

// 各家族屏幕池（对齐 Python _MAC_SCREENS / _IPHONE_SCREENS / _WIN_SCREENS）。
var (
	macScreens     = []string{"1440x900", "1512x982", "1728x1117", "2560x1440", "1920x1080"}
	iphoneScreens  = []string{"390x844", "393x852", "428x926", "430x932"}
	windowsScreens = []string{"1920x1080", "1366x768", "2560x1440", "1536x864", "1440x900"}
)

// winPlatformVersions 是 sec-ch-ua-platform-version 的候选池（对齐 codex-auto
// fingerprint.py:501 的 win_platform_versions）。"10.0.19045"=Win10 22H2，"15.0.0"=Win11。
// 真实 Chrome 148 既可能跑在 Win10 也可能 Win11，故装配时随机，不随 Chrome 版本钉死
// （钉死本身就是可聚类的弱特征）。注意：UA 里固定写 Windows NT 10.0，CH 可报 15.0.0——
// 这是真实浏览器行为（UA 的 OS 版本被冻结在 10.0，CH 才报真实 OS 版本）。
var winPlatformVersions = []string{"10.0.19045", "15.0.0"}

// chromeDesktop 构造一个 Windows 桌面 Chrome 预设。
// platformVersion 仅占位（装配期被 winPlatformVersions 随机覆盖，见 identity.go）。
func chromeDesktop(name, ver, fullVer, notABrand, platformVersion string) *PresetSpec {
	secChUA := fmt.Sprintf(`"Chromium";v="%s", "Google Chrome";v="%s", %s`, ver, ver, notABrand)
	secChUAFull := fmt.Sprintf(`"Chromium";v="%s", "Google Chrome";v="%s", %s`, fullVer, fullVer, notABrand)
	return &PresetSpec{
		Name:   name,
		Family: FamilyChrome,
		UserAgent: fmt.Sprintf(
			"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%s Safari/537.36",
			fullVer,
		),
		SecChUA:                secChUA,
		SecChUAFullVersionList: secChUAFull,
		SecChUAPlatform:        `"Windows"`,
		SecChUAPlatformVersion: fmt.Sprintf(`"%s"`, platformVersion),
		SecChUAMobile:          "?0",
		SecChUAArch:            `"x86"`,
		SecChUABitness:         `"64"`,
		SecChUAModel:           `""`,
		NavigatorPlatform:      "Win32",
		NavigatorVendor:        "Google Inc.",
		HardwareChoices:        []int{4, 6, 8, 12, 16, 24},
		DeviceMemoryGB:         []int{4, 8}, // Chromium 才暴露 deviceMemory（spec 封顶 8）
		MaxTouchPoints:         0,
		DevicePixelRatios:      []float64{1.0, 1.25, 1.5},
		Screens:                windowsScreens,
	}
}

// safariDesktop 构造一个 macOS 桌面 Safari 预设。Safari 不发 Client Hints（sec-ch-ua 全空）。
func safariDesktop(name, safariVer, webkitVer string, macosVersions []string) *PresetSpec {
	// UA 在装配时随机挑一个 macos 版本（对齐 Python _gen_mac_safari），此处先用占位，
	// 具体在 identity.go 里用 rng 定死，保证「一次注册内不变」。
	return &PresetSpec{
		Name:              name,
		Family:            FamilyMacSafari,
		UserAgent:         "", // 由 identity.go 用 macosVersions 装配
		NavigatorPlatform: "MacIntel",
		NavigatorVendor:   "Apple Computer, Inc.",
		HardwareChoices:   []int{8, 10, 12, 16},
		DeviceMemoryGB:    nil, // Safari 不暴露 deviceMemory
		MaxTouchPoints:    0,
		DevicePixelRatios: []float64{2.0}, // Retina 必定 2.0
		Screens:           macScreens,
	}
}

// safariIOS 构造一个 iOS Safari 预设（触屏，maxTouchPoints=5，像素比 2/3）。
func safariIOS(name, safariVer, webkitVer string, iosVersions []string) *PresetSpec {
	return &PresetSpec{
		Name:              name,
		Family:            FamilyIOSSafari,
		UserAgent:         "", // 由 identity.go 用 iosVersions 装配
		NavigatorPlatform: "iPhone",
		NavigatorVendor:   "Apple Computer, Inc.",
		HardwareChoices:   []int{4, 6}, // A15/A16/A17
		DeviceMemoryGB:    nil,         // iOS Safari 不暴露
		MaxTouchPoints:    5,
		DevicePixelRatios: []float64{2.0, 3.0},
		Screens:           iphoneScreens,
	}
}

// firefoxDesktop 构造一个 Windows 桌面 Firefox 预设。Firefox 不发 Client Hints，vendor 为空串。
func firefoxDesktop(name, ver string) *PresetSpec {
	return &PresetSpec{
		Name:   name,
		Family: FamilyFirefox,
		UserAgent: fmt.Sprintf(
			"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:%s) Gecko/20100101 Firefox/%s",
			ver, ver,
		),
		NavigatorPlatform: "Win32",
		NavigatorVendor:   "", // Firefox navigator.vendor 为空串（不是 undefined）
		HardwareChoices:   []int{4, 6, 8, 12, 16},
		DeviceMemoryGB:    nil, // Firefox 不暴露 deviceMemory
		MaxTouchPoints:    0,
		DevicePixelRatios: []float64{1.0, 1.5},
		Screens:           windowsScreens,
	}
}

// lookupPreset 按名取预设；未知名返回 nil（调用方需显式处理，绝不静默回退——
// 静默回退正是 httpcloak 作者极力避免的「一个错字符换回旧指纹」的事故）。
func lookupPreset(name string) *PresetSpec {
	for _, p := range presetPool {
		if p.Name == name {
			return p
		}
	}
	return nil
}

// familyOfPresetName 从预设名粗判家族（用于校验外部传入的 preset 是否在池内）。
func familyOfPresetName(name string) BrowserFamily {
	switch {
	case strings.HasPrefix(name, "chrome"):
		return FamilyChrome
	case strings.HasPrefix(name, "firefox"):
		return FamilyFirefox
	case strings.Contains(name, "-ios"):
		return FamilyIOSSafari
	case strings.HasPrefix(name, "safari"):
		return FamilyMacSafari
	}
	return ""
}
