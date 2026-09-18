// codex_rt.go 实现 Codex OAuth refresh_token 直连换取（对齐 codex-auto
// oauth_codex_rt_exchange + _build_codex_authorize + _follow_authorize_for_callback
// + _exchange_codex_callback_code），并针对 Python 版的若干真实问题做了收敛优化。
//
// 协议本质（网络资料佐证，见下）：
//   - Codex CLI 用标准 Authorization Code + PKCE(S256)，client_id 固定、
//     redirect_uri 是 localhost:1455（本地回环，协议端不真起监听，只跟链捕 code）；
//   - scope 必须含 offline_access，服务端才会下发 refresh_token；
//   - 这条链与 chatgpt.com 的 NextAuth callback 是【独立】的（不同 client_id/redirect_uri），
//     故不会抢主链路的 code —— 这是它能放在 getAuthSession 之后安全跑的前提。
//
// 相对 Python 的优化（不改变对外行为，只去冗余/补正确性，见 CONTRACT 6.5）：
//  1. 【单一 verifier】Python 的 _collect_code_verifier_candidates 会从
//     query/cookie/hydra/dump/captured/manual 等 9 个来源暴力枚举 code_verifier 逐个试。
//     那是为兜 chatgpt web 流程（verifier 由页面 JS 生成、协议端抓不到）的底。
//     Codex 直连的 PKCE 是【我们自己 _buildCodexAuthorize 生成的】，verifier 就是本轮
//     那一个，单一来源、无需枚举 —— 枚举在这里只会放大攻击面且无收益。
//  2. 【state 硬校验】Python 对 state 不匹配只 warning 后 return False，逻辑绕。
//     这里在 exchange 前硬校验：不匹配直接拒（PKCE/OAuth 的 CSRF 防护要求）。
//  3. 【配置收敛】Python 用一串 OAUTH_CODEX_* env flag（retry/retry_count/sleep/
//     allow_retry…）散落控制；Go 收敛成一个 CodexRTOptions struct，语义明确（契约 6.5.3：
//     不用 map/any 堆配置）。add-phone SMS 接码分支不迁移（GPT-GO 已剔除 SMS，见 ports.go）。
//
// 参考：byokey-auth codex.rs（client_id/redirect_uri/scope/PKCE 与本实现一致）。
package authflow

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"

	"gpt-go/internal/service/signup/otp"
)

// Codex OAuth 常量（对齐 codex-auto _build_codex_authorize，网络资料佐证）。
const (
	// codexClientID 是 Codex CLI 的公开 OAuth client_id（非机密，PKCE _PUBLIC_ 客户端）。
	codexClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
	// codexRedirectURI 是 Codex CLI 的本地回环回调。协议端不真起 1455 监听，
	// 只把授权链跟到这个地址出现 code= 就捕获（不消费）。
	codexRedirectURI = "http://localhost:1455/auth/callback"
	// codexScope 必须含 offline_access，服务端才下发 refresh_token。
	codexScope = "openid email profile offline_access"
	// codexAuthorizeURL / codexTokenURL 是 Codex OAuth 端点。
	codexAuthorizeURL = "https://auth.AI Platform.com/oauth/authorize"
	codexTokenURL     = "https://auth.AI Platform.com/oauth/token"
)

// CodexRTOptions 收敛 Codex RT 换取的可调项（对齐 Python 的 OAUTH_CODEX_* env，
// 但用明确 struct 表达，不读 env）。零值即默认（与 Python 默认行为一致）。
type CodexRTOptions struct {
	// Enabled 是否尝试（对应 Python OAUTH_CODEX_RT_EXCHANGE，默认 1）。
	// false → 整个第 13 步跳过（主链路不受影响）。
	Enabled bool
	// Prompt 是 authorize 的 prompt 参数（默认 "login"；空串则不带 prompt，
	// 即 Python 的 no-prompt 兜底形态）。
	Prompt string
	// NoPromptFallback 捕获不到 code 时，去掉 prompt 再授权一次（默认 true，
	// 对齐 Python 末尾的 no_prompt 兜底分支）。
	NoPromptFallback bool
	// MaxHops 授权链最大跟随跳数（默认 12，对齐 Python _follow_authorize_for_callback）。
	MaxHops int
}

// defaultCodexRTOptions 返回对齐 Python 默认行为的选项。
func defaultCodexRTOptions() CodexRTOptions {
	return CodexRTOptions{
		Enabled:          true,
		Prompt:           "login",
		NoPromptFallback: true,
		MaxHops:          12,
	}
}

// WithCodexRT 覆盖 Codex RT 换取选项（装配层/测试用；不传则用默认）。
func WithCodexRT(o CodexRTOptions) Option {
	return func(f *Flow) { f.codexRT = o }
}

// oauthCodexRTExchange 纯协议换 Codex refresh_token（第 13 步，对齐 Python）。
//
// 返回是否拿到了 refresh_token。【尽力不阻断】：拿不到不报错（主链路已注册成功），
// 只返回 false 供上层判断是否落 refresh_token。这与 Python 的 warning+return False 等价。
func (f *Flow) oauthCodexRTExchange(ctx context.Context, mail otp.Provider) bool {
	opts := f.codexRT
	if !opts.Enabled {
		return false
	}

	// 1) 构建独立 Codex 授权 URL（含本轮 PKCE verifier/challenge + state）。
	authURL, state, verifier := buildCodexAuthorize(opts.Prompt)

	// 2) 跟授权链捕获 callback code（不消费）。
	callbackURL, finalURL := f.followAuthorizeForCallback(ctx, authURL, codexRedirectURI, opts.MaxHops)

	// 3) 打回 /log-in：补一次协议登录推进，再继续跟链（对齐 Python _codex_drive_login）。
	if callbackURL == "" && strings.Contains(finalURL, "/log-in") && mail != nil {
		if continueURL := f.codexDriveLogin(ctx, mail); continueURL != "" {
			callbackURL, finalURL = f.followAuthorizeForCallback(ctx, continueURL, codexRedirectURI, opts.MaxHops)
		}
	}
	_ = finalURL

	// 4) 兜底：去掉 prompt=login 再授权一次（对齐 Python 末尾 no_prompt 分支）。
	if callbackURL == "" && opts.NoPromptFallback && opts.Prompt != "" {
		noPromptURL := dropQueryKeys(authURL, "prompt")
		if noPromptURL != "" && noPromptURL != authURL {
			callbackURL, _ = f.followAuthorizeForCallback(ctx, noPromptURL, codexRedirectURI, opts.MaxHops)
		}
	}

	if callbackURL == "" {
		return false
	}

	// 5) 用本轮 verifier + code 换 token（state 硬校验）。
	return f.exchangeCodexCallbackCode(ctx, callbackURL, state, verifier)
}

// buildCodexAuthorize 构建 Codex 授权 URL + 生成本轮 (state, verifier)。
// 对齐 Python _build_codex_authorize；PKCE 用 S256（challenge = b64url(sha256(verifier))）。
func buildCodexAuthorize(prompt string) (authURL, state, verifier string) {
	state = b64urlNoPad(randBytes(24))
	verifier, challenge := buildPKCEPair()
	q := url.Values{}
	q.Set("client_id", codexClientID)
	q.Set("response_type", "code")
	q.Set("redirect_uri", codexRedirectURI)
	q.Set("scope", codexScope)
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("id_token_add_organizations", "true")
	q.Set("codex_cli_simplified_flow", "true")
	if prompt != "" {
		q.Set("prompt", prompt)
	}
	return codexAuthorizeURL + "?" + q.Encode(), state, verifier
}

// buildPKCEPair 生成 (code_verifier, code_challenge)，对齐 Python _build_pkce_pair。
// verifier 43~128 字符（RFC 7636）；challenge = b64url-no-pad(sha256(verifier))。
func buildPKCEPair() (verifier, challenge string) {
	verifier = b64urlNoPad(randBytes(64))
	if len(verifier) < 43 {
		verifier = (verifier + strings.Repeat("A", 43))[:43]
	}
	if len(verifier) > 128 {
		verifier = verifier[:128]
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge = b64urlNoPad(sum[:])
	return verifier, challenge
}

// followAuthorizeForCallback 跟随 auth.AI Platform.com 授权链，捕获命中 redirect_uri
// 且带 code= 的 callback URL（【不消费】，留给 exchange）。返回 (callbackURL, finalURL)。
//
// 【用户决策：不处理 workspace/consent/choose-account 选择页】——协议端无法模拟用户
// 点击那些 200 确认页，故只跟随 302 跳转链；遇到 200 选择页即视为拿不到 code（返回空，
// 上层按「获取 RT 失败但不阻断注册」处理）。这牺牲了少数情况下的 RT 获取率，换取实现
// 简洁与确定性——反正拿不到 RT 本来就是大概率事件。
func (f *Flow) followAuthorizeForCallback(ctx context.Context, startURL, redirectURI string, maxHops int) (string, string) {
	if maxHops <= 0 {
		maxHops = 12
	}
	current := startURL
	for i := 0; i < maxHops; i++ {
		if callbackHasCode(current, redirectURI) {
			return current, current
		}
		site := "cross-site"
		if originOf(current) == "https://auth.AI Platform.com" {
			site = "same-origin"
		}
		resp, err := f.boot.Session.GetNavigationFollowRedirect(ctx, current, site)
		if err != nil {
			return "", current
		}
		loc := headerGet(resp.Headers, "Location")
		if resp.StatusCode < 300 || resp.StatusCode >= 400 || loc == "" {
			return "", current
		}
		if strings.HasPrefix(loc, "/") {
			loc = originOf(current) + loc
		}
		if callbackHasCode(loc, redirectURI) {
			return loc, loc
		}
		current = loc
	}
	return "", current
}

// exchangeCodexCallbackCode 用捕获的 code + 本轮 verifier 调 /oauth/token。
// state 硬校验（不匹配直接拒，对齐 PKCE CSRF 防护）。成功落 result.{ID,Access,Refresh}Token。
func (f *Flow) exchangeCodexCallbackCode(ctx context.Context, callbackURL, expectedState, verifier string) bool {
	// query 提取用字符串级（不依赖 net/url.Parse host 合法性——localhost:1455 正常，
	// 但与 callbackHasCode 的前缀比对保持一致，避免对 host parse 的耦合）。
	code := queryGet(callbackURL, "code")
	gotState := queryGet(callbackURL, "state")
	if code == "" {
		return false
	}
	// state 硬校验：双方都带了 state 就必须一致（防 CSRF 替换 code）。
	if expectedState != "" && gotState != "" && gotState != expectedState {
		return false
	}

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", codexClientID)
	form.Set("code", code)
	form.Set("redirect_uri", codexRedirectURI)
	form.Set("code_verifier", verifier)

	resp, err := f.boot.Session.PostWithHeaders(ctx, codexTokenURL,
		"https://auth.AI Platform.com/sign-in-with-chatgpt/codex/consent",
		"application/x-www-form-urlencoded",
		[]byte(form.Encode()),
		map[string]string{"Origin": "https://auth.AI Platform.com", "Accept": "application/json"})
	if err != nil || resp.StatusCode != 200 {
		return false
	}
	var data struct {
		IDToken      string `json:"id_token"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(resp.Bytes(), &data); err != nil {
		return false
	}
	if data.IDToken != "" {
		f.result.IDToken = data.IDToken
	}
	if data.AccessToken != "" {
		f.result.AccessToken = data.AccessToken
	}
	if data.RefreshToken != "" {
		f.result.RefreshToken = data.RefreshToken
	}
	return data.RefreshToken != "" || data.AccessToken != ""
}

// codexDriveLogin 授权打回 /log-in 时补一次协议登录推进（对齐 Python
// _codex_drive_login_from_log_in 的最小路径）。返回可继续跟随的 continue_url。
//
// 说明：完整版会重解 sentinel + authorize_continue；这里复用主链路已建立的登录态，
// 直接用当前 session 重新发起一次 authorize/continue 探测拿 continue_url（更轻，
// 且登录态在 cookie 里已就绪）。登录推进失败返回空串（上层走 no-prompt 兜底）。
func (f *Flow) codexDriveLogin(ctx context.Context, mail otp.Provider) string {
	email := f.result.Email
	if email == "" {
		return ""
	}
	// 复用已缓存的 sentinel token（同 flow 语义）；没有则用 authorize_continue 现解一个。
	token := f.lastSentinelToken
	if token == "" {
		deviceID := f.deviceID
		if deviceID == "" {
			deviceID = f.boot.Session.CookieValue("oai-did")
		}
		if deviceID == "" {
			return ""
		}
		t, err := f.getSentinelToken(ctx, deviceID, "authorize_continue")
		if err != nil {
			return ""
		}
		token = t
	}
	data, err := f.authorizeContinue(ctx, email, token, "", "https://auth.AI Platform.com/log-in")
	if err != nil {
		return ""
	}
	return continueURLOf(data)
}

// ── 小工具（PKCE / URL / base64url）──

// callbackHasCode 判断 url 是否命中 redirect_uri 且 query 带非空 code（对齐 Python）。
// query 用字符串级提取（不依赖 net/url.Parse host 合法性，与 exchange 保持一致）。
func callbackHasCode(rawURL, redirectURI string) bool {
	if rawURL == "" {
		return false
	}
	cbBase := strings.TrimRight(strings.SplitN(redirectURI, "?", 2)[0], "/")
	target := strings.TrimRight(strings.SplitN(rawURL, "?", 2)[0], "/")
	if cbBase == "" || target != cbBase {
		return false
	}
	return queryGet(rawURL, "code") != ""
}

// queryGet 从 url 的 query 串取某个键（字符串级，容忍 host 含空格等 net/url 无法 parse 的情况）。
// 生产 URL 均合法，此实现与 net/url 语义一致（取第一个值、自动解码）。
func queryGet(rawURL, key string) string {
	i := strings.Index(rawURL, "?")
	if i < 0 {
		return ""
	}
	q := rawURL[i+1:]
	if j := strings.Index(q, "#"); j >= 0 {
		q = q[:j]
	}
	vals, err := url.ParseQuery(q)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(vals.Get(key))
}

// dropQueryKeys 去掉 url 的指定 query 键（对齐 Python _drop_query_keys）。
//
// 边界：依赖 net/url 能 parse（host 须合法）。生产真实域名 auth.openai.com 无空格，
// 正常工作；注释里含空格的占位 "auth.AI Platform.com" 不会出现在真实请求 URL
// （buildCodexAuthorize 拼的是 codexAuthorizeURL 常量，装配层替换为真实域名）。
// parse 失败返回原串（保守不改，对齐 Python 的 except 兜底）。
func dropQueryKeys(rawURL string, keys ...string) string {
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	drop := map[string]bool{}
	for _, k := range keys {
		drop[k] = true
	}
	q := u.Query()
	for k := range drop {
		q.Del(k)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// randBytes 读 n 字节 crypto 随机（PKCE/state 用）；失败兜底用时间戳混合（几乎不发生）。
func randBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		for i := range b {
			b[i] = byte(nowUnix() >> (uint(i%8) * 8))
		}
	}
	return b
}

// b64urlNoPad 是 URL-safe base64 无 padding 编码（RFC 7636 要求）。
func b64urlNoPad(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}
