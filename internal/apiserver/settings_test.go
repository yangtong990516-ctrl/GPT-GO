package apiserver

import (
	"net/http"
	"path/filepath"
	"testing"

	"gpt-go/internal/config"
	"gpt-go/internal/service/settings"
	"gpt-go/internal/store"
	"gpt-go/internal/store/mongo"
)

// newSettingsServer builds a Server whose settings store points at a temp file.
func newSettingsServer(t *testing.T) *Server {
	t.Helper()
	s := NewServer(config.Default(), mongo.NewManager("autoregister"), nil)
	ss := store.NewSettingsStore(filepath.Join(t.TempDir(), "settings.json"))
	s.settingsStore = ss
	s.settingsSvc = settings.NewService(ss)
	return s
}

// TestSettingsGetDefault mirrors GET /api/settings/execution with no saved file.
func TestSettingsGetDefault(t *testing.T) {
	s := newSettingsServer(t)
	code, body := doJSON(t, s, http.MethodGet, "/api/settings/execution", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if body["registrationMode"] != "protocol" {
		t.Errorf("registrationMode = %v, want protocol", body["registrationMode"])
	}
	if body["proxyCheckConcurrency"] != float64(16) {
		t.Errorf("proxyCheckConcurrency = %v, want 16", body["proxyCheckConcurrency"])
	}
	if body["concurrency"] != float64(2) {
		t.Errorf("concurrency = %v, want 2", body["concurrency"])
	}
}

// TestSettingsPutRoundTrip mirrors PUT then GET, verifying the merged
// enableRegistrationSecurity switch + numeric fields persist round-trip.
func TestSettingsPutRoundTrip(t *testing.T) {
	s := newSettingsServer(t)
	payload := `{"enableRegistrationSecurity":true,"registrationMode":"protocol",` +
		`"proxyRetryCount":3,"proxyCheckConcurrency":20,"maxRegistrationsPerExitIp":5,` +
		`"concurrency":4,"taskTimeoutSeconds":120}`
	code, body := doJSON(t, s, http.MethodPut, "/api/settings/execution", payload)
	if code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200", code)
	}
	if body["enableRegistrationSecurity"] != true {
		t.Errorf("enableRegistrationSecurity = %v, want true", body["enableRegistrationSecurity"])
	}

	code2, body2 := doJSON(t, s, http.MethodGet, "/api/settings/execution", "")
	if code2 != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", code2)
	}
	if body2["enableRegistrationSecurity"] != true {
		t.Errorf("round-trip lost enableRegistrationSecurity: %v", body2)
	}
	if body2["concurrency"] != float64(4) {
		t.Errorf("concurrency = %v, want 4", body2["concurrency"])
	}
}

// TestSettingsPutValidation mirrors the pydantic Field constraints (422).
func TestSettingsPutValidation(t *testing.T) {
	s := newSettingsServer(t)
	code, body := doJSON(t, s, http.MethodPut, "/api/settings/execution",
		`{"concurrency":99,"proxyRetryCount":1,"proxyCheckConcurrency":16,"maxRegistrationsPerExitIp":0,"taskTimeoutSeconds":0}`)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", code)
	}
	if body["detail"] == nil {
		t.Fatalf("detail = nil, want validation error")
	}
}
