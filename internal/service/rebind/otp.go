// otp.go 换绑专用 OTP provider：直接轮询邮箱 accessUrl 取 6 位验证码。
//
// 对齐 Python _wait_for_code_from_access_url + _RebindMailProvider：
//   - accessUrl 追加 wait 参数时保留原有 query（email=xxx 不能丢）；
//   - 收件人/发件人元数据过滤（防串邮箱）；
//   - 时间戳过滤旧码（issuedAfter 时间窗，防拿到历史邮件的验证码）；
//   - 简单响应（顶层 {"code": "..."}）直接采信，多邮件列表按时间戳过滤。
//
// 与 signup/otp 的 Provider 接口对齐，可直接喂给 authflow.RunLogin。
package rebind

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gpt-go/internal/service/signup/otp"
)

// codeRe 匹配 6 位数字（Go regexp 不支持 lookbehind/lookahead，
// 用非数字边界 + 捕获组实现等价语义）。
var codeRe = regexp.MustCompile(`(?:^|[^0-9])([0-9]{6})(?:[^0-9]|$)`)

// AccessURLProvider 是换绑接码的 otp.Provider 实现（对齐 _RebindMailProvider）。
//
// 与 signup/otp.JSONCodeProvider 的差异：
//   - 适配"多邮件列表"响应（mailcom 风格），按 received_at 时间戳过滤；
//   - 适配"顶层单 code"简单响应（mailcode 风格），直接采信；
//   - 保留 accessUrl 原有 query 参数（email=xxx）。
type AccessURLProvider struct {
	accessURL string
	client    *http.Client
}

// NewAccessURLProvider 构造接码 provider（timeout 为单次 HTTP 请求超时）。
func NewAccessURLProvider(accessURL string, timeout time.Duration) *AccessURLProvider {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &AccessURLProvider{
		accessURL: strings.TrimSpace(accessURL),
		client: &http.Client{
			Timeout: timeout,
			// 邮箱 accessUrl 是控制面端点（常是本地 MailCom），绝不走代理。
			Transport: &http.Transport{Proxy: nil},
		},
	}
}

// Caps 对齐 otp.Provider 能力声明（非池化、非临时）。
func (p *AccessURLProvider) Caps() otp.Capabilities {
	return otp.Capabilities{Kind: "rebind_access_url", DisplayName: "换绑邮箱接码", Pooled: false, Ephemeral: false}
}

// CreateMailbox 返回固定地址（Ephemeral=false，地址由换绑任务提供，不新造）。
func (p *AccessURLProvider) CreateMailbox(ctx context.Context) (string, error) {
	return "", nil
}

// MarkDead 非池化 provider 无操作（对齐 Provider 可选方法）。
func (p *AccessURLProvider) MarkDead(reason string) {}

// Exhausted 非池化 provider 恒 false（对齐 Provider 可选方法）。
func (p *AccessURLProvider) Exhausted() bool { return false }

// PeekOTP 非破坏性预读（对齐 Provider 可选方法：换绑登录的 OTP 已在 WaitForOTP
// 轮询，这里复用同一逻辑做一次预读，省一封重复码）。
func (p *AccessURLProvider) PeekOTP(ctx context.Context, emailAddr string, issuedAfterUnix int64, peekTimeout float64) (string, error) {
	code, found, err := p.pollOnce(ctx, strings.ToLower(strings.TrimSpace(emailAddr)), issuedAfterUnix, 1)
	if err != nil {
		return "", err
	}
	if !found {
		return "", nil
	}
	return code, nil
}

// WaitForOTP 阻塞等待 6 位验证码（对齐 _wait_for_code_from_access_url）。
//
//   - issuedAfter>0 时只接受该时间窗之后的码（防旧邮件串号）；
//   - 收件人过滤：响应含 to/recipient 元数据时，必须匹配 emailAddr；
//   - 发件人过滤：响应含 from/sender 元数据时，必须含 "AI Platform"；
//   - 超时返回 *otp.Error(Kind="timeout")。
func (p *AccessURLProvider) WaitForOTP(ctx context.Context, emailAddr string, timeoutSeconds int, issuedAfter int64) (string, error) {
	if p.accessURL == "" {
		return "", otp.NewError("email_access_url_missing", true, "config", nil)
	}
	if timeoutSeconds <= 0 {
		timeoutSeconds = 180
	}
	deadline := time.Now().Add(time.Duration(timeoutSeconds) * time.Second)
	expected := strings.ToLower(strings.TrimSpace(emailAddr))

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", otp.NewError("ctx_canceled", false, "canceled", ctx.Err())
		default:
		}
		// 剩余等待秒数作为 wait 参数（长轮询 hint），上限 10s。
		remain := int(time.Until(deadline).Seconds())
		if remain < 1 {
			remain = 1
		}
		if remain > 10 {
			remain = 10
		}
		code, found, err := p.pollOnce(ctx, expected, issuedAfter, remain)
		if err != nil {
			return "", err
		}
		if found {
			return code, nil
		}
		// 2s 轮询间隔（对齐 Python time.sleep(2)）。
		select {
		case <-ctx.Done():
			return "", otp.NewError("ctx_canceled", false, "canceled", ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}
	return "", otp.NewError("email_code_timeout", false, "timeout", nil)
}

// pollOnce 单次轮询 accessUrl；found=false 表示本次无新码继续等。
func (p *AccessURLProvider) pollOnce(ctx context.Context, expected string, issuedAfter int64, waitSec int) (string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, appendWaitParam(p.accessURL, waitSec), nil)
	if err != nil {
		return "", false, otp.NewError("bad_access_url", true, "config", err)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		// 网络错误可重试（对齐 Python mailbox_network_error retryable=True）。
		return "", false, nil
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", false, nil
	}
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		payload = string(raw)
	}

	// 收件人过滤：有元数据时必须匹配目标邮箱。
	recipients := metadataValues(payload, map[string]bool{
		"to": true, "to_email": true, "toemail": true,
		"recipient": true, "recipient_email": true, "email": true,
	})
	if len(recipients) > 0 && expected != "" {
		match := false
		for _, r := range recipients {
			if strings.Contains(r, expected) {
				match = true
				break
			}
		}
		if !match {
			return "", false, nil
		}
	}
	// 发件人过滤：有元数据时必须含 AI Platform。
	senders := metadataValues(payload, map[string]bool{
		"from": true, "from_email": true, "fromemail": true,
		"sender": true, "sender_email": true,
	})
	if len(senders) > 0 {
		match := false
		for _, s := range senders {
			if strings.Contains(s, "AI Platform") {
				match = true
				break
			}
		}
		if !match {
			return "", false, nil
		}
	}

	candidates := codeCandidates(payload, 0)
	// 带时间戳的候选：只接受 issuedAfter 时间窗之后的（-5s 容差对齐 Python）。
	var timed []codeCandidate
	for _, c := range candidates {
		if c.receivedAt > 0 {
			timed = append(timed, c)
		}
	}
	if len(timed) > 0 {
		for _, c := range timed {
			if issuedAfter <= 0 || c.receivedAt >= issuedAfter-5 {
				return c.code, true, nil
			}
		}
		return "", false, nil
	}
	// 简单响应（顶层单 code）直接采信（对齐 _is_simple_code_response）。
	if len(candidates) > 0 && isSimpleCodeResponse(payload) {
		return candidates[0].code, true, nil
	}
	return "", false, nil
}

// appendWaitParam 向 accessUrl 追加 wait 参数，保留原有 query（对齐 _append_wait_param）。
func appendWaitParam(accessURL string, waitSeconds int) string {
	u, err := url.Parse(accessURL)
	if err != nil {
		return accessURL
	}
	q := u.Query()
	q.Set("wait", strconv.Itoa(waitSeconds))
	u.RawQuery = q.Encode()
	return u.String()
}

// codeCandidate 是一个验证码候选（code + 可选接收时间戳）。
type codeCandidate struct {
	code       string
	receivedAt int64
}

// codeCandidates 递归提取响应中的 6 位码（对齐 _code_candidates）。
// inheritedTime 是外层邮件的时间戳（邮件列表里每封邮件自己的时间）。
func codeCandidates(v any, inheritedTime int64) []codeCandidate {
	var out []codeCandidate
	switch t := v.(type) {
	case map[string]any:
		ownTime := inheritedTime
		for _, k := range []string{"received_at", "receivedAt", "created_at", "createdAt", "create_time", "timestamp", "time"} {
			if raw, ok := t[k]; ok {
				if ts := parseTimestamp(raw); ts > 0 {
					ownTime = ts
				}
				break
			}
		}
		for _, k := range []string{"verification_code", "verificationCode", "otp", "code"} {
			if raw, ok := t[k]; ok {
				if m := codeRe.FindStringSubmatch(fmt.Sprint(raw)); len(m) > 1 {
					out = append(out, codeCandidate{code: m[1], receivedAt: ownTime})
				}
			}
		}
		for k, v2 := range t {
			if k == "verification_code" || k == "verificationCode" || k == "otp" || k == "code" {
				continue
			}
			out = append(out, codeCandidates(v2, ownTime)...)
		}
	case []any:
		for _, v2 := range t {
			out = append(out, codeCandidates(v2, inheritedTime)...)
		}
	case string:
		if m := codeRe.FindStringSubmatch(t); len(m) > 1 {
			out = append(out, codeCandidate{code: m[1], receivedAt: inheritedTime})
		}
	}
	return out
}

// isSimpleCodeResponse 判断是否"顶层单 code"简单响应（对齐 _is_simple_code_response）。
func isSimpleCodeResponse(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	hasCode := false
	for _, k := range []string{"code", "verification_code", "verificationCode", "otp"} {
		if _, ok := m[k]; ok {
			hasCode = true
			break
		}
	}
	if !hasCode {
		return false
	}
	// 含邮件列表数组字段 → 不是简单响应。
	for _, k := range []string{"messages", "items", "data", "mails", "emails", "list", "results", "value"} {
		if arr, ok := m[k].([]any); ok && len(arr) > 0 {
			if _, isMap := arr[0].(map[string]any); isMap {
				return false
			}
		}
	}
	return true
}

// metadataValues 递归提取元数据字段值（对齐 _metadata_values，全小写返回）。
func metadataValues(v any, keys map[string]bool) []string {
	var out []string
	switch t := v.(type) {
	case map[string]any:
		for k, v2 := range t {
			if keys[strings.ToLower(k)] {
				switch s := v2.(type) {
				case string:
					out = append(out, strings.ToLower(strings.TrimSpace(s)))
				case float64:
					out = append(out, strings.ToLower(strings.TrimSpace(strconv.FormatFloat(s, 'f', -1, 64))))
				}
			} else {
				out = append(out, metadataValues(v2, keys)...)
			}
		}
	case []any:
		for _, v2 := range t {
			out = append(out, metadataValues(v2, keys)...)
		}
	}
	return out
}

// parseTimestamp 解析时间戳（对齐 _timestamp：秒/毫秒/ISO8601）。
func parseTimestamp(v any) int64 {
	switch t := v.(type) {
	case float64:
		n := int64(t)
		if n > 10_000_000_000 {
			return n / 1000
		}
		return n
	case string:
		s := strings.TrimSpace(strings.ReplaceAll(t, "Z", "+00:00"))
		if s == "" {
			return 0
		}
		if parsed, err := time.Parse(time.RFC3339, s); err == nil {
			return parsed.Unix()
		}
	}
	return 0
}
