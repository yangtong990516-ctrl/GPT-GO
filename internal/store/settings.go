// SettingsStore persists execution settings to a local JSON file, mirroring
// settings_store.py: atomic write (tmp + fsync + replace + backup), schema
// v1/v2 migration, and corruption detection.
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gpt-go/internal/model"
	"gpt-go/internal/util"
)

// DefaultSettingsPath mirrors settings_store._default_settings_path():
// AUTOREGISTER_SETTINGS_PATH env, else ~/.autoregister/settings.json.
func DefaultSettingsPath() string {
	if p := util.EnvOrDefault("AUTOREGISTER_SETTINGS_PATH", ""); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "settings.json"
	}
	return filepath.Join(home, ".autoregister", "settings.json")
}

// CorruptSettingsError mirrors settings_store.CorruptSettingsError.
type CorruptSettingsError struct{ msg string }

func (e *CorruptSettingsError) Error() string { return e.msg }

// SettingsStore is the file-backed execution-settings store.
type SettingsStore struct {
	path string
	mu   sync.Mutex
}

// NewSettingsStore returns a SettingsStore at the given path.
func NewSettingsStore(path string) *SettingsStore {
	return &SettingsStore{path: path}
}

// Load returns the stored settings, or defaults when the file is absent.
// It returns *CorruptSettingsError when the file is invalid.
func (s *SettingsStore) Load() (model.ExecutionSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(s.path); os.IsNotExist(err) {
		return model.DefaultExecutionSettings(), nil
	}
	return s.loadExisting()
}

// Save writes the incoming settings (validated by the caller) atomically.
// Unlike the Python reference (which silently drops requireTrialOnCheck and
// autoMultiCountryProbe), every field is persisted.
func (s *SettingsStore) Save(in model.ExecutionSettingsInput) (model.ExecutionSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	out := model.ExecutionSettings{
		SchemaVersion:               2,
		EnableRegistrationSecurity:  in.EnableRegistrationSecurity,
		RegistrationMode:            "protocol",
		ProxyRetryCount:             in.ProxyRetryCount,
		ProxyCheckConcurrency:       in.ProxyCheckConcurrency,
		MaxRegistrationsPerExitIP:   in.MaxRegistrationsPerExitIP,
		Concurrency:                 in.Concurrency,
		TaskTimeoutSeconds:          in.TaskTimeoutSeconds,
		UpdatedAt:                   &now,
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return model.ExecutionSettings{}, err
	}
	tmp := s.path + ".tmp"
	bak := s.path + ".bak"

	payload, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return model.ExecutionSettings{}, err
	}
	payload = append(payload, '\n')

	// Write tmp + fsync.
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return model.ExecutionSettings{}, err
	}
	if _, err := f.Write(payload); err != nil {
		f.Close()
		os.Remove(tmp)
		return model.ExecutionSettings{}, err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return model.ExecutionSettings{}, err
	}
	f.Close()

	// Backup existing file, then atomically replace.
	if _, err := os.Stat(s.path); err == nil {
		if err := copyFile(s.path, bak); err != nil {
			os.Remove(tmp)
			return model.ExecutionSettings{}, err
		}
	}
	if err := os.Rename(tmp, s.path); err != nil {
		os.Remove(tmp)
		return model.ExecutionSettings{}, err
	}
	return out, nil
}

// loadExisting reads and validates/migrates the on-disk payload.
func (s *SettingsStore) loadExisting() (model.ExecutionSettings, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return model.ExecutionSettings{}, &CorruptSettingsError{msg: fmt.Sprintf("配置文件损坏或不符合受支持的 schema：%s", s.path)}
	}
	var payload map[string]any
	if err := util.UnmarshalLenient(raw, &payload); err != nil {
		return model.ExecutionSettings{}, &CorruptSettingsError{msg: fmt.Sprintf("配置文件损坏或不符合受支持的 schema：%s", s.path)}
	}
	return migrateSettings(payload, s.path)
}

// migrateSettings mirrors _validate_or_migrate: v2 (drop legacy browser fields,
// force protocol) and v1 (migrate the two carried fields).
func migrateSettings(payload map[string]any, path string) (model.ExecutionSettings, error) {
	ver, _ := payload["schemaVersion"].(float64)
	switch int(ver) {
	case 2:
		for _, k := range []string{
			"browserProvider", "browserExecutablePath", "roxyApiKey", "roxyApiPort",
			"antBrowserExecutablePath", "antApiKey", "antApiPort", "headless",
		} {
			delete(payload, k)
		}
		out := model.DefaultExecutionSettings()
		out.UpdatedAt = nil
		out.EnableRegistrationSecurity = boolField(payload, "enableRegistrationSecurity", out.EnableRegistrationSecurity)
		out.RegistrationMode = "protocol"
		out.ProxyRetryCount = intField(payload, "proxyRetryCount", out.ProxyRetryCount)
		out.ProxyCheckConcurrency = intField(payload, "proxyCheckConcurrency", out.ProxyCheckConcurrency)
		out.MaxRegistrationsPerExitIP = intField(payload, "maxRegistrationsPerExitIp", out.MaxRegistrationsPerExitIP)
		out.Concurrency = intField(payload, "concurrency", out.Concurrency)
		out.TaskTimeoutSeconds = intField(payload, "taskTimeoutSeconds", out.TaskTimeoutSeconds)
		if v, ok := payload["updatedAt"].(string); ok && v != "" {
			out.UpdatedAt = &v
		}
		return out, nil
	case 1:
		out := model.DefaultExecutionSettings()
		out.Concurrency = intField(payload, "concurrency", out.Concurrency)
		out.TaskTimeoutSeconds = intField(payload, "taskTimeoutSeconds", out.TaskTimeoutSeconds)
		out.UpdatedAt = nil
		if v, ok := payload["updatedAt"].(string); ok && v != "" {
			out.UpdatedAt = &v
		}
		return out, nil
	default:
		return model.ExecutionSettings{}, &CorruptSettingsError{msg: fmt.Sprintf("配置文件损坏或不符合受支持的 schema：%s", path)}
	}
}

func boolField(m map[string]any, key string, def bool) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return def
}

func intField(m map[string]any, key string, def int) int {
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	return def
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}
