// Package mfa 实现已有账号的 2FA(TOTP)协议操作,迁移自 codex-auto
// account_security_service.py:
//   - CheckStatus: 用 access_token 查 /me 的 mfa_flag_enabled,回写 totpStatus(无重认证)。
//   - Enroll/Activate: enroll 拿 [FUNC]+session_id → 本地生成 6 位码 → activate 绑定。
//     注意:enroll 需要账号处于 recent_auth(最近一次密码认证)状态,否则 401
//     recent_auth_required。调用方负责先完成重认证拿到可用的 access_token。
package mfa

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"gpt-go/internal/service/signup/core"
	"gpt-go/internal/service/signup/totp"
)

const (
	// chatgptMeURL 返回当前账号信息(含 mfa_flag_enabled)。
	chatgptMeURL = "https://chatgpt.com/backend-api/me"
	// chatgptMFAEnrollURL 提交 enroll 申请(factor_type=totp),返回 [FUNC]+session_id。
	chatgptMFAEnrollURL = "https://chatgpt.com/backend-api/accounts/mfa/enroll"
	// chatgptMFAActivateURL 用 6 位码 + session_id 激活绑定。
	chatgptMFAActivateURL = "https://chatgpt.com/backend-api/accounts/mfa/user/activate_enrollment"
)

// Session 抽象本包需要的 HTTP 能力(*core.Session 满足;测试可注入替身)。
type Session interface {
	GetWithHeaders(ctx context.Context, url, referer string, extra map[string]string) (*core.Response, error)
	PostWithHeaders(ctx context.Context, url, referer, contentType string, body []byte, extra map[string]string) (*core.Response, error)
}

// Error 是带机器可读 code 的 2FA 错误(对齐 codex AccountSecurityError)。
type Error struct {
	Code string
	Msg  string
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Msg) }

func errf(code, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// bearer 构造带 Authorization 的额外头。
func bearer(accessToken string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + strings.TrimSpace(accessToken)}
}

// StatusResult 是 CheckStatus 的结果。
type StatusResult struct {
	MfaFlagEnabled       bool   `json:"mfaFlagEnabled"`
	TotpSecretConfigured bool   `json:"totpSecretConfigured"`
	TotpStatus           string `json:"totpStatus"` // enabled/orphaned/dirty/未知
}

// CheckStatus 用 access_token 查询账号 2FA 是否启用(对齐 check_2fa_status)。
// localSecretConfigured 传入本地是否已有 [FUNC](用于判定 totpStatus)。
// 无需 recent_auth,只需有效 access_token。
func CheckStatus(ctx context.Context, sess Session, accessToken string, localSecretConfigured bool) (*StatusResult, error) {
	if strings.TrimSpace(accessToken) == "" {
		return nil, errf("missing_token", "账号没有 access_token")
	}
	resp, err := sess.GetWithHeaders(ctx, chatgptMeURL, "https://chatgpt.com/", bearer(accessToken))
	if err != nil {
		return nil, errf("me_check_failed", "查询 2FA 状态网络错误: %v", err)
	}
	if resp.StatusCode != 200 {
		return nil, errf("me_check_failed", "查询 2FA 状态失败 status=%d", resp.StatusCode)
	}
	var body struct {
		MfaFlagEnabled bool `json:"mfa_flag_enabled"`
	}
	_ = json.Unmarshal(resp.Bytes(), &body)
	status := ""
	switch {
	case body.MfaFlagEnabled && !localSecretConfigured:
		status = "orphaned" // 服务端已启用但本地 [FUNC] 缺失(失效,需补)
	case body.MfaFlagEnabled && localSecretConfigured:
		status = "enabled"
	case !body.MfaFlagEnabled && localSecretConfigured:
		status = "dirty"
	}
	return &StatusResult{
		MfaFlagEnabled:       body.MfaFlagEnabled,
		TotpSecretConfigured: localSecretConfigured,
		TotpStatus:           status,
	}, nil
}

// enrollResult 是 enroll 响应的解析结果。
type enrollResult struct {
	Secret    string // 规范化后的 [FUNC]
	SessionID string
}

// parseDataField 兼容顶层与 {data:{...}} 嵌套两种响应形态(对齐 codex)。
func parseDataField(body []byte) map[string]any {
	var m map[string]any
	if json.Unmarshal(body, &m) != nil {
		return nil
	}
	if d, ok := m["data"].(map[string]any); ok {
		return d
	}
	return m
}

// EnrollAndActivate 对处于 recent_auth 状态的账号 enroll + activate TOTP,
// 返回规范化 [FUNC](对齐 _enroll_totp)。调用前必须确保账号已 recent_auth,
// 否则返回 code=recent_auth_required。
func EnrollAndActivate(ctx context.Context, sess Session, accessToken string) (string, error) {
	// ── enroll ──
	enrollBody, _ := json.Marshal(map[string]any{"factor_type": "totp"})
	enroll, err := sess.PostWithHeaders(ctx, chatgptMFAEnrollURL, "https://chatgpt.com/",
		"application/json", enrollBody, bearer(accessToken))
	if err != nil {
		return "", errf("enroll_failed", "enroll 网络错误: %v", err)
	}
	data := parseDataField(enroll.Bytes())
	secret, _ := data["totp_secret"].(string)
	if secret == "" {
		secret, _ = data["secret"].(string)
	}
	sessionID, _ := data["session_id"].(string)
	if enroll.StatusCode != 200 || secret == "" || sessionID == "" {
		code := "enroll_failed"
		if enroll.StatusCode == 401 && strings.Contains(string(enroll.Bytes()), "recent_auth_required") {
			code = "recent_auth_required"
		}
		return "", errf(code, "2FA enroll 失败 status=%d: %s", enroll.StatusCode, trunc(string(enroll.Bytes()), 200))
	}
	secretNorm, err := normalizeSecret(secret)
	if err != nil {
		return "", errf("enroll_bad_secret", "enroll 返回的 [FUNC] 无效: %v", err)
	}

	// ── activate ──
	code, err := totp.Now(secretNorm)
	if err != nil {
		return "", errf("totp_gen_failed", "本地生成 TOTP 码失败: %v", err)
	}
	actBody, _ := json.Marshal(map[string]any{
		"code": code, "factor_type": "totp", "session_id": sessionID,
	})
	activated, err := sess.PostWithHeaders(ctx, chatgptMFAActivateURL, "https://chatgpt.com/",
		"application/json", actBody, bearer(accessToken))
	if err != nil {
		return "", errf("activate_failed", "activate 网络错误: %v", err)
	}
	actData := parseDataField(activated.Bytes())
	success, _ := actData["success"].(bool)
	if activated.StatusCode != 200 || !success {
		return "", errf("activate_failed", "2FA 激活失败 status=%d: %s", activated.StatusCode, trunc(string(activated.Bytes()), 200))
	}
	return secretNorm, nil
}

// normalizeSecret 用 totp 包校验并规范化 [FUNC](去空格/大写/补 padding)。
func normalizeSecret(secret string) (string, error) {
	s := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(secret), " ", ""))
	// 生成一次验证码即校验密钥合法性。
	if _, err := totp.Now(s); err != nil {
		return "", err
	}
	return s, nil
}

// Verify 用本地 [FUNC] 生成当前验证码做有效性校验(对齐 verify_totp)。
func Verify(secret string) (code string, valid bool, err error) {
	s, nerr := normalizeSecret(secret)
	if nerr != nil {
		return "", false, nerr
	}
	c, gerr := totp.Now(s)
	if gerr != nil {
		return "", true, gerr
	}
	return c, true, nil
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
