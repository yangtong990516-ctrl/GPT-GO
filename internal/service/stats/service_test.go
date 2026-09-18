package stats

import (
	"context"
	"testing"
	"time"

	"gpt-go/internal/model"
	"gpt-go/internal/store"
)

func seedAccount(t *testing.T, s store.AccountStore, email string, accountType string, phoneBound, promo *bool, totpSecret string, createdAt time.Time) {
	t.Helper()
	ok, err := s.Create(context.Background(), store.AccountDocument{
		Email:             email,
		EmailNormalized:   email,
		AccountType:       accountType,
		PhoneBound:        phoneBound,
		PromotionEligible: promo,
		TotpSecret:        totpSecret,
		CreatedAt:         createdAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = ok
}

func seedEmail(t *testing.T, s store.EmailStore, email, status, sourceType string) {
	t.Helper()
	_, err := s.Upsert(context.Background(), store.EmailDocument{
		Email:           email,
		EmailNormalized: email,
		Status:          status,
		SourceType:      sourceType,
		ImportedAt:      time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
}

var proxySeq int

func seedProxy(t *testing.T, s store.ProxyStore, enabled bool, status string) {
	t.Helper()
	proxySeq++
	_, err := s.Upsert(context.Background(), model.ProxyDocument{
		Host:    "proxy-" + status + "-" + boolStr(enabled) + "-" + itoa(proxySeq),
		Port:    1000,
		Enabled: enabled,
		Status:  status,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func boolStr(b bool) string {
	if b {
		return "t"
	}
	return "f"
}

func bptr(v bool) *bool { return &v }

// TestOverview mirrors overview_stats aggregation across account/email/proxy.
func TestOverview(t *testing.T) {
	accounts := store.NewMockAccountStore()
	emails := store.NewMockEmailStore()
	proxies := store.NewMockProxyStore()

	now := time.Now().UTC()
	// Accounts: 1 plus bound, 1 plus unbound, 1 free eligible, 1 free ineligible, 1 totp.
	seedAccount(t, accounts, "a@x.com", "plus", bptr(true), nil, "SECRET", now)
	seedAccount(t, accounts, "b@x.com", "plus", bptr(false), nil, "", now)
	seedAccount(t, accounts, "c@x.com", "free", nil, bptr(true), "", now)
	seedAccount(t, accounts, "d@x.com", "free", nil, bptr(false), "", now)

	// Emails: 2 available standard, 1 available alias, 1 reserved, 1 failed.
	seedEmail(t, emails, "e1@x.com", "available", "")
	seedEmail(t, emails, "e2@x.com", "available", "")
	seedEmail(t, emails, "alias@x.com", "available", "mailcom_alias")
	seedEmail(t, emails, "r@x.com", "reserved", "")
	seedEmail(t, emails, "f@x.com", "failed", "")

	// Proxies: 2 enabled-available, 1 enabled-used, 1 disabled-quarantined.
	seedProxy(t, proxies, true, model.ProxyStatusAvailable)
	seedProxy(t, proxies, true, model.ProxyStatusAvailable)
	seedProxy(t, proxies, true, model.ProxyStatusUsed)
	seedProxy(t, proxies, false, model.ProxyStatusQuarantined)

	svc := NewService(accounts, emails, proxies)
	got, err := svc.Overview(context.Background())
	if err != nil {
		t.Fatalf("Overview() error = %v", err)
	}

	if got.Accounts.Total != 4 {
		t.Errorf("accounts.total = %d, want 4", got.Accounts.Total)
	}
	if got.Accounts.Plus.Total != 2 || got.Accounts.Plus.Bound != 1 || got.Accounts.Plus.Unbound != 1 {
		t.Errorf("plus = %+v, want total=2 bound=1 unbound=1", got.Accounts.Plus)
	}
	if got.Accounts.Free.Total != 2 || got.Accounts.Free.Eligible != 1 || got.Accounts.Free.Ineligible != 1 {
		t.Errorf("free = %+v, want total=2 eligible=1 ineligible=1", got.Accounts.Free)
	}
	if got.Accounts.TotpComplete != 1 {
		t.Errorf("totpComplete = %d, want 1", got.Accounts.TotpComplete)
	}

	if got.Emails.Available != 3 {
		t.Errorf("emails.available = %d, want 3 (2 standard + 1 alias)", got.Emails.Available)
	}
	if got.Emails.Aliases != 1 {
		t.Errorf("emails.aliases = %d, want 1", got.Emails.Aliases)
	}
	if got.Emails.Reserved != 1 || got.Emails.Failed != 1 {
		t.Errorf("emails.reserved/failed = %d/%d, want 1/1", got.Emails.Reserved, got.Emails.Failed)
	}

	if got.Proxies.Total != 4 || got.Proxies.Enabled != 3 || got.Proxies.Available != 2 || got.Proxies.Used != 1 || got.Proxies.Quarantined != 1 {
		t.Errorf("proxies = %+v, want total=4 enabled=3 available=2 used=1 quarantined=1", got.Proxies)
	}
}

// TestOverviewExcludesRegistered mirrors the $nin exclusion of registered
// accounts from email available counts.
func TestOverviewExcludesRegistered(t *testing.T) {
	accounts := store.NewMockAccountStore()
	emails := store.NewMockEmailStore()
	proxies := store.NewMockProxyStore()

	// A registered account whose email is also in the email pool.
	seedEmail(t, emails, "used@x.com", "available", "")
	// Mark it as registered via the mock's registered-accounts set.
	emails.SetRegisteredEmails("used@x.com")

	svc := NewService(accounts, emails, proxies)
	got, err := svc.Overview(context.Background())
	if err != nil {
		t.Fatalf("Overview() error = %v", err)
	}
	if got.Emails.Available != 0 {
		t.Errorf("emails.available = %d, want 0 (registered excluded)", got.Emails.Available)
	}
}

// TestOverviewPlusRatioAndAlive 验证 GPT-GO 扩展的大盘指标：plusRatio 占比 + 验活分布。
func TestOverviewPlusRatioAndAlive(t *testing.T) {
	accounts := store.NewMockAccountStore()
	emails := store.NewMockEmailStore()
	proxies := store.NewMockProxyStore()

	strPtr := func(s string) *string { return &s }
	seedAlive := func(email, accountType string, alive *string) {
		t.Helper()
		_, err := accounts.Create(context.Background(), store.AccountDocument{
			Email: email, EmailNormalized: email, AccountType: accountType,
			AliveStatus: alive, CreatedAt: time.Now().UTC(),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	// 4 账号：1 plus(alive)、1 free(dead)、1 free(unknown)、1 free(未验活)。
	seedAlive("p@x.com", "plus", strPtr(model.AliveAlive))
	seedAlive("f1@x.com", "free", strPtr(model.AliveDead))
	seedAlive("f2@x.com", "free", strPtr(model.AliveUnknown))
	seedAlive("f3@x.com", "free", nil) // 未验活

	svc := NewService(accounts, emails, proxies)
	got, err := svc.Overview(context.Background())
	if err != nil {
		t.Fatalf("Overview() error = %v", err)
	}
	// plusRatio = 1/4*100 = 25.0
	if got.Accounts.PlusRatio != 25.0 {
		t.Errorf("plusRatio = %v, want 25.0", got.Accounts.PlusRatio)
	}
	// 验活分布：alive=1 dead=1 unknown=1 unchecked=1 running=0
	al := got.Accounts.Alive
	if al.Alive != 1 || al.Dead != 1 || al.Unknown != 1 || al.Unchecked != 1 || al.Running != 0 {
		t.Errorf("alive 分布错误: %+v", al)
	}
}

// TestOverviewPlusRatioZeroTotal 验证：无账号时 plusRatio=0（不panic）。
func TestOverviewPlusRatioZeroTotal(t *testing.T) {
	svc := NewService(store.NewMockAccountStore(), store.NewMockEmailStore(), store.NewMockProxyStore())
	got, err := svc.Overview(context.Background())
	if err != nil {
		t.Fatalf("Overview() error = %v", err)
	}
	if got.Accounts.PlusRatio != 0 {
		t.Errorf("空账号 plusRatio 应 0, got %v", got.Accounts.PlusRatio)
	}
	if got.Accounts.Alive.Unchecked != 0 {
		t.Errorf("空账号 unchecked 应 0, got %d", got.Accounts.Alive.Unchecked)
	}
}
