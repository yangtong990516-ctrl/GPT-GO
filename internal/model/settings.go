// Package model defines the execution-settings API models, mirroring
// settings_store.py (ExecutionSettingsInput / StoredExecutionSettings /
// ExecutionSettings) and the /api/settings/execution routes.
package model

// ExecutionSettingsInput mirrors settings_store.ExecutionSettingsInput (the PUT
// body). Field names stay camelCase to match the persisted JSON and frontend.
type ExecutionSettingsInput struct {
	RequireRegistrationPassword bool   `json:"requireRegistrationPassword"`
	EnableRegistrationTotp      bool   `json:"enableRegistrationTotp"`
	RequireTrialOnCheck         bool   `json:"requireTrialOnCheck"`
	AutoMultiCountryProbe       bool   `json:"autoMultiCountryProbe"`
	RegistrationMode            string `json:"registrationMode"`
	ProxyRetryCount             int    `json:"proxyRetryCount"`
	ProxyCheckConcurrency       int    `json:"proxyCheckConcurrency"`
	MaxRegistrationsPerExitIP   int    `json:"maxRegistrationsPerExitIp"`
	Concurrency                 int    `json:"concurrency"`
	TaskTimeoutSeconds          int    `json:"taskTimeoutSeconds"`
}

// ExecutionSettings mirrors settings_store.ExecutionSettings (the GET response,
// plus updatedAt and schemaVersion).
type ExecutionSettings struct {
	SchemaVersion               int     `json:"schemaVersion"`
	RequireRegistrationPassword bool    `json:"requireRegistrationPassword"`
	EnableRegistrationTotp      bool    `json:"enableRegistrationTotp"`
	RequireTrialOnCheck         bool    `json:"requireTrialOnCheck"`
	AutoMultiCountryProbe       bool    `json:"autoMultiCountryProbe"`
	RegistrationMode            string  `json:"registrationMode"`
	ProxyRetryCount             int     `json:"proxyRetryCount"`
	ProxyCheckConcurrency       int     `json:"proxyCheckConcurrency"`
	MaxRegistrationsPerExitIP   int     `json:"maxRegistrationsPerExitIp"`
	Concurrency                 int     `json:"concurrency"`
	TaskTimeoutSeconds          int     `json:"taskTimeoutSeconds"`
	UpdatedAt                   *string `json:"updatedAt"`
}

// DefaultExecutionSettings mirrors settings_store.default_settings().
func DefaultExecutionSettings() ExecutionSettings {
	return ExecutionSettings{
		SchemaVersion:               2,
		RequireRegistrationPassword: false,
		EnableRegistrationTotp:      true,
		RequireTrialOnCheck:         false,
		AutoMultiCountryProbe:       false,
		RegistrationMode:            "protocol",
		ProxyRetryCount:             1,
		ProxyCheckConcurrency:       16,
		MaxRegistrationsPerExitIP:   0,
		Concurrency:                 2,
		TaskTimeoutSeconds:          0,
		UpdatedAt:                   nil,
	}
}

// settingsValidationBounds mirrors the pydantic Field constraints in
// settings_store.py.
const (
	settingsProxyRetryCountMax = 5
	settingsProxyCheckConcMin  = 1
	settingsProxyCheckConcMax  = 32
	settingsMaxPerExitIPMax    = 32
	settingsConcurrencyMin     = 1
	settingsConcurrencyMax     = 12
)

// ValidateExecutionSettings checks the PUT payload against the same bounds as
// the pydantic Field(ge/le) constraints. Returns an empty string when valid,
// otherwise a human-readable reason.
func ValidateExecutionSettings(in ExecutionSettingsInput) string {
	if in.RegistrationMode != "" && in.RegistrationMode != "protocol" {
		return "registrationMode 必须是 protocol"
	}
	if in.ProxyRetryCount < 0 || in.ProxyRetryCount > settingsProxyRetryCountMax {
		return "proxyRetryCount 必须在 0..5"
	}
	if in.ProxyCheckConcurrency < settingsProxyCheckConcMin || in.ProxyCheckConcurrency > settingsProxyCheckConcMax {
		return "proxyCheckConcurrency 必须在 1..32"
	}
	if in.MaxRegistrationsPerExitIP < 0 || in.MaxRegistrationsPerExitIP > settingsMaxPerExitIPMax {
		return "maxRegistrationsPerExitIp 必须在 0..32"
	}
	if in.Concurrency < settingsConcurrencyMin || in.Concurrency > settingsConcurrencyMax {
		return "concurrency 必须在 1..12"
	}
	if in.TaskTimeoutSeconds < 0 {
		return "taskTimeoutSeconds 必须 >= 0"
	}
	return ""
}
