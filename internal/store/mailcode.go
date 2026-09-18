// Package store defines the mailcode configuration storage contract and its
// in-memory mock. The Mongo-backed implementation lands when MongoDB is set up
// locally via Docker; until then the mock drives development.
package store

import (
	"context"
	"time"
)

// MailcodeConfigDocument is the persisted shape of mailcode_settings, mirroring
// mailcode_service.py save_config (resource key "mailcode_config").
type MailcodeConfigDocument struct {
	BaseURL   string     `bson:"baseUrl"`
	Domain    string     `bson:"domain"`
	UpdatedAt *time.Time `bson:"updatedAt"`
}

// MailcodeStore is the mailcode configuration storage contract.
type MailcodeStore interface {
	// Load returns the stored config document (empty when never saved).
	Load(ctx context.Context) (MailcodeConfigDocument, error)
	// Save upserts the config document and returns the stored value.
	Save(ctx context.Context, d MailcodeConfigDocument) error
}

// MockMailcodeStore is an in-memory MailcodeStore.
type MockMailcodeStore struct {
	doc MailcodeConfigDocument
}

// NewMockMailcodeStore returns an empty in-memory mailcode store.
func NewMockMailcodeStore() *MockMailcodeStore {
	return &MockMailcodeStore{}
}

// Load returns the stored document (zero value when never saved).
func (m *MockMailcodeStore) Load(_ context.Context) (MailcodeConfigDocument, error) {
	return m.doc, nil
}

// Save stores the document.
func (m *MockMailcodeStore) Save(_ context.Context, d MailcodeConfigDocument) error {
	m.doc = d
	return nil
}
