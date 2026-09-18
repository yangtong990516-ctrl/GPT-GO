package apiserver

import (
	"net/http"
	"testing"

	"gpt-go/internal/config"
	"gpt-go/internal/store/mongo"
)

func newSentinelServer() *Server {
	return NewServer(config.Default(), mongo.NewManager("autoregister"), nil)
}

// TestSentinelGetConfigDefault mirrors GET /api/sentinel/config with no saved config.
func TestSentinelGetConfigDefault(t *testing.T) {
	s := newSentinelServer()
	code, body := doJSONBody(t, s, http.MethodGet, "/api/sentinel/config", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if body["enabled"] != true {
		t.Errorf("enabled = %v, want true", body["enabled"])
	}
	// interval_hours is snake_case (sentinel module preserves the Python dict keys).
	if body["interval_hours"] != float64(24) {
		t.Errorf("interval_hours = %v, want 24", body["interval_hours"])
	}
	if body["proxy"] != "" {
		t.Errorf("proxy = %v, want empty", body["proxy"])
	}
	// No error field on success.
	if _, ok := body["error"]; ok {
		t.Errorf("unexpected error field: %v", body["error"])
	}
}

// TestSentinelSaveConfig mirrors PUT /api/sentinel/config.
func TestSentinelSaveConfig(t *testing.T) {
	s := newSentinelServer()
	defer s.StopBackground()

	code, body := doJSONBody(t, s, http.MethodPut, "/api/sentinel/config",
		`{"enabled": false, "interval_hours": 720, "proxy": " http://127.0.0.1:7890 "}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%v)", code, body)
	}
	if body["enabled"] != false {
		t.Errorf("enabled = %v, want false", body["enabled"])
	}
	if body["interval_hours"] != float64(720) {
		t.Errorf("interval_hours = %v, want 720", body["interval_hours"])
	}
	if body["proxy"] != "http://127.0.0.1:7890" {
		t.Errorf("proxy = %v, want stripped", body["proxy"])
	}

	// Reload reflects the saved config.
	code, body = doJSONBody(t, s, http.MethodGet, "/api/sentinel/config", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if body["enabled"] != false {
		t.Errorf("reload enabled = %v, want false", body["enabled"])
	}
}

// TestSentinelVersionDefault mirrors GET /api/sentinel/version with no check yet.
func TestSentinelVersionDefault(t *testing.T) {
	s := newSentinelServer()
	code, body := doJSONBody(t, s, http.MethodGet, "/api/sentinel/version", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if body["version"] != "20260810913b" {
		t.Errorf("version = %v, want 20260810913b", body["version"])
	}
	if body["configured_version"] != "20260810913b" {
		t.Errorf("configured_version = %v", body["configured_version"])
	}
	if body["reachable"] != false {
		t.Errorf("reachable = %v, want false", body["reachable"])
	}
	// url/etag/is_expired are null before any check runs.
	if body["url"] != nil {
		t.Errorf("url = %v, want null", body["url"])
	}
	if body["is_expired"] != nil {
		t.Errorf("is_expired = %v, want null", body["is_expired"])
	}
	if body["proxy_used"] != false {
		t.Errorf("proxy_used = %v, want false", body["proxy_used"])
	}
}
