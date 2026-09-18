package remail

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"gpt-go/internal/model"
	"gpt-go/internal/store"
)

// fakeUpserter records UpsertMailbox calls.
type fakeUpserter struct {
	calls []upsertCall
}

type upsertCall struct {
	email, accessURL, kind, source string
}

func (f *fakeUpserter) UpsertMailbox(_ context.Context, email, accessURL, kind, source string) (bool, error) {
	f.calls = append(f.calls, upsertCall{email, accessURL, kind, source})
	return true, nil
}

func newTestService() (*Service, *fakeUpserter) {
	up := &fakeUpserter{}
	return NewService(store.NewMockRemailStore(), up), up
}

// TestGetConfigDefaults mirrors get_config defaults.
func TestGetConfigDefaults(t *testing.T) {
	s, _ := newTestService()
	cfg, err := s.GetConfig(context.Background())
	if err != nil {
		t.Fatalf("GetConfig error: %v", err)
	}
	if cfg.BaseURL != "https://remail.aishop6.com" {
		t.Errorf("BaseURL = %q, want default", cfg.BaseURL)
	}
	if cfg.APIKey != "" || cfg.ProjectID != nil || cfg.EmailSuffix != "" {
		t.Errorf("cfg = %+v, want empty defaults", cfg)
	}
	if cfg.UpdatedAt != nil {
		t.Errorf("UpdatedAt = %v, want nil", cfg.UpdatedAt)
	}
}

// TestSaveConfigNormalizes mirrors save_config normalization.
func TestSaveConfigNormalizes(t *testing.T) {
	s, _ := newTestService()
	cfg, err := s.SaveConfig(context.Background(), model.RemailConfigInput{
		APIKey:      "  rk-12345678  ",
		ProjectID:   2,
		EmailSuffix: "@icloud.com",
		BaseURL:     "https://remail.aishop6.com/",
	})
	if err != nil {
		t.Fatalf("SaveConfig error: %v", err)
	}
	if cfg.APIKey != "rk-12345678" {
		t.Errorf("APIKey = %q, want stripped", cfg.APIKey)
	}
	if cfg.EmailSuffix != "icloud.com" {
		t.Errorf("EmailSuffix = %q, want icloud.com", cfg.EmailSuffix)
	}
	if cfg.BaseURL != "https://remail.aishop6.com" {
		t.Errorf("BaseURL = %q, want trailing slash stripped", cfg.BaseURL)
	}
	if cfg.ProjectID == nil || *cfg.ProjectID != 2 {
		t.Errorf("ProjectID = %v, want 2", cfg.ProjectID)
	}
}

// TestSaveConfigSuffixFromEmail mirrors "admin@icloud.com -> icloud.com".
func TestSaveConfigSuffixFromEmail(t *testing.T) {
	s, _ := newTestService()
	cfg, err := s.SaveConfig(context.Background(), model.RemailConfigInput{
		APIKey:      "rk-12345678",
		ProjectID:   2,
		EmailSuffix: "user@icloud.com",
		BaseURL:     "https://remail.aishop6.com",
	})
	if err != nil {
		t.Fatalf("SaveConfig error: %v", err)
	}
	if cfg.EmailSuffix != "icloud.com" {
		t.Errorf("EmailSuffix = %q, want icloud.com", cfg.EmailSuffix)
	}
}

// TestBuildAccessURL mirrors build_access_url (email then token, @ -> %40).
func TestBuildAccessURL(t *testing.T) {
	got := BuildAccessURL("https://remail.aishop6.com", "Test@icloud.com", "tok-123")
	want := "https://remail.aishop6.com/v1/pickup?email=test%40icloud.com&token=tok-123"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestProbeOK mirrors probe with a 200 response.
func TestProbeOK(t *testing.T) {
	s, _ := newTestService()
	s.do = func(_ context.Context, _ string, _ string, _ map[string]string, _ any) (int, []byte, error) {
		return 200, []byte(`{"apiKey":{}}`), nil
	}
	res, err := s.Probe(context.Background(), "rk-12345678")
	if err != nil {
		t.Fatalf("Probe error: %v", err)
	}
	if !res.OK || !res.Reachable {
		t.Errorf("res = %+v, want ok+reachable", res)
	}
	if res.Message != "ReMail API Key 有效，连接正常" {
		t.Errorf("Message = %q", res.Message)
	}
}

// TestProbeNon200 mirrors probe with non-200.
func TestProbeNon200(t *testing.T) {
	s, _ := newTestService()
	s.do = func(_ context.Context, _ string, _ string, _ map[string]string, _ any) (int, []byte, error) {
		return 401, []byte(`{}`), nil
	}
	res, err := s.Probe(context.Background(), "rk-12345678")
	if err != nil {
		t.Fatalf("Probe error: %v", err)
	}
	if res.OK || !res.Reachable {
		t.Errorf("res = %+v, want !ok + reachable", res)
	}
	if !strings.Contains(res.Message, "HTTP 401") {
		t.Errorf("Message = %q, want HTTP 401", res.Message)
	}
}

// TestProbeUnreachable mirrors probe on network error.
func TestProbeUnreachable(t *testing.T) {
	s, _ := newTestService()
	s.do = func(_ context.Context, _ string, _ string, _ map[string]string, _ any) (int, []byte, error) {
		return 0, nil, &fakeErr{msg: "connection refused"}
	}
	res, err := s.Probe(context.Background(), "rk-12345678")
	if err != nil {
		t.Fatalf("Probe error: %v", err)
	}
	if res.OK || res.Reachable {
		t.Errorf("res = %+v, want !ok + !reachable", res)
	}
}

type fakeErr struct{ msg string }

func (e *fakeErr) Error() string { return e.msg }

// TestGetBalance mirrors wallet parsing.
func TestGetBalance(t *testing.T) {
	s, _ := newTestService()
	// seed config
	_, _ = s.SaveConfig(context.Background(), model.RemailConfigInput{
		APIKey: "rk-12345678", ProjectID: 2, EmailSuffix: "icloud.com", BaseURL: "https://remail.aishop6.com",
	})
	s.do = func(_ context.Context, _ string, _ string, _ map[string]string, _ any) (int, []byte, error) {
		return 200, []byte(`{"consumerBalance":"15.00","totalRecharged":"150000.00","historicalSpend":"150330.00"}`), nil
	}
	w, err := s.GetBalance(context.Background())
	if err != nil {
		t.Fatalf("GetBalance error: %v", err)
	}
	if !w.OK {
		t.Errorf("OK = false, want true")
	}
	if w.ConsumerBalance != "15.00" || w.TotalRecharged != "150000.00" || w.HistoricalSpend != "150330.00" {
		t.Errorf("wallet = %+v, wrong values", w)
	}
}

// TestImportPurchasedOrders imports orders with serviceToken already present.
func TestImportPurchasedOrders(t *testing.T) {
	s, up := newTestService()
	_, _ = s.SaveConfig(context.Background(), model.RemailConfigInput{
		APIKey: "rk-12345678", ProjectID: 2, EmailSuffix: "icloud.com", BaseURL: "https://remail.aishop6.com",
	})
	// orders page with 2 items (both purchase, icloud, valid)
	s.do = func(_ context.Context, _ string, rawURL string, _ map[string]string, _ any) (int, []byte, error) {
		items := []map[string]any{
			{"id": "2", "orderNo": "ON-2", "deliveryEmail": "b@icloud.com", "serviceToken": "tok-2", "productType": "icloud", "status": "active"},
			{"id": "1", "orderNo": "ON-1", "deliveryEmail": "a@icloud.com", "serviceToken": "tok-1", "productType": "icloud", "status": "active"},
		}
		body, _ := json.Marshal(map[string]any{"items": items})
		return 200, body, nil
	}
	records, err := s.ImportPurchasedOrders(context.Background(), model.RemailImportRequest{Limit: 100})
	if err != nil {
		t.Fatalf("Import error: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("len = %d, want 2", len(records))
	}
	if len(up.calls) != 2 {
		t.Fatalf("upsert calls = %d, want 2", len(up.calls))
	}
	if records[0].Email != "b@icloud.com" || records[1].Email != "a@icloud.com" {
		t.Errorf("order = %v", records)
	}
	for _, r := range records {
		if r.AccessURL == "" || !r.Imported || r.Duplicate {
			t.Errorf("record = %+v, want imported", r)
		}
	}
}

// TestImportFiltersInvalid mirrors filtering invalid status + product type + suffix.
func TestImportFiltersInvalid(t *testing.T) {
	s, _ := newTestService()
	_, _ = s.SaveConfig(context.Background(), model.RemailConfigInput{
		APIKey: "rk-12345678", ProjectID: 2, EmailSuffix: "icloud.com", BaseURL: "https://remail.aishop6.com",
	})
	s.do = func(_ context.Context, _ string, _ string, _ map[string]string, _ any) (int, []byte, error) {
		items := []map[string]any{
			{"id": "3", "orderNo": "ON-3", "deliveryEmail": "c@icloud.com", "serviceToken": "tok-3", "productType": "icloud", "status": "refunded"}, // invalid status
			{"id": "2", "orderNo": "ON-2", "deliveryEmail": "b@icloud.com", "serviceToken": "tok-2", "productType": "gmail", "status": "active"},    // wrong type
			{"id": "1", "orderNo": "ON-1", "deliveryEmail": "a@icloud.com", "serviceToken": "tok-1", "productType": "icloud", "status": "active"},   // valid
		}
		body, _ := json.Marshal(map[string]any{"items": items})
		return 200, body, nil
	}
	records, err := s.ImportPurchasedOrders(context.Background(), model.RemailImportRequest{
		Limit:       100,
		ProductType: "icloud",
		EmailSuffix: "icloud.com",
	})
	if err != nil {
		t.Fatalf("Import error: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("len = %d, want 1", len(records))
	}
	if records[0].Email != "a@icloud.com" {
		t.Errorf("email = %q, want a@icloud.com", records[0].Email)
	}
}

// TestCreateMailboxes mirrors batch ordering.
func TestCreateMailboxes(t *testing.T) {
	s, up := newTestService()
	_, _ = s.SaveConfig(context.Background(), model.RemailConfigInput{
		APIKey: "rk-12345678", ProjectID: 2, EmailSuffix: "icloud.com", BaseURL: "https://remail.aishop6.com",
	})
	s.do = func(_ context.Context, _ string, rawURL string, _ map[string]string, _ any) (int, []byte, error) {
		// batch order endpoint returns 2 succeeded orders
		body := []map[string]any{
			{"status": "succeeded", "order": map[string]any{"orderNo": "ON-1", "deliveryEmail": "a@icloud.com", "serviceToken": "tok-1", "verificationCode": "123456", "status": "completed"}},
			{"status": "succeeded", "order": map[string]any{"orderNo": "ON-2", "deliveryEmail": "b@icloud.com", "serviceToken": "tok-2", "verificationCode": "654321", "status": "completed"}},
		}
		b, _ := json.Marshal(body)
		return 200, b, nil
	}
	records, err := s.CreateMailboxes(context.Background(), model.RemailMailboxCreate{Count: 2})
	if err != nil {
		t.Fatalf("CreateMailboxes error: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("len = %d, want 2", len(records))
	}
	if len(up.calls) != 2 {
		t.Fatalf("upsert calls = %d, want 2", len(up.calls))
	}
	if records[0].Email != "a@icloud.com" || records[1].Email != "b@icloud.com" {
		t.Errorf("records = %v", records)
	}
	if records[0].OrderNo != "ON-1" || records[1].OrderNo != "ON-2" {
		t.Errorf("orderNo = %v", records)
	}
}

// TestCreateMailboxesNoProject mirrors the 400 projectId missing: the error is
// captured into a record (Python catches RemailError and appends a record).
func TestCreateMailboxesNoProject(t *testing.T) {
	s, _ := newTestService()
	// no config saved -> ProjectID nil
	records, err := s.CreateMailboxes(context.Background(), model.RemailMailboxCreate{Count: 2})
	if err != nil {
		t.Fatalf("CreateMailboxes returned error: %v (Python appends record instead)", err)
	}
	if len(records) != 1 {
		t.Fatalf("len = %d, want 1 (error record)", len(records))
	}
	if records[0].Error == nil || !strings.Contains(*records[0].Error, "projectId") {
		t.Errorf("record error = %v, want projectId missing", records[0].Error)
	}
	if records[0].Imported {
		t.Errorf("imported = true, want false")
	}
}
