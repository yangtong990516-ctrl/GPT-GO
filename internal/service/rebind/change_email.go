// change_email.go 换绑三端点：eligibility → begin（发码）→ verify（确认）。
//
// 对齐 Python AccountRebindService.change_email：
//   - 请求头一整套：Authorization + oai-device-id + oai-session-id +
//     oai-client-version + chatgpt-account-id + Cookie(session)，缺了被风控拒；
//   - eligibility 不带 chatgpt-account-id（Python 原样：path != eligibility 才带）；
//   - begin 400 时抓响应体确诊根因（unusual activity / invalid email / 缺字段）。
package rebind

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gpt-go/internal/service/signup/core"
)

const (
	chatgptBase        = "https://chatgpt.com"
	pathEligibility    = "/backend-api/accounts/change_email/eligibility"
	pathBegin          = "/backend-api/accounts/change_email/begin"
	pathVerify         = "/backend-api/accounts/change_email/verify"
	urlEligibility     = chatgptBase + pathEligibility
	urlBegin           = chatgptBase + pathBegin
	urlVerify          = chatgptBase + pathVerify
	oaiClientVersion   = "prod-46437587156517d920436051cb9ab60a95f0503a"
	oaiClientBuildNum  = "9723596"
	changeEmailTimeout = 30 // 秒，对齐 Python timeout=30
)

// RebindError 是换绑协议错误（对齐 Python AccountRebindError）。
type RebindError struct {
	Code      string
	Retryable bool
}

func (e *RebindError) Error() string { return e.Code }

// NewRebindError 构造协议错误码（对齐 AccountRebindError(code, retryable=...)）。
func NewRebindError(code string, retryable bool) *RebindError {
	return &RebindError{Code: code, Retryable: retryable}
}

// changeEmailHeaders 组装 change_email 全套请求头（对齐 build_headers）。
func changeEmailHeaders(at, deviceID, sessionID, accountID, cookieHeader, path string, withJSON bool) map[string]string {
	h := map[string]string{
		"Authorization":             "Bearer " + at,
		"Accept":                    "*/*",
		"Referer":                   chatgptBase + "/",
		"oai-device-id":             deviceID,
		"oai-session-id":            sessionID,
		"oai-client-version":        oaiClientVersion,
		"oai-client-build-number":   oaiClientBuildNum,
		"oai-language":              "zh-CN",
		"x-AI Platform-target-path": path,
		"x-AI Platform-target-route": path,
	}
	if withJSON {
		h["Content-Type"] = "application/json"
		h["Origin"] = chatgptBase
	}
	// 对齐 Python：eligibility 不带 chatgpt-account-id。
	if accountID != "" && path != pathEligibility {
		h["chatgpt-account-id"] = accountID
	}
	if cookieHeader != "" {
		h["Cookie"] = cookieHeader
	}
	return h
}

// CheckEligibility 第一步：检查账号是否允许换绑。
// 对齐 Python：eligible != true → email_change_ineligible:{type}。
func CheckEligibility(ctx context.Context, sess *core.Session, at, deviceID, sessionID, accountID, cookieHeader string) error {
	resp, err := sess.GetWithHeaders(ctx, urlEligibility, chatgptBase+"/",
		changeEmailHeaders(at, deviceID, sessionID, accountID, cookieHeader, pathEligibility, false))
	if err != nil {
		return NewRebindError("eligibility_network:"+sanitizeErr(err), true)
	}
	if resp.StatusCode >= 400 {
		return NewRebindError(fmt.Sprintf("eligibility_http_%d", resp.StatusCode), false)
	}
	var body struct {
		Eligible        bool   `json:"eligible"`
		EligibilityType string `json:"eligibility_type"`
	}
	if err := json.Unmarshal(resp.Bytes(), &body); err != nil {
		return NewRebindError("eligibility_bad_json", false)
	}
	if !body.Eligible {
		kind := strings.ToLower(strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
				return r
			}
			return '_'
		}, body.EligibilityType))
		if kind == "" {
			kind = "unknown"
		}
		return NewRebindError("email_change_ineligible:"+kind, false)
	}
	return nil
}

// BeginChangeEmail 第二步：向新邮箱发送换绑验证码。
// 返回发码时间戳（供 OTP issuedAfter 时间窗过滤旧码）。
// 对齐 Python：400 时抓响应体前 120 字符确诊根因。
func BeginChangeEmail(ctx context.Context, sess *core.Session, at, deviceID, sessionID, accountID, cookieHeader, newEmail string) (int64, error) {
	body, _ := json.Marshal(map[string]string{"email": newEmail})
	issuedAt := unixNow()
	resp, err := sess.PostWithHeaders(ctx, urlBegin, chatgptBase+"/", "application/json", body,
		changeEmailHeaders(at, deviceID, sessionID, accountID, cookieHeader, pathBegin, true))
	if err != nil {
		return 0, NewRebindError("begin_network:"+sanitizeErr(err), true)
	}
	if resp.StatusCode >= 400 {
		snippet := string(resp.Bytes())
		if len(snippet) > 120 {
			snippet = snippet[:120]
		}
		return 0, NewRebindError(fmt.Sprintf("email_change_begin_http_%d:%s", resp.StatusCode, snippet), false)
	}
	return issuedAt, nil
}

// VerifyChangeEmail 第三步：用验证码确认换绑（不可逆点）。
// 对齐 Python：>=400 → email_change_verify_http_{status}。
func VerifyChangeEmail(ctx context.Context, sess *core.Session, at, deviceID, sessionID, accountID, cookieHeader, newEmail, code string) error {
	body, _ := json.Marshal(map[string]string{"email": newEmail, "code": code})
	resp, err := sess.PostWithHeaders(ctx, urlVerify, chatgptBase+"/", "application/json", body,
		changeEmailHeaders(at, deviceID, sessionID, accountID, cookieHeader, pathVerify, true))
	if err != nil {
		return NewRebindError("verify_network:"+sanitizeErr(err), true)
	}
	if resp.StatusCode >= 400 {
		return NewRebindError(fmt.Sprintf("email_change_verify_http_%d", resp.StatusCode), false)
	}
	return nil
}

// Logout 换绑完成后登出旧会话（对齐 Python session.get("/auth/logout")）。
// 尽力而为：失败不影响换绑结果。
func Logout(ctx context.Context, sess *core.Session) {
	_, _ = sess.Get(ctx, chatgptBase+"/auth/logout", chatgptBase+"/")
}

// sanitizeErr 错误脱敏：去 Bearer/JWT，限长 120（对齐 _safe_error 的 240 减半策略）。
func sanitizeErr(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	// 去 JWT（eyJ 开头三段式）。
	if idx := strings.Index(s, "eyJ"); idx >= 0 {
		end := idx
		for end < len(s) && (s[end] == '.' || s[end] == '-' || s[end] == '_' ||
			(s[end] >= 'a' && s[end] <= 'z') || (s[end] >= 'A' && s[end] <= 'Z') ||
			(s[end] >= '0' && s[end] <= '9')) {
			end++
		}
		s = s[:idx] + "[REDACTED]" + s[end:]
	}
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}

// unixNow 当前 Unix 秒（独立函数便于测试替换）。
func unixNow() int64 {
	return time.Now().Unix()
}
