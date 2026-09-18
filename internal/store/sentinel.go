// Sentinel version-check storage contract and in-memory mock. Mirrors
// sentinel_version_scheduler.py collections:
//   - sentinel_settings        (key "_id":"main", runtime switch)
//   - sentinel_version_checks  (detection history, kept to the latest 50)
package store

import (
	"context"
	"sort"
	"sync"

	"gpt-go/internal/model"
)

// sentinelKeepChecks is how many detection results to retain (mirrors the
// Python skip(50) trim in _persist).
const sentinelKeepChecks = 50

// SentinelStore is the sentinel version-check storage contract.
type SentinelStore interface {
	// LoadConfig returns the stored config. The bool is false when no document
	// has been saved yet (the caller falls back to defaults).
	LoadConfig(ctx context.Context) (model.SentinelConfig, bool, error)
	// SaveConfig upserts the config document.
	SaveConfig(ctx context.Context, cfg model.SentinelConfig) error
	// AppendCheck persists one detection result, trimming history to the latest
	// sentinelKeepChecks entries (oldest dropped first).
	AppendCheck(ctx context.Context, r model.SentinelCheckResult) error
	// LatestCheck returns the most recent detection result, or nil when none.
	LatestCheck(ctx context.Context) (*model.SentinelCheckResult, error)
}

// MockSentinelStore is an in-memory SentinelStore (mutex-guarded for concurrent
// access from the scheduler goroutine and HTTP handlers).
type MockSentinelStore struct {
	mu     sync.Mutex
	config *model.SentinelConfig // nil == never saved
	checks []model.SentinelCheckResult
}

// NewMockSentinelStore returns an empty in-memory sentinel store.
func NewMockSentinelStore() *MockSentinelStore {
	return &MockSentinelStore{}
}

// LoadConfig returns the stored config; ok is false when never saved.
func (m *MockSentinelStore) LoadConfig(_ context.Context) (model.SentinelConfig, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.config == nil {
		return model.SentinelConfig{}, false, nil
	}
	return *m.config, true, nil
}

// SaveConfig upserts the config document.
func (m *MockSentinelStore) SaveConfig(_ context.Context, cfg model.SentinelConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := cfg
	m.config = &c
	return nil
}

// AppendCheck appends a result and trims history to sentinelKeepChecks entries.
func (m *MockSentinelStore) AppendCheck(_ context.Context, r model.SentinelCheckResult) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checks = append(m.checks, r)
	if len(m.checks) > sentinelKeepChecks {
		// Drop the oldest entries (sort by CheckedAt ascending keeps the latest
		// sentinelKeepChecks after truncation).
		sort.SliceStable(m.checks, func(i, j int) bool {
			return m.checks[i].CheckedAt.Before(m.checks[j].CheckedAt)
		})
		m.checks = m.checks[len(m.checks)-sentinelKeepChecks:]
	}
	return nil
}

// LatestCheck returns the most recent result by CheckedAt, or nil when empty.
func (m *MockSentinelStore) LatestCheck(_ context.Context) (*model.SentinelCheckResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.checks) == 0 {
		return nil, nil
	}
	latest := m.checks[0]
	for _, c := range m.checks[1:] {
		if c.CheckedAt.After(latest.CheckedAt) {
			latest = c
		}
	}
	out := latest
	return &out, nil
}
