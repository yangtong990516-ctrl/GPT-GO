package account

import (
	"context"
	"net/http"
	"testing"
	"time"

	"gpt-go/internal/model"
	"gpt-go/internal/store"
	"gpt-go/internal/util"
)

func now() time.Time { return time.Now().UTC() }

func seedDoc(t *testing.T, s store.AccountStore, d store.AccountDocument) {
	t.Helper()
	if _, err := s.Create(context.Background(), d); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func boolPtr(b bool) *bool    { return &b }
func strPtr(s string) *string { return &s }

// TestListPromotionUntriedPlus mirrors promotion=untried_plus filter:
// accountType=free && promotionEligible=true.
func TestListPromotionUntriedPlus(t *testing.T) {
	s := store.NewMockAccountStore()
	// free + eligible=true -> matches untried_plus
	seedDoc(t, s, store.AccountDocument{
		ID: "1", Email: "a@x.com", EmailNormalized: "a@x.com", AccountType: "free",
		PromotionEligible: boolPtr(true), CreatedAt: now(),
	})
	// plus + eligible=true -> NOT untried_plus (accountType != free)
	seedDoc(t, s, store.AccountDocument{
		ID: "2", Email: "b@x.com", EmailNormalized: "b@x.com", AccountType: "plus",
		PromotionEligible: boolPtr(true), CreatedAt: now(),
	})
	// free + eligible=false -> NOT untried_plus
	seedDoc(t, s, store.AccountDocument{
		ID: "3", Email: "c@x.com", EmailNormalized: "c@x.com", AccountType: "free",
		PromotionEligible: boolPtr(false), CreatedAt: now(),
	})
	// free + eligible=nil -> NOT untried_plus
	seedDoc(t, s, store.AccountDocument{
		ID: "4", Email: "d@x.com", EmailNormalized: "d@x.com", AccountType: "free",
		PromotionEligible: nil, CreatedAt: now(),
	})

	svc := NewService(s)
	result, err := svc.List(context.Background(), model.AccountListFilter{Promotion: model.PromotionUntriedPlus}, 1, 10)
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	if result.Total != 1 {
		t.Fatalf("Total = %d, want 1", result.Total)
	}
	if len(result.Items) != 1 || result.Items[0].ID != "1" {
		t.Errorf("items = %v, want only ID=1", result.Items)
	}
}

// TestListPromotionIneligible mirrors promotion=ineligible (eligible=false).
func TestListPromotionIneligible(t *testing.T) {
	s := store.NewMockAccountStore()
	seedDoc(t, s, store.AccountDocument{ID: "1", Email: "a@x.com", EmailNormalized: "a@x.com", AccountType: "free", PromotionEligible: boolPtr(false), CreatedAt: now()})
	seedDoc(t, s, store.AccountDocument{ID: "2", Email: "b@x.com", EmailNormalized: "b@x.com", AccountType: "free", PromotionEligible: boolPtr(true), CreatedAt: now()})

	svc := NewService(s)
	result, err := svc.List(context.Background(), model.AccountListFilter{Promotion: model.PromotionIneligible}, 1, 10)
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	if result.Total != 1 || result.Items[0].ID != "1" {
		t.Errorf("total=%d items=%v, want ID=1", result.Total, result.Items)
	}
}

// TestListPromotionUnchecked mirrors promotion=unchecked (eligible=nil).
func TestListPromotionUnchecked(t *testing.T) {
	s := store.NewMockAccountStore()
	seedDoc(t, s, store.AccountDocument{ID: "1", Email: "a@x.com", EmailNormalized: "a@x.com", AccountType: "free", PromotionEligible: nil, CreatedAt: now()})
	seedDoc(t, s, store.AccountDocument{ID: "2", Email: "b@x.com", EmailNormalized: "b@x.com", AccountType: "free", PromotionEligible: boolPtr(true), CreatedAt: now()})

	svc := NewService(s)
	result, err := svc.List(context.Background(), model.AccountListFilter{Promotion: model.PromotionUnchecked}, 1, 10)
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	if result.Total != 1 || result.Items[0].ID != "1" {
		t.Errorf("total=%d items=%v, want ID=1", result.Total, result.Items)
	}
}

// TestAccountRecordMapping mirrors _account_record: totpSecretConfigured derived,
// emailAccessUrl rewritten (non-api798 unchanged), promotionCampaigns -> [].
func TestAccountRecordMapping(t *testing.T) {
	s := store.NewMockAccountStore()
	campaigns := []model.PromotionCampaign{
		{Plan: "plus", ID: "plus-1-month-free", PromotionType: "free_trial", Title: "Free Trial 1 Month"},
	}
	seedDoc(t, s, store.AccountDocument{
		ID: "1", Email: "a@x.com", EmailNormalized: "a@x.com",
		ChatgptPassword: "pw", TotpSecret: "secret", EmailAccessURL: "https://mail.example.com/api/mail?email=a@x.com",
		AccountType: "free", PromotionCampaigns: campaigns,
		CreatedAt: now(), RegistrationCountry: strPtr("US"),
	})

	svc := NewService(s)
	result, err := svc.List(context.Background(), model.AccountListFilter{}, 1, 10)
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	if result.Total != 1 {
		t.Fatalf("total = %d", result.Total)
	}
	r := result.Items[0]
	if r.TotpSecretConfigured != true {
		t.Errorf("TotpSecretConfigured = %v, want true", r.TotpSecretConfigured)
	}
	if len(r.PromotionCampaigns) != 1 {
		t.Fatalf("promotionCampaigns = %v, want 1", r.PromotionCampaigns)
	}
	c := r.PromotionCampaigns[0]
	if c.ID != "plus-1-month-free" || c.Plan != "plus" || c.Title != "Free Trial 1 Month" {
		t.Errorf("campaign = %+v", c)
	}
	// CampaignLabel mapping
	if model.CampaignLabel["plus-1-month-free"] != "免费试用 1 个月" {
		t.Errorf("CampaignLabel wrong: %q", model.CampaignLabel["plus-1-month-free"])
	}
	if model.CampaignLabel["plus-1-month-50-pct-off"] != "首月 5 折" {
		t.Errorf("CampaignLabel 首月5折 wrong: %q", model.CampaignLabel["plus-1-month-50-pct-off"])
	}
}

// TestAccountRecordTotpNotConfigured mirrors totpSecretConfigured=false when empty.
func TestAccountRecordTotpNotConfigured(t *testing.T) {
	s := store.NewMockAccountStore()
	seedDoc(t, s, store.AccountDocument{ID: "1", Email: "a@x.com", EmailNormalized: "a@x.com", TotpSecret: "", CreatedAt: now()})
	svc := NewService(s)
	result, _ := svc.List(context.Background(), model.AccountListFilter{}, 1, 10)
	if result.Items[0].TotpSecretConfigured {
		t.Errorf("TotpSecretConfigured = true, want false (empty totpSecret)")
	}
}

// TestCreateDuplicate mirrors DuplicateKeyError -> 409.
func TestCreateDuplicate(t *testing.T) {
	s := store.NewMockAccountStore()
	seedDoc(t, s, store.AccountDocument{ID: "1", Email: "a@x.com", EmailNormalized: "a@x.com", CreatedAt: now()})
	svc := NewService(s)
	_, err := svc.Create(context.Background(), model.AccountCreate{
		Email: "A@x.com", ChatgptPassword: "pw", TotpSecret: "s", EmailAccessURL: "u",
	})
	if err == nil {
		t.Fatalf("expected duplicate error")
	}
	me, ok := err.(*util.HTTPError)
	if !ok || me.Status != http.StatusConflict {
		t.Errorf("err = %v, want 409 duplicate", err)
	}
}

// TestListAliveFilter mirrors alive query param (alive/dead/unknown/unchecked).
func TestListAliveFilter(t *testing.T) {
	s := store.NewMockAccountStore()
	seedDoc(t, s, store.AccountDocument{ID: "1", Email: "a@x.com", EmailNormalized: "a@x.com", AliveStatus: strPtr("alive"), CreatedAt: now()})
	seedDoc(t, s, store.AccountDocument{ID: "2", Email: "b@x.com", EmailNormalized: "b@x.com", AliveStatus: strPtr("dead"), CreatedAt: now()})
	seedDoc(t, s, store.AccountDocument{ID: "3", Email: "c@x.com", EmailNormalized: "c@x.com", AliveStatus: nil, CreatedAt: now()})

	svc := NewService(s)
	r1, _ := svc.List(context.Background(), model.AccountListFilter{Alive: "alive"}, 1, 10)
	if r1.Total != 1 || r1.Items[0].ID != "1" {
		t.Errorf("alive filter: total=%d items=%v, want ID=1", r1.Total, r1.Items)
	}
	r2, _ := svc.List(context.Background(), model.AccountListFilter{Alive: "dead"}, 1, 10)
	if r2.Total != 1 || r2.Items[0].ID != "2" {
		t.Errorf("dead filter: total=%d items=%v, want ID=2", r2.Total, r2.Items)
	}
	r3, _ := svc.List(context.Background(), model.AccountListFilter{Alive: "unchecked"}, 1, 10)
	if r3.Total != 1 || r3.Items[0].ID != "3" {
		t.Errorf("unchecked filter: total=%d items=%v, want ID=3", r3.Total, r3.Items)
	}
}

// TestStats mirrors overview_stats account block.
func TestStats(t *testing.T) {
	s := store.NewMockAccountStore()
	seedDoc(t, s, store.AccountDocument{ID: "1", Email: "a@x.com", EmailNormalized: "a@x.com", AccountType: "plus", PhoneBound: boolPtr(true), TotpSecret: "s", CreatedAt: now()})
	seedDoc(t, s, store.AccountDocument{ID: "2", Email: "b@x.com", EmailNormalized: "b@x.com", AccountType: "plus", PhoneBound: boolPtr(false), TotpSecret: "", CreatedAt: now()})
	seedDoc(t, s, store.AccountDocument{ID: "3", Email: "c@x.com", EmailNormalized: "c@x.com", AccountType: "free", PromotionEligible: boolPtr(true), TotpSecret: "s", CreatedAt: now()})
	seedDoc(t, s, store.AccountDocument{ID: "4", Email: "d@x.com", EmailNormalized: "d@x.com", AccountType: "free", PromotionEligible: boolPtr(false), TotpSecret: "", CreatedAt: now()})

	svc := NewService(s)
	st, err := svc.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats error: %v", err)
	}
	if st.Total != 4 {
		t.Errorf("Total = %d, want 4", st.Total)
	}
	if st.Today != 4 {
		t.Errorf("Today = %d, want 4 (all seeded with now())", st.Today)
	}
	if st.TotpComplete != 2 {
		t.Errorf("TotpComplete = %d, want 2 (ids 1,3)", st.TotpComplete)
	}
	if st.Plus.Total != 2 || st.Plus.Bound != 1 || st.Plus.Unbound != 1 {
		t.Errorf("Plus = %+v, want total=2 bound=1 unbound=1", st.Plus)
	}
	if st.Free.Total != 2 || st.Free.Eligible != 1 || st.Free.Ineligible != 1 {
		t.Errorf("Free = %+v, want total=2 eligible=1 ineligible=1", st.Free)
	}
}

// TestDelete mirrors delete_accounts.
func TestDelete(t *testing.T) {
	s := store.NewMockAccountStore()
	seedDoc(t, s, store.AccountDocument{ID: "1", Email: "a@x.com", EmailNormalized: "a@x.com", CreatedAt: now()})
	seedDoc(t, s, store.AccountDocument{ID: "2", Email: "b@x.com", EmailNormalized: "b@x.com", CreatedAt: now()})
	svc := NewService(s)
	res, err := svc.Delete(context.Background(), []string{"1"})
	if err != nil {
		t.Fatalf("Delete error: %v", err)
	}
	if res.Deleted != 1 {
		t.Errorf("Deleted = %d, want 1", res.Deleted)
	}
}
