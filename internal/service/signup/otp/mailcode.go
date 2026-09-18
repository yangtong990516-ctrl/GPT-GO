// mailcode.go 自建 Mailcow + mailcode-api 邮箱的 Provider 实现。
//
// 对齐 codex-auto：主项目 mailbox_client 用 accessUrl（/api/mail?email=X）
// 轮询读信取 OTP。本实现把这个能力落进 otp.Provider 接口。
//
// 数据来源：GPT-GO 的邮箱池记录里已存 AccessURL（mailcode.BuildAccessURL 生成），
// 本 provider 直接复用该 URL 轮询，不重复实现邮箱创建逻辑（创建走 mailcode.Service）。
package otp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"
)

// mailcodeKind 是本 provider 的唯一标识（= 号池里存的 mail_source）。
const mailcodeKind = "mailcode"

// init 注册 mailcode provider 到全局注册表。
func init() {
	Register(mailcodeKind, newMailcodeFromConfig)
}

// MailcodeProvider 实现 Provider：通过 mailcode-api 的 AccessURL 轮询读信取 OTP。
//
// 能力：Ephemeral=false（地址是邮箱池里固定的）、Pooled=true（号是买来的、废了要换）。
type MailcodeProvider struct {
	// accessURL 是读信入口（base + "/api/mail?email=X"），来自邮箱池记录。
	// 它已绑定具体邮箱；WaitForOTP 的 emailAddr 参数用于校验/日志。
	accessURL string
	// httpClient 用于轮询读信（可注入 mock 便于测试）。
	httpClient *http.Client
	// pollInterval 是轮询间隔（默认 3s，对齐真浏览器等码节奏，避免打爆 mailcode-api）。
	pollInterval time.Duration
	// codePattern 可选自定义验证码正则（默认用 ExtractOTP 内置 reOTP6）。
	codePattern *regexp.Regexp
	// dead 标记本号是否已废（Pooled 语义）。
	dead bool
	// baseline 是「取码动作开始前邮箱里已有的码」（对齐 codex wait_for_new_code 的
	// baseline/submitted_at 语义）。passwordless 流程下 authorize/continue 会先抢发
	// 一封码 X,随后的 resend 会让 X 在服务端【立即失效】并改发新码 Y。若取到 X 提交
	// → wrong_email_otp_code。故 WaitForOTP 必须【拒绝等于 baseline 的码】,只接受与
	// 它不同的新码 Y。由 PeekOTP/显式 SetBaseline 在发码前记录。
	baseline string
}

// MailcodeOption 是构造选项。
type MailcodeOption func(*MailcodeProvider)

// WithMailcodeHTTPClient 注入自定义 HTTP 客户端（测试/超时控制用）。
func WithMailcodeHTTPClient(c *http.Client) MailcodeOption {
	return func(p *MailcodeProvider) { p.httpClient = c }
}

// WithMailcodePollInterval 覆盖轮询间隔（压测提速用；生产保持默认 3s）。
func WithMailcodePollInterval(d time.Duration) MailcodeOption {
	return func(p *MailcodeProvider) { p.pollInterval = d }
}

// WithMailcodeCodePattern 注入自定义验证码正则（该服务商验证码格式特殊时用）。
func WithMailcodeCodePattern(re *regexp.Regexp) MailcodeOption {
	return func(p *MailcodeProvider) { p.codePattern = re }
}

// NewMailcodeProvider 直接由 AccessURL 构造（最常用入口：号池记录里有 accessUrl 时）。
func NewMailcodeProvider(accessURL string, opts ...MailcodeOption) *MailcodeProvider {
	p := &MailcodeProvider{
		accessURL:    accessURL,
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		pollInterval: 3 * time.Second,
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

// newMailcodeFromConfig 是注册表工厂：从「settings + 号池记录」构造。
// account 里的 accessUrl 是读信入口（优先）；否则用 settings.baseURL + account.email 现拼。
func newMailcodeFromConfig(ctx context.Context, settings, account map[string]any) (Provider, error) {
	accessURL := strField(account, "accessUrl", "access_url", "accessURL")
	if accessURL == "" {
		// 兜底：baseURL + email 现拼（对齐 mailcode.BuildAccessURL 的格式）。
		base := strField(settings, "baseURL", "base_url", "base")
		email := strField(account, "email")
		if base == "" || email == "" {
			return nil, NewError("mailcode 缺少 accessUrl 且无法由 baseURL+email 现拼", true, "config", nil)
		}
		accessURL = buildAccessURL(base, email)
	}
	return NewMailcodeProvider(accessURL), nil
}

// Caps 返回能力声明（对齐 base.py 能力维度）。
func (p *MailcodeProvider) Caps() Capabilities {
	return Capabilities{
		Kind:                   mailcodeKind,
		DisplayName:            "自建 Mailcow 邮箱",
		Pooled:                 true,  // 号是买来的，废了要换
		Ephemeral:              false, // 地址固定（从号池取）
		AcceptsExistingAccount: false, // 撞老号 = 这号没用了
	}
}

// CreateMailbox 返回固定地址（Ephemeral=false，地址由号池提供，这里不新造）。
func (p *MailcodeProvider) CreateMailbox(ctx context.Context) (string, error) {
	// mailcode 地址由号池记录提供（本 provider 持有的是 accessURL 不是 email），
	// 故返回空：调用方应从号池记录取 email，不依赖 provider 造地址。
	return "", nil
}

// WaitForOTP 轮询 AccessURL 直到取到 issuedAfter 之后的 OTP 或超时。
func (p *MailcodeProvider) WaitForOTP(ctx context.Context, emailAddr string, timeoutSeconds int, issuedAfterUnix int64) (string, error) {
	if p.accessURL == "" {
		return "", NewError("mailcode 缺少 accessUrl（读信入口）", true, "mailbox", nil)
	}
	if timeoutSeconds <= 0 {
		timeoutSeconds = 120
	}
	deadline := time.Now().Add(time.Duration(timeoutSeconds) * time.Second)

	for {
		// 超时判定（对齐 Python TimeoutError）。
		if time.Now().After(deadline) {
			return "", NewError(fmt.Sprintf("等待验证码超时（%ds）", timeoutSeconds), false, "timeout", nil)
		}
		// ctx 取消（对齐 should_cancel）。
		select {
		case <-ctx.Done():
			return "", NewError("验证码等待被取消", false, "cancelled", ctx.Err())
		default:
		}

		code, fatal, err := p.fetchOnce(ctx, issuedAfterUnix)
		if err != nil {
			// 网络类错误：非致命，继续轮询到超时（瞬时抖动不该判死一个号）。
			if fatal {
				p.dead = true
				return "", err
			}
			// 非致命：短暂等待后重试。
			if !sleepOrDone(ctx, p.pollInterval) {
				return "", NewError("验证码等待被取消", false, "cancelled", ctx.Err())
			}
			continue
		}
		if code != "" {
			// 新鲜度(对齐 codex wait_for_new_code):若该码与 baseline(取码前已有码 X)
			// 相同 → 它是已被 resend 作废的旧码,绝不能提交,继续轮询等新码 Y。
			if p.baseline != "" && code == p.baseline {
				if !sleepOrDone(ctx, p.pollInterval) {
					return "", NewError("验证码等待被取消", false, "cancelled", ctx.Err())
				}
				continue
			}
			// 新码(≠baseline):防抖确认——立刻再抓一次,一致才返回,避免页面刷新
			// 瞬间/解析闪变拿到半成品。确认失败则继续轮询。
			if confirm, _, cerr := p.fetchOnce(ctx, issuedAfterUnix); cerr == nil && confirm == code {
				return code, nil
			}
			// 二次确认不一致:短暂等待后重新轮询(页面可能正在更新)。
			if !sleepOrDone(ctx, p.pollInterval) {
				return "", NewError("验证码等待被取消", false, "cancelled", ctx.Err())
			}
			continue
		}
		// 本轮无新码：等一个轮询间隔再试。
		if !sleepOrDone(ctx, p.pollInterval) {
			return "", NewError("验证码等待被取消", false, "cancelled", ctx.Err())
		}
	}
}

// PeekOTP 非破坏性预读（探一次就走，拿不到返回 "",nil 不抛异常）。
func (p *MailcodeProvider) PeekOTP(ctx context.Context, emailAddr string, issuedAfterUnix int64, waitSeconds float64) (string, error) {
	if p.accessURL == "" {
		return "", nil
	}
	// 可选短等（默认 0：打一次就走，不阻塞）。
	if waitSeconds > 0 {
		if !sleepOrDone(ctx, time.Duration(waitSeconds*float64(time.Second))) {
			return "", nil
		}
	}
	code, _, err := p.fetchOnce(ctx, issuedAfterUnix)
	if err != nil {
		return "", nil // peek 失败不抛，走正常发码路径
	}
	// 记录 baseline:取码前邮箱已有码(可能是 resend 会作废的旧码 X)。
	// WaitForOTP 据此拒绝它,只接受 resend 之后的新码 Y。
	if code != "" {
		p.baseline = code
	}
	return code, nil
}

// MarkDead 标记本号废掉（Pooled 语义）。
func (p *MailcodeProvider) MarkDead(reason string) { p.dead = true }

// Exhausted 本号是否已判定不可用。
func (p *MailcodeProvider) Exhausted() bool { return p.dead }

// ────────────────────────────────────────────────────────────
//  内部：单次读信 + 抠码（防串号过滤）
// ────────────────────────────────────────────────────────────

// mailcodeResponse 是 /api/mail 的宽松响应模型。
//
// mailcode-api 的返回结构未在 codex_audit / GPT-GO 现成定义（主项目 mailbox_client
// 处理），这里用多字段兜底：既兼容「服务端已解析好的验证码」字段，
// 也兼容「原始邮件正文」字段（用 ExtractOTP 现抠）。
type mailcodeResponse struct {
	// 服务端已解析好的验证码字段（多种命名兜底，对齐 bridge.py 的 getattr 链）。
	VerificationCode string `json:"verificationCode"`
	Code             string `json:"code"`
	OTP              string `json:"otp"`
	// 邮件正文/原文字段（需在本地 ExtractOTP 抠码）。
	Body    string `json:"body"`
	Text    string `json:"text"`
	HTML    string `json:"html"`
	Raw     string `json:"raw"`
	Subject string `json:"subject"`
	// 邮件到达时间（防串号：只取 issuedAfter 之后的）。多种命名兜底。
	ReceivedAtUnix int64 `json:"receivedAt"`
	DateUnix       int64 `json:"date"`
	Timestamp      int64 `json:"timestamp"`
	CreatedAtUnix  int64 `json:"createdAt"`
	// 邮件列表（部分实现返回多封，取最新一封符合时间窗的）。
	Messages []mailcodeMessage `json:"messages"`
	Emails   []mailcodeMessage `json:"emails"`
}

// mailcodeMessage 是单封邮件的宽松模型。
type mailcodeMessage struct {
	VerificationCode string `json:"verificationCode"`
	Code             string `json:"code"`
	Body             string `json:"body"`
	Text             string `json:"text"`
	HTML             string `json:"html"`
	Raw              string `json:"raw"`
	ReceivedAtUnix   int64  `json:"receivedAt"`
	DateUnix         int64  `json:"date"`
	Timestamp        int64  `json:"timestamp"`
}

// fetchOnce 单次读信并抠码。
//
// 返回 (code, fatal, err)：
//
//	code 非空 = 取到符合时间窗的 OTP；
//	fatal=true + err = 邮箱/凭据已废（调用方应判死）；
//	fatal=false + err = 网络/瞬时错误（可重试）；
//	code="" + err=nil = 读信成功但暂无符合时间窗的新码（继续轮询）。
func (p *MailcodeProvider) fetchOnce(ctx context.Context, issuedAfterUnix int64) (string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.accessURL, nil)
	if err != nil {
		return "", true, NewError("mailcode 构造请求失败", true, "mailbox", err)
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", false, NewError("mailcode 读信请求失败", false, "network", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		// 凭据失效 = 号废了（致命）。
		return "", true, NewError(fmt.Sprintf("mailcode 凭据失效 HTTP %d", resp.StatusCode), true, "mailbox", nil)
	}
	if resp.StatusCode != http.StatusOK {
		// 其它状态码：非致命（可能是邮件还没到/服务端临时异常），继续轮询。
		io.Copy(io.Discard, resp.Body)
		return "", false, NewError(fmt.Sprintf("mailcode 读信 HTTP %d", resp.StatusCode), false, "network", nil)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20)) // 限 4MiB 防恶意大响应
	if err != nil {
		return "", false, NewError("mailcode 读响应体失败", false, "network", err)
	}
	var mr mailcodeResponse
	if err := json.Unmarshal(body, &mr); err != nil {
		// 响应不是 JSON：可能直接返回了纯文本邮件 → 用 ExtractOTP 抠。
		if code := ExtractOTP(string(body), p.codePattern); code != "" {
			return code, false, nil
		}
		return "", false, NewError("mailcode 响应解析失败", false, "parse", err)
	}

	// 1) 顶层已解析验证码（且符合时间窗）。
	if code := firstNonEmpty(mr.VerificationCode, mr.Code, mr.OTP); code != "" {
		if p.withinWindow(mr.bestTimestamp(), issuedAfterUnix) {
			return code, false, nil
		}
	}
	// 2) 顶层正文抠码。
	if code := ExtractOTP(firstNonEmpty(mr.HTML, mrraw(mr), mr.Body, mr.Text, mr.Subject), p.codePattern); code != "" {
		if p.withinWindow(mr.bestTimestamp(), issuedAfterUnix) {
			return code, false, nil
		}
	}
	// 3) 邮件列表：取最新一封符合时间窗的。
	for _, m := range append(append([]mailcodeMessage{}, mr.Messages...), mr.Emails...) {
		ts := m.bestTimestamp()
		if !p.withinWindow(ts, issuedAfterUnix) {
			continue
		}
		if code := firstNonEmpty(m.VerificationCode, m.Code); code != "" {
			return code, false, nil
		}
		if code := ExtractOTP(firstNonEmpty(m.HTML, m.Raw, m.Body, m.Text), p.codePattern); code != "" {
			return code, false, nil
		}
	}
	return "", false, nil
}

// withinWindow 防串号判定：邮件时间 ts 是否在 issuedAfter 之后。
// ts<=0（响应没带时间）时放行（无法判断就不过滤，对齐 Python 的兜底宽松）。
func (p *MailcodeProvider) withinWindow(ts, issuedAfterUnix int64) bool {
	if issuedAfterUnix <= 0 || ts <= 0 {
		return true
	}
	return ts >= issuedAfterUnix
}

// bestTimestamp 取响应里可用的最早非零时间戳。
func (m *mailcodeResponse) bestTimestamp() int64 {
	return firstNonZero(m.ReceivedAtUnix, m.DateUnix, m.Timestamp, m.CreatedAtUnix)
}

// bestTimestamp 取单封邮件里可用的最早非零时间戳。
func (m *mailcodeMessage) bestTimestamp() int64 {
	return firstNonZero(m.ReceivedAtUnix, m.DateUnix, m.Timestamp)
}

// ── 小工具 ──

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

func mrraw(m mailcodeResponse) string { return m.Raw }

func firstNonZero(vs ...int64) int64 {
	for _, v := range vs {
		if v != 0 {
			return v
		}
	}
	return 0
}

func strField(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

// sleepOrDone 睡眠 d 或 ctx 取消即返回 false。
func sleepOrDone(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// buildAccessURL 现拼读信入口（对齐 mailcode.BuildAccessURL：base + "/api/mail?email=X"）。
// 这里不依赖 mailcode 包（避免 otp → mailcode 反向耦合），复制其拼接规则。
func buildAccessURL(base, email string) string {
	b := []byte(base)
	for len(b) > 0 && b[len(b)-1] == '/' {
		b = b[:len(b)-1]
	}
	return string(b) + "/api/mail?email=" + email
}
