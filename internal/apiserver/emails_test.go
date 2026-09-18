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

func newEmailServer(st store.EmailStore) *Server {
	return NewServerWithEmailStore(config.Default(), mongo.NewManager("autoregister"), st)
}

func doJSON(t *testing.T, s *Server, method, path, body string) (int, map[string]any) {
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
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("invalid JSON response: %v", err)
		}
	}
	return rec.Code, out
}

// TestEmailsListEmpty: GET /api/emails returns empty page with correct shape.
func TestEmailsListEmpty(t *testing.T) {
	s := newEmailServer(store.NewMockEmailStore())
	code, body := doJSON(t, s, http.MethodGet, "/api/emails", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if body["total"] != float64(0) {
		t.Errorf("total = %v, want 0", body["total"])
	}
	if body["page"] != float64(1) {
		t.Errorf("page = %v, want 1", body["page"])
	}
	if body["pageSize"] != float64(10) {
		t.Errorf("pageSize = %v, want 10", body["pageSize"])
	}
	if items, ok := body["items"].([]any); !ok || len(items) != 0 {
		t.Errorf("items = %v, want []", body["items"])
	}
}

// TestEmailsListInvalidSource: source=remail is rejected (route pattern excludes it).
func TestEmailsListInvalidSource(t *testing.T) {
	s := newEmailServer(store.NewMockEmailStore())
	code, body := doJSON(t, s, http.MethodGet, "/api/emails?source=remail", "")
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", code)
	}
	detail := body["detail"].(map[string]any)
	if detail["code"] != "invalid_source" {
		t.Errorf("code = %v, want invalid_source", detail["code"])
	}
}

// TestEmailsImportAndList: import then list, verify round-trip.
func TestEmailsImportAndList(t *testing.T) {
	s := newEmailServer(store.NewMockEmailStore())

	code, body := doJSON(t, s, http.MethodPost, "/api/emails/import",
		`{"rawText":"alice@example.com----https://mail.example.com/get_code?email=alice@example.com"}`)
	if code != http.StatusOK {
		t.Fatalf("import status = %d, want 200", code)
	}
	if body["imported"] != float64(1) {
		t.Errorf("imported = %v, want 1", body["imported"])
	}

	code, body = doJSON(t, s, http.MethodGet, "/api/emails?status=available", "")
	if code != http.StatusOK {
		t.Fatalf("list status = %d", code)
	}
	if body["total"] != float64(1) {
		t.Errorf("total = %v, want 1", body["total"])
	}
}

// TestEmailsBulkDelete: delete then verify count.
func TestEmailsBulkDelete(t *testing.T) {
	st := store.NewMockEmailStore()
	st.Upsert(t.Context(), store.EmailDocument{ID: "1", Email: "a@x.com", EmailNormalized: "a@x.com", AccessURL: "https://x", Status: "available"})
	st.Upsert(t.Context(), store.EmailDocument{ID: "2", Email: "b@x.com", EmailNormalized: "b@x.com", AccessURL: "https://x", Status: "available"})
	s := newEmailServer(st)

	code, body := doJSON(t, s, http.MethodPost, "/api/emails/bulk-delete", `{"ids":["1"]}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if body["deleted"] != float64(1) {
		t.Errorf("deleted = %v, want 1", body["deleted"])
	}
}

// TestEmailsResetFailed: reset-failed returns modified count under "deleted".
func TestEmailsResetFailed(t *testing.T) {
	st := store.NewMockEmailStore()
	st.Upsert(t.Context(), store.EmailDocument{ID: "1", Email: "a@x.com", EmailNormalized: "a@x.com", AccessURL: "https://x", Status: "failed"})
	st.Upsert(t.Context(), store.EmailDocument{ID: "2", Email: "b@x.com", EmailNormalized: "b@x.com", AccessURL: "https://x", Status: "failed"})
	st.Upsert(t.Context(), store.EmailDocument{ID: "3", Email: "c@x.com", EmailNormalized: "c@x.com", AccessURL: "https://x", Status: "available"})
	s := newEmailServer(st)

	code, body := doJSON(t, s, http.MethodPost, "/api/emails/reset-failed", `{"ids":["1"]}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if body["deleted"] != float64(1) {
		t.Errorf("deleted = %v, want 1 (only id 1 failed)", body["deleted"])
	}
}

// TestEmailsUpdateStatusNotFound: 404 with resource_not_found.
func TestEmailsUpdateStatusNotFound(t *testing.T) {
	s := newEmailServer(store.NewMockEmailStore())
	code, body := doJSON(t, s, http.MethodPost, "/api/emails/nope/status", `{"status":"failed"}`)
	if code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", code)
	}
	detail := body["detail"].(map[string]any)
	if detail["code"] != "resource_not_found" {
		t.Errorf("code = %v, want resource_not_found", detail["code"])
	}
	if detail["message"] != "邮箱不存在" {
		t.Errorf("message = %v, want 邮箱不存在", detail["message"])
	}
}

// TestEmailsExportNoIDs: 422 when scope != all and no ids.
func TestEmailsExportNoIDs(t *testing.T) {
	s := newEmailServer(store.NewMockEmailStore())
	code, body := doJSON(t, s, http.MethodPost, "/api/emails/export", `{"scope":"selected","ids":[]}`)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", code)
	}
	detail := body["detail"].(map[string]any)
	if detail["code"] != "ids_required" {
		t.Errorf("code = %v, want ids_required", detail["code"])
	}
}
