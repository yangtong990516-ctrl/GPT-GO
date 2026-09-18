// Remail configuration storage contract and in-memory mock. Mirrors
// remail_service.py remail_settings collection (resource key "remail_config").
package store

import (
	"context"
	"time"
)

// RemailConfigDocument is the persisted shape of remail_settings.
type RemailConfigDocument struct {
	APIKey      string     `bson:"apiKey"`
	ProjectID   *int       `bson:"projectId"`
	EmailSuffix string     `bson:"emailSuffix"`
	BaseURL     string     `bson:"baseUrl"`
	UpdatedAt   *time.Time `bson:"updatedAt"`
}

// RemailStore is the remail configuration storage contract.
type RemailStore interface {
	// Load returns the stored config document (empty when never saved).
	Load(ctx context.Context) (RemailConfigDocument, error)
	// Save upserts the config document.
	Save(ctx context.Context, d RemailConfigDocument) error
}

// MockRemailStore is an in-memory RemailStore.
type MockRemailStore struct {
	doc RemailConfigDocument
}

// NewMockRemailStore returns an empty in-memory remail store.
func NewMockRemailStore() *MockRemailStore {
	return &MockRemailStore{}
}

// Load returns the stored document (zero value when never saved).
func (m *MockRemailStore) Load(_ context.Context) (RemailConfigDocument, error) {
	return m.doc, nil
}

// Save stores the document.
func (m *MockRemailStore) Save(_ context.Context, d RemailConfigDocument) error {
	m.doc = d
	return nil
}
