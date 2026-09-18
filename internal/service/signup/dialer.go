// dialer.go 会话型住宅代理的「现场拨号」器（模型 B 核心）。
//
// 设计背景（务必先读 model/proxy.go 的「会话型住宅代理」注释）：
//
//	你给的代理是会话型住宅代理——host:port 是服务商网关，username 里嵌一个
//	session-id，改它就拨出一个不同的出口 IP。所以一条凭证是一个「IP 生成器」
//	（通道），不是一个固定 IP。本文件负责：取一条通道 → 现场生成随机 session →
//	拼临时 proxy URL → probe 实测出口 IP + 国家 → 校验「同国家 + 不同 IP」→ 重拨。
//
// 国家【不锁死】：username 里的 `-res-VN`/`_area-VN` 只是「期望国家」提示，仅用于
// 选通道时粗筛；真实国家必须每次拨号后由 probe 实测（chatgpt.com/cdn-cgi/trace 的
// loc= + GeoIP），当次有效。这是「保证相同国家」的可靠实现——标签会骗人，实测 IP 不会。
package signup

import (
	"context"
	"crypto/rand"
	"fmt"
	"sync"
	"time"

	"gpt-go/internal/model"
)

// ── 出口 IP 去重注册表（对齐 codex-auto _try_claim_exit_ip 的进程内注册表）─────────
//
// 住宅代理服务商会：① 把多个 session 调度到同一出口 IP；② 同一通道短时间改 session
// 可能仍是同一 IP（session 粘性，如 ipipbright 的 life-5=5 分钟保持）。所以「换 session」
// 不等于「换 IP」，必须实测出口 IP 并记录去重——这是「每账号不同 IP」的硬保证。
//
// 注册表按「运行批次」记已用的出口 IP，带 TTL 过期自清（避免某 IP 被永久堵死）。

// exitIPRegistry 记录批次内已占用的出口 IP（带 TTL + 每 IP 计数），线程安全。
//
// 对齐 codex-auto _try_claim_exit_ip：住宅/会话代理多个账号可能分配到同一出口 IP，
// 故对「同一出口 IP 同时在跑的注册数」计数，超过 cap（maxRegistrationsPerExitIp）就拒。
// 每条占用带 TTL，过期自动清理（避免某 IP 被永久堵死）。
type exitIPRegistry struct {
	mu     sync.Mutex
	claims map[string][]time.Time // exitIP → 各次占用的过期时刻（长度即并发占用数）
}

func newExitIPRegistry() *exitIPRegistry {
	return &exitIPRegistry{claims: map[string][]time.Time{}}
}

// claim 尝试占用一个出口 IP；该 IP 的活跃占用数已达 cap（未过期）返回 false。
//
// cap 语义（对齐 codex-auto _try_claim_exit_ip 的 cap=maxRegistrationsPerExitIp）：
//   - cap <= 0：不限（任何出口 IP 都放行）——对齐 settings 默认 0；
//   - cap == 1：严格「同 IP 同时只一个号」（GPT-GO 默认，最保守）；
//   - cap > 1：允许同 IP 最多 cap 个并发注册。
func (r *exitIPRegistry) claim(exitIP string, ttl time.Duration, cap int) bool {
	if exitIP == "" {
		return false
	}
	if cap <= 0 {
		return true // 不限（对齐 codex-auto cap<=0 直接放行）
	}
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	// 先清过期（只留未过期的占用时刻）。
	live := r.claims[exitIP][:0]
	for _, exp := range r.claims[exitIP] {
		if now.Before(exp) {
			live = append(live, exp)
		}
	}
	if len(live) >= cap {
		r.claims[exitIP] = live
		return false
	}
	r.claims[exitIP] = append(live, now.Add(ttl))
	return true
}

// release 释放一个出口 IP 的一次占用（对齐 codex-auto _release_exit_ip）。
// 注册结束（成功/失败）调用，让该 IP 的并发额度即时回收（不等 TTL）。
func (r *exitIPRegistry) release(exitIP string) {
	if exitIP == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	live := r.claims[exitIP]
	if len(live) <= 1 {
		delete(r.claims, exitIP)
		return
	}
	// 去掉一个占用时刻（去哪个都行，TTL 排序无关）。
	r.claims[exitIP] = live[:len(live)-1]
}

// ── 拨号器 ───────────────────────────────────────────────────────────────────

// EgressProber 实测一个代理 URL 的出口（国家 + IP）。由 proxy.Prober 适配而来，
// 抽象成接口便于单测注入 mock。
type EgressProber interface {
	// ProbeEgress 经 proxyURL 探测出口，返回 (国家码, 出口IP, 时区, 错误)。
	ProbeEgress(ctx context.Context, proxyURL string) (country, egressIP, timezoneID string, err error)
}

// DialResult 是一次成功拨号的结果：可直接喂给 core.NewBootstrap。
type DialResult struct {
	// ProxyURL 是临时拼出的代理 URL（username 里的 session 已被随机替换）。
	ProxyURL string
	// EgressIP 是实测出口 IP（同一次注册内不重复）。
	EgressIP string
	// Country 是实测国家（== 目标国家才返回成功）。
	Country string
	// TimezoneID 是出口 IP 的 IANA 时区（顺手给 core 做时区/语言联动）。
	TimezoneID string
	// ChannelID 是所用通道的代理条目 ID（便于审计/归还）。
	ChannelID string
	// SessionID 是本次生成的随机 session（便于排查「哪个会话拨出的哪个 IP」）。
	SessionID string
}

// SessionDialer 把「通道租约」拨成「一个新出口 IP 的临时代理」。
type SessionDialer struct {
	prober EgressProber
	reg    *exitIPRegistry
	// claimTTL 是出口 IP 占用的过期时间（对齐代理租约时长）。
	claimTTL time.Duration
	// maxRedial 单通道最多重拨次数（换 session 重试）。
	maxRedial int
	// exitIPCap 是同一出口 IP 允许的并发注册上限（= settings.maxRegistrationsPerExitIp）。
	//   <=0 不限；==1 严格同 IP 同时一个号（默认，最保守）；>1 允许 cap 个并发。
	//   对齐 codex-auto _try_claim_exit_ip 的 cap 语义。
	exitIPCap int
}

// DialerOption 配置拨号器。
type DialerOption func(*SessionDialer)

// WithClaimTTL 设置出口 IP 占用 TTL（默认 30 分钟，覆盖一次完整注册）。
func WithClaimTTL(d time.Duration) DialerOption {
	return func(s *SessionDialer) { s.claimTTL = d }
}

// WithMaxRedial 设置单通道最大重拨次数（默认 4）。
func WithMaxRedial(n int) DialerOption {
	return func(s *SessionDialer) { s.maxRedial = n }
}

// WithExitIPCap 设置同一出口 IP 的并发注册上限（= settings.maxRegistrationsPerExitIp）。
// <=0 不限；==1 同 IP 同时一个号（默认）；>1 允许 cap 个。对齐 codex-auto cap 语义。
func WithExitIPCap(cap int) DialerOption {
	return func(s *SessionDialer) { s.exitIPCap = cap }
}

// NewSessionDialer 创建拨号器。prober 必填（实测出口）。
// 默认 exitIPCap=1（同 IP 同时一个号，最保守防连坐）。
func NewSessionDialer(prober EgressProber, opts ...DialerOption) *SessionDialer {
	d := &SessionDialer{
		prober:    prober,
		reg:       newExitIPRegistry(),
		claimTTL:  30 * time.Minute,
		maxRedial: 4,
		exitIPCap: 1,
	}
	for _, o := range opts {
		o(d)
	}
	return d
}

// Dial 用一条通道 lease 拨出一个「目标国家 + 本次未用过」的出口 IP。
//
// wantCountry 是目标国家（如 "VN"）；空串表示不校验国家（仍做 IP 去重）。
// 返回 *DialResult；拨不出（重拨耗尽 / 一直同 IP / 国家一直不符）返回错误。
//
// 流程（对齐 codex-auto 租代理循环里的「纯净度复检 + 同出口IP上限」思想）：
//
//	for 重拨次数:
//	  生成随机 session → 替换 username 会话片段 → 拼临时 proxy URL
//	  probe 实测 → (country, egressIP, tz)
//	  国家不符？        → 换 session 重拨
//	  egressIP 已用过？ → 换 session 重拨
//	  占用 egressIP 成功 → 返回
func (d *SessionDialer) Dial(ctx context.Context, lease *ProxyLease, wantCountry string) (*DialResult, error) {
	if lease == nil || lease.Host == "" {
		return nil, fmt.Errorf("dialer: 通道租约为空")
	}
	kind, sessRe := model.ClassifySession(lease.Username)
	if kind == model.SessionKindStatic || sessRe == nil {
		// 静态通道：IP 固定，只能测一次、去重一次（不能换 session 重拨）。
		return d.dialStatic(ctx, lease, wantCountry)
	}

	want := normalizeCountry(wantCountry)
	var lastErr error
	for attempt := 0; attempt < d.maxRedial; attempt++ {
		sess := randomSessionID()
		// 替换 username 里的会话片段 → 这条通道本次拨号用的临时凭证。
		username := model.RewriteSession(lease.Username, kind, sess)
		proxyURL := buildProxyURL(lease.Scheme, username, lease.Password, lease.Host, lease.Port)

		country, egressIP, tz, err := d.prober.ProbeEgress(ctx, proxyURL)
		if err != nil {
			lastErr = err
			continue // 网络瞬断：换 session 重拨
		}
		// ① 国家校验（实测，不锁死）：实测国家必须 == 目标国家（若指定）。
		if want != "" && normalizeCountry(country) != want {
			lastErr = fmt.Errorf("dialer: 实测国家=%s 不符目标=%s（重拨 %d/%d）", country, want, attempt+1, d.maxRedial)
			continue
		}
		// ② 出口 IP 去重：本次批次内不重复（住宅代理同 session 可能同 IP）。
		if egressIP == "" {
			lastErr = fmt.Errorf("dialer: 未探测到出口 IP（重拨 %d/%d）", attempt+1, d.maxRedial)
			continue
		}
		if !d.reg.claim(egressIP, d.claimTTL, d.exitIPCap) {
			lastErr = fmt.Errorf("dialer: 出口 IP=%s 已达并发上限 cap=%d（重拨 %d/%d）", egressIP, d.exitIPCap, attempt+1, d.maxRedial)
			continue
		}
		return &DialResult{
			ProxyURL:   proxyURL,
			EgressIP:   egressIP,
			Country:    normalizeCountry(country),
			TimezoneID: tz,
			ChannelID:  lease.ID,
			SessionID:  sess,
		}, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("dialer: 通道 %s 重拨 %d 次仍无可用出口", lease.ID, d.maxRedial)
	}
	return nil, lastErr
}

// ReleaseEgress 释放一次注册占用的出口 IP 额度（对齐 codex-auto _release_exit_ip）。
// 注册结束（无论成败）调用，让该 IP 的并发额度即时回收（不等 TTL 自然过期）。
// 无拨号器/静态通道时由 service 层按 DialResult.EgressIP 调用。
func (d *SessionDialer) ReleaseEgress(egressIP string) {
	if d == nil || d.reg == nil {
		return
	}
	d.reg.release(egressIP)
}

// dialStatic 处理静态通道：IP 固定，测一次、去重一次。
func (d *SessionDialer) dialStatic(ctx context.Context, lease *ProxyLease, wantCountry string) (*DialResult, error) {
	proxyURL := lease.ProxyURL()
	country, egressIP, tz, err := d.prober.ProbeEgress(ctx, proxyURL)
	if err != nil {
		return nil, err
	}
	want := normalizeCountry(wantCountry)
	if want != "" && normalizeCountry(country) != want {
		return nil, fmt.Errorf("dialer: 静态代理国家=%s 不符目标=%s", country, want)
	}
	if egressIP != "" && !d.reg.claim(egressIP, d.claimTTL, d.exitIPCap) {
		return nil, fmt.Errorf("dialer: 静态代理出口 IP=%s 已达并发上限 cap=%d", egressIP, d.exitIPCap)
	}
	return &DialResult{
		ProxyURL:   proxyURL,
		EgressIP:   egressIP,
		Country:    normalizeCountry(country),
		TimezoneID: tz,
		ChannelID:  lease.ID,
	}, nil
}

// randomSessionID 生成服务商可接受的随机 session-id（[A-Za-z0-9]，16 位）。
// 用 crypto/rand（session-id 无需可复现，且要避免批量注册时撞号）。
func randomSessionID() string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	const n = 16
	var buf [n]byte
	var rnd [n]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		// 极端兜底：crypto/rand 失败用时间戳（几乎不会发生）。
		for i := range buf {
			buf[i] = alphabet[(time.Now().UnixNano()/int64(i+1))%int64(len(alphabet))]
		}
		return string(buf[:])
	}
	for i := range buf {
		buf[i] = alphabet[int(rnd[i])%len(alphabet)]
	}
	return string(buf[:])
}

// buildProxyURL 用临时 username 拼代理 URL（对齐 model.ProxyURL 的编码语义）。
func buildProxyURL(scheme, username, Password, host string, port int) string {
	return model.ProxyURL(host, port, username, Password, scheme)
}

// normalizeCountry 两位大写国家码，空串原样返回（不校验时）。
func normalizeCountry(s string) string {
	if s == "" {
		return ""
	}
	return model.NormalizeCountryCode(s)
}
