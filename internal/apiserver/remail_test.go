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

func newRemailServer() *Server {
	return NewServerWithAllStores(
		config.Default(),
		mongo.NewManager("autoregister"),
		store.NewMockEmailStore(),
		store.NewMockMailcodeStore(),
		store.NewMockRemailStore(),
	)
}

// TestRemailGetConfigDefault mirrors GET /api/remail/config default.
func TestRemailGetConfigDefault(t *testing.T) {
	s := newRemailServer()
	code, body := doJSONBody(t, s, http.MethodGet, "/api/remail/config", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if body["baseUrl"] != "https://remail.aishop6.com" {
		t.Errorf("baseUrl = %v", body["baseUrl"])
	}
	if body["apiKey"] != "" {
		t.Errorf("apiKey = %v, want empty", body["apiKey"])
	}
	if body["projectId"] != nil {
		t.Errorf("projectId = %v, want null", body["projectId"])
	}
	if body["updatedAt"] != nil {
		t.Errorf("updatedAt = %v, want null", body["updatedAt"])
	}
}

// TestRemailSaveConfig mirrors PUT /api/remail/config.
func TestRemailSaveConfig(t *testing.T) {
	s := newRemailServer()
	code, body := doJSONBody(t, s, http.MethodPut, "/api/remail/config",
		`{"apiKey":"rk-12345678","projectId":2,"emailSuffix":"@icloud.com","baseUrl":"https://remail.aishop6.com/"}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if body["apiKey"] != "rk-12345678" {
		t.Errorf("apiKey = %v", body["apiKey"])
	}
	if body["emailSuffix"] != "icloud.com" {
		t.Errorf("emailSuffix = %v, want icloud.com", body["emailSuffix"])
	}
	if body["baseUrl"] != "https://remail.aishop6.com" {
		t.Errorf("baseUrl = %v", body["baseUrl"])
	}
	if body["projectId"] != float64(2) {
		t.Errorf("projectId = %v, want 2", body["projectId"])
	}
	if body["updatedAt"] == nil {
		t.Errorf("updatedAt = nil, want set")
	}
}

// TestRemailSaveConfigInvalid mirrors Pydantic validation errors.
func TestRemailSaveConfigInvalid(t *testing.T) {
	s := newRemailServer()
	// apiKey too short (<8)
	code, _ := doJSONBody(t, s, http.MethodPut, "/api/remail/config",
		`{"apiKey":"rk","projectId":2,"emailSuffix":"icloud.com","baseUrl":"https://remail.aishop6.com"}`)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("short apiKey status = %d, want 422", code)
	}
	// projectId < 1
	code, _ = doJSONBody(t, s, http.MethodPut, "/api/remail/config",
		`{"apiKey":"rk-12345678","projectId":0,"emailSuffix":"icloud.com","baseUrl":"https://remail.aishop6.com"}`)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("projectId=0 status = %d, want 422", code)
	}
	// baseUrl not https
	code, _ = doJSONBody(t, s, http.MethodPut, "/api/remail/config",
		`{"apiKey":"rk-12345678","projectId":2,"emailSuffix":"icloud.com","baseUrl":"http://remail.aishop6.com"}`)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("http baseUrl status = %d, want 422", code)
	}
}

// TestRemailProbeRoute checks probe route shape.
func TestRemailProbeRoute(t *testing.T) {
	s := newRemailServer()
	// no apiKey saved -> probe uses empty key, real HTTP; assert shape only.
	code, body := doJSONBody(t, s, http.MethodPost, "/api/remail/probe",
		`{"apiKey":"rk-12345678"}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if _, ok := body["ok"]; !ok {
		t.Errorf("missing ok: %v", body)
	}
	if _, ok := body["reachable"]; !ok {
		t.Errorf("missing reachable: %v", body)
	}
	if _, ok := body["message"]; !ok {
		t.Errorf("missing message: %v", body)
	}
}

// TestRemailWalletRoute checks wallet route shape.
func TestRemailWalletRoute(t *testing.T) {
	s := newRemailServer()
	code, body := doJSONBody(t, s, http.MethodGet, "/api/remail/wallet", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if _, ok := body["ok"]; !ok {
		t.Errorf("missing ok: %v", body)
	}
	if _, ok := body["consumerBalance"]; !ok {
		t.Errorf("missing consumerBalance: %v", body)
	}
	if _, ok := body["totalRecharged"]; !ok {
		t.Errorf("missing totalRecharged: %v", body)
	}
	if _, ok := body["historicalSpend"]; !ok {
		t.Errorf("missing historicalSpend: %v", body)
	}
}

// TestRemailCreateMailboxesInvalidCount mirrors count validation.
func TestRemailCreateMailboxesInvalidCount(t *testing.T) {
	s := newRemailServer()
	code, _ := doJSONBody(t, s, http.MethodPost, "/api/remail/create-mailboxes",
		`{"count":0}`)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("count=0 status = %d, want 422", code)
	}
	code, _ = doJSONBody(t, s, http.MethodPost, "/api/remail/create-mailboxes",
		`{"count":1001}`)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("count=1001 status = %d, want 422", code)
	}
}

// TestRemailImportInvalidLimit mirrors limit validation.
func TestRemailImportInvalidLimit(t *testing.T) {
	s := newRemailServer()
	code, _ := doJSONBody(t, s, http.MethodPost, "/api/remail/import-purchased-orders",
		`{"limit":0}`)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("limit=0 status = %d, want 422", code)
	}
}

// TestRemailImportNoAPIKey mirrors the 400 remail_no_api_key when no API key saved.
func TestRemailImportNoAPIKey(t *testing.T) {
	s := newRemailServer()
	req := httptest.NewRequest(http.MethodPost, "/api/remail/import-purchased-orders",
		strings.NewReader(`{"limit":100}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	// No API key saved -> authHeaders raises remail_no_api_key (400).
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	detail, _ := body["detail"].(map[string]any)
	if detail == nil || detail["code"] != "remail_no_api_key" {
		t.Errorf("detail = %v, want code remail_no_api_key", body["detail"])
	}
}
