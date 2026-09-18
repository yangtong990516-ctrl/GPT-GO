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

func newMailcodeServer() *Server {
	return NewServerWithStores(
		config.Default(),
		mongo.NewManager("autoregister"),
		store.NewMockEmailStore(),
		store.NewMockMailcodeStore(),
	)
}

func doJSONBody(t *testing.T, s *Server, method, path, body string) (int, map[string]any) {
	t.Helper()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	var out map[string]any
	if len(rec.Body.Bytes()) > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
	}
	return rec.Code, out
}

// TestMailcodeGetConfigDefault mirrors GET /api/mailcode/config with no saved config.
func TestMailcodeGetConfigDefault(t *testing.T) {
	s := newMailcodeServer()
	code, body := doJSONBody(t, s, http.MethodGet, "/api/mailcode/config", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if body["baseUrl"] != "https://mail.example.com" {
		t.Errorf("baseUrl = %v, want default", body["baseUrl"])
	}
	if body["domain"] != "example.com" {
		t.Errorf("domain = %v, want example.com", body["domain"])
	}
	if body["updatedAt"] != nil {
		t.Errorf("updatedAt = %v, want null", body["updatedAt"])
	}
}

// TestMailcodeSaveConfig mirrors PUT /api/mailcode/config.
func TestMailcodeSaveConfig(t *testing.T) {
	s := newMailcodeServer()
	code, body := doJSONBody(t, s, http.MethodPut, "/api/mailcode/config",
		`{"baseUrl":"https://mail.liwei-inc.com/","domain":"@liwei-inc.com"}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if body["baseUrl"] != "https://mail.liwei-inc.com" {
		t.Errorf("baseUrl = %v, want stripped", body["baseUrl"])
	}
	if body["domain"] != "liwei-inc.com" {
		t.Errorf("domain = %v, want liwei-inc.com", body["domain"])
	}
	if body["updatedAt"] == nil {
		t.Errorf("updatedAt = nil, want set")
	}
}

// TestMailcodeSaveConfigInvalidBaseURL mirrors Pydantic validation: must start https://.
func TestMailcodeSaveConfigInvalidBaseURL(t *testing.T) {
	s := newMailcodeServer()
	code, _ := doJSONBody(t, s, http.MethodPut, "/api/mailcode/config",
		`{"baseUrl":"http://mail.example.com","domain":"example.com"}`)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", code)
	}
}

// TestMailcodeProbeOK mirrors POST /api/mailcode/probe (with real HTTP will hit
// the injected httpGet; here we only check the route shape via a 200 mock is
// exercised through the service layer tests).
func TestMailcodeProbeRoute(t *testing.T) {
	s := newMailcodeServer()
	code, body := doJSONBody(t, s, http.MethodPost, "/api/mailcode/probe",
		`{"baseUrl":"https://mail.example.com"}`)
	// The default httpGet actually dials out; in a hermetic test env the result
	// is non-deterministic, so we only assert a valid JSON response with the
	// expected shape (ok/reachable/message present).
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if _, ok := body["ok"]; !ok {
		t.Errorf("missing 'ok' field in %v", body)
	}
	if _, ok := body["reachable"]; !ok {
		t.Errorf("missing 'reachable' field in %v", body)
	}
	if _, ok := body["message"]; !ok {
		t.Errorf("missing 'message' field in %v", body)
	}
}

// TestMailcodeCreateMailboxesArray decodes the array response directly.
func TestMailcodeCreateMailboxesArray(t *testing.T) {
	s := newMailcodeServer()
	_, _ = doJSONBody(t, s, http.MethodPut, "/api/mailcode/config",
		`{"baseUrl":"https://mail.liwei-inc.com","domain":"liwei-inc.com"}`)

	req := httptest.NewRequest(http.MethodPost, "/api/mailcode/create-mailboxes",
		strings.NewReader(`{"emails":["a@liwei-inc.com"],"count":0}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var records []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &records); err != nil {
		t.Fatalf("invalid JSON array: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("len = %d, want 1", len(records))
	}
	r := records[0]
	if r["email"] != "a@liwei-inc.com" {
		t.Errorf("email = %v, want a@liwei-inc.com", r["email"])
	}
	if r["imported"] != true {
		t.Errorf("imported = %v, want true", r["imported"])
	}
	if r["duplicate"] != false {
		t.Errorf("duplicate = %v, want false", r["duplicate"])
	}
	if r["error"] != nil {
		t.Errorf("error = %v, want null", r["error"])
	}
}

// TestMailcodeCreateMailboxesInvalidCount mirrors count validation (0..200).
func TestMailcodeCreateMailboxesInvalidCount(t *testing.T) {
	s := newMailcodeServer()
	code, _ := doJSONBody(t, s, http.MethodPost, "/api/mailcode/create-mailboxes",
		`{"count":201}`)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", code)
	}
}
