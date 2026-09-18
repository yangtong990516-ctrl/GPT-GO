package email

import (
	"context"
	"testing"
	"time"

	"gpt-go/internal/model"
	"gpt-go/internal/store"
)

// TestImport mirrors import_emails behavior (resource_service.py:2934).
func TestImport(t *testing.T) {
	ctx := context.Background()
	s := NewService(store.NewMockEmailStore())

	raw := "alice@example.com----https://mail.example.com/get_code?email=alice@example.com\n" +
		"bob@example.com----secretpassword\n" + // mailcom password (no ://)
		"bad-line-no-separator\n" + // error
		"alice@example.com----https://mail.example.com/get_code?email=alice@example.com\n" + // duplicate
		"invalid@@----https://x" // error (invalid email)

	result, err := s.Import(ctx, raw)
	if err != nil {
		t.Fatalf("Import error: %v", err)
	}
	if result.Total != 5 {
		t.Errorf("Total = %d, want 5", result.Total)
	}
	if result.Imported != 2 {
		t.Errorf("Imported = %d, want 2", result.Imported)
	}
	if result.DuplicateCount != 1 {
		t.Errorf("DuplicateCount = %d, want 1", result.DuplicateCount)
	}
	if result.ErrorCount != 2 {
		t.Errorf("ErrorCount = %d, want 2", result.ErrorCount)
	}
}

// TestImportBOMAndLabels mirrors normalize_email prefix stripping + BOM strip.
func TestImportBOMAndLabels(t *testing.T) {
	ctx := context.Background()
	s := NewService(store.NewMockEmailStore())

	// BOM + "邮箱：" prefix label
	raw := "\ufeff邮箱：alice@example.com----https://mail.example.com/get_code?email=alice@example.com"
	result, err := s.Import(ctx, raw)
	if err != nil {
		t.Fatalf("Import error: %v", err)
	}
	if result.Imported != 1 {
		t.Errorf("Imported = %d, want 1 (label stripped)", result.Imported)
	}
}

// TestImportSkipsRegistered mirrors upsert_email returning False when the email
// is already registered in accounts.
func TestImportSkipsRegistered(t *testing.T) {
	ctx := context.Background()
	st := store.NewMockEmailStore()
	st.SetRegisteredEmails("alice@example.com")
	s := NewService(st)

	raw := "alice@example.com----https://mail.example.com/get_code?email=alice@example.com"
	result, err := s.Import(ctx, raw)
	if err != nil {
		t.Fatalf("Import error: %v", err)
	}
	if result.Imported != 0 {
		t.Errorf("Imported = %d, want 0 (registered)", result.Imported)
	}
	if result.DuplicateCount != 1 {
		t.Errorf("DuplicateCount = %d, want 1 (registered counts as duplicate)", result.DuplicateCount)
	}
}

// TestListFiltering mirrors list_emails source/status/q filtering.
func TestListFiltering(t *testing.T) {
	ctx := context.Background()
	st := store.NewMockEmailStore()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seed := func(id, email, src, status string, t time.Time) {
		st.Upsert(ctx, store.EmailDocument{
			ID: id, Email: email, EmailNormalized: normalizeEmail(email),
			AccessURL: "https://x", ImportedAt: t, Status: status, SourceType: src,
		})
	}
	seed("1", "a@x.com", "manual", "available", base)
	seed("2", "b@x.com", "mailcom_alias", "available", base.Add(time.Hour))
	seed("3", "c@x.com", "mailcode", "failed", base.Add(2*time.Hour))

	s := NewService(st)

	// status filter: available only
	pg, err := s.List(ctx, 1, 10, "", "all", "available")
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	if pg.Total != 2 {
		t.Errorf("Total = %d, want 2 (available)", pg.Total)
	}

	// source filter: mailcom_alias only
	pg, err = s.List(ctx, 1, 10, "", "mailcom_alias", "all")
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	if pg.Total != 1 {
		t.Errorf("Total = %d, want 1 (mailcom_alias)", pg.Total)
	}
	if len(pg.Items) != 1 || pg.Items[0].Email != "b@x.com" {
		t.Errorf("Items = %v, want [b@x.com]", pg.Items)
	}

	// source filter: standard => manual only (excludes alias/mailcode)
	pg, err = s.List(ctx, 1, 10, "", "standard", "all")
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	if pg.Total != 1 || pg.Items[0].Email != "a@x.com" {
		t.Errorf("standard Total = %d items=%v, want [a@x.com]", pg.Total, pg.Items)
	}
}

// TestListOrdering mirrors importedAt descending.
func TestListOrdering(t *testing.T) {
	ctx := context.Background()
	st := store.NewMockEmailStore()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	st.Upsert(ctx, store.EmailDocument{ID: "1", Email: "a@x.com", EmailNormalized: "a@x.com", AccessURL: "https://x", ImportedAt: base, Status: "available"})
	st.Upsert(ctx, store.EmailDocument{ID: "2", Email: "b@x.com", EmailNormalized: "b@x.com", AccessURL: "https://x", ImportedAt: base.Add(time.Hour), Status: "available"})
	st.Upsert(ctx, store.EmailDocument{ID: "3", Email: "c@x.com", EmailNormalized: "c@x.com", AccessURL: "https://x", ImportedAt: base.Add(2 * time.Hour), Status: "available"})

	s := NewService(st)
	pg, err := s.List(ctx, 1, 10, "", "all", "available")
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	// expected order: c (newest), b, a
	if len(pg.Items) != 3 || pg.Items[0].Email != "c@x.com" || pg.Items[1].Email != "b@x.com" || pg.Items[2].Email != "a@x.com" {
		t.Errorf("order = %v, want [c b a]", emailsOf(pg.Items))
	}
}

// TestExport mirrors export_emails (resource_service.py:3222).
func TestExport(t *testing.T) {
	ctx := context.Background()
	st := store.NewMockEmailStore()
	st.Upsert(ctx, store.EmailDocument{ID: "1", Email: "a@x.com", EmailNormalized: "a@x.com", AccessURL: "https://mail.example.com/get_code?email=a@x.com", ImportedAt: time.Now().UTC(), Status: "available"})
	st.Upsert(ctx, store.EmailDocument{ID: "2", Email: "b@x.com", EmailNormalized: "b@x.com", AccessURL: "https://x", ImportedAt: time.Now().UTC(), Status: "failed"}) // excluded: not available

	s := NewService(st)
	exp, err := s.Export(ctx, "all", nil)
	if err != nil {
		t.Fatalf("Export error: %v", err)
	}
	if exp.Count != 1 {
		t.Errorf("Count = %d, want 1 (only available)", exp.Count)
	}
	if exp.Format != nil {
		t.Errorf("Format = %v, want nil", exp.Format)
	}
	if len(exp.Filename) == 0 {
		t.Errorf("Filename empty")
	}
}

// TestSetStatusNotFound mirrors set_email_status raising ResourceNotFoundError.
func TestSetStatusNotFound(t *testing.T) {
	ctx := context.Background()
	s := NewService(store.NewMockEmailStore())
	_, err := s.SetStatus(ctx, "nonexistent", "failed")
	if err != store.ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func emailsOf(items []model.EmailRecord) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Email)
	}
	return out
}
