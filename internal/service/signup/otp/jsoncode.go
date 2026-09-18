// jsoncode.go 通用「接码平台」OTP Provider：轮询一个返回 JSON 的 URL，取验证码。
//
// 支持的两种 JSON 形态（按 remail 官方 OpenAPI 文档 + 通用接码平台归纳）：
//
//	形态A remail 取件（GET /v1/pickup?email=&token=）：
//	  {"items":[{"sender":"...","receivedAt":"2026-01-01T00:00:00Z","verificationCode":"829104",...}],
//	   "fetch":{...}}
//	  → 每封邮件有 receivedAt 时间戳。【防串号】用 receivedAt >= issuedAfter 过滤，
//	    取时间窗内 receivedAt 最新一封的 verificationCode —— 对齐 codex-auto
//	    mail_providers/base.py「issued_after 防串号时间窗，务必尊重」的通用契约。
//
//	形态B 通用接码平台（普通 URL 返回 JSON）：
//	  {"verificationCode":"123456"} 或 {"code":"123456"} 或嵌套 {"data":{"verificationCode":...}}
//	  → 顶层字段取 code（字段名可在 codeFields 配置，默认 verificationCode→code，
//	    对齐 bridge.py `verification_code or code` 非空即用）。
//
// 与「邮箱原文类」（mailcode.go，从邮件 HTML/文本正则提码，见 extract.go）的区别：
// 接码平台的 code 是平台已解析好的【结构化字段】，取到非空即用（不再过 extract.go 正则
// 校验）；只有形态A 需要按 receivedAt 做时间窗过滤（防读到上一轮旧码）。
package otp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// jsoncodeKind 是本 provider 的标识（= 号池里存的 mail_source）。
// 通用接码平台（remail / url-json），区别于 mailcode（邮箱原文）。
const jsoncodeKind = "jsoncode"

// init 注册 jsoncode provider 到全局注册表。
func init() {
	Register(jsoncodeKind, newJSONCodeFromConfig)
}

// JSONCodeProvider 实现 Provider：轮询 JSON URL，取 verificationCode/code 字段。
//
// 能力：Pooled=true（接码平台的号是买来的、废了要换）、Ephemeral=false
// （地址由号池/平台分配，非每次新造）。
type JSONCodeProvider struct {
	// pollURL 是轮询入口（GET 返回含 code 的 JSON）。它已绑定具体号码/订单。
	pollURL string
	// codeFields 是按优先级尝试的 JSON 字段名（默认 ["verificationCode","code"]，
	// 对齐 bridge.py 的 `verification_code or code`）。支持点路径（如 "data.code"）。
	codeFields []string
	// httpClient 用于轮询（可注入 mock 便于测试）。
	httpClient *http.Client
	// pollInterval 是轮询间隔（默认 3s，避免打爆接码平台）。
	pollInterval time.Duration
	// dead 标记本号是否已废（Pooled 语义）。
	dead bool
}

// JSONCodeOption 是构造选项。
type JSONCodeOption func(*JSONCodeProvider)

// WithJSONCodeHTTPClient 注入自定义 HTTP 客户端（测试/超时控制用）。
func WithJSONCodeHTTPClient(c *http.Client) JSONCodeOption {
	return func(p *JSONCodeProvider) { p.httpClient = c }
}

// WithJSONCodePollInterval 覆盖轮询间隔（压测提速用；生产保持默认 3s）。
func WithJSONCodePollInterval(d time.Duration) JSONCodeOption {
	return func(p *JSONCodeProvider) { p.pollInterval = d }
}

// WithJSONCodeFields 覆盖 code 字段名候选（该平台字段名特殊时用，支持点路径）。
func WithJSONCodeFields(fields ...string) JSONCodeOption {
	return func(p *JSONCodeProvider) {
		if len(fields) > 0 {
			p.codeFields = fields
		}
	}
}

// NewJSONCodeProvider 由 pollURL 构造（最常用入口：号池/订单记录里有轮询 URL 时）。
func NewJSONCodeProvider(pollURL string, opts ...JSONCodeOption) *JSONCodeProvider {
	p := &JSONCodeProvider{
		pollURL:      pollURL,
		codeFields:   []string{"verificationCode", "code"}, // 对齐 bridge.py 优先级
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		pollInterval: 3 * time.Second,
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Caps 实现 Provider：声明能力（Pooled 接码号、非 Ephemeral）。
func (p *JSONCodeProvider) Caps() Capabilities {
	return Capabilities{
		Kind:        jsoncodeKind,
		DisplayName: "通用接码平台(URL-JSON)",
		Pooled:      true,
		Ephemeral:   false,
	}
}

// WaitForOTP 轮询 JSON URL，直到取到【时间窗内】的 code 或超时/取消。
//
// 对齐 codex-auto base.py「issued_after 防串号时间窗，务必尊重」：
//   - 形态A（remail items[]）：只取 receivedAt >= issuedAfterUnix 的邮件，且取其中最新一封；
//     旧码（receivedAt 早于窗口）一律忽略，等不到新码就继续轮询。
//   - 形态B（顶层字段）：响应本身无时间戳——平台只返回该号当前最新一条（接码语义天然成立），
//     字段非空即用；为兼容未来带时间戳的平台，若 codeFields 命中条目带 receivedAt 也过滤。
//
// 超时返回 *Error(Kind="timeout")；ctx 取消返回 *Error(Kind="cancelled")。
func (p *JSONCodeProvider) WaitForOTP(ctx context.Context, emailAddr string, timeoutSeconds int, issuedAfterUnix int64) (string, error) {
	if p.pollURL == "" {
		p.dead = true
		return "", NewError("接码平台缺少轮询 URL", true, "missing_poll_url", nil)
	}
	if timeoutSeconds <= 0 {
		timeoutSeconds = 120
	}
	deadline := time.Now().Add(time.Duration(timeoutSeconds) * time.Second)

	for {
		// 取消检查（对齐 bridge.py should_cancel）。
		select {
		case <-ctx.Done():
			return "", NewError("验证码等待被取消", false, "cancelled", ctx.Err())
		default:
		}

		code, err := p.fetchCode(ctx, issuedAfterUnix)
		if err == nil && code != "" {
			return code, nil
		}
		// 取码失败（网络/解析）或时间窗内暂无新码都不立即致死——继续轮询到超时。
		if time.Now().After(deadline) {
			p.dead = true
			return "", NewError(fmt.Sprintf("等待验证码超时（%ds）", timeoutSeconds), false, "timeout", nil)
		}
		select {
		case <-ctx.Done():
			return "", NewError("验证码等待被取消", false, "cancelled", ctx.Err())
		case <-time.After(p.pollInterval):
		}
	}
}

// fetchCode 拉一次 JSON 并提取【时间窗内】的 code。
//
// 解析顺序：先按形态A（remail items[]）找 receivedAt 最新且 >= issuedAfterUnix 的邮件；
// 没有 items 结构再按形态B（顶层 codeFields）取非空字段。都取不到返回 ""（平台还没出码）。
func (p *JSONCodeProvider) fetchCode(ctx context.Context, issuedAfterUnix int64) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.pollURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("接码平台返回 HTTP %d", resp.StatusCode)
	}
	var data any
	if err := json.Unmarshal(body, &data); err != nil {
		return "", fmt.Errorf("接码平台响应非 JSON: %w", err)
	}

	// ── 形态A：remail {items:[{receivedAt, verificationCode, ...}]} ──
	// 防串号：只取 receivedAt >= issuedAfterUnix 的邮件，取最新一封的 code。
	if code, found := latestItemCode(data, issuedAfterUnix); found {
		return code, nil
	}

	// ── 形态B：通用顶层字段（verificationCode → code → 自定义）──
	// 平台只返回当前最新一条，字段非空即用（对齐 bridge.py 非空即返回）。
	for _, field := range p.codeFields {
		if v := jsonFieldString(data, field); v != "" {
			return v, nil
		}
	}
	return "", nil // 时间窗内暂无新码 / 平台还没出码，返回空让上层继续轮询
}

// latestItemCode 从 remail items[] 中取【receivedAt >= issuedAfterUnix 的最新一封】的 code。
// 返回 (code, 是否命中 items 结构)。
//
// 命中判定：只要响应含 items 数组就算命中（即使时间窗内无新邮件、code 为空）——
// 这时不应再退化到顶层字段（remail 顶层本就没有 code），由上层继续轮询。
func latestItemCode(data any, issuedAfterUnix int64) (string, bool) {
	m, ok := data.(map[string]any)
	if !ok {
		return "", false
	}
	rawItems, ok := m["items"].([]any)
	if !ok {
		return "", false // 非 items 结构 → 走形态B
	}
	var bestCode string
	var bestTime time.Time
	for _, it := range rawItems {
		item, ok := it.(map[string]any)
		if !ok {
			continue
		}
		code := jsonFieldString(item, "verificationCode")
		if code == "" {
			continue
		}
		ts := parseRFC3339(jsonFieldString(item, "receivedAt"))
		// 防串号：receivedAt 早于时间窗起点的旧码忽略；receivedAt 缺失视为 0（也忽略，
		// 宁可等下一封也不拿无法确认时效的码——对齐「务必尊重 issued_after」）。
		if ts.Before(time.Unix(issuedAfterUnix, 0)) {
			continue
		}
		if bestCode == "" || ts.After(bestTime) {
			bestCode, bestTime = code, ts
		}
	}
	return bestCode, true
}

// parseRFC3339 解析 RFC3339 / ISO8601 时间串（remail receivedAt 是 date-time）；失败返回零值。
func parseRFC3339(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	// 兜底：无时区的 date-time（按 UTC 解释）。
	if t, err := time.Parse("2006-01-02T15:04:05", s); err == nil {
		return t.UTC()
	}
	return time.Time{}
}

// jsonFieldString 从已解析的 JSON（map 嵌套）按点路径取字符串值；非字符串/缺失返回 ""。
// 例：field="data.verificationCode" → data["data"]["verificationCode"]。
func jsonFieldString(data any, field string) string {
	cur := data
	for _, key := range strings.Split(field, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur, ok = m[key]
		if !ok {
			return ""
		}
	}
	s, _ := cur.(string)
	return strings.TrimSpace(s)
}

// CreateMailbox 实现 Provider：Ephemeral=false（地址由号池/平台分配，不每次新造），
// 按接口约定返回 "",nil——具体地址由号池记录提供（service 层 reserveEmail 给）。
func (p *JSONCodeProvider) CreateMailbox(ctx context.Context) (string, error) {
	return "", nil
}

// PeekOTP 非破坏性预读（对齐 base.py peek_otp）：拉一次，时间窗内有码就返回，无码返回 "",nil。
func (p *JSONCodeProvider) PeekOTP(ctx context.Context, emailAddr string, issuedAfterUnix int64, waitSeconds float64) (string, error) {
	code, err := p.fetchCode(ctx, issuedAfterUnix)
	if err != nil {
		return "", nil // 预读失败不抛错，让调用方走原发码路径
	}
	return code, nil
}

// MarkDead 实现 Provider：标记本号废掉（Pooled 语义）。
func (p *JSONCodeProvider) MarkDead(reason string) { p.dead = true }

// Exhausted 实现 Provider：本号是否已判定不可用。
func (p *JSONCodeProvider) Exhausted() bool { return p.dead }

// newJSONCodeFromConfig 是注册表工厂：从「全局配置 + 号池记录」构造。
//
// settings/account 里找轮询 URL（按常见键名优先级）：pollUrl / accessUrl / url。
// 找不到返回致命错误（不静默回退）。
func newJSONCodeFromConfig(ctx context.Context, settings, account map[string]any) (Provider, error) {
	url := firstNonEmptyStr(
		strOf(account, "pollUrl"), strOf(account, "accessUrl"), strOf(account, "url"),
		strOf(settings, "pollUrl"), strOf(settings, "accessUrl"), strOf(settings, "url"),
	)
	if url == "" {
		return nil, NewError("jsoncode: 配置缺少轮询 URL(pollUrl/accessUrl/url)", true, "missing_poll_url", nil)
	}
	// 可选自定义字段名（settings.codeFields 为 []string）。
	var fields []string
	if raw, ok := settings["codeFields"].([]any); ok {
		for _, it := range raw {
			if s, ok := it.(string); ok && s != "" {
				fields = append(fields, s)
			}
		}
	}
	return NewJSONCodeProvider(url, WithJSONCodeFields(fields...)), nil
}

// ── 小工具 ──

// strOf 从 map 取字符串键（缺失/非字符串返回 ""）。
func strOf(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	s, _ := m[key].(string)
	return strings.TrimSpace(s)
}

// firstNonEmptyStr 返回第一个非空串。
func firstNonEmptyStr(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
