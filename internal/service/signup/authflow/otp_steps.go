// otp_steps.go 认证动作集合：发码策略（resend/send/passwordless-send）+ 密码验证 + TOTP 2FA。
//
// 对齐 codex-auto auth_flow.py 的认证主线（已剔除 SMS/add-phone）：
//   - kickoffOTPDelivery  统一发码策略（resend vs send 决策，防 state 破坏）
//   - resendOTP           重发（复用同 challenge，已有账号必须用这个）
//   - sendPasswordlessOTP passwordless 发码
//   - loginPasswordVerify 已有账号密码登录
//   - submitMfaTOTP       提交 TOTP 2FA 码（mfa-challenge）
//
// 与 steps.go 的分工：steps.go 是「注册主链」的步骤；本文件是「认证动作」的可复用集合，
// 注册链、登录链、改密链共用（降低重复实现）。
package authflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"gpt-go/internal/service/signup/otp"
	"gpt-go/internal/service/signup/totp"
)

// ═══════════════════════════════════════════════════════════════════════════
// 发码策略（对齐 Python kickoff_otp_delivery / resend_otp / send_passwordless_otp）
// ═══════════════════════════════════════════════════════════════════════════

// kickoffOTPDelivery 统一发码策略，根据是否已有账号选 resend vs send（对齐 Python）。
//
// 关键防 state 破坏逻辑（base.py / auth_flow.py:2655 血泪教训）：
//
//	已有账号 / passwordless：authorize/continue 已在服务端触发发码（state S，OTP X 在投递），
//	  这里【只能 resend】（复用同 challenge state）。若调 sendOTP，会新建 challenge 让 state
//	  跳到 Y，旧邮件 X 立即失效 → verify 时 401 wrong_email_otp_code。
//	新注册：passwordless/send-otp → email-otp/send → resend 按序兜底。
//
// 返回 true = 已成功触发一次发码（resend 或 send）；false = 全失败。
func (f *Flow) kickoffOTPDelivery(ctx context.Context, modeHint string) bool {
	isExisting := f.isExistingFlow(modeHint)

	if isExisting {
		// 已有账号：只能 resend（复用同 challenge，旧码不失效）。
		if f.resendOTP(ctx, "https://auth.AI Platform.com/email-verification") {
			return true
		}
		// resend 失败兜底：sendOTP 新建 challenge（旧 state 已坏，不得不重启）。
		if err := f.sendOTP(ctx, "https://auth.AI Platform.com/email-verification"); err == nil {
			return true
		}
		return false
	}

	// 新注册（原顺序）：passwordless/send-otp → resend → send。
	if f.sendPasswordlessOTP(ctx, "https://auth.AI Platform.com/create-account/password") {
		return true
	}
	if f.resendOTP(ctx, "https://auth.AI Platform.com/email-verification") {
		return true
	}
	if err := f.sendOTP(ctx, "https://auth.AI Platform.com/create-account/password"); err == nil {
		return true
	}
	return false
}

// isExistingFlow 判定当前是否「已有账号」流（决定 resend vs send，对齐 Python 2663 行）。
//
// 已有账号的判定来源（任一命中即视为已有）：
//   - modeHint 含 existing / passwordless_login / passwordless_signup
//   - signup 探测到的 existingVerificationMode == passwordless_signup
//   - f.isExistingAccount（authorize_continue / 入口已标记）
func (f *Flow) isExistingFlow(modeHint string) bool {
	m := strings.ToLower(strings.TrimSpace(modeHint))
	if strings.Contains(m, "existing") ||
		strings.Contains(m, "passwordless_login") ||
		strings.Contains(m, "passwordless_signup") {
		return true
	}
	if f.existingVerificationMode == "passwordless_signup" {
		return true
	}
	return f.isExistingAccount
}

// resendOTP 重发 OTP（POST /api/accounts/email-otp/resend），复用同 challenge。
// 对齐 Python resend_otp：成功返回 true，失败返回 false（不抛，供 kickoff 兜底）。
func (f *Flow) resendOTP(ctx context.Context, referer string) bool {
	resp, err := f.boot.Session.PostWithHeaders(ctx,
		"https://auth.AI Platform.com/api/accounts/email-otp/resend",
		referer, "application/json", []byte("{}"),
		sentinelHeaders(f.deviceID, f.lastSentinelToken, f.lastSentinelSoToken))
	if err != nil {
		return false
	}
	// 200 = 重发成功；其它（409 invalid_state / 429）= 失败，让调用方兜底。
	return resp.StatusCode == 200
}

// sendPasswordlessOTP 是 passwordless 发码（POST /api/accounts/passwordless/send-otp）。
// 对齐 Python send_passwordless_otp：新注册无密码账号触发发码用。
func (f *Flow) sendPasswordlessOTP(ctx context.Context, referer string) bool {
	resp, err := f.boot.Session.PostWithHeaders(ctx,
		"https://auth.AI Platform.com/api/accounts/passwordless/send-otp",
		referer, "application/json", []byte("{}"),
		sentinelHeaders(f.deviceID, f.lastSentinelToken, f.lastSentinelSoToken))
	if err != nil {
		return false
	}
	return resp.StatusCode == 200
}

// ═══════════════════════════════════════════════════════════════════════════
// 密码认证（对齐 Python login_password_verify）—— 已有账号 login_password 分支
// ═══════════════════════════════════════════════════════════════════════════

// loginPasswordVerify 已有账号密码登录一步（POST /api/accounts/password/verify）。
// 返回服务端响应（含 page.type / continue_url，据此判 mfa_challenge 或完成）。
func (f *Flow) loginPasswordVerify(ctx context.Context, password string) (map[string]any, error) {
	payload, _ := json.Marshal(map[string]string{"password": password})
	resp, err := f.boot.Session.PostWithHeaders(ctx,
		"https://auth.AI Platform.com/api/accounts/password/verify",
		"https://auth.AI Platform.com/log-in/password",
		"application/json", payload,
		sentinelHeaders(f.deviceID, f.lastSentinelToken, f.lastSentinelSoToken))
	if err != nil {
		return nil, fmt.Errorf("password/verify: %w", err)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("密码登录失败: HTTP %d body=%s", resp.StatusCode, truncate(resp.Text(), 260))
	}
	var data map[string]any
	if err := json.Unmarshal(resp.Bytes(), &data); err != nil {
		return map[string]any{}, nil
	}
	return data, nil
}

// ═══════════════════════════════════════════════════════════════════════════
// TOTP 2FA（对齐 Python submit_mfa_totp + _totp_now）—— mfa-challenge 分支
// ═══════════════════════════════════════════════════════════════════════════

// submitMfaTOTP 提交 TOTP 2FA 验证码（POST /api/accounts/mfa/verify）。
//
//	totpCode    6 位 TOTP 动态码（由 submitMfaTOTPWithSecret 现算，或调用方提供）
//	challengeID 从 continue_url 提取的 challenge ID（/mfa-challenge/<id>）
//
// 对齐 Python：id 优先用 totp_factor_id（若已知），否则用 challenge_id。
func (f *Flow) submitMfaTOTP(ctx context.Context, totpCode, challengeID string) (map[string]any, error) {
	verifyID := challengeID
	if f.result.TotpFactorID != "" {
		verifyID = f.result.TotpFactorID
	}
	payload, _ := json.Marshal(map[string]string{
		"code": totpCode, "type": "totp", "id": verifyID,
	})
	resp, err := f.boot.Session.PostWithHeaders(ctx,
		"https://auth.AI Platform.com/api/accounts/mfa/verify",
		"https://auth.AI Platform.com/mfa-challenge",
		"application/json", payload,
		sentinelHeaders(f.deviceID, f.lastSentinelToken, f.lastSentinelSoToken))
	if err != nil {
		return nil, fmt.Errorf("mfa/verify: %w", err)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("TOTP 验证失败: HTTP %d body=%s", resp.StatusCode, truncate(resp.Text(), 260))
	}
	var data map[string]any
	if err := json.Unmarshal(resp.Bytes(), &data); err != nil {
		return map[string]any{}, nil
	}
	return data, nil
}

// submitMfaTOTPWithSecret 用已保存的 totp_secret 现算 TOTP 码并提交（对齐 Python 4111 行）。
// 这是登录链 mfa-challenge 的便捷入口：从 continue_url 提取 challenge_id、算码、提交。
func (f *Flow) submitMfaTOTPWithSecret(ctx context.Context, continueURL string) (map[string]any, error) {
	secret := strings.TrimSpace(f.result.TotpSecret)
	if secret == "" {
		return nil, fmt.Errorf("进入 mfa-challenge 但没有 totp_secret，无法继续")
	}
	challengeID := extractMfaChallengeID(continueURL)
	if challengeID == "" {
		return nil, fmt.Errorf("无法从 continue_url 提取 mfa challenge_id: %s", truncate(continueURL, 180))
	}
	code, err := totp.Now(secret)
	if err != nil {
		return nil, fmt.Errorf("TOTP 算码失败: %w", err)
	}
	return f.submitMfaTOTP(ctx, code, challengeID)
}

// extractMfaChallengeID 从 continue_url 提取 mfa challenge ID。
// 形如 .../mfa-challenge/<id> 或 .../mfa-challenge/<id>?...，取路径最后一段。
func extractMfaChallengeID(continueURL string) string {
	if !strings.Contains(continueURL, "/mfa-challenge/") {
		return ""
	}
	// 去掉 query，取路径最后一段。
	path := continueURL
	if i := strings.Index(path, "?"); i >= 0 {
		path = path[:i]
	}
	path = strings.TrimRight(path, "/")
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return ""
}

// isMfaChallengeState 判断是否进入 mfa-challenge（需 TOTP 第二步）。
// 对齐 Python _is_mfa_challenge_state：page.type == mfa_challenge 或 continue_url 含 /mfa-challenge/。
func isMfaChallengeState(pageType, continueURL string) bool {
	pt := strings.ToLower(strings.TrimSpace(pageType))
	cu := strings.ToLower(strings.TrimSpace(continueURL))
	return pt == "mfa_challenge" || strings.Contains(cu, "/mfa-challenge/")
}

// ═══════════════════════════════════════════════════════════════════════════
// 取码便捷入口（peek + wait，对齐 Python 的「先 peek 省一封，再 wait」）
// ═══════════════════════════════════════════════════════════════════════════

// fetchOTPWithPeek 先 peek（命中服务端抢跑发的码则省一封）再 wait 取 OTP。
//
// 对齐 Python 的取码策略：
//   - login_hint 会让服务端【抢跑发码】（比正式提交早 20s），一轮能收 3 封【码一样】的信，
//     先 peek 命中就不用再等/再发。
//   - issuedAfterUnix 是发码前取的时间戳，防串号（只接受这之后到的邮件）。
//
// mail 为 nil 时返回明确错误（调用方应保证 OTP 分支必传 provider）。
func (f *Flow) fetchOTPWithPeek(ctx context.Context, mail otp.Provider, email string, timeoutSeconds int, issuedAfterUnix int64) (string, error) {
	if mail == nil {
		return "", fmt.Errorf("取码需要邮箱 provider（mail 为 nil）")
	}
	// 先 peek：命中即返回（省一封，避免撞服务端发码频控）。
	if code, _ := mail.PeekOTP(ctx, email, issuedAfterUnix, 0); code != "" {
		return code, nil
	}
	// 再 wait：阻塞等码（传 issuedAfter 防串号）。
	return mail.WaitForOTP(ctx, email, timeoutSeconds, issuedAfterUnix)
}
