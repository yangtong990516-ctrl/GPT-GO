package store

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

// MockEmailStore is an in-memory EmailStore used to drive development and
// behavior verification until MongoDB is available locally. It mirrors the
// observable behavior of resource_service's Mongo operations:
//   - importedAt descending ordering,
//   - distinct registered emails exclusion,
//   - upsert-on-emailNormalized (no duplicate insert),
//   - set_status clears reservedBy/reservedAt.
type MockEmailStore struct {
	mu       sync.Mutex
	emails   map[string]*EmailDocument // key = emailNormalized
	accounts map[string]bool           // registered emailNormalized set
}

// NewMockEmailStore returns an empty in-memory store.
func NewMockEmailStore() *MockEmailStore {
	return &MockEmailStore{
		emails:   map[string]*EmailDocument{},
		accounts: map[string]bool{},
	}
}

// SetRegisteredEmails seeds the registered-accounts set (for tests).
func (m *MockEmailStore) SetRegisteredEmails(emails ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range emails {
		m.accounts[strings.ToLower(strings.TrimSpace(e))] = true
	}
}

// Upsert inserts a document when emailNormalized is absent.
func (m *MockEmailStore) Upsert(_ context.Context, d EmailDocument) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.emails[d.EmailNormalized]; exists {
		return false, nil
	}
	cp := d
	m.emails[d.EmailNormalized] = &cp
	return true, nil
}

// List filters and pages documents, ordered importedAt desc.
func (m *MockEmailStore) List(_ context.Context, q EmailQuery) ([]EmailDocument, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	docs := m.filtered(q)
	total := len(docs)
	sort.SliceStable(docs, func(i, j int) bool {
		return docs[i].ImportedAt.After(docs[j].ImportedAt)
	})

	start := (q.Page - 1) * q.Size
	if start < 0 {
		start = 0
	}
	if start > len(docs) {
		start = len(docs)
	}
	end := start + q.Size
	if end > len(docs) {
		end = len(docs)
	}

	out := make([]EmailDocument, 0, end-start)
	for _, d := range docs[start:end] {
		out = append(out, *d)
	}
	return out, total, nil
}

func (m *MockEmailStore) filtered(q EmailQuery) []*EmailDocument {
	excluded := m.registeredNormalized()
	var docs []*EmailDocument
	for _, d := range m.emails {
		if q.Status != "" && d.Status != q.Status {
			continue
		}
		if !sourceMatches(q.Source, d.SourceType) {
			continue
		}
		if q.Query != "" && !strings.Contains(d.EmailNormalized, q.Query) {
			continue
		}
		if excluded[d.EmailNormalized] {
			continue
		}
		docs = append(docs, d)
	}
	return docs
}

func (m *MockEmailStore) registeredNormalized() map[string]bool {
	out := map[string]bool{}
	for k := range m.accounts {
		out[k] = true
	}
	return out
}

// sourceMatches mirrors email_source_filter (resource_service.py:185).
func sourceMatches(source, sourceType string) bool {
	switch source {
	case "mailcom_alias":
		return sourceType == "mailcom_alias"
	case "mailcode":
		return sourceType == "mailcode"
	case "remail":
		return sourceType == "remail"
	case "standard":
		// {$or:[{sourceType:{$exists:false}},{sourceType:{$nin:["mailcom_alias","mailcode"]}}]}
		switch sourceType {
		case "mailcom_alias", "mailcode":
			return false
		}
		return true
	default: // "all" or "" => no filter
		return true
	}
}

// SetStatus updates status fields and clears reservedBy/reservedAt.
func (m *MockEmailStore) SetStatus(_ context.Context, id, status, statusReason string, statusUpdatedAt time.Time, errorCode string) (*EmailDocument, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.emails {
		if d.ID == id {
			d.Status = status
			if statusReason != "" {
				d.StatusReason = statusReason
			} else {
				d.StatusReason = ""
			}
			ts := statusUpdatedAt
			d.StatusUpdatedAt = &ts
			if errorCode != "" {
				d.ErrorCode = errorCode
			}
			cp := *d
			return &cp, nil
		}
	}
	return nil, ErrNotFound
}

// RegisteredEmails returns the registered emails (sorted for determinism).
func (m *MockEmailStore) RegisteredEmails(_ context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.accounts))
	for k := range m.accounts {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

// Count returns the number of documents matching pred (nil pred = all).
func (m *MockEmailStore) Count(_ context.Context, pred func(EmailDocument) bool) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, d := range m.emails {
		if pred == nil || pred(*d) {
			n++
		}
	}
	return n, nil
}

// ListForExport returns available emails (excluded registered), importedAt desc.
func (m *MockEmailStore) ListForExport(_ context.Context, ids []string) ([]EmailDocument, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var docs []*EmailDocument
	for _, d := range m.emails {
		if d.Status != "available" {
			continue
		}
		if len(ids) > 0 && !containsID(ids, d.ID) {
			continue
		}
		if m.accounts[d.EmailNormalized] {
			continue
		}
		docs = append(docs, d)
	}
	sort.SliceStable(docs, func(i, j int) bool {
		return docs[i].ImportedAt.After(docs[j].ImportedAt)
	})
	out := make([]EmailDocument, 0, len(docs))
	for _, d := range docs {
		out = append(out, *d)
	}
	return out, nil
}

func containsID(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// ResetFailed resets failed -> available.
func (m *MockEmailStore) ResetFailed(_ context.Context, ids []string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, d := range m.emails {
		if d.Status != "failed" {
			continue
		}
		if len(ids) > 0 && !containsID(ids, d.ID) {
			continue
		}
		d.Status = "available"
		d.StatusReason = ""
		d.StatusUpdatedAt = nil
		count++
	}
	return count, nil
}

// Delete removes documents by id.
func (m *MockEmailStore) Delete(_ context.Context, ids []string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for key, d := range m.emails {
		if containsID(ids, d.ID) {
			delete(m.emails, key)
			count++
		}
	}
	return count, nil
}

// ── 换绑（rebind 模块）Mock 实现 ──

func (m *MockEmailStore) ReserveForRebind(_ context.Context, runID string) (*EmailDocument, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.emails {
		if d.Status == "available" {
			now := time.Now().UTC()
			d.Status = "reserved"
			d.RebindReservedBy = runID
			d.ReservedAt = &now
			cp := *d
			return &cp, nil
		}
	}
	return nil, nil
}

func (m *MockEmailStore) ReserveForRebindByID(_ context.Context, emailID, runID string) (*EmailDocument, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.emails {
		if d.ID == emailID && d.Status == "available" {
			now := time.Now().UTC()
			d.Status = "reserved"
			d.RebindReservedBy = runID
			d.ReservedAt = &now
			cp := *d
			return &cp, nil
		}
	}
	return nil, nil
}

func (m *MockEmailStore) ConsumeRebindEmail(_ context.Context, emailID, runID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.emails {
		if d.ID == emailID && d.RebindReservedBy == runID {
			d.Status = "used"
			d.UsagePurpose = "rebind"
			d.RebindReservedBy = ""
			d.ReservedAt = nil
			return true, nil
		}
	}
	return false, nil
}

func (m *MockEmailStore) ReleaseRebindReservation(_ context.Context, emailID, runID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.emails {
		if d.ID == emailID && d.RebindReservedBy == runID {
			d.Status = "available"
			d.RebindReservedBy = ""
			d.ReservedAt = nil
			return true, nil
		}
	}
	return false, nil
}
