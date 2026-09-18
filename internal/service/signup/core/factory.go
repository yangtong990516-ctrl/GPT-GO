package core

import (
	"math/rand"
	"time"
)

// Bootstrap 是注册地基的「一次性装配结果」，authflow 在每次注册开始时拿一个。
//
// 它把三件必须「同生共死」的东西绑在一起：
//
//	Identity —— 三路自洽的浏览器画像（TLS/UA/SO 的单一事实来源）
//	Session  —— 承载该画像的 HTTP 会话（httpcloak）
//	Geo      —— 出口 IP 的地理信息（国家码 + 真实 IANA 时区，来自 git-lfs GeoLite2）
//
// authflow 只面向 Bootstrap 编程，不再各自碰 fingerprint / httpclient / geoip ——
// 这正是「基础骨架先搭好、再走 authflow」的落地点。
type Bootstrap struct {
	Identity *Identity
	Session  *Session
	Geo      *GeoLocation // GeoIP 缺失时为 nil（Timezone/Country 已回退处理）
}

// BootstrapOptions 控制装配过程。
type BootstrapOptions struct {
	// Proxy 是出口代理 URL（socks5:// 自动标准化 socks5h://）。
	Proxy string
	// Timeout 是单次请求超时；<=0 时用默认 45s。
	Timeout time.Duration
	// Vendor 是代理厂商标识（作 rng seed 盐，让不同池的指纹序列独立）。
	Vendor string
	// RNG 可传入固定 seed 以复现身份（测试用）；nil 时按 Vendor/随机生成。
	RNG *rand.Rand

	// CountryHint 是代理自带的国家码提示（租约里的 country），GeoIP 缺失时作兜底。
	CountryHint string
	// EgressIP 是已探测到的出口 IP；非空则跳过内部探测直接用它查 GeoIP。
	// 留空则由调用方在 warmup/check_proxy 后再调 BindEgress 绑定（见下）。
	EgressIP string
}

// NewBootstrap 装配注册地基：先按国家/时区线索生成 Identity，再建 Session。
//
// 时区/国家来源优先级（「git-lfs 时区自动填充」的落地顺序）：
//  1. 若 opts.EgressIP 非空 → 立即 LookupGeo 解出真实 CountryCode + TimezoneID（最准）。
//  2. 否则用 opts.CountryHint 作国家码、时区走国家兜底（GeoIP 在 warmup 后由 BindEgress 补登）。
//
// 正常用法：check_proxy/warmup 拿到出口 IP 后调 BindEgress 重建 Identity（见下），
// 使时区/语言与真实出口一致。这里先给一套可立即开工的默认值。
func NewBootstrap(opts BootstrapOptions) (*Bootstrap, error) {
	rng := resolveRand(opts.RNG, opts.Vendor)

	country := opts.CountryHint
	timezone := ""
	var geo *GeoLocation

	if opts.EgressIP != "" {
		if g := LookupGeo(opts.EgressIP); g != nil {
			geo = g
			if g.CountryCode != "" {
				country = g.CountryCode
			}
			timezone = g.TimezoneID
		}
	}

	identity := NewIdentity(rng, country, timezone, opts.Vendor)

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 45 * time.Second
	}
	sess, err := NewSession(identity, opts.Proxy, timeout)
	if err != nil {
		return nil, err
	}

	return &Bootstrap{
		Identity: identity,
		Session:  sess,
		Geo:      geo,
	}, nil
}

// BindEgress 在拿到真实出口 IP 后，重建 Identity（不换会话），使时区/语言与出口一致。
//
// 对齐 Python check_proxy 的「IP 地理联动」：探测到国家码后重新生成指纹（带时区/语言）。
// 注意：httpcloak 的 TLS 指纹已在建会话时定死（① 不可变），这里只刷新 ② HTTP 层与 ③ JS 层
// 的「国家相关」字段（时区/语言）；浏览器家族与版本保持不变（仍是同一台机器，只是人到了别国）。
//
// 返回是否发生了有效绑定（GeoIP 命中且国家/时区有更新）。
func (b *Bootstrap) BindEgress(egressIP string, rng *rand.Rand) bool {
	g := LookupGeo(egressIP)
	if g == nil {
		return false
	}

	// 【2026-09 审计修正】国家码与时区必须【同源】：二者同取自这一条 GeoIP 记录，
	// 要么都采纳、要么都不采纳。绝不允许「国家码用 GeoIP 的、时区却保留旧的」这种混搭——
	// 那会出现「时区在芝加哥、语言却是德语」的新矛盾（codex-auto 没这问题，因为它的
	// 国家码和时区同出 cloudflare trace 一处；我们引入 GeoIP 第二数据源，就必须保证
	// 这条记录内部自洽）。GeoIP 记录的 CountryCode 与 TimezoneID 天然同属一条，故成对采纳。
	if g.CountryCode == "" || g.TimezoneID == "" {
		return false // 记录不完整，宁可整体回退也不混搭
	}
	b.Geo = g

	changed := false
	if g.CountryCode != b.Identity.CountryCode {
		b.Identity.CountryCode = g.CountryCode
		changed = true
	}
	if g.TimezoneID != b.Identity.Timezone {
		b.Identity.Timezone = g.TimezoneID
		changed = true
	}
	if changed {
		// 国家/时区变了 → 语言联动刷新（② Accept-Language + ③ navigator.languages）。
		// 语言池由新国家码驱动，与刚刷新的时区同属一个地理画像，保持自洽。
		r := resolveRand(rng, "")
		b.Identity.LangPrimary, b.Identity.AcceptLang, b.Identity.Languages =
			buildAcceptLanguage(r, b.Identity.CountryCode)
	}
	return changed
}

// DeviceID 返回当前会话的设备 ID（oai-did cookie）。
//
// 【红线 / codex-auto auth_flow.py:1800-1803】auth.openai.com 域下的请求必须补
// `oai-device-id` 头，且这个值必须同时是：① warmup 种下的 oai-did cookie、② sentinel
// 沙箱的 device_id。三处必须同一个值，否则断状态机（invalid_state / OTP silent-drop）。
//
// authflow 的正确用法：warmup 种到 oai-did 后，调本方法取值，一处取值三处用——
// 塞进 `oai-device-id` 请求头 + `Identity.SentinelEnv(deviceID, ...)`。
// 切勿让这三处各自生成/读取，那是对齐被破坏的最常见途径。
func (b *Bootstrap) DeviceID() string {
	return b.Session.CookieValue("oai-did")
}

// RotateTLSIdentity 在 TLS 瞬断时做【同家族】指纹轮换，并重建 Session。
//
// 对齐 Python _rotate_impersonate_session：只换浏览器版本，会话级的屏幕/语言/时区/硬件保持不变。
// 轮换成功返回 true 并替换内部 Session（旧 Session 关闭）；无同族候选返回 false。
//
// 使用时机：check_proxy/csrf 等早期步骤遇 TLS 握手失败时调用；中后段（已种 oai-did/csrf）
// 不可轮换（会丢 cookie → 409），只能原 session 重试（session.go 已内建）。
func (b *Bootstrap) RotateTLSIdentity(proxy string, timeout time.Duration, rng *rand.Rand) (bool, error) {
	r := resolveRand(rng, "")
	next := b.Identity.RotateSameFamily(r)
	if next == nil {
		return false, nil
	}
	if timeout <= 0 {
		timeout = 45 * time.Second
	}
	newSess, err := NewSession(next, proxy, timeout)
	if err != nil {
		return false, err
	}
	// 关闭旧会话，绑定新身份 + 新会话。
	b.Session.Close()
	b.Identity = next
	b.Session = newSess
	return true, nil
}

// SentinelEnv 导出喂给 Sentinel 沙箱（P4 那条线）的 JS 层画像。
//
// 这是 TLS ↔ UA ↔ SO 三路自洽的「③」出口：字段与 Identity 同源，绝不另造。
// P4 的 Solver 拿到这个结构后，把 navigator/screen/timezone 注入 v8go 沙箱即可。
type SentinelEnv struct {
	DeviceID            string
	Flow                string
	UserAgent           string
	ScreenWidth         int
	ScreenHeight        int
	Language            string
	Languages           []string
	Platform            string
	Vendor              string
	HardwareConcurrency int
	DeviceMemory        *int
	MaxTouchPoints      int
	DevicePixelRatio    float64
	Timezone            string
	BrowserType         string
}

// SentinelEnv 由 Identity 生成 SentinelEnv（deviceID/flow 由 authflow 在调用点补）。
func (id *Identity) SentinelEnv(deviceID, flow string) SentinelEnv {
	w, h := id.ScreenSize()
	return SentinelEnv{
		DeviceID:            deviceID,
		Flow:                flow,
		UserAgent:           id.UserAgent, // 与 HTTP 头完全同一个 UA 串（②③ 同源）
		ScreenWidth:         w,
		ScreenHeight:        h,
		Language:            id.LangPrimary,
		Languages:           id.Languages,
		Platform:            id.NavigatorPlatform,
		Vendor:              id.NavigatorVendor,
		HardwareConcurrency: id.HardwareConcurrency,
		DeviceMemory:        id.DeviceMemory, // 仅 Chromium 非 nil；nil → JS 侧 undefined
		MaxTouchPoints:      id.MaxTouchPoints,
		DevicePixelRatio:    id.DevicePixelRatio,
		Timezone:            id.Timezone, // 来自 GeoIP 的真实 IANA 时区
		BrowserType:         string(id.Family),
	}
}
