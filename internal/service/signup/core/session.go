package core

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"gpt-go/internal/service/signup/humanize"

	"github.com/sardanioss/httpcloak"
)

// Session 是「承载同一 Identity」的 HTTP 会话，封装 httpcloak。
//
// 职责边界（三路中的 ① 与 ② 在这里落地，③ 通过 Identity 暴露给沙箱）：
//   - ① TLS/H2/H3：由 httpcloak preset 保证（本包只传 preset 名，不参与指纹细节）。
//   - ② HTTP 头：本包在每次请求时按 Identity 注入 UA / sec-ch-ua* / Accept-Language，
//     与 httpcloak 自动生成的预设头保持一致（同源 Identity，故不会自相矛盾）。
//   - ③ JS 层：本包不发 JS，仅把 Identity 原样交给上层（authflow → sentinel 沙箱）。
//
// 与 Python _TlsRetrySession 的对齐：
//   - TLS 握手瞬断（链路级，5.4% 偶发）做【原 session 重试】，绝不重建 session
//     （重建会丢 warmup 种的 oai-did/csrf，导致 409 invalid_state）。
//   - 仅 TLS 握手类错误重试；HTTP 错误码/超时/业务异常原样上抛（避免把服务端明确拒绝也变成重试）。
//
// 线程安全：httpcloak.Session 非并发安全，故遵循「一次注册一个 session」，
// 与 codex-auto 一致；批量并发靠「多号各自持有 session」，不共享。
type Session struct {
	inner    *httpcloak.Session
	identity *Identity
	retries  int
	backoff  time.Duration
}

// NewSession 用给定 Identity 创建会话。
//
//	proxy:   出口代理 URL（socks5:// 会标准化为 socks5h://，DNS 走代理端，减少 TLS 握手异常）。
//	timeout:  单次请求超时。
//
// identity.PresetName 必须是 presetPool 内的合法名（httpcloak 对未知 preset 会在首次请求报错，
// 这里提前校验，把「拼错一个字符换回旧指纹」的事故挡在构造期）。
func NewSession(identity *Identity, proxy string, timeout time.Duration) (*Session, error) {
	if identity == nil {
		return nil, fmt.Errorf("core: identity 不能为空")
	}
	if lookupPreset(identity.PresetName) == nil {
		return nil, fmt.Errorf("core: 未知 httpcloak preset %q（不在受控池内）", identity.PresetName)
	}
	if timeout <= 0 {
		timeout = 45 * time.Second // 对齐 Python warmup 富余超时（实测成功轮 3.4~10.9s，15s 卡边缘）
	}

	opts := []httpcloak.SessionOption{
		httpcloak.WithSessionTimeout(timeout),
		httpcloak.WithRedirects(true, 10),
		httpcloak.WithInsecureSkipVerify(), // 对齐 curl_cffi 默认不校验证书
	}
	if normalized := normalizeProxyScheme(proxy); normalized != "" {
		opts = append(opts, httpcloak.WithSessionProxy(normalized))
	}

	inner := httpcloak.NewSession(identity.PresetName, opts...)
	return &Session{
		inner:    inner,
		identity: identity,
		retries:  2,                       // 对齐 Python retries=2
		backoff:  1500 * time.Millisecond, // 对齐 Python backoff=1.5s
	}, nil
}

// Identity 返回本会话绑定的身份（三路自洽的唯一事实来源）。
func (s *Session) Identity() *Identity { return s.identity }

// Close 释放底层资源。
func (s *Session) Close() {
	if s.inner != nil {
		s.inner.Close()
	}
}

// normalizeProxyScheme 把 socks5:// 标准化为 socks5h://（DNS 走代理端解析）。
// 对齐 Python create_http_session 的 socks5://→socks5h:// 归一化逻辑。
func normalizeProxyScheme(proxy string) string {
	proxy = strings.TrimSpace(proxy)
	if proxy == "" {
		return ""
	}
	if strings.HasPrefix(proxy, "socks5://") {
		return "socks5h://" + proxy[len("socks5://"):]
	}
	return proxy
}

// ---------------------------------------------------------------------------
// 请求构造（② HTTP 头注入）
// ---------------------------------------------------------------------------

// commonHeaders 构造 XHR/fetch 类请求头（对齐 Python _common_headers）。
// sec-ch-ua* 全从 Identity 取：Chromium 发全套，Safari/Firefox 一个都不发（真实浏览器行为）。
// referer 决定 Origin（必须同源，否则 auth.openai.com 易触发 invalid_state）。
func (s *Session) commonHeaders(referer string) map[string][]string {
	id := s.identity
	origin := originOf(referer, "https://chatgpt.com")

	h := map[string][]string{
		"Accept":          {"application/json"},
		"Referer":         {referer},
		"Origin":          {origin},
		"User-Agent":      {id.UserAgent},
		"Accept-Language": {id.AcceptLang},
		"Accept-Encoding": {"gzip, deflate, br, zstd"},
		"Sec-Fetch-Dest":  {"empty"},
		"Sec-Fetch-Mode":  {"cors"},
		"Sec-Fetch-Site":  {"same-origin"},
		"priority":        {"u=1, i"},
	}
	s.injectClientHints(h)
	return h
}

// navigationHeaders 构造整页导航类请求头（对齐 Python _navigation_headers）。
// 与 commonHeaders 区别仅在 Sec-Fetch-* 那组（document/navigate/none + user + UIR）。
// Client Hints 两边必须一致（都从 Identity 取）。
func (s *Session) navigationHeaders() map[string][]string {
	id := s.identity
	h := map[string][]string{
		"Accept": {"text/html,application/xhtml+xml,application/xml;q=0.9," +
			"image/avif,image/webp,image/apng,*/*;q=0.8"},
		"Accept-Language":           {id.AcceptLang},
		"Accept-Encoding":           {"gzip, deflate, br, zstd"},
		"Sec-Fetch-Dest":            {"document"},
		"Sec-Fetch-Mode":            {"navigate"},
		"Sec-Fetch-Site":            {"none"},
		"Sec-Fetch-User":            {"?1"},
		"Upgrade-Insecure-Requests": {"1"},
		"priority":                  {"u=0, i"},
		"User-Agent":                {id.UserAgent},
	}
	s.injectClientHints(h)
	return h
}

// injectClientHints 按家族注入 Client Hints（仅 Chromium 发，其余一个不发）。
// 这是「UA 说 Chrome、sec-ch-ua 也得说 Chrome」的关键对齐点。
func (s *Session) injectClientHints(h map[string][]string) {
	id := s.identity
	if id.SecChUA == "" {
		return // Safari/Firefox：真实浏览器不发 Client Hints
	}
	h["sec-ch-ua"] = []string{id.SecChUA}
	if id.SecChUAMobile != "" {
		h["sec-ch-ua-mobile"] = []string{id.SecChUAMobile}
	} else {
		h["sec-ch-ua-mobile"] = []string{"?0"}
	}
	if id.SecChUAPlatform != "" {
		h["sec-ch-ua-platform"] = []string{id.SecChUAPlatform}
	}
	if id.SecChUAFullVersionList != "" {
		h["sec-ch-ua-full-version-list"] = []string{id.SecChUAFullVersionList}
	}
	if id.SecChUAArch != "" {
		h["sec-ch-ua-arch"] = []string{id.SecChUAArch}
	}
	if id.SecChUABitness != "" {
		h["sec-ch-ua-bitness"] = []string{id.SecChUABitness}
	}
	if id.SecChUAModel != "" {
		h["sec-ch-ua-model"] = []string{id.SecChUAModel}
	}
	if id.SecChUAPlatformVersion != "" {
		h["sec-ch-ua-platform-version"] = []string{id.SecChUAPlatformVersion}
	}
}

// originOf 从 referer 提取 scheme://host 作为 Origin；失败回退 def。
func originOf(referer, def string) string {
	ref := strings.TrimSpace(referer)
	if ref == "" {
		return def
	}
	// 轻量解析：找 "://" 与其后的第一个 "/"。
	i := strings.Index(ref, "://")
	if i < 0 {
		return def
	}
	rest := ref[i+3:]
	if j := strings.IndexByte(rest, '/'); j >= 0 {
		rest = rest[:j]
	}
	if rest == "" {
		return def
	}
	return ref[:i+3] + rest
}

// ---------------------------------------------------------------------------
// 请求方法（带 TLS 瞬断原 session 重试）
// ---------------------------------------------------------------------------

// Response 是一次响应的最小视图（屏蔽 httpcloak 类型，便于上层稳定依赖）。
type Response struct {
	StatusCode int
	Headers    map[string][]string
	FinalURL   string
	Protocol   string // "h1" / "h2" / "h3"
	body       []byte
}

// NewResponse 构造一个带响应体的 Response（测试替身/适配器用）。
// body 是 unexported，跨包 mock（如 plancheck 的 session 替身）需要这个导出构造器。
func NewResponse(statusCode int, body []byte) *Response {
	return &Response{StatusCode: statusCode, body: body}
}

// Text 返回响应体字符串。
func (r *Response) Text() string { return string(r.body) }

// Bytes 返回响应体字节。
func (r *Response) Bytes() []byte { return r.body }

// GetHeader 取首个响应头值（大小写不敏感）。
func (r *Response) GetHeader(key string) string {
	for k, vs := range r.Headers {
		if strings.EqualFold(k, key) && len(vs) > 0 {
			return vs[0]
		}
	}
	return ""
}

// Get 发起 GET（XHR 语义）。
// GetWithHeaders 发起 GET，并在公共头之上合并 extra 额外头（与 PostWithHeaders
// 同机制）：支付检测的 OAICS 状态接口需要 Authorization Bearer 头。
// extra 的键值覆盖同名公共头；空值头不带。
func (s *Session) GetWithHeaders(ctx context.Context, url, referer string, extra map[string]string) (*Response, error) {
	h := s.commonHeaders(referer)
	for k, v := range extra {
		if v == "" {
			continue
		}
		h[k] = []string{v}
	}
	return s.do(ctx, "GET", url, "", nil, h)
}

func (s *Session) Get(ctx context.Context, url, referer string) (*Response, error) {
	return s.do(ctx, "GET", url, "", nil, s.commonHeaders(referer))
}

// GetNavigation 发起整页导航 GET（用于 warmup 种 cookie，对齐 Python warmup 的导航头）。
func (s *Session) GetNavigation(ctx context.Context, url string) (*Response, error) {
	return s.do(ctx, "GET", url, "", nil, s.navigationHeaders())
}

// GetNavigationFollowRedirect 发起「重定向链中」的导航 GET。
//
// 【红线 / codex-auto auth_flow.py:2097-2099】302 自动跟随不是用户手动点击，真浏览器
// 此时【不发 sec-fetch-user】，且 sec-fetch-site 按同源/跨站另算。若在重定向链里复用
// GetNavigation（它始终带 Sec-Fetch-User: ?1），就会失真被风控抓。
//
// 用法：warmup 的首个直达用 GetNavigation；后续 302 跳转链上的导航一律用本方法。
// site 传 "same-origin"（同域跳）或 "cross-site"（跨域跳，如 chatgpt.com→auth.openai.com）。
func (s *Session) GetNavigationFollowRedirect(ctx context.Context, url, site string) (*Response, error) {
	h := s.navigationHeaders()
	delete(h, "Sec-Fetch-User") // 自动跟随非用户点击，真浏览器不发此头
	if site != "" {
		h["Sec-Fetch-Site"] = []string{site}
	}
	return s.do(ctx, "GET", url, "", nil, h)
}

// Post 发起 POST（XHR 语义）。contentType 如 "application/json" / "text/plain;charset=UTF-8"。
func (s *Session) Post(ctx context.Context, url, referer, contentType string, body []byte) (*Response, error) {
	return s.PostWithHeaders(ctx, url, referer, contentType, body, nil)
}

// PostWithHeaders 发起 POST，并在公共头之上合并 extra 额外头。
//
// 用途（authflow 的硬需求）：
//   - `OpenAI-sentinel-token` / `-so-token`：sentinel PoW token（so 为空时不带）；
//   - `oai-device-id`：auth.openai.com 域请求必带，值必须 == oai-did cookie（三处同源）。
//
// extra 的键值会覆盖同名公共头；传 nil 等价于 Post。
func (s *Session) PostWithHeaders(ctx context.Context, url, referer, contentType string, body []byte, extra map[string]string) (*Response, error) {
	h := s.commonHeaders(referer)
	if contentType != "" {
		h["Content-Type"] = []string{contentType}
	}
	for k, v := range extra {
		if v == "" {
			continue // 空值头不带（对齐「服务端没要求就不带 SO 头」红线）
		}
		h[k] = []string{v}
	}
	return s.do(ctx, "POST", url, contentType, body, h)
}

// Do 是底层执行器：构造 httpcloak.Request，注入头，带 TLS 瞬断重试。
func (s *Session) do(ctx context.Context, method, url, contentType string, body []byte, headers map[string][]string) (*Response, error) {
	var lastErr error
	for attempt := 0; attempt <= s.retries; attempt++ {
		if attempt > 0 {
			// TLS 瞬断重试退避（原 session，不重建）。
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(s.backoff):
			}
		}
		// 人类节奏(对齐 codex humanize.human_pause):每个请求前按 URL 插入对数正态
		// 随机延迟。协议连发(零间隔)的时序特征会被风控识别为脚本——高并发下尤为致命。
		humanize.Pause(nil, url)

		req := &httpcloak.Request{
			Method:  method,
			URL:     url,
			Headers: cloneHeaders(headers),
		}
		if body != nil {
			req.Body = io.NopCloser(strings.NewReader(string(body)))
		}

		resp, err := s.inner.Do(ctx, req)
		if err != nil {
			lastErr = err
			// 仅 TLS 握手类错误才重试；其它错误直接返回。
			if !isTLSHandshakeErr(err) || ctx.Err() != nil {
				return nil, err
			}
			continue
		}
		if resp == nil {
			return nil, fmt.Errorf("core: 空响应且无错误（%s %s）", method, url)
		}

		out, convErr := s.convertResponse(resp)
		if convErr != nil {
			return nil, convErr
		}
		return out, nil
	}
	return nil, fmt.Errorf("core: TLS 瞬断重试 %d 次仍失败: %w", s.retries, lastErr)
}

// convertResponse 把 httpcloak.Response 读成内存态 Response（读完即关 Body，不泄漏）。
func (s *Session) convertResponse(resp *httpcloak.Response) (*Response, error) {
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("core: 读取响应体失败: %w", err)
	}
	return &Response{
		StatusCode: resp.StatusCode,
		Headers:    resp.Headers,
		FinalURL:   resp.FinalURL,
		Protocol:   resp.Protocol,
		body:       data,
	}, nil
}

func cloneHeaders(src map[string][]string) map[string][]string {
	dst := make(map[string][]string, len(src))
	for k, vs := range src {
		dst[k] = append([]string(nil), vs...)
	}
	return dst
}

// isTLSHandshakeErr 判断是否 TLS 握手类错误（对齐 Python _is_tls_handshake_error）。
// 只兜链路级瞬断；HTTP 错误码、超时、业务异常不在此列。
func isTLSHandshakeErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, k := range []string{
		"tls", "ssl", "handshake", "certificate",
		"eof", "connection reset", "broken pipe",
		"first record does not look like a tls",
	} {
		if strings.Contains(msg, k) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Cookie 访问（authflow 需要读 oai-did / csrf 等）
// ---------------------------------------------------------------------------

// CookieInfo 是一个 cookie 的最小视图。
type CookieInfo struct {
	Name   string
	Value  string
	Domain string
	Path   string
}

// GetCookies 返回当前会话 cookie 快照。
func (s *Session) GetCookies() []CookieInfo {
	states := s.inner.GetCookies()
	out := make([]CookieInfo, 0, len(states))
	for _, c := range states {
		out = append(out, CookieInfo{
			Name:   c.Name,
			Value:  c.Value,
			Domain: c.Domain,
			Path:   c.Path,
		})
	}
	return out
}

// CookieValue 按名取 cookie 值；不存在返回空串。
func (s *Session) CookieValue(name string) string {
	for _, c := range s.inner.GetCookies() {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

// SetCookie 手动种 cookie（authflow 在特定步骤需要）。
func (s *Session) SetCookie(name, value, domain, path string) {
	s.inner.SetCookie(httpcloak.CookieInfo{
		Name:   name,
		Value:  value,
		Domain: domain,
		Path:   path,
	})
}
