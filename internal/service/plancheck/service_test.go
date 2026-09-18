// service_test.go 合并检查（验活+套餐）的 mock 测试（不联网）。
//
// 用 mock AccountStore（store.MockAccountStore）+ mock ProxyStore + mock session
// （httptest 喂 accounts/check 响应）验证 CheckCombined 的各分支与字段落库。
package plancheck

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"gpt-go/internal/service/signup"
	"gpt-go/internal/service/signup/core"
	"gpt-go/internal/store"
)

// ── mock ProxyStore ──

type mockProxyStore struct {
	count int
}

func (m *mockProxyStore) CountEligibleProxies(ctx context.Context, country, group string) (int, error) {
	if m.count <= 0 {
		return 8, nil
	}
	return m.count, nil
}
func (m *mockProxyStore) AcquireProxy(ctx context.Context, owner string, excluded []string, lease int, country, group string) (*signup.ProxyLease, error) {
	return &signup.ProxyLease{ID: "px-1", Host: "127.0.0.1", Port: 8080, Scheme: "http", Country: country}, nil
}
func (m *mockProxyStore) ReleaseProxy(ctx context.Context, id, owner string) error { return nil }
func (m *mockProxyStore) ReturnProxy(ctx context.Context, id, owner string) error  { return nil }
func (m *mockProxyStore) AcquireProxyByID(ctx context.Context, proxyID, owner string, lease int) (*signup.ProxyLease, error) {
	return &signup.ProxyLease{ID: proxyID, Host: "127.0.0.1", Port: 8080, Scheme: "http"}, nil
}

// ── mock session（httptest 喂 accounts/check 响应）──

type mockSession struct {
	body   string
	status int
}

func (m *mockSession) Get(ctx context.Context, url, referer string) (*core.Response, error) {
	st := m.status
	if st == 0 {
		st = http.StatusOK
	}
	return core.NewResponse(st, []byte(m.body)), nil
}

// GetWithHeaders 与 Get 同行为(测试桩不关心 Authorization 头)。
func (m *mockSession) GetWithHeaders(ctx context.Context, url, referer string, extra map[string]string) (*core.Response, error) {
	return m.Get(ctx, url, referer)
}
func (m *mockSession) Close() {}

// sessionReturning 构造返回固定 accounts/check body 的会话工厂。
func sessionReturning(body string) func(string, time.Duration) (planSession, error) {
	return func(string, time.Duration) (planSession, error) {
		return &mockSession{body: body}, nil
	}
}

// seedAccount 往 mock store 塞一个带 token 的账号。
func seedAccount(t *testing.T, as *store.MockAccountStore, id, email, token, country string) {
	t.Helper()
	ok, err := as.Create(context.Background(), store.AccountDocument{
		ID: id, Email: email, EmailNormalized: email,
		CreatedAt: time.Now().UTC(), AccountType: "free",
		AccessToken: token, AccessTokenConfigured: true,
		RegistrationCountry: strP(country),
	})
	if err != nil || !ok {
		t.Fatalf("seed account %s 失败: %v", id, err)
	}
}

func strP(s string) *string { return &s }

const freePlusPromoBody = `{"accounts":{"default":{
	"account":{"plan_type":"free","account_id":"acc-x"},
	"entitlement":{"subscription_plan":"chatgptfreeplan","has_active_subscription":false},
	"eligible_promo_campaigns":{"plus":{"id":"plus-1-month-free"}}}}}`

const plusPaidBody = `{"accounts":{"default":{
	"account":{"plan_type":"free"},
	"entitlement":{"subscription_plan":"chatgptplusplan","has_active_subscription":true}}}}`

// TestCheckCombinedAliveFreeWithPromo 验证：free+plus促销 → alive + promotionEligible=true + accountType=free。
func TestCheckCombinedAliveFreeWithPromo(t *testing.T) {
	as := store.NewMockAccountStore()
	seedAccount(t, as, "1", "a@x.com", "tok-1", "US")
	svc := New(as, &mockProxyStore{}, WithSessionFactory(sessionReturning(freePlusPromoBody)))

	res, err := svc.CheckCombined(context.Background(), []string{"1"}, "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.Alive != 1 || res.Failed != 0 || res.Dead != 0 {
		t.Fatalf("汇总错误: %+v", res)
	}
	doc, _ := as.Get(context.Background(), "1")
	if *doc.AliveStatus != "alive" {
		t.Fatalf("aliveStatus 应 alive, got %v", *doc.AliveStatus)
	}
	if *doc.PlanCheckStatus != "success" {
		t.Fatalf("planCheckStatus 应 success, got %v", *doc.PlanCheckStatus)
	}
	if doc.PromotionEligible == nil || !*doc.PromotionEligible {
		t.Fatal("promotionEligible 应 true（free+plus促销）")
	}
	if doc.PromotionCampaignID == nil || *doc.PromotionCampaignID != "plus-1-month-free" {
		t.Fatalf("campaignID 错误: %+v", doc.PromotionCampaignID)
	}
	if doc.AccountType != "free" {
		t.Fatalf("accountType 应 free, got %q", doc.AccountType)
	}
}

// TestCheckCombinedPlusPaid 验证：entitlement 活跃 plus 订阅 → accountType=plus + 无试用资格。
func TestCheckCombinedPlusPaid(t *testing.T) {
	as := store.NewMockAccountStore()
	seedAccount(t, as, "1", "b@x.com", "tok-2", "JP")
	svc := New(as, &mockProxyStore{}, WithSessionFactory(sessionReturning(plusPaidBody)))

	if _, err := svc.CheckCombined(context.Background(), []string{"1"}, ""); err != nil {
		t.Fatalf("err: %v", err)
	}
	doc, _ := as.Get(context.Background(), "1")
	if doc.AccountType != "plus" {
		t.Fatalf("entitlement 活跃 plus 应置 accountType=plus, got %q", doc.AccountType)
	}
	if doc.HasActiveSubscription == nil || !*doc.HasActiveSubscription {
		t.Fatal("hasActiveSubscription 应 true")
	}
	if doc.PromotionEligible == nil || *doc.PromotionEligible {
		t.Fatal("已付费 plus 不应有试用资格（promotionEligible=false）")
	}
}

// TestCheckCombinedSkipsNoToken 验证：无 token 账号 → skipped（不落 running）。
func TestCheckCombinedSkipsNoToken(t *testing.T) {
	as := store.NewMockAccountStore()
	// 无 AccessToken。
	_, _ = as.Create(context.Background(), store.AccountDocument{
		ID: "1", Email: "c@x.com", EmailNormalized: "c@x.com", CreatedAt: time.Now().UTC(),
	})
	svc := New(as, &mockProxyStore{}, WithSessionFactory(sessionReturning(freePlusPromoBody)))
	res, _ := svc.CheckCombined(context.Background(), []string{"1"}, "")
	if res.Skipped != 1 {
		t.Fatalf("无 token 应 skipped, got %+v", res)
	}
}

// TestCheckCombinedDedup 验证：重复 id 去重（对齐 codex dict.fromkeys）。
func TestCheckCombinedDedup(t *testing.T) {
	as := store.NewMockAccountStore()
	seedAccount(t, as, "1", "d@x.com", "tok", "US")
	svc := New(as, &mockProxyStore{}, WithSessionFactory(sessionReturning(freePlusPromoBody)))
	res, _ := svc.CheckCombined(context.Background(), []string{"1", "1", "1"}, "")
	if res.Requested != 1 {
		t.Fatalf("重复 id 应去重为 1, got %d", res.Requested)
	}
}

// TestCheckCombinedConcurrent 验证：多号并发各自独立落库（Go 并发优势），无数据竞争。
func TestCheckCombinedConcurrent(t *testing.T) {
	as := store.NewMockAccountStore()
	const n = 20
	for i := 0; i < n; i++ {
		seedAccount(t, as, fmt.Sprintf("id-%d", i), fmt.Sprintf("u%d@x.com", i), "tok", "US")
	}
	svc := New(as, &mockProxyStore{count: 8}, WithSessionFactory(sessionReturning(freePlusPromoBody)), WithMaxConcurrency(8))

	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("id-%d", i)
	}
	res, err := svc.CheckCombined(context.Background(), ids, "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.Alive != n {
		t.Fatalf("并发 %d 号应全部 alive, got alive=%d failed=%d", n, res.Alive, res.Failed)
	}
	// 抽查几个落库正确。
	for _, id := range []string{"id-0", "id-10", "id-19"} {
		doc, _ := as.Get(context.Background(), id)
		if *doc.AliveStatus != "alive" || *doc.PlanCheckStatus != "success" {
			t.Fatalf("%s 落库错误: alive=%v plan=%v", id, *doc.AliveStatus, *doc.PlanCheckStatus)
		}
	}
}

// TestClaimMutualExclusion 验证：同一号并发认领只有一个成功（running 互斥）。
func TestClaimMutualExclusion(t *testing.T) {
	as := store.NewMockAccountStore()
	seedAccount(t, as, "1", "e@x.com", "tok", "US")
	var wg sync.WaitGroup
	won := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			if d, _ := as.ClaimPlanCheck(context.Background(), "1", 5); d != nil {
				won <- fmt.Sprintf("w%d", k)
			}
		}(i)
	}
	wg.Wait()
	close(won)
	cnt := 0
	for range won {
		cnt++
	}
	if cnt != 1 {
		t.Fatalf("并发认领应只有 1 个成功（running 互斥）, got %d", cnt)
	}
}
