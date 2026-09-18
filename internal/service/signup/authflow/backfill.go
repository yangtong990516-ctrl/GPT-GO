package authflow

// backfill.go 协议补密码 + 2FA(passwordless 账号补设密码,成功后由钩子绑 TOTP)。
// 迁移自 codex-auto auth_flow.backfill_password_and_totp(2026-08-23 实测跑通,无浏览器)。
//
// 完整链路:
//  1. signin 带 post_login_add_password=true + connection=password + reauth=password
//     (这三个参数是建立「补密码」服务端状态的关键,缺了 password/add 会 400 invalid_auth_step)
//  2. authorize_continue → email_otp_verification(passwordless OTP 登录)
//  3. 邮箱 OTP 接码 → verify_otp → reset_password_new_password
//     (若账号已有 2FA:verify_otp 后进入 mfa_challenge,需先用已存 totp_secret 通过 TOTP)
//  4. POST /api/accounts/password/add 提交新密码 → 200 + continue_url(callback code)
//  5. follow_redirect_chain 消费 callback → getAuthSession 拿 access_token
//  6. 调用方在拿到 recent_auth access_token 后做 enroll+activate 绑 2FA
//
// 约束(对齐 codex):密码提交成功才允许做 2FA;密码失败则跳过 2FA 并返回错误。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"gpt-go/internal/service/signup"
	"gpt-go/internal/service/signup/otp"
)

// BackfillParams 是补密码+2FA 的输入。
type BackfillParams struct {
	Email      string       // 已有账号邮箱
	Password   string       // 新密码(空则现场生成随机密码,结果回写 result.Password)
	TotpSecret string       // 账号已有 TOTP 密钥(命中 mfa_challenge 时用)
	Mail       otp.Provider // 取邮箱 OTP 的 provider(OTP 重认证必需)
	OTPTimeout int          // OTP 等待超时(秒)
}

// RunBackfill 执行 passwordless 账号的补密码链路,返回含 access_token 的 AuthResult。
// 成功后 result.Password 是生效的新密码、result.AccessToken 满足 recent_auth(可供调用方绑 2FA)。
func (f *Flow) RunBackfill(ctx context.Context, p BackfillParams) (*signup.AuthResult, error) {
	email := strings.TrimSpace(p.Email)
	if email == "" {
		return nil, signup.NewRegistrationError("backfill_failed", "", fmt.Errorf("run_backfill 缺少邮箱"))
	}
	if p.Mail == nil {
		return nil, signup.NewRegistrationError("backfill_failed", email,
			fmt.Errorf("补密码走邮箱 OTP 重认证,BackfillParams.Mail 不能为 nil"))
	}
	f.isExistingAccount = true
	f.result.Email = email
	f.result.TotpSecret = strings.TrimSpace(p.TotpSecret)

	// 新密码:入参为空则现场生成(对齐 codex _random_password),落 result 供调用方回写库。
	pwd := strings.TrimSpace(p.Password)
	if pwd == "" {
		pwd = randomPassword()
	}
	f.result.Password = pwd

	// 0) warmup 硬门槛(补密码链路也要 oai-did)。
	if !f.warmup(ctx) {
		return nil, signup.NewRegistrationError("warmup_failed", email,
			fmt.Errorf("warmup 未种到 oai-did(补密码链路无法继续)"))
	}

	// 1) csrf。
	csrf, err := f.getCSRFToken(ctx)
	if err != nil {
		return nil, signup.NewRegistrationError("csrf_failed", email, err)
	}

	// 2) signin(补密码三参数:post_login_add_password/connection=password/reauth=password)。
	authURL, err := f.getBackfillAuthURL(ctx, csrf, email)
	if err != nil {
		return nil, signup.NewRegistrationError("signin_failed", email, err)
	}

	// 3) oauth_init → sentinel。
	deviceID, err := f.authOAuthInit(ctx, authURL)
	if err != nil {
		return nil, signup.NewRegistrationError("oauth_init_failed", email, err)
	}
	sentinelToken, err := f.getSentinelToken(ctx, deviceID, "authorize_continue")
	if err != nil {
		return nil, signup.NewRegistrationError("sentinel_failed", email, err)
	}

	// 4) authorize_continue → 应进入 email_otp_verification(passwordless OTP)。
	otpTimeout := p.OTPTimeout
	if otpTimeout <= 0 {
		otpTimeout = 120
	}
	otpSentAt := nowUnix()
	step, err := f.authorizeContinue(ctx, email, sentinelToken, "login", "https://auth.AI Platform.com/log-in")
	if err != nil {
		return nil, signup.NewRegistrationError("authorize_failed", email, err)
	}
	pageType := pageTypeOf(step)

	// 5) 接码(authorize 已触发发码,kickoff 只 resend;首次失败重发一次再试)。
	f.kickoffOTPDelivery(ctx, "existing_login")
	code, err := f.waitOTPWithRetry(ctx, p.Mail, email, otpTimeout, otpSentAt, "login")
	if err != nil {
		return nil, signup.NewRegistrationError("otp_fetch_failed", email, err)
	}
	otpResp, err := f.verifyOTP(ctx, code)
	if err != nil {
		return nil, signup.NewRegistrationError("otp_verify_failed", email, err)
	}
	pageType = pageTypeOf(otpResp)
	continueURL := continueURLOf(otpResp)

	// 6) 已有 2FA 的账号:verify_otp 后进入 mfa_challenge,先用已存 totp_secret 过 TOTP。
	if isMfaChallengeState(pageType, continueURL) {
		mfaResp, err := f.submitMfaTOTPWithSecret(ctx, continueURL)
		if err != nil {
			return nil, signup.NewRegistrationError("mfa_failed", email, err)
		}
		pageType = pageTypeOf(mfaResp)
		continueURL = continueURLOf(mfaResp)
	}

	// 7) 提交新密码(正确端点 /api/accounts/password/add)→ 拿 callback code。
	callbackURL, err := f.submitNewPassword(ctx, pwd)
	if err != nil {
		return nil, signup.NewRegistrationError("password_add_failed", email, err)
	}

	// 8) 消费 callback 建立 session(无 code 则跳过,直接尝试拿 session)。
	if callbackURL != "" && strings.Contains(callbackURL, "code=") {
		_, _ = f.followRedirectChain(ctx, callbackURL)
	}

	// 9) 拿 access_token。
	if err := f.getAuthSession(ctx); err != nil {
		return nil, signup.NewRegistrationError("auth_session_failed", email, err)
	}
	if strings.TrimSpace(f.result.AccessToken) == "" {
		// 密码可能已生效但没拿到 token:按 codex 约束,此时不做 2FA,但密码已设成功。
		// 返回部分成功(Password 已写 result),由调用方决定是否视为完成。
		return f.result, signup.NewRegistrationError("no_access_token", email,
			fmt.Errorf("补密码成功但未拿到 access_token(2FA 无法继续)"))
	}
	f.result.ExitIP = f.exitIP
	return f.result, nil
}

// getBackfillAuthURL 是补密码专用 signin(对齐 codex backfill 的 signin 三参数)。
// 与普通 getAuthURL 的差别:query 带 post_login_add_password=true + connection=password +
// reauth=password,缺了 password/add 会 400 invalid_auth_step。
func (f *Flow) getBackfillAuthURL(ctx context.Context, csrfToken, email string) (string, error) {
	deviceID := f.deviceID
	if deviceID == "" {
		deviceID = f.boot.Session.CookieValue("oai-did")
	}
	q := url.Values{}
	q.Set("post_login_add_password", "true")
	q.Set("connection", "password")
	q.Set("reauth", "password")
	q.Set("prompt", "login")
	q.Set("max_age", "0")
	q.Set("login_hint", email)
	q.Set("ext-oai-did", deviceID)
	q.Set("auth_session_logging_id", uuidv4())
	signinURL := "https://chatgpt.com/api/auth/signin/AI Platform?" + q.Encode()
	form := "callbackUrl=%2F&csrfToken=" + csrfToken + "&json=true"
	resp, err := f.boot.Session.Post(ctx, signinURL,
		"https://chatgpt.com/auth/login_with?callback_path=/",
		"application/x-www-form-urlencoded", []byte(form))
	if err != nil {
		return "", fmt.Errorf("backfill signin: %w", err)
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("backfill signin: HTTP %d body=%s", resp.StatusCode, truncate(resp.Text(), 200))
	}
	var body struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(resp.Bytes(), &body); err != nil || body.URL == "" {
		return "", fmt.Errorf("backfill signin: 未返回 auth url")
	}
	f.authURL = body.URL
	return body.URL, nil
}

// submitNewPassword 提交新密码(对齐 codex password/add)。
// 正确端点 https://auth.AI Platform.com/api/accounts/password/add;
// 返回 continue_url(含 callback code,供消费建 session)。
func (f *Flow) submitNewPassword(ctx context.Context, password string) (string, error) {
	payload, _ := json.Marshal(map[string]string{"password": password})
	resp, err := f.boot.Session.PostWithHeaders(ctx,
		"https://auth.AI Platform.com/api/accounts/password/add",
		"https://auth.AI Platform.com/email-verification",
		"application/json", payload,
		map[string]string{"Referer": "https://auth.AI Platform.com/email-verification"})
	if err != nil {
		return "", fmt.Errorf("password/add: %w", err)
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("password/add: HTTP %d body=%s", resp.StatusCode, truncate(resp.Text(), 200))
	}
	var data struct {
		ContinueURL string `json:"continue_url"`
	}
	_ = json.Unmarshal(resp.Bytes(), &data)
	return data.ContinueURL, nil
}
