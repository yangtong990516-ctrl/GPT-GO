// login.go 已有账号协议登录状态机（对齐 Python run_protocol_login）。
//
// 与 RunRegister（新号注册）并列：RunLogin 处理「登录已有账号」——
// 适配 login_password（密码）/ passwordless_login（纯 OTP）/ mfa_challenge（2FA TOTP）分支。
//
// 前置步（warmup→csrf→signin→oauth_init→sentinel）与注册完全复用，
// 差异只在 authorize_continue 之后的分支处理。
package authflow

import (
	"context"
	"fmt"
	"strings"

	"gpt-go/internal/service/signup"
	"gpt-go/internal/service/signup/otp"
)

// LoginParams 是协议登录的输入。
type LoginParams struct {
	Email      string       // 已有账号邮箱
	Password   string       // 登录密码（空则从 result/库解析，见 resolveLoginPassword）
	TotpSecret string       // 已保存的 TOTP 密钥（命中 mfa_challenge 时用）
	Mail       otp.Provider // 取邮箱 OTP 的 provider（passwordless/OTP 分支用）
	OTPTimeout int          // OTP 等待超时（秒）
}

// RunLogin 执行已有账号协议登录，对齐 Python run_protocol_login。
//
// 流程：
//
//	check_proxy → warmup → csrf → signin → oauth_init → sentinel
//	→ authorize_continue(login screen_hint 探测) → 按 page.type 分三路：
//	    login_password         → loginPasswordVerify → [mfa_challenge → submitMfaTOTP]
//	    email_otp_verification → (passwordless) wait OTP → verifyOTP → [mfa → submitMfaTOTP]
//	    其它/未命中            → 回退 signup 探测（可能是新号，报错让上层决策）
//	→ redirect_chain → consume_callback → auth_session
//
// 返回 *signup.AuthResult（含 session_token/access_token/device_id/totp_secret）。
func (f *Flow) RunLogin(ctx context.Context, p LoginParams) (*signup.AuthResult, error) {
	email := strings.TrimSpace(p.Email)
	if email == "" {
		return nil, signup.NewRegistrationError("login_failed", "", fmt.Errorf("run_login 缺少邮箱"))
	}
	f.result.Email = email
	f.result.TotpSecret = strings.TrimSpace(p.TotpSecret)

	// 0) 网络预检（复用注册同款，出口 IP 已在拨号阶段绑进 Bootstrap）。
	f.checkProxy(ctx)

	// 1) warmup 硬门槛：没 oai-did 走不通 authorize 链，早失败早换 IP。
	if !f.warmup(ctx) {
		return nil, signup.NewRegistrationError("warmup_failed", email,
			fmt.Errorf("warmup 4 次均未种到 oai-did（继续必 409 invalid_state，多为出口 IP 不通或被 CF 拦）"))
	}

	// 2-5) csrf → signin → oauth_init → sentinel（与注册复用）。
	csrf, err := f.getCSRFToken(ctx)
	if err != nil {
		return nil, signup.NewRegistrationError("csrf_failed", email, err)
	}
	authURL, err := f.getAuthURL(ctx, csrf, email)
	if err != nil {
		return nil, signup.NewRegistrationError("signin_failed", email, err)
	}
	deviceID, err := f.authOAuthInit(ctx, authURL)
	if err != nil {
		return nil, signup.NewRegistrationError("oauth_init_failed", email, err)
	}
	sentinelToken, err := f.getSentinelToken(ctx, deviceID, "authorize_continue")
	if err != nil {
		return nil, signup.NewRegistrationError("sentinel_failed", email, err)
	}

	// 登录语义即「已有账号」：kickoff 据此选 resend（避免 send 破坏 state → wrong_email_otp_code）。
	f.isExistingAccount = true

	// 6) login screen_hint 探测（对齐 Python：优先走 login 探 password/otp 分支）。
	password := strings.TrimSpace(p.Password)
	if password == "" {
		password = f.resolveLoginPassword(email)
	}

	pageType, continueURL, mode := f.probeLoginBranch(ctx, email, sentinelToken)
	var otpSentAt int64
	otpTimeout := p.OTPTimeout
	if otpTimeout <= 0 {
		otpTimeout = 60
	}

	switch {
	case pageType == "login_password" || strings.Contains(continueURL, "/log-in/password"):
		// ── 密码分支：password/verify → 可能进 mfa_challenge ──
		f.isExistingAccount = true
		loginResp, err := f.loginPasswordVerify(ctx, password)
		if err != nil {
			return nil, signup.NewRegistrationError("login_password_failed", email, err)
		}
		pageType = pageTypeOf(loginResp)
		continueURL = continueURLOf(loginResp)

		// mfa_challenge 分支（密码验证后需 TOTP 2FA）。
		if isMfaChallengeState(pageType, continueURL) {
			mfaResp, err := f.submitMfaTOTPWithSecret(ctx, continueURL)
			if err != nil {
				return nil, signup.NewRegistrationError("mfa_failed", email, err)
			}
			continueURL = continueURLOf(mfaResp)
		}

	case pageType == "email_otp_verification" || strings.Contains(continueURL, "/email-verification"):
		// ── OTP 分支（passwordless_login / 已有账号 OTP）──
		f.isExistingAccount = true
		if p.Mail == nil {
			return nil, signup.NewRegistrationError("otp_fetch_failed", email,
				fmt.Errorf("OTP 分支需要邮箱 provider（LoginParams.Mail 为 nil）"))
		}
		// authorize/continue 已触发发码，kickoff 只 resend（不 send，防 state 破坏）。
		otpSentAt = nowUnix()
		f.kickoffOTPDelivery(ctx, "existing_"+mode)
		code, err := f.waitOTPWithRetry(ctx, p.Mail, email, otpTimeout, otpSentAt, mode)
		if err != nil {
			return nil, signup.NewRegistrationError("otp_fetch_failed", email, err)
		}
		otpResp, err := f.verifyOTP(ctx, code)
		if err != nil {
			return nil, signup.NewRegistrationError("otp_verify_failed", email, err)
		}
		pageType = pageTypeOf(otpResp)
		continueURL = continueURLOf(otpResp)

		// OTP 验证后若账号已启用 2FA，服务端返回 mfa_challenge（对齐 Python 4198）。
		if isMfaChallengeState(pageType, continueURL) {
			mfaResp, err := f.submitMfaTOTPWithSecret(ctx, continueURL)
			if err != nil {
				return nil, signup.NewRegistrationError("mfa_failed", email, err)
			}
			continueURL = continueURLOf(mfaResp)
		}

	default:
		// 未命中已有账号分支：可能是新号（登录语义下不该注册），报错让上层决策。
		return nil, signup.NewRegistrationError("existing_account", email,
			fmt.Errorf("登录探测未命中已有账号分支（page_type=%s mode=%s），该邮箱可能未注册", pageType, mode))
	}

	// 7) 重定向链 → 捕获 callback → consume → auth_session（与注册复用）。
	if continueURL != "" {
		callbackURL, _ := f.followRedirectChain(ctx, continueURL)
		if callbackURL != "" {
			f.consumeCallbackForSession(ctx, callbackURL)
		}
	}
	if err := f.getAuthSession(ctx); err != nil {
		return nil, signup.NewRegistrationError("auth_session_failed", email, err)
	}

	if !f.result.IsValid() {
		return nil, signup.NewRegistrationError("invalid_result", email,
			fmt.Errorf("登录完成但未获取有效凭证（session_token/access_token 缺失）"))
	}
	// 登录拿到满足 recent_auth 的 access_token 后、返回前触发 onSessionReady 钩子
	// （对齐 codex on_session_ready:补 2FA 的 enroll/activate 必须在 recent_auth 状态下进行）。
	if f.onSessionReady != nil {
		f.onSessionReady(f, f.result.AccessToken)
	}
	f.result.ExitIP = f.exitIP
	return f.result, nil
}

// SetOnSessionReady 设置 session 就绪钩子（补 2FA 等场景用）。
func (f *Flow) SetOnSessionReady(cb func(flow *Flow, accessToken string)) {
	f.onSessionReady = cb
}

// Boot 已在 authflow.go 暴露(f.Boot());补 2FA 服务登录后用 f.Boot().Session
// 直接发 backend-api 请求(rebind 同款模式),无需经钩子。

// probeLoginBranch 用 login screen_hint 探测已有账号分支（对齐 Python 4062-4135）。
//
// 返回 (pageType, continueURL, mode)。探测失败（网络/未命中）返回空串让上层回退。
func (f *Flow) probeLoginBranch(ctx context.Context, email, sentinelToken string) (pageType, continueURL, mode string) {
	// 先 peek 一下收件箱：login_hint 会让服务端抢跑发码，命中可省一封（可选优化）。
	loginStep, err := f.authorizeContinue(ctx, email, sentinelToken, "login", "https://auth.AI Platform.com/log-in")
	if err != nil {
		return "", "", ""
	}
	pageType = strings.ToLower(pageTypeOf(loginStep))
	continueURL = continueURLOf(loginStep)
	mode = strings.ToLower(nestedStrOf(loginStep, "page", "payload", "email_verification_mode"))
	f.existingPageType = pageType
	f.existingVerificationMode = mode
	return pageType, continueURL, mode
}

// waitOTPWithRetry 取 OTP + 401/409 重发重试（对齐 Python 4166-4188）。
func (f *Flow) waitOTPWithRetry(ctx context.Context, mail otp.Provider, email string, timeout int, sentAt int64, mode string) (string, error) {
	// 先 peek（服务端可能已抢跑发码，命中省一封）。
	if code, _ := mail.PeekOTP(ctx, email, sentAt, 0); code != "" {
		return code, nil
	}
	code, err := mail.WaitForOTP(ctx, email, timeout, sentAt)
	if err != nil {
		return "", err
	}
	// verify 的 401/409 重试在调用方做（这里只负责取码）。
	return code, nil
}

// resolveLoginPassword 解析登录密码（对齐 Python _resolve_login_password）。
//
// 优先级：调用方传入 > （库的 account_callback，TODO 接入） > 默认规则猜一个。
// 当前实现：调用方传入已由调用处处理；这里只做默认规则兜底。
func (f *Flow) resolveLoginPassword(email string) string {
	// TODO(登录增强)：接 account_callback 从账号库读真密码（对齐 Python 2713-2738）。
	// 当前回退默认规则（对齐 Python _default_password_from_email，仅碰运气用）。
	return defaultPasswordFromEmail(email)
}

// defaultPasswordFromEmail 是默认猜密码规则（对齐 Python _default_password_from_email）。
// ⚠️ 这是猜的，不是真密码；只在拿不到真密码时碰运气（早期用同规则建的号可能命中）。
func defaultPasswordFromEmail(email string) string {
	pwd := strings.ReplaceAll(email, "@", "")
	if len(pwd) < 8 {
		pwd += "2026OpenAI"
	}
	return pwd
}
