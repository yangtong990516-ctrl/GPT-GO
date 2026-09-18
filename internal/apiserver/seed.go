// seed.go Mock 模式下的演示种子数据：让换绑/支付检测页面开箱即可看到完整交互。
//
// 只在内存 Mock store 生效（Mongo 接入后此函数空跑）；数据全部是可安全展示的
// 假邮箱/假代理/假 token，不会触发任何真实外部请求。
package apiserver

import (
	"context"
	"testing"
	"time"

	"gpt-go/internal/model"
	"gpt-go/internal/store"
)

// seedMockDemoData 向 Mock store 注入演示数据。
//
// 注入内容：
//   - 6 个邮箱（4 available + 1 used + 1 failed）→ 换绑页「可用邮箱」卡片 + 邮箱池列表；
//   - 4 个代理（2 个 VN available + 1 个 PH available + 1 个 used）→ 换绑页「可用代理」卡片
//     + 支付检测线路点选器；
//   - 5 个账号（3 个有 AT 可换绑 + 1 个已换绑过 + 1 个缺 AT 不可换绑）→ 换绑页账号列表
//     + 账号池「换绑」列展示。
func seedMockDemoData(s *Server) {
	// 测试模式跳过种子数据（避免污染断言：测试期望空 store 起步）。
	if testing.Testing() {
		return
	}
	ctx := context.Background()
	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)

	// ── 邮箱池：4 个可用（换绑目标邮箱来源）+ 1 已用 + 1 失败 ──
	emails := []store.EmailDocument{
		{ID: "em-001", Email: "alpha@demo-mail.com", EmailNormalized: "alpha@demo-mail.com",
			AccessURL: "https://mail.demo/api/alpha", Status: "available",
			SourceType: "mailcode", ImportedAt: now.Add(-72 * time.Hour)},
		{ID: "em-002", Email: "bravo@demo-mail.com", EmailNormalized: "bravo@demo-mail.com",
			AccessURL: "https://mail.demo/api/bravo", Status: "available",
			SourceType: "mailcode", ImportedAt: now.Add(-70 * time.Hour)},
		{ID: "em-003", Email: "charlie@demo-mail.com", EmailNormalized: "charlie@demo-mail.com",
			AccessURL: "https://mail.demo/api/charlie", Status: "available",
			SourceType: "mailcom", ImportedAt: now.Add(-68 * time.Hour)},
		{ID: "em-004", Email: "delta@demo-mail.com", EmailNormalized: "delta@demo-mail.com",
			AccessURL: "https://mail.demo/api/delta", Status: "available",
			SourceType: "mailcom", ImportedAt: now.Add(-66 * time.Hour)},
		{ID: "em-005", Email: "echo@demo-mail.com", EmailNormalized: "echo@demo-mail.com",
			AccessURL: "https://mail.demo/api/echo", Status: "used",
			SourceType: "mailcode", ImportedAt: now.Add(-96 * time.Hour),
			UsagePurpose: "rebind"},
		{ID: "em-006", Email: "foxtrot@demo-mail.com", EmailNormalized: "foxtrot@demo-mail.com",
			AccessURL: "https://mail.demo/api/foxtrot", Status: "failed",
			SourceType: "mailcode", ImportedAt: now.Add(-90 * time.Hour),
			StatusReason: "接码超时"},
	}
	for _, e := range emails {
		_, _ = s.emailStore.Upsert(ctx, e)
	}

	// ── 代理池：2 个 VN + 1 个 PH 可用 + 1 个已用 ──
	latency := 120
	proxies := []model.ProxyDocument{
		{ID: "px-001", Host: "vn-proxy-1.demo.local", Port: 3010, Username: "demo", Password: "demo",
			Enabled: true, Status: "available", Country: "VN", Group: "default",
			Scheme: "http", LatencyMs: &latency, LastCheckedAt: &nowStr},
		{ID: "px-002", Host: "vn-proxy-2.demo.local", Port: 3011, Username: "demo", Password: "demo",
			Enabled: true, Status: "available", Country: "VN", Group: "default",
			Scheme: "http", LatencyMs: &latency, LastCheckedAt: &nowStr},
		{ID: "px-003", Host: "ph-proxy-1.demo.local", Port: 3020, Username: "demo", Password: "demo",
			Enabled: true, Status: "available", Country: "PH", Group: "default",
			Scheme: "socks5", LatencyMs: &latency, LastCheckedAt: &nowStr},
		{ID: "px-004", Host: "us-proxy-1.demo.local", Port: 3030, Username: "demo", Password: "demo",
			Enabled: true, Status: "available", Country: "US", Group: "default",
			Scheme: "http", LatencyMs: &latency, LastCheckedAt: &nowStr},
	}
	for _, p := range proxies {
		_, _ = s.proxyStore.Upsert(ctx, p)
	}

	// ── 账号池：3 个可换绑（有 AT）+ 1 个已换绑过 + 1 个缺 AT ──
	rebindSuccess := "success"
	prevEmail := "old-hotel@demo-mail.com"
	reboundEmail := "echo@demo-mail.com"
	reboundAt := now.Add(-24 * time.Hour)
	rebindCountry := "VN"
	accounts := []store.AccountDocument{
		{
			ID: "ac-001", Email: "user1@demo-mail.com", EmailNormalized: "user1@demo-mail.com",
			ChatgptPassword: "demo-pass-1", TotpSecret: "JBSWY3DPEHPK3PXP",
			EmailAccessURL: "https://mail.demo/api/user1",
			AccessToken: "demo-token-user1", AccessTokenConfigured: true,
			AccountType: "free", RegistrationCountry: strPtr("VN"),
			CreatedAt: now.Add(-48 * time.Hour),
		},
		{
			ID: "ac-002", Email: "user2@demo-mail.com", EmailNormalized: "user2@demo-mail.com",
			ChatgptPassword: "demo-pass-2", TotpSecret: "JBSWY3DPEHPK3PXQ",
			EmailAccessURL: "https://mail.demo/api/user2",
			AccessToken: "demo-token-user2", AccessTokenConfigured: true,
			AccountType: "free", RegistrationCountry: strPtr("VN"),
			CreatedAt: now.Add(-46 * time.Hour),
		},
		{
			ID: "ac-003", Email: "user3@demo-mail.com", EmailNormalized: "user3@demo-mail.com",
			ChatgptPassword: "demo-pass-3", TotpSecret: "JBSWY3DPEHPK3PXR",
			EmailAccessURL: "https://mail.demo/api/user3",
			AccessToken: "demo-token-user3", AccessTokenConfigured: true,
			AccountType: "plus", RegistrationCountry: strPtr("PH"),
			CreatedAt: now.Add(-44 * time.Hour),
		},
		{
			// 已换绑过的账号：展示「换绑」列的 success 徽章 + previousEmail tooltip。
			ID: "ac-004", Email: "echo@demo-mail.com", EmailNormalized: "echo@demo-mail.com",
			ChatgptPassword: "demo-pass-4", TotpSecret: "JBSWY3DPEHPK3PXS",
			EmailAccessURL: "https://mail.demo/api/echo",
			AccessToken: "demo-token-echo", AccessTokenConfigured: true,
			AccountType: "free", RegistrationCountry: strPtr("VN"),
			RebindStatus: &rebindSuccess, PreviousEmail: &prevEmail,
			ReboundEmail: &reboundEmail, ReboundAt: &reboundAt,
			RebindProxyCountry: &rebindCountry,
			CreatedAt: now.Add(-96 * time.Hour),
		},
		{
			// 缺 AT 的账号：换绑页不显示（需要登录凭证）。
			ID: "ac-005", Email: "user5@demo-mail.com", EmailNormalized: "user5@demo-mail.com",
			ChatgptPassword: "demo-pass-5", TotpSecret: "JBSWY3DPEHPK3PXT",
			EmailAccessURL: "https://mail.demo/api/user5",
			AccessToken: "", AccessTokenConfigured: false,
			AccountType: "free", RegistrationCountry: strPtr("VN"),
			CreatedAt: now.Add(-40 * time.Hour),
		},
	}
	for _, a := range accounts {
		_, _ = s.accountStore.Create(ctx, a)
	}
}

// strPtr 返回字符串指针（种子数据辅助）。
func strPtr(s string) *string { return &s }

// rebindPoolCounter 把 emailStore + proxySvc 适配为 rebindapi.PoolCounter。
type rebindPoolCounter struct {
	emails  store.EmailStore
	proxies interface {
		CountEligible(ctx context.Context, country, group string) (int, error)
	}
}

func (r rebindPoolCounter) CountAvailableEmails(ctx context.Context) (int, error) {
	return r.emails.Count(ctx, func(d store.EmailDocument) bool { return d.Status == "available" })
}

func (r rebindPoolCounter) CountEligibleProxies(ctx context.Context, country, group string) (int, error) {
	return r.proxies.CountEligible(ctx, country, group)
}
