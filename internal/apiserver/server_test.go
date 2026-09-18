package apiserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"gpt-go/internal/config"
	"gpt-go/internal/store/mongo"
)

func newTestServer(m *mongo.Manager) *Server {
	return NewServer(config.Default(), m, nil)
}

// TestHealthDegradedWhenOffline: a fresh manager is offline, so /api/health
// returns status "degraded" and mongodb.status "offline" (matches Python).
func TestHealthDegradedWhenOffline(t *testing.T) {
	m := mongo.NewManager("autoregister")
	srv := newTestServer(m)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if body["status"] != "degraded" {
		t.Errorf("status = %v, want degraded", body["status"])
	}
	if body["mode"] != "local" {
		t.Errorf("mode = %v, want local", body["mode"])
	}
	mongodb, ok := body["mongodb"].(map[string]any)
	if !ok {
		t.Fatalf("mongodb missing: %v", body["mongodb"])
	}
	if mongodb["status"] != "offline" {
		t.Errorf("mongodb.status = %v, want offline", mongodb["status"])
	}
	if mongodb["database"] != "autoregister" {
		t.Errorf("mongodb.database = %v, want autoregister", mongodb["database"])
	}
	if mongodb["error"] != nil {
		t.Errorf("mongodb.error = %v, want null", mongodb["error"])
	}
	if mongodb["nextRetrySeconds"] != nil {
		t.Errorf("mongodb.nextRetrySeconds = %v, want null", mongodb["nextRetrySeconds"])
	}
}

func TestHealthOKWhenOnline(t *testing.T) {
	m := mongo.NewManager("autoregister")
	m.MarkOnline()
	srv := newTestServer(m)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("status = %v, want ok", body["status"])
	}
	if mongodb := body["mongodb"].(map[string]any); mongodb["status"] != "online" {
		t.Errorf("mongodb.status = %v, want online", mongodb["status"])
	}
}

func TestHealthReconnecting(t *testing.T) {
	m := mongo.NewManager("autoregister")
	next := 5
	m.MarkReconnecting(errors.New("server selection timeout"), &next)
	srv := newTestServer(m)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body["status"] != "degraded" {
		t.Errorf("status = %v, want degraded", body["status"])
	}
	mongodb := body["mongodb"].(map[string]any)
	if mongodb["status"] != "reconnecting" {
		t.Errorf("mongodb.status = %v, want reconnecting", mongodb["status"])
	}
	if got, ok := mongodb["nextRetrySeconds"].(float64); !ok || int(got) != 5 {
		t.Errorf("mongodb.nextRetrySeconds = %v, want 5", mongodb["nextRetrySeconds"])
	}
}
