// Package model defines the proxy-pool API models and pure helpers, mirroring
// resource_models.py + resource_service.py (proxy parsing/scheme/country).
package model

import (
	"regexp"
	"strconv"
	"strings"
)

// ── 常量 ────────────────────────────────────────────────────────────────────

// Proxy status values mirror resource_models.py:16.
const (
	ProxyStatusAvailable   = "available"
	ProxyStatusUnknown     = "unknown"
	ProxyStatusUsed        = "used"
	ProxyStatusQuarantined = "quarantined"
)

// Proxy scheme values mirror resource_models.py:17.
const (
	ProxySchemeHTTP    = "http"
	ProxySchemeHTTPS   = "https"
	ProxySchemeSocks5  = "socks5"
	ProxySchemeSocks5h = "socks5h"
)

// DefaultProxyGroup mirrors probe_store.DEFAULT_PROXY_GROUP.
const DefaultProxyGroup = "默认组"

// LocalProxyGroup mirrors probe_store.LOCAL_PROXY_GROUP (127.0.0.1:7890 本地代理).
const LocalProxyGroup = "__local_127_0_0_1_7890__"

// LocalProxyIDPrefix mirrors probe_store.LOCAL_PROXY_ID_PREFIX.
const LocalProxyIDPrefix = "local-7890:"

// supportedProxySchemes mirrors SUPPORTED_PROXY_SCHEMES.
var supportedProxySchemes = map[string]bool{
	ProxySchemeHTTP:    true,
	ProxySchemeHTTPS:   true,
	ProxySchemeSocks5:  true,
	ProxySchemeSocks5h: true,
}

// proxyCountryPattern mirrors PROXY_COUNTRY_PATTERN (resource_service.py:62).
var proxyCountryPattern = regexp.MustCompile(`(?:^|[-_.])(?:region|country|res|area|dc|res_sc)-([A-Za-z]{2})(?:[-_.:]|$)`)

// ── 纯函数（协议/国家/组 规范化） ───────────────────────────────────────────

// NormalizeProxyScheme mirrors normalize_proxy_scheme: socks/socks5/socks5h →
// socks5h (远程 DNS)，http/https 原样，其余回退 http。
func NormalizeProxyScheme(scheme string) string {
	s := strings.ToLower(strings.TrimSpace(scheme))
	switch s {
	case "socks", ProxySchemeSocks5, ProxySchemeSocks5h:
		return ProxySchemeSocks5h
	case ProxySchemeHTTP, ProxySchemeHTTPS:
		return s
	default:
		return ProxySchemeHTTP
	}
}

// NormalizeCountryCode mirrors normalize_country_code: 两位大写，否则 "ZZ"。
func NormalizeCountryCode(value string) string {
	s := strings.ToUpper(strings.TrimSpace(value))
	if len(s) == 2 && isASCIILetter(s[0]) && isASCIILetter(s[1]) {
		return s
	}
	return "ZZ"
}

// InferProxyCountry mirrors infer_proxy_country: 从 username/host 里匹配
// `-res-VN` / `_area-VN` 等国家片段。
func InferProxyCountry(username, host string) string {
	for _, candidate := range []string{username, host} {
		if m := proxyCountryPattern.FindStringSubmatch(candidate); m != nil {
			return strings.ToUpper(m[1])
		}
	}
	return "ZZ"
}

// NormalizeProxyGroup mirrors normalize_proxy_group: 折叠空白，截断 64，空则默认组。
func NormalizeProxyGroup(value string) string {
	s := strings.Join(strings.Fields(value), " ")
	if len(s) > 64 {
		s = s[:64]
	}
	if s == "" {
		return DefaultProxyGroup
	}
	return s
}

// InferProxyVendor mirrors infer_proxy_vendor: 识别 kookeey/cliproxy/iprocket。
func InferProxyVendor(host string) string {
	h := strings.ToLower(strings.TrimRight(strings.TrimSpace(host), "."))
	if strings.Contains(h, "kookeey") {
		return "kookeey"
	}
	if strings.Contains(h, "cliproxy") {
		return "cliproxy"
	}
	if isIprocketHost(h) {
		return "iprocket"
	}
	return ""
}

// InferProxySchemeByVendor mirrors infer_proxy_scheme_by_vendor: 按厂商+端口纠正
// 协议（IPRocket 9595 → socks5h，Kookeey → http，Cliproxy → socks5h）。
func InferProxySchemeByVendor(host string, port int, fallback string) string {
	h := strings.ToLower(strings.TrimRight(strings.TrimSpace(host), "."))
	if strings.Contains(h, "kookeey") {
		return ProxySchemeHTTP
	}
	if strings.Contains(h, "cliproxy") {
		return ProxySchemeSocks5h
	}
	if isIprocketHost(h) {
		if port == 9595 || port == 59999 || port == 619999 {
			return ProxySchemeSocks5h
		}
		return ProxySchemeHTTP
	}
	return fallback
}

func isIprocketHost(h string) bool {
	return strings.HasSuffix(h, ".iprocket.io") ||
		strings.HasSuffix(h, ".iprocket.pro") ||
		h == "proxy.iproyal.net" || strings.HasSuffix(h, ".iproyal.net") ||
		h == "proxy.iproyal.com" || strings.HasSuffix(h, ".iproyal.com") ||
		h == "1024proxy.io" || strings.HasSuffix(h, ".1024proxy.io")
}

func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// ── 会话型住宅代理（模型 B：通道 = IP 生成器）────────────────────────────────
//
// 你给的代理是「会话型住宅代理」：host:port 固定（服务商网关），username 里嵌一个
// 会话标识（session-id），改这个 id 服务商就拨出一个【不同的出口 IP】。因此：
//
//   - 一条凭证 ≠ 一个 IP，而是一个「可无限拨号的 IP 生成器」（通道）。
//   - 注册时现场生成随机 session-id 替换 username 里的会话片段 → 拨出新 IP。
//   - 国家【不锁死】：username 里的 `-res-VN`/`_area-VN` 只是「期望国家」提示，
//     真实国家必须每次拨号后用出口 IP 实测（probe 的 loc= / GeoIP），当次有效。
//
// 支持的会话标记（兼容两家，可扩展）：
//   - iprocket:      com...-res-VN-Lsid-901244458-TTL-1800    → `-Lsid-<数字>`
//   - ipipbright:    bru..._area-VN_life-5_session-U0nM1dHxwW → `_session-<字母数字>`

// SessionKind 标识通道的会话标记类型（决定如何重写 session-id）。
type SessionKind string

const (
	// SessionKindStatic 无会话标记 → 静态代理（IP 固定，走旧的 available/used 模型）。
	SessionKindStatic SessionKind = "static"
	// SessionKindIprocketLsid iprocket 的 `-Lsid-<数字>` 会话标记。
	SessionKindIprocketLsid SessionKind = "iprocket_lsid"
	// SessionKindIpipbright ipipbright 的 `_session-<字母数字>` 会话标记。
	SessionKindIpipbright SessionKind = "ipipbright_session"
)

var (
	// iprocketLsidRe 匹配 iprocket 的 `-Lsid-<数字>` 会话片段（保留前缀用于替换）。
	iprocketLsidRe = regexp.MustCompile(`(?i)(-Lsid-)([0-9]+)`)
	// ipipbrightSessionRe 匹配 ipipbright 的 `_session-<字母数字>` 会话片段。
	ipipbrightSessionRe = regexp.MustCompile(`(?i)(_session-)([A-Za-z0-9]+)`)
)

// ClassifySession 判定 username 属于哪种会话型通道；返回 (kind, 会话片段正则)。
// 不是会话型（静态代理）返回 SessionKindStatic + nil。
func ClassifySession(username string) (SessionKind, *regexp.Regexp) {
	if iprocketLsidRe.MatchString(username) {
		return SessionKindIprocketLsid, iprocketLsidRe
	}
	if ipipbrightSessionRe.MatchString(username) {
		return SessionKindIpipbright, ipipbrightSessionRe
	}
	return SessionKindStatic, nil
}

// RewriteSession 把 username 里的会话片段替换为 newSession，返回新 username。
// kind 必须是 ClassifySession 判出的非 static 类型；否则原样返回。
// newSession 只允许 [A-Za-z0-9]（服务商对 session-id 的字符集约束，防注入）。
func RewriteSession(username string, kind SessionKind, newSession string) string {
	newSession = sanitizeSessionID(newSession)
	if newSession == "" {
		return username
	}
	switch kind {
	case SessionKindIprocketLsid:
		return iprocketLsidRe.ReplaceAllString(username, "${1}"+newSession)
	case SessionKindIpipbright:
		return ipipbrightSessionRe.ReplaceAllString(username, "${1}"+newSession)
	}
	return username
}

// sanitizeSessionID 只保留 [A-Za-z0-9]，防代理鉴权串被注入特殊字符。
func sanitizeSessionID(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			b.WriteByte(c)
		}
	}
	return b.String()
}

// ProxyURL mirrors plan_check_service.proxy_url: scheme://[user:pass@]host:port，
// 凭证用 quote(..., safe=”) 全量编码（等价 url.PathEscape 语义，但保留 RFC3986
// 保留字符原样编码）。socks5/socks5h 统一走 socks5h。
func ProxyURL(host string, port int, username, password, scheme string) string {
	s := NormalizeProxyScheme(scheme)
	authority := host + ":" + strconv.Itoa(port)
	if username != "" || password != "" {
		authority = quoteAll(username) + ":" + quoteAll(password) + "@" + authority
	}
	return s + "://" + authority
}

// quoteAll mirrors urllib.parse.quote(s, safe=”)：编码除 A-Za-z0-9_.-~ 外所有字节
// （用 %XX，空格编码为 %20 而非 +）。
func quoteAll(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '_' || c == '.' || c == '-' || c == '~' {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0xf])
		}
	}
	return b.String()
}

// ── 数据模型 ────────────────────────────────────────────────────────────────

// ProxyRecord mirrors ProxyRecord (resource_models.py:203)。
type ProxyRecord struct {
	ID            string  `json:"id"`
	Host          string  `json:"host"`
	Port          int     `json:"port"`
	Username      string  `json:"username"`
	Password      string  `json:"password"`
	Enabled       bool    `json:"enabled"`
	Status        string  `json:"status"`
	LatencyMs     *int    `json:"latencyMs"`
	LastCheckedAt *string `json:"lastCheckedAt"`
	Country       string  `json:"country"`
	Group         string  `json:"group"`
	Scheme        string  `json:"scheme"`
}

// ProxyLease mirrors probe_store.ProxyLease。
type ProxyLease struct {
	ID                string
	Host              string
	Port              int
	Username          string
	Password          string
	Country           string
	Group             string
	Scheme            string
	TimezoneID        string
	TimezoneOffsetSec *int
}

// ProxyDocument is the persisted shape of a proxy record (store-internal).
type ProxyDocument struct {
	ID                  string   `bson:"_id"`
	Host                string   `bson:"host"`
	Port                int      `bson:"port"`
	Username            string   `bson:"username"`
	Password            string   `bson:"password"`
	Enabled             bool     `bson:"enabled"`
	Status              string   `bson:"status"`
	LatencyMs           *int     `bson:"latencyMs"`
	LastCheckedAt       *string  `bson:"lastCheckedAt"`
	Country             string   `bson:"country"`
	Group               string   `bson:"group"`
	Scheme              string   `bson:"scheme"`
	TimezoneID          string   `bson:"timezoneId"`
	TimezoneOffsetSec   *int     `bson:"timezoneOffsetSec"`
	CreatedAt           string   `bson:"createdAt"`
	RandomScore         int64    `bson:"randomScore"`
	LastSelectedAt      *string  `bson:"lastSelectedAt"`
	LeaseOwner          string   `bson:"leaseOwner"`
	LeaseUntil          *string  `bson:"leaseUntil"`
	ActiveLeaseOwners   []string `bson:"activeLeaseOwners"`
	ActiveLeaseCount    int      `bson:"activeLeaseCount"`
	ConsecutiveFailures int      `bson:"consecutiveFailures"`
	UsedAt              *string  `bson:"usedAt"`
	StatusUpdatedAt     *string  `bson:"statusUpdatedAt"`
}

// ProxyImportInput mirrors ProxyImportInput。
type ProxyImportInput struct {
	RawText string  `json:"rawText"`
	Country *string `json:"country"`
	Group   *string `json:"group"`
}

// ProxyUpdate mirrors ProxyUpdate（enabled/country/group 至少一个）。
type ProxyUpdate struct {
	Enabled *bool   `json:"enabled"`
	Country *string `json:"country"`
	Group   *string `json:"group"`
}

// ProxyStatusUpdateInput mirrors ProxyStatusUpdateInput。
type ProxyStatusUpdateInput struct {
	Status string `json:"status"`
}

// RestoreUsedProxiesInput mirrors RestoreUsedProxiesInput。
type RestoreUsedProxiesInput struct {
	Country *string `json:"country"`
	Group   *string `json:"group"`
}

// ProxyCountrySummary mirrors ProxyCountrySummary。
type ProxyCountrySummary struct {
	Country string `json:"country"`
	Total   int    `json:"total"`
	Enabled int    `json:"enabled"`
}

// ProxyGroupSummary mirrors ProxyGroupSummary。
type ProxyGroupSummary struct {
	Country     string   `json:"country"`
	Group       string   `json:"group"`
	Total       int      `json:"total"`
	Enabled     int      `json:"enabled"`
	Available   int      `json:"available"`
	Used        int      `json:"used"`
	Quarantined int      `json:"quarantined"`
	Schemes     []string `json:"schemes"`
}

// ProxyGroupUpdate mirrors ProxyGroupUpdate。
type ProxyGroupUpdate struct {
	Country    string  `json:"country"`
	Group      string  `json:"group"`
	NewCountry *string `json:"newCountry"`
	NewGroup   *string `json:"newGroup"`
	Enabled    *bool   `json:"enabled"`
}

// ProxyTestInput mirrors ProxyTestInput。
type ProxyTestInput struct {
	Country        *string `json:"country"`
	Group          *string `json:"group"`
	TimeoutSeconds float64 `json:"timeoutSeconds"`
}

// ProxyTestResult mirrors ProxyTestResult。
type ProxyTestResult struct {
	Tested           int              `json:"tested"`
	Available        int              `json:"available"`
	Failed           int              `json:"failed"`
	AverageLatencyMs *int             `json:"averageLatencyMs"`
	Countries        []map[string]any `json:"countries"`
}

// BulkIdsInput mirrors BulkIdsInput。
type BulkIdsInput struct {
	IDs []string `json:"ids"`
}

// ProxyGroupUpdateResult mirrors update_proxy_group 返回的 {"matched","modified"}。
type ProxyGroupUpdateResult struct {
	Matched  int `json:"matched"`
	Modified int `json:"modified"`
}

// RestoreUsedResult mirrors restore-used 返回 {"restored": count}。
type RestoreUsedResult struct {
	Restored int `json:"restored"`
}
