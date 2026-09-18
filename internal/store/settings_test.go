package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"gpt-go/internal/model"
)

// TestSettingsDefault mirrors settings_store.load() when the file is absent.
func TestSettingsDefault(t *testing.T) {
	s := NewSettingsStore(filepath.Join(t.TempDir(), "settings.json"))
	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := model.DefaultExecutionSettings()
	if got.Concurrency != want.Concurrency || got.ProxyCheckConcurrency != want.ProxyCheckConcurrency {
		t.Errorf("defaults = %+v, want %+v", got, want)
	}
	if got.SchemaVersion != 2 {
		t.Errorf("schemaVersion = %d, want 2", got.SchemaVersion)
	}
	if got.RegistrationMode != "protocol" {
		t.Errorf("registrationMode = %q, want protocol", got.RegistrationMode)
	}
}

// TestSettingsSaveLoadRoundTrip mirrors settings_store.save() then load(): every
// field (including requireTrialOnCheck / autoMultiCountryProbe, which the Python
// reference dropped) persists and round-trips.
func TestSettingsSaveLoadRoundTrip(t *testing.T) {
	s := NewSettingsStore(filepath.Join(t.TempDir(), "settings.json"))
	in := model.ExecutionSettingsInput{
		RequireRegistrationPassword: true,
		EnableRegistrationTotp:      false,
		RequireTrialOnCheck:         true,
		AutoMultiCountryProbe:       true,
		RegistrationMode:            "protocol",
		ProxyRetryCount:             3,
		ProxyCheckConcurrency:       20,
		MaxRegistrationsPerExitIP:   5,
		Concurrency:                 4,
		TaskTimeoutSeconds:          120,
	}
	_, err := s.Save(in)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !got.RequireTrialOnCheck {
		t.Errorf("requireTrialOnCheck = false, want true (Python dropped this field)")
	}
	if !got.AutoMultiCountryProbe {
		t.Errorf("autoMultiCountryProbe = false, want true")
	}
	if got.ProxyRetryCount != 3 || got.ProxyCheckConcurrency != 20 || got.MaxRegistrationsPerExitIP != 5 || got.Concurrency != 4 || got.TaskTimeoutSeconds != 120 {
		t.Errorf("numeric fields not round-tripped: %+v", got)
	}
	if got.UpdatedAt == nil {
		t.Errorf("updatedAt = nil, want set")
	}
}

// TestSettingsMigrateV1 mirrors _validate_or_migrate for schemaVersion 1.
func TestSettingsMigrateV1(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	legacy := map[string]any{
		"schemaVersion":      1,
		"concurrency":        5,
		"taskTimeoutSeconds": 300,
	}
	raw, _ := json.Marshal(legacy)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewSettingsStore(path)
	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Concurrency != 5 || got.TaskTimeoutSeconds != 300 {
		t.Errorf("v1 migration = %+v, want concurrency=5 timeout=300", got)
	}
	if got.RegistrationMode != "protocol" || got.ProxyRetryCount != 1 {
		t.Errorf("v1 migration defaults wrong: %+v", got)
	}
}

// TestSettingsMigrateV2DropsBrowser mirrors v2 dropping legacy browser fields and
// forcing protocol-only mode.
func TestSettingsMigrateV2DropsBrowser(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	payload := map[string]any{
		"schemaVersion":               2,
		"concurrency":                 6,
		"taskTimeoutSeconds":          90,
		"proxyRetryCount":             2,
		"proxyCheckConcurrency":       24,
		"maxRegistrationsPerExitIp":   3,
		"requireRegistrationPassword": true,
		"enableRegistrationTotp":      false,
		"requireTrialOnCheck":         true,
		"autoMultiCountryProbe":       true,
		"registrationMode":            "browser", // must be forced to protocol
		"browserProvider":             "ant",     // legacy field dropped
		"headless":                    true,
	}
	raw, _ := json.Marshal(payload)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewSettingsStore(path)
	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.RegistrationMode != "protocol" {
		t.Errorf("registrationMode = %q, want protocol (forced)", got.RegistrationMode)
	}
	if got.Concurrency != 6 || got.ProxyRetryCount != 2 {
		t.Errorf("v2 fields wrong: %+v", got)
	}
	if !got.RequireTrialOnCheck || !got.AutoMultiCountryProbe {
		t.Errorf("v2 requireTrialOnCheck/autoMultiCountryProbe not preserved: %+v", got)
	}
}

// TestSettingsCorrupt mirrors CorruptSettingsError on invalid JSON.
func TestSettingsCorrupt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewSettingsStore(path)
	_, err := s.Load()
	if _, ok := err.(*CorruptSettingsError); !ok {
		t.Errorf("Load() error = %T, want *CorruptSettingsError", err)
	}
}
