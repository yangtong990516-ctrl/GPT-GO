package apiserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gpt-go/internal/config"
	"gpt-go/internal/store"
	"gpt-go/internal/store/mongo"
)

func newAccountServer(as store.AccountStore) *Server {
	return NewServerWithAccountStore(
		config.Default(),
		mongo.NewManager("autoregister"),
		store.NewMockEmailStore(),
		as,
	)
}

func seedAccount(t *testing.T, as store.AccountStore, d store.AccountDocument) {
	t.Helper()
	if _, err := as.Create(nil, d); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

// TestAccountsListEmpty mirrors GET /api/accounts with no accounts.
func TestAccountsListEmpty(t *testing.T) {
	s := newAccountServer(store.NewMockAccountStore())
	code, body := doJSONBody(t, s, http.MethodGet, "/api/accounts?page=1&pageSize=10", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if body["total"] != float64(0) {
		t.Errorf("total = %v, want 0", body["total"])
	}
	arr, _ := body["items"].([]any)
	if arr == nil || len(arr) != 0 {
		t.Errorf("items = %v, want empty array", body["items"])
	}
}

// TestAccountsCreateAndList mirrors POST /api/accounts (201) + GET list.
func TestAccountsCreateAndList(t *testing.T) {
	as := store.NewMockAccountStore()
	s := newAccountServer(as)

	req := httptest.NewRequest(http.MethodPost, "/api/accounts",
		strings.NewReader(`{"email":"Test@x.com","chatgptPassword":"pw123","totpSecret":"SECRET","emailAccessUrl":"https://mail.example.com/api/mail?email=test@x.com","accountType":"free"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if created["email"] != "test@x.com" {
		t.Errorf("email = %v, want test@x.com (lowercased)", created["email"])
	}
	if created["accountType"] != "free" {
		t.Errorf("accountType = %v", created["accountType"])
	}
	if created["totpSecretConfigured"] != true {
		t.Errorf("totpSecretConfigured = %v, want true", created["totpSecretConfigured"])
	}
	// 24 fields present
	for _, f := range []string{"id", "email", "chatgptPassword", "totpSecret", "totpStatus", "totpSecretConfigured", "emailAccessUrl", "createdAt", "accountType", "phoneBound", "accessTokenConfigured", "accessTokenExpiresAt", "accessTokenUpdatedAt", "refreshToken", "accessTokenMissing", "atRefillStatus", "atRefillError", "atRefillErrorAt", "planCheckStatus", "planCheckedAt", "planCheckErrorCode", "subscriptionPlan", "planExpiresAt", "promotionCampaigns", "trialScanCheckedAt", "trialScanCountries", "trialScanError", "registrationCountry", "registrationIp", "registrationIsp", "aliveStatus", "aliveCheckedAt", "aliveErrorCode", "aliveHttpStatus", "rebindStatus", "remark", "promotionEligible"} {
		if _, ok := created[f]; !ok {
			t.Errorf("missing field %q in created record", f)
		}
	}

	// list
	code2, body := doJSONBody(t, s, http.MethodGet, "/api/accounts?page=1&pageSize=10", "")
	if code2 != http.StatusOK {
		t.Fatalf("list status = %d", code2)
	}
	if body["total"] != float64(1) {
		t.Errorf("total = %v, want 1", body["total"])
	}
}

// TestAccountsPromotionFilter mirrors GET /api/accounts?promotion=untried_plus.
func TestAccountsPromotionFilter(t *testing.T) {
	as := store.NewMockAccountStore()
	seedAccount(t, as, store.AccountDocument{ID: "1", Email: "a@x.com", EmailNormalized: "a@x.com", AccountType: "free", PromotionEligible: boolPtr(true)})
	seedAccount(t, as, store.AccountDocument{ID: "2", Email: "b@x.com", EmailNormalized: "b@x.com", AccountType: "plus", PromotionEligible: boolPtr(true)})
	s := newAccountServer(as)

	code, body := doJSONBody(t, s, http.MethodGet, "/api/accounts?page=1&pageSize=10&promotion=untried_plus", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if body["total"] != float64(1) {
		t.Errorf("total = %v, want 1 (only free+eligible)", body["total"])
	}
}

// TestAccountsBulkDelete mirrors POST /api/accounts/bulk-delete.
func TestAccountsBulkDelete(t *testing.T) {
	as := store.NewMockAccountStore()
	seedAccount(t, as, store.AccountDocument{ID: "1", Email: "a@x.com", EmailNormalized: "a@x.com"})
	seedAccount(t, as, store.AccountDocument{ID: "2", Email: "b@x.com", EmailNormalized: "b@x.com"})
	s := newAccountServer(as)

	code, body := doJSONBody(t, s, http.MethodPost, "/api/accounts/bulk-delete", `{"ids":["1"]}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if body["deleted"] != float64(1) {
		t.Errorf("deleted = %v, want 1", body["deleted"])
	}
}

// TestAccountsCreateDuplicate mirrors 409 on duplicate email.
func TestAccountsCreateDuplicate(t *testing.T) {
	as := store.NewMockAccountStore()
	seedAccount(t, as, store.AccountDocument{ID: "1", Email: "a@x.com", EmailNormalized: "a@x.com"})
	s := newAccountServer(as)

	req := httptest.NewRequest(http.MethodPost, "/api/accounts",
		strings.NewReader(`{"email":"A@x.com","chatgptPassword":"pw","totpSecret":"s","emailAccessUrl":"u"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

// TestAccountsCreateInvalid mirrors 422 on validation.
func TestAccountsCreateInvalid(t *testing.T) {
	s := newAccountServer(store.NewMockAccountStore())
	// email too short (<3)
	code, _ := doJSONBody(t, s, http.MethodPost, "/api/accounts",
		`{"email":"a","chatgptPassword":"pw","totpSecret":"s","emailAccessUrl":"u"}`)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", code)
	}
	// invalid accountType
	code, _ = doJSONBody(t, s, http.MethodPost, "/api/accounts",
		`{"email":"a@x.com","chatgptPassword":"pw","totpSecret":"s","emailAccessUrl":"u","accountType":"premium"}`)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 for bad accountType", code)
	}
}

// TestStatsOverview mirrors GET /api/stats/overview (account block).
func TestStatsOverview(t *testing.T) {
	as := store.NewMockAccountStore()
	seedAccount(t, as, store.AccountDocument{ID: "1", Email: "a@x.com", EmailNormalized: "a@x.com", AccountType: "plus", PhoneBound: boolPtr(true), TotpSecret: "s"})
	seedAccount(t, as, store.AccountDocument{ID: "2", Email: "b@x.com", EmailNormalized: "b@x.com", AccountType: "free", PromotionEligible: boolPtr(true), TotpSecret: "s"})
	s := newAccountServer(as)

	code, body := doJSONBody(t, s, http.MethodGet, "/api/stats/overview", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	accounts, _ := body["accounts"].(map[string]any)
	if accounts == nil {
		t.Fatalf("accounts block missing: %v", body)
	}
	if accounts["total"] != float64(2) {
		t.Errorf("accounts.total = %v, want 2", accounts["total"])
	}
	plus, _ := accounts["plus"].(map[string]any)
	if plus["total"] != float64(1) || plus["bound"] != float64(1) {
		t.Errorf("plus = %v", plus)
	}
	free, _ := accounts["free"].(map[string]any)
	if free["total"] != float64(1) || free["eligible"] != float64(1) {
		t.Errorf("free = %v", free)
	}
}

func boolPtr(b bool) *bool { return &b }
