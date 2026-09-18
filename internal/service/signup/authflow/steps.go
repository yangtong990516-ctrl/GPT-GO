// steps.go 实现注册状态机的 10 步，逐步对齐 codex-auto _runtime/auth_flow.py。
//
// 每步标注了对齐的 Python 方法与关键实测约束。核心红线（见 authflow.go 头注释）在这里
// 逐条落地：device_id 三处同源、重定向去 sec-fetch-user、中后段只原 session 重试、
// sentinel SO 按本 flow 服务端要求带。
package authflow

import (
	"gpt-go/internal/service/signup/humanize"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gpt-go/internal/service/signup/sentinel"
)

// ═══════════════════════════════════════════════════════════════════════════
// Step 0: check_proxy —— 网络预检（对齐 Python check_proxy）
// ═══════════════════════════════════════════════════════════════════════════
// codex-auto 用 cloudflare trace 探国家+出口 IP，并据国家重生成指纹。本 Go 版的国家/时区
// 已在 service 层拨号时由 probe 实测 + GeoIP 绑定进 core.Bootstrap（国家动态实测），
// 故这里只做「出口 IP 复确认」——若 warmup 后 cookie 里能拿到出口线索再校验。
// 失败仅告警不阻断（对齐 Python：有些代理不通 trace 但能通 chatgpt）。
func (f *Flow) checkProxy(ctx context.Context) bool {
	// 出口 IP 已在拨号阶段实测并绑进 Bootstrap（f.boot.Geo），这里只确认非空。
	// 不在此重复探测（拨号已探过，避免多打一次请求增加风控特征）。
	if f.boot != nil && f.boot.Geo != nil && f.boot.Geo.CountryCode != "" {
		f.countryCode = f.boot.Geo.CountryCode
		return true
	}
	// GeoIP 缺失不阻断（国家会在后续 BindEgress 时补登）。
	return true
}

// ═══════════════════════════════════════════════════════════════════════════
// Step 1: warmup —— 拿 oai-did cookie【硬门槛】（对齐 Python warmup）
// ═══════════════════════════════════════════════════════════════════════════
// 判据不是 HTTP 200，而是 cookie jar 里有没有 oai-did（CF 403 只给 __cf_bm）。
// 失败 = 后面 authorize/continue 必 409 invalid_state（实测 5/5），故返回 false 让上层拦掉。
// Client Hints 已由 core.Session.GetNavigation 自动注入（② 头与 ① TLS 同族），无需手搓。
func (f *Flow) warmup(ctx context.Context) bool {
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			// 重试只换出口 IP（新 session=新出口），指纹不变（指纹没问题）。
			// 注：模型 B 下「换 IP」靠拨号器在更上层重拨；此处同 session 重试即可
			//（core.Session 对 TLS 瞬断已内建原 session 重试）。
			sleepCtx(ctx, time.Duration(3+attempt*2)*time.Second)
		}
		resp, err := f.boot.Session.GetNavigation(ctx, "https://chatgpt.com/")
		_ = resp // 状态码仅用于日志；判据是 cookie
		if err != nil {
			// TLS 瞬断/超时：core 已重试，仍失败则进下一轮（上层拨号可换 IP）。
			continue
		}
		// 唯一可信判据：oai-did 到底种上没有。
		if did := f.boot.Session.CookieValue("oai-did"); did != "" {
			f.deviceID = did
			f.result.DeviceID = did
			return true
		}
	}
	return false
}

// ═══════════════════════════════════════════════════════════════════════════
// Step 2: get_csrf_token（对齐 Python get_csrf_token）
// ═══════════════════════════════════════════════════════════════════════════
func (f *Flow) getCSRFToken(ctx context.Context) (string, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		resp, err := f.boot.Session.Get(ctx, "https://chatgpt.com/api/auth/csrf", "https://chatgpt.com/auth/login")
		if err != nil {
			lastErr = err
			// TLS 错且未种关键 cookie，可同版本轮换（core 红线：仅同版本变体）。
			if rotated, rerr := f.boot.RotateTLSIdentity(f.config.Proxy, 0, nil); rotated && rerr == nil {
				continue
			}
			return "", fmt.Errorf("csrf: TLS 握手失败: %w", err)
		}
		if resp.StatusCode == 403 && attempt < 2 {
			sleepCtx(ctx, time.Duration((attempt+1)*5)*time.Second) // CF 403 递增退避
			continue
		}
		if resp.StatusCode != 200 {
			return "", fmt.Errorf("csrf: HTTP %d", resp.StatusCode)
		}
		var body struct {
			CSRFToken string `json:"csrfToken"`
		}
		if err := json.Unmarshal(resp.Bytes(), &body); err != nil || body.CSRFToken == "" {
			return "", fmt.Errorf("csrf: 解析 csrfToken 失败")
		}
		f.csrfToken = body.CSRFToken
		f.result.CSRFToken = body.CSRFToken
		return body.CSRFToken, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("csrf: 重试耗尽")
	}
	return "", lastErr
}

// ═══════════════════════════════════════════════════════════════════════════
// Step 3: get_auth_url —— 拿 authorize 地址（对齐 Python get_auth_url）
// ═══════════════════════════════════════════════════════════════════════════
// body(form): callbackUrl=/&csrfToken&json=true；query 带 ext-oai-did=device_id。
// 实测约束：referer 是 auth/login_with?callback_path=/，不多带 login_hint/passkey。
func (f *Flow) getAuthURL(ctx context.Context, csrfToken, email string) (string, error) {
	deviceID := f.deviceID
	if deviceID == "" {
		deviceID = f.boot.Session.CookieValue("oai-did")
	}
	q := "prompt=login&screen_hint=login&ext-oai-did=" + deviceID + "&auth_session_logging_id=" + uuidv4()
	signinURL := "https://chatgpt.com/api/auth/signin/openai?" + q
	form := "callbackUrl=%2F&csrfToken=" + csrfToken + "&json=true"
	resp, err := f.boot.Session.Post(ctx, signinURL,
		"https://chatgpt.com/auth/login_with?callback_path=/",
		"application/x-www-form-urlencoded", []byte(form))
	if err != nil {
		return "", fmt.Errorf("signin: %w", err)
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("signin: HTTP %d body=%s", resp.StatusCode, truncate(resp.Text(), 200))
	}
	var body struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(resp.Bytes(), &body); err != nil || body.URL == "" {
		return "", fmt.Errorf("signin: 未返回 auth url")
	}
	f.authURL = body.URL
	return body.URL, nil
}

// ═══════════════════════════════════════════════════════════════════════════
// Step 4: auth_oauth_init —— 建 state + 种 oai-did（对齐 Python auth_oauth_init）
// ═══════════════════════════════════════════════════════════════════════════
// 跨站跳转（chatgpt.com→auth.openai.com）+ 302 自动跟随 → 用 GetNavigationFollowRedirect
// （去掉 sec-fetch-user，sec-fetch-site=cross-site）。这步建立的就是后面 authorize/continue
// 要用的 state，头不像真浏览器必 409。
func (f *Flow) authOAuthInit(ctx context.Context, authURL string) (string, error) {
	resp, err := f.boot.Session.GetNavigationFollowRedirect(ctx, authURL, "cross-site")
	if err != nil {
		return "", fmt.Errorf("oauth_init: %w", err)
	}
	_ = resp
	// device_id 优先取 cookie（三处同源的源头）；兜底从 HTML 提取，再兜底生成。
	deviceID := f.boot.Session.CookieValue("oai-did")
	if deviceID == "" {
		deviceID = extractDeviceIDFromHTML(resp.Text())
	}
	if deviceID == "" {
		deviceID = uuidv4() // 兜底（对齐 Python：拿不到就生成）
	}
	// 三处同源：写回 cookie（保证 cookie==请求头==SentinelEnv.DeviceID）。
	if f.boot.Session.CookieValue("oai-did") == "" {
		f.boot.Session.SetCookie("oai-did", deviceID, ".chatgpt.com", "/")
	}
	f.deviceID = deviceID
	f.result.DeviceID = deviceID
	return deviceID, nil
}

// ═══════════════════════════════════════════════════════════════════════════
// Step 5: get_sentinel_token —— 解 PoW（对齐 Python get_sentinel_token）
// ═══════════════════════════════════════════════════════════════════════════
// 通过 sentinel.Solver 接口（P4 那条线实现）求解；本包只负责：
//
//	① 从 core.Identity.SentinelEnv 取三路自洽画像（deviceID 三处同源）；
//	② 把 core.Session 当 ChallengeFetcher 发 /sentinel/req（与 TLS 会话同源）。
//
// so_token 只在服务端本 flow 要求时带（P4 按 SoRequired 判定），不拿别的 flow 凑数。
func (f *Flow) getSentinelToken(ctx context.Context, deviceID, flow string) (string, error) {
	if f.solver == nil {
		return "", fmt.Errorf("sentinel: Solver 未注入（P4 未接入）")
	}
	// 人类模拟(对齐 codex human_action_pause("challenge")):看到挑战框→等 SDK 运行
	// 的认知停顿 0.8~2.4s,再求解提交,避免 challenge 秒解的非人时序特征。
	humanize.ActionPause(nil, "challenge")
	env := f.sentinelEnv(deviceID, flow)
	res, err := f.solver.GetToken(ctx, env, sentinel.Flow(flow))
	if err != nil {
		return "", fmt.Errorf("sentinel[%s]: %w", flow, err)
	}
	f.lastSentinelToken = res.Token
	f.lastSentinelSoToken = res.SOToken // 服务端没要求时为空 → 请求头不带
	return res.Token, nil
}

// sentinelEnv 把 core.Identity.SentinelEnv 适配为 sentinel.EnvPayload（字段一一对应）。
// DeviceID 必须 == oai-did cookie（三处同源红线）；Flow 转 sentinel.Flow 类型。
func (f *Flow) sentinelEnv(deviceID, flow string) sentinel.EnvPayload {
	e := f.boot.Identity.SentinelEnv(deviceID, flow)
	return sentinel.EnvPayload{
		DeviceID:            e.DeviceID,
		UserAgent:           e.UserAgent,
		ScreenWidth:         e.ScreenWidth,
		ScreenHeight:        e.ScreenHeight,
		Language:            e.Language,
		Languages:           e.Languages,
		Platform:            e.Platform,
		Vendor:              e.Vendor,
		HardwareConcurrency: e.HardwareConcurrency,
		DeviceMemory:        e.DeviceMemory,
		BrowserType:         e.BrowserType,
		DevicePixelRatio:    e.DevicePixelRatio,
		MaxTouchPoints:      e.MaxTouchPoints,
		Timezone:            e.Timezone,
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// Step 6: authorize_continue + signup（对齐 Python authorize_continue / signup）
// ═══════════════════════════════════════════════════════════════════════════
// authorizeContinue 调 /api/accounts/authorize/continue，返回 page.type 等。
// sentinel/so token 走 OpenAI-sentinel-token / -so-token 头；429 频控原地退避重试。
func (f *Flow) authorizeContinue(ctx context.Context, email, sentinelToken, screenHint, referer string) (map[string]any, error) {
	payload := map[string]any{"username": map[string]string{"value": email, "kind": "email"}}
	if screenHint != "" { // 登录探测那拍不带 screen_hint（对齐实测）
		payload["screen_hint"] = screenHint
	}
	body, _ := json.Marshal(payload)
	var lastBody string
	var status int
	for attempt := 0; attempt < 3; attempt++ {
		r, err := f.boot.Session.PostWithHeaders(ctx,
			"https://auth.openai.com/api/accounts/authorize/continue",
			referer, "application/json", body,
			sentinelHeaders(f.deviceID, sentinelToken, f.lastSentinelSoToken))
		if err != nil {
			return nil, fmt.Errorf("authorize_continue: %w", err)
		}
		status = r.StatusCode
		lastBody = r.Text()
		if status == 429 && attempt < 2 { // 频控：原地等 5-10s 重试（对齐实测）
			sleepCtx(ctx, time.Duration(5*(attempt+1))*time.Second)
			continue
		}
		break
	}
	if status != 200 {
		return nil, fmt.Errorf("authorize_continue(screen_hint=%q): HTTP %d body=%s", screenHint, status, truncate(lastBody, 200))
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(lastBody), &data); err != nil {
		return map[string]any{}, nil
	}
	return data, nil
}

// signup 提交注册邮箱，先探测（不带 screen_hint）再正式 signup，返回 (isNew, error)。
// 三分支：create_account_password=新账号；email_otp_verification+passwordless_signup=新账号无密码；其它=已有账号。
func (f *Flow) signup(ctx context.Context, email, sentinelToken string) (bool, error) {
	// ① 登录探测（不带 screen_hint，referer=log-in）——缺这拍后面 user/register 必 400。
	//    探测结果只影响服务端状态，失败不阻断主流程。
	if _, err := f.authorizeContinue(ctx, email, sentinelToken, "", "https://auth.openai.com/log-in"); err != nil {
		// 探测失败仅记录，不影响后续 signup（对齐 Python）。
		_ = err
	}
	// ② 正式 signup。
	data, err := f.authorizeContinue(ctx, email, sentinelToken, "signup", "https://auth.openai.com/create-account")
	if err != nil {
		return false, err
	}
	pageType, _ := nestedStr(data, "page", "type")
	continueURL, _ := data["continue_url"].(string)
	switch {
	case pageType == "create_account_password" || strings.Contains(continueURL, "/create-account/"):
		f.isExistingAccount = false
		f.existingPageType = pageType
		f.isExistingAccountSet = true
		return true, nil
	case pageType == "email_otp_verification":
		mode, _ := nestedStr(data, "page", "payload", "email_verification_mode")
		f.existingVerificationMode = mode
		f.existingPageType = pageType
		// passwordless_signup 也是新账号（只是没走设密码分支）。
		f.isExistingAccount = mode != "passwordless_signup"
		f.isExistingAccountSet = true
		return f.existingVerificationMode != "passwordless_signup" && false, nil // 见下：passwordless 返回 isNew=false 但非已有账号
	default:
		// 其它（login_password 等）→ 已有账号。
		f.isExistingAccount = true
		f.existingPageType = pageType
		f.isExistingAccountSet = true
		return false, nil
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// Step 7: register_password（对齐 Python register_password）
// ═══════════════════════════════════════════════════════════════════════════
// 刷新 sentinel（flow=username_password_create，此 flow 服务端【不下发 so 块】→ 不带 SO 头）。
// 200 才落盘密码（POST 没成功 = OpenAI 侧没生效）；passwordless 默认跳过（必 400）。
func (f *Flow) registerPassword(ctx context.Context, email, password string) (bool, error) {
	// 用本 flow 的 sentinel token（username_password_create 无 SO 块，故 soTokenForRequest 为空）。
	token, err := f.getSentinelToken(ctx, f.deviceID, "username_password_create")
	if err != nil {
		// 对齐 Python：刷新失败降级用上一步 token（flow 不匹配是风控特征，但比崩掉强）。
		token = f.lastSentinelToken
	}
	payload, _ := json.Marshal(map[string]string{"password": password, "username": email})
	resp, err := f.boot.Session.PostWithHeaders(ctx,
		"https://auth.openai.com/api/accounts/user/register",
		"https://auth.openai.com/create-account/passwordd",
		"application/json", payload,
		sentinelHeaders(f.deviceID, token, "")) // 此 flow 无 SO → 不传 SO 头
	if err != nil {
		return false, fmt.Errorf("user/register: %w", err)
	}
	if resp.StatusCode != 200 {
		// account_creation_failed：服务端已选 passwordless 或 state 不允许设密码（流程不匹配，非账号/代理问题）。
		f.result.Password = "" // 清掉内存里的死密码，避免误导
		return false, nil
	}
	// 200 = 账号连同密码已建好。立刻回调落盘（密码只活内存，进程退就没了）。
	f.result.Password = password
	if f.onPassword != nil {
		f.onPassword(email, password)
	}
	return true, nil
}

// ═══════════════════════════════════════════════════════════════════════════
// Step 7b: send_otp（对齐 Python send_otp）—— 整页导航形态
// ═══════════════════════════════════════════════════════════════════════════
// GET /api/accounts/email-otp/send 是整页导航（accept=text/html、不带 sentinel 头、
// allow_redirects=False）。302→email-verification=成功；302→/error/invalid_auth_step=拒绝。
func (f *Flow) sendOTP(ctx context.Context, referer string) error {
	resp, err := f.boot.Session.GetNavigationFollowRedirect(ctx,
		"https://auth.openai.com/api/accounts/email-otp/send", "same-origin")
	if err != nil {
		return fmt.Errorf("send_otp: %w", err)
	}
	loc := headerGet(resp.Headers, "Location")
	if resp.StatusCode == 200 || resp.StatusCode == 302 {
		if strings.Contains(loc, "/error") || strings.Contains(loc, "invalid_auth_step") {
			return fmt.Errorf("send_otp: 服务端拒绝发码 invalid_auth_step: %s", truncate(loc, 180))
		}
		return nil // 302 → email-verification = 真发码成功
	}
	return fmt.Errorf("send_otp: HTTP %d body=%s", resp.StatusCode, truncate(resp.Text(), 200))
}

// ═══════════════════════════════════════════════════════════════════════════
// Step 7c: verify_otp（对齐 Python verify_otp）
// ═══════════════════════════════════════════════════════════════════════════
func (f *Flow) verifyOTP(ctx context.Context, code string) (map[string]any, error) {
	// 人类模拟(对齐 codex verify_otp 的 human_action_pause("otp_input")):
	// 收到验证码后停顿 2.5~8s(模拟切回页面+看清 6 位数字再输入)。否则秒取秒提交
	// 的时序特征被风控识别为脚本,高并发下 OTP 被服务端作废 → wrong_email_otp_code。
	humanize.ActionPause(nil, "otp_input")
	payload, _ := json.Marshal(map[string]string{"code": code})
	resp, err := f.boot.Session.Post(ctx,
		"https://auth.openai.com/api/accounts/email-otp/validate",
		"https://auth.openai.com/email-verification",
		"application/json", payload)
	if err != nil {
		return nil, fmt.Errorf("verify_otp: %w", err)
	}
	if resp.StatusCode != 200 {
		// 仅记录状态码与错误码(不打印真实验证码,防泄露);诊断期已据此定位根因。
		return nil, fmt.Errorf("verify_otp: HTTP %d body=%s", resp.StatusCode, truncate(resp.Text(), 260))
	}
	var data map[string]any
	if err := json.Unmarshal(resp.Bytes(), &data); err != nil {
		return map[string]any{}, nil
	}
	return data, nil
}

// ═══════════════════════════════════════════════════════════════════════════
// Step 8: create_account（对齐 Python create_account）
// ═══════════════════════════════════════════════════════════════════════════
// 刷新 sentinel（flow=oauth_create_account），POST {name, birthdate}，200 → _account_created。
func (f *Flow) createAccount(ctx context.Context) (string, error) {
	token, err := f.getSentinelToken(ctx, f.deviceID, "oauth_create_account")
	if err != nil {
		token = f.lastSentinelToken // 降级（对齐 Python）
	}
	name := randomFullName()
	birthdate := randomBirthdate()
	payload, _ := json.Marshal(map[string]string{"name": name, "birthdate": birthdate})
	resp, err := f.boot.Session.PostWithHeaders(ctx,
		"https://auth.openai.com/api/accounts/create_account",
		"https://auth.openai.com/about-you",
		"application/json", payload,
		sentinelHeaders(f.deviceID, token, f.lastSentinelSoToken))
	if err != nil {
		return "", fmt.Errorf("create_account: %w", err)
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("create_account: HTTP %d body=%s", resp.StatusCode, truncate(resp.Text(), 260))
	}
	var data struct {
		ContinueURL string `json:"continue_url"`
	}
	_ = json.Unmarshal(resp.Bytes(), &data)
	f.accountCreated = true
	return data.ContinueURL, nil
}

// ═══════════════════════════════════════════════════════════════════════════
// Step 9: follow_redirect_chain（对齐 Python follow_redirect_chain）
// ═══════════════════════════════════════════════════════════════════════════
// 逐跳整页导航（去 sec-fetch-user，按同站/跨站调 sec-fetch-site），捕获 callback URL 不消费 code。
// 返回 (callbackURL, finalURL)。
func (f *Flow) followRedirectChain(ctx context.Context, startURL string) (string, string) {
	current := startURL
	callbackURL := ""
	referer := "https://auth.openai.com/"
	for i := 0; i < 12; i++ {
		site := "cross-site"
		if originOf(current) == originOf(referer) {
			site = "same-origin"
		}
		resp, err := f.boot.Session.GetNavigationFollowRedirect(ctx, current, site)
		if err != nil {
			break
		}
		referer = current
		if strings.Contains(current, "/api/auth/callback/openai") {
			callbackURL = current
		}
		loc := headerGet(resp.Headers, "Location")
		if resp.StatusCode >= 300 && resp.StatusCode < 400 && loc != "" {
			if strings.HasPrefix(loc, "/") {
				loc = originOf(current) + loc
			}
			// 捕获 callback 但不主动 GET（留给后面消费 code）。
			if strings.Contains(loc, "/api/auth/callback/openai") && strings.Contains(loc, "code=") {
				callbackURL = loc
				current = loc
				break
			}
			current = loc
			continue
		}
		break
	}
	return callbackURL, current
}

// ═══════════════════════════════════════════════════════════════════════════
// Step 10: get_auth_session（对齐 Python get_auth_session）
// ═══════════════════════════════════════════════════════════════════════════
// GET chatgpt.com/api/auth/session → session_token(cookie 优先/JSON) + access_token(JSON)。
func (f *Flow) getAuthSession(ctx context.Context) error {
	resp, err := f.boot.Session.Get(ctx, "https://chatgpt.com/api/auth/session", "https://chatgpt.com/")
	if err != nil {
		return fmt.Errorf("auth_session: %w", err)
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("auth_session: HTTP %d", resp.StatusCode)
	}
	var body struct {
		SessionToken string `json:"sessionToken"`
		AccessToken  string `json:"accessToken"`
	}
	_ = json.Unmarshal(resp.Bytes(), &body)
	st := f.boot.Session.CookieValue("__Secure-next-auth.session-token")
	if st == "" {
		st = body.SessionToken
	}
	if st != "" {
		f.result.SessionToken = st
	}
	if body.AccessToken != "" {
		f.result.AccessToken = body.AccessToken
	}
	// 抓 Cookie 头(含 oai-did 设备指纹 + session-token),供 token-heal 回灌三路自洽。
	// 拼成 "name=value; name2=value2" 形式;注册成功的最终会话指纹。
	if cookies := f.boot.Session.GetCookies(); len(cookies) > 0 {
		parts := make([]string, 0, len(cookies))
		for _, c := range cookies {
			if c.Name == "" {
				continue
			}
			parts = append(parts, c.Name+"="+c.Value)
		}
		f.result.CookieHeader = strings.Join(parts, "; ")
	}
	return nil
}

// consumeCallbackForSession 主动 GET callback 让 NextAuth 种 session-token cookie
// （对齐 Python _consume_callback_for_session）。必须在 oauth_token_exchange 之前做。
func (f *Flow) consumeCallbackForSession(ctx context.Context, callbackURL string) bool {
	if callbackURL == "" || !strings.Contains(callbackURL, "code=") {
		return false
	}
	current := callbackURL
	for hop := 0; hop < 8; hop++ {
		site := "cross-site"
		if originOf(current) == "https://chatgpt.com" {
			site = "same-origin"
		}
		resp, err := f.boot.Session.GetNavigationFollowRedirect(ctx, current, site)
		if err != nil {
			break
		}
		loc := headerGet(resp.Headers, "Location")
		if resp.StatusCode < 300 || resp.StatusCode >= 400 || loc == "" {
			break
		}
		if strings.HasPrefix(loc, "/") {
			loc = originOf(current) + loc
		}
		current = loc
		if strings.Contains(current, "chatgpt.com") && !strings.Contains(current, "/api/auth/callback") {
			break
		}
	}
	return f.boot.Session.CookieValue("__Secure-next-auth.session-token") != ""
}
