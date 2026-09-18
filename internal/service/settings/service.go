// Package settings implements the execution-settings business logic, mirroring
// settings_store.py + the /api/settings/execution routes.
package settings

import (
	"context"
	"errors"

	"gpt-go/internal/model"
	"gpt-go/internal/store"
	"gpt-go/internal/util"
)

// Service reads/writes execution settings via a file-backed store.
type Service struct {
	store *store.SettingsStore
}

// NewService returns a settings Service.
func NewService(s *store.SettingsStore) *Service {
	return &Service{store: s}
}

// Get returns the current execution settings (or defaults).
func (s *Service) Get(ctx context.Context) (model.ExecutionSettings, error) {
	return s.store.Load()
}

// Update validates and persists the incoming settings. Validation failures map
// to util.InvalidBody (422); corruption/write failures surface as *HTTPError.
func (s *Service) Update(ctx context.Context, in model.ExecutionSettingsInput) (model.ExecutionSettings, error) {
	if reason := model.ValidateExecutionSettings(in); reason != "" {
		return model.ExecutionSettings{}, &util.HTTPError{Status: 422, Code: util.CodeInvalidBody, Message: reason}
	}
	out, err := s.store.Save(in)
	if err != nil {
		var corrupt *store.CorruptSettingsError
		if errors.As(err, &corrupt) {
			return model.ExecutionSettings{}, &util.HTTPError{Status: 500, Code: "settings_corrupted", Message: err.Error()}
		}
		return model.ExecutionSettings{}, &util.HTTPError{Status: 500, Code: "settings_write_failed", Message: "无法原子保存配置文件"}
	}
	return out, nil
}
