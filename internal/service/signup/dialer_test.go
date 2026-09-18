// dialer_test.go 会话型拨号器单测：session 重写、同国家校验、同出口 IP 去重、重拨。
package signup

import (
	"context"
	"errors"
	"testing"
	"time"

	"gpt-go/internal/model"
)

// mockProber 是测试用 EgressProber：按调用序返回预设结果。
type mockProber struct {
	results []egressRes
	calls   int
	proxies []string // 记录每次探测的 proxyURL（验证 session 已被替换）
}

type egressRes struct {
	country, ip, tz string
	err             error
}

func (m *mockProber) ProbeEgress(ctx context.Context, proxyURL string) (string, string, string, error) {
	m.proxies = append(m.proxies, proxyURL)
	if m.calls >= len(m.results) {
		return "", "", "", errors.New("no more mock results")
	}
	r := m.results[m.calls]
	m.calls++
	return r.country, r.ip, r.tz, r.err
}

func iprocketLease() *ProxyLease {
	return &ProxyLease{
		ID: "ch1", Host: "prem.country.iprocket.io", Port: 9595,
		Username: "com44840385-res-VN-Lsid-901244458-TTL-1800", Password: "Sy3p57oVo24q1tY",
		Scheme: "socks5h", Country: "VN",
	}
}

// TestClassifySession 验证两种会话标记的分类。
func TestClassifySession(t *testing.T) {
	cases := []struct {
		user string
		want model.SessionKind
	}{
		{"com44840385-res-VN-Lsid-901244458-TTL-1800", model.SessionKindIprocketLsid},
		{"bru36433_area-VN_life-5_session-U0nM1dHxwW", model.SessionKindIpipbright},
		{"plain-static-user", model.SessionKindStatic},
	}
	for _, c := range cases {
		got, re := model.ClassifySession(c.user)
		if got != c.want {
			t.Fatalf("ClassifySession(%q)=%s, want %s", c.user, got, c.want)
		}
		if c.want == model.SessionKindStatic && re != nil {
			t.Fatalf("static 不应返回正则")
		}
	}
}

// TestRewriteSession 验证 session 替换（保留前缀、只改 id）。
func TestRewriteSession(t *testing.T) {
	kind, _ := model.ClassifySession("com44840385-res-VN-Lsid-901244458-TTL-1800")
	got := model.RewriteSession("com44840385-res-VN-Lsid-901244458-TTL-1800", kind, "NEWSESSION1")
	want := "com44840385-res-VN-Lsid-NEWSESSION1-TTL-1800"
	if got != want {
		t.Fatalf("iprocket rewrite=%q, want %q", got, want)
	}
	kind2, _ := model.ClassifySession("bru36433_area-VN_life-5_session-U0nM1dHxwW")
	got2 := model.RewriteSession("bru36433_area-VN_life-5_session-U0nM1dHxwW", kind2, "AbC123")
	want2 := "bru36433_area-VN_life-5_session-AbC123"
	if got2 != want2 {
		t.Fatalf("ipipbright rewrite=%q, want %q", got2, want2)
	}
}

// TestDialReplacesSession 验证拨号会用随机 session 替换 username（拼进 proxy URL）。
func TestDialReplacesSession(t *testing.T) {
	mp := &mockProber{results: []egressRes{{country: "VN", ip: "1.2.3.4", tz: "Asia/Ho_Chi_Minh"}}}
	d := NewSessionDialer(mp)
	res, err := d.Dial(context.Background(), iprocketLease(), "VN")
	if err != nil {
		t.Fatalf("Dial err: %v", err)
	}
	if res.EgressIP != "1.2.3.4" || res.Country != "VN" {
		t.Fatalf("Dial result wrong: %+v", res)
	}
	// proxy URL 里的 session 应已被替换（不再是 901244458）。
	if len(mp.proxies) == 0 {
		t.Fatal("prober 未被调用")
	}
	if containsStr(mp.proxies[0], "901244458") {
		t.Fatalf("session 未被替换: %s", mp.proxies[0])
	}
	if res.SessionID == "" {
		t.Fatal("应记录本次 session id")
	}
}

// TestDialCountryMismatchRedials 验证国家不符会换 session 重拨直到匹配。
func TestDialCountryMismatchRedials(t *testing.T) {
	mp := &mockProber{results: []egressRes{
		{country: "US", ip: "1.1.1.1"},                         // 第1次：国家不符
		{country: "TH", ip: "2.2.2.2"},                         // 第2次：国家不符
		{country: "VN", ip: "3.3.3.3", tz: "Asia/Ho_Chi_Minh"}, // 第3次：命中
	}}
	d := NewSessionDialer(mp)
	res, err := d.Dial(context.Background(), iprocketLease(), "VN")
	if err != nil {
		t.Fatalf("Dial err: %v", err)
	}
	if mp.calls != 3 {
		t.Fatalf("应重拨 3 次, 实际 %d", mp.calls)
	}
	if res.Country != "VN" {
		t.Fatalf("最终国家应=VN, got %s", res.Country)
	}
}

// TestDialSameIPRedials 验证同出口 IP 会重拨（住宅代理同 session 可能同 IP）。
func TestDialSameIPRedials(t *testing.T) {
	mp := &mockProber{results: []egressRes{
		{country: "VN", ip: "9.9.9.9"}, // 第1次
		{country: "VN", ip: "9.9.9.9"}, // 第2次：同 IP（被去重）
		{country: "VN", ip: "8.8.8.8"}, // 第3次：新 IP
	}}
	d := NewSessionDialer(mp)
	// 第一次拨号占用 9.9.9.9
	r1, err := d.Dial(context.Background(), iprocketLease(), "VN")
	if err != nil {
		t.Fatalf("Dial1 err: %v", err)
	}
	if r1.EgressIP != "9.9.9.9" {
		t.Fatalf("Dial1 IP 应=9.9.9.9, got %s", r1.EgressIP)
	}
	// 第二次拨号：9.9.9.9 已占用 → 重拨到 8.8.8.8
	r2, err := d.Dial(context.Background(), iprocketLease(), "VN")
	if err != nil {
		t.Fatalf("Dial2 err: %v", err)
	}
	if r2.EgressIP != "8.8.8.8" {
		t.Fatalf("Dial2 应跳到 8.8.8.8（同 IP 去重）, got %s", r2.EgressIP)
	}
}

// TestDialExhausted 验证重拨耗尽返回错误。
func TestDialExhausted(t *testing.T) {
	mp := &mockProber{results: []egressRes{
		{country: "US", ip: "1.1.1.1"},
		{country: "US", ip: "1.1.1.1"},
	}}
	d := NewSessionDialer(mp, WithMaxRedial(2))
	_, err := d.Dial(context.Background(), iprocketLease(), "VN")
	if err == nil {
		t.Fatal("国家一直不符 + 重拨耗尽应返回错误")
	}
}

// TestDialStatic 验证静态通道（无 session）IP 固定、测一次。
func TestDialStatic(t *testing.T) {
	mp := &mockProber{results: []egressRes{{country: "VN", ip: "5.5.5.5"}}}
	d := NewSessionDialer(mp)
	static := &ProxyLease{ID: "s1", Host: "1.2.3.4", Port: 8080, Username: "u", Password: "p", Scheme: "http"}
	res, err := d.Dial(context.Background(), static, "VN")
	if err != nil {
		t.Fatalf("static Dial err: %v", err)
	}
	if res.EgressIP != "5.5.5.5" || mp.calls != 1 {
		t.Fatalf("static 应测一次得 5.5.5.5, got %+v calls=%d", res, mp.calls)
	}
}

// ── L4 出口 IP 并发上限（maxRegistrationsPerExitIp）──

// TestExitIPCapZeroUnlimited 验证 cap<=0 时不限（对齐 settings 默认 0 = 不限）。
func TestExitIPCapZeroUnlimited(t *testing.T) {
	d := NewSessionDialer(&mockProber{}, WithExitIPCap(0))
	for i := 0; i < 5; i++ {
		if !d.reg.claim("9.9.9.9", time.Minute, d.exitIPCap) {
			t.Fatalf("cap=0 应不限，第 %d 次 claim 失败", i)
		}
	}
}

// TestExitIPCapN 验证 cap>1 时允许 cap 个并发、超出拒绝（对齐 codex-auto _try_claim_exit_ip）。
func TestExitIPCapN(t *testing.T) {
	d := NewSessionDialer(&mockProber{}, WithExitIPCap(2))
	if !d.reg.claim("9.9.9.9", time.Minute, d.exitIPCap) {
		t.Fatal("cap=2 第 1 次应成功")
	}
	if !d.reg.claim("9.9.9.9", time.Minute, d.exitIPCap) {
		t.Fatal("cap=2 第 2 次应成功")
	}
	if d.reg.claim("9.9.9.9", time.Minute, d.exitIPCap) {
		t.Fatal("cap=2 第 3 次应拒绝（已达上限）")
	}
	// 不同 IP 互不影响。
	if !d.reg.claim("8.8.8.8", time.Minute, d.exitIPCap) {
		t.Fatal("不同 IP 应可独立占用")
	}
}

// TestExitIPRelease 验证 release 即时回收额度（不等 TTL）。
func TestExitIPRelease(t *testing.T) {
	d := NewSessionDialer(&mockProber{}, WithExitIPCap(1))
	if !d.reg.claim("9.9.9.9", time.Minute, d.exitIPCap) {
		t.Fatal("第 1 次应成功")
	}
	if d.reg.claim("9.9.9.9", time.Minute, d.exitIPCap) {
		t.Fatal("cap=1 第 2 次应拒绝")
	}
	d.ReleaseEgress("9.9.9.9")
	if !d.reg.claim("9.9.9.9", time.Minute, d.exitIPCap) {
		t.Fatal("release 后应可回收额度")
	}
}

// TestExitIPCapExpiry 验证过期占用自动清理（TTL 到期后额度恢复）。
func TestExitIPCapExpiry(t *testing.T) {
	d := NewSessionDialer(&mockProber{}, WithExitIPCap(1))
	// 用一个已经过期的 TTL 占用（ttl<0 → 立即过期）。
	if !d.reg.claim("9.9.9.9", -time.Second, d.exitIPCap) {
		t.Fatal("首次 claim 应成功")
	}
	// 该占用已过期，下一次 claim 应清掉过期记录并成功。
	if !d.reg.claim("9.9.9.9", time.Minute, d.exitIPCap) {
		t.Fatal("过期占用应被清理，再次 claim 应成功")
	}
}

// TestClampConcurrency 验证并发数钳制到 settings 边界 [1,12]。
func TestClampConcurrency(t *testing.T) {
	cases := map[int]int{0: 1, -5: 1, 1: 1, 5: 5, 12: 12, 13: 12, 100: 12}
	for in, want := range cases {
		if got := clampConcurrency(in); got != want {
			t.Fatalf("clampConcurrency(%d)=%d, want %d", in, got, want)
		}
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
