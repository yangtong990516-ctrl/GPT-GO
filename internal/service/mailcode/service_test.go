package mailcode

import (
	"context"
	"strings"
	"testing"

	"gpt-go/internal/model"
	"gpt-go/internal/store"
	"gpt-go/internal/util"
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
	return NewService(store.NewMockMailcodeStore(), up), up
}

// TestGetConfigDefaults mirrors get_config defaults when nothing saved.
func TestGetConfigDefaults(t *testing.T) {
	s, _ := newTestService()
	cfg, err := s.GetConfig(context.Background())
	if err != nil {
		t.Fatalf("GetConfig error: %v", err)
	}
	if cfg.BaseURL != "https://mail.example.com" {
		t.Errorf("BaseURL = %q, want default", cfg.BaseURL)
	}
	if cfg.Domain != "example.com" {
		t.Errorf("Domain = %q, want example.com", cfg.Domain)
	}
	if cfg.UpdatedAt != nil {
		t.Errorf("UpdatedAt = %v, want nil", cfg.UpdatedAt)
	}
}

// TestSaveConfigNormalizes mirrors save_config: rstrip / and domain normalization.
func TestSaveConfigNormalizes(t *testing.T) {
	s, _ := newTestService()
	cfg, err := s.SaveConfig(context.Background(), model.MailcodeConfigInput{
		BaseURL: "https://mail.liwei-inc.com/",
		Domain:  "@liwei-inc.com",
	})
	if err != nil {
		t.Fatalf("SaveConfig error: %v", err)
	}
	if cfg.BaseURL != "https://mail.liwei-inc.com" {
		t.Errorf("BaseURL = %q, want trailing slash stripped", cfg.BaseURL)
	}
	if cfg.Domain != "liwei-inc.com" {
		t.Errorf("Domain = %q, want liwei-inc.com", cfg.Domain)
	}
	if cfg.UpdatedAt == nil {
		t.Errorf("UpdatedAt = nil, want set")
	}
}

// TestSaveConfigDomainFromEmail mirrors the "admin@example.com -> example.com" case.
func TestSaveConfigDomainFromEmail(t *testing.T) {
	s, _ := newTestService()
	cfg, err := s.SaveConfig(context.Background(), model.MailcodeConfigInput{
		BaseURL: "https://mail.example.com",
		Domain:  "admin@example.com",
	})
	if err != nil {
		t.Fatalf("SaveConfig error: %v", err)
	}
	if cfg.Domain != "example.com" {
		t.Errorf("Domain = %q, want example.com", cfg.Domain)
	}
}

// TestBuildAccessURL mirrors build_access_url.
func TestBuildAccessURL(t *testing.T) {
	got := BuildAccessURL("https://mail.liwei-inc.com", "  Test@Liwei-inc.com ")
	want := "https://mail.liwei-inc.com/api/mail?email=test%40liwei-inc.com"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestProbeOK mirrors probe with a 200 /health response.
func TestProbeOK(t *testing.T) {
	s, _ := newTestService()
	s.httpGet = func(_ context.Context, _ string) (int, error) { return 200, nil }
	res, err := s.Probe(context.Background(), "https://mail.liwei-inc.com")
	if err != nil {
		t.Fatalf("Probe error: %v", err)
	}
	if !res.OK || !res.Reachable {
		t.Errorf("res = %+v, want ok+reachable", res)
	}
	if res.Message != "mailcode-api 连接正常" {
		t.Errorf("Message = %q, want 连接正常", res.Message)
	}
}

// TestProbeNon200 mirrors probe with a non-200 status: reachable=true, ok=false.
func TestProbeNon200(t *testing.T) {
	s, _ := newTestService()
	s.httpGet = func(_ context.Context, _ string) (int, error) { return 500, nil }
	res, err := s.Probe(context.Background(), "https://mail.liwei-inc.com")
	if err != nil {
		t.Fatalf("Probe error: %v", err)
	}
	if res.OK {
		t.Errorf("OK = true, want false")
	}
	if !res.Reachable {
		t.Errorf("Reachable = false, want true (HTTP responded)")
	}
}

// TestProbeUnreachable mirrors probe when the HTTP call errors.
func TestProbeUnreachable(t *testing.T) {
	s, _ := newTestService()
	s.httpGet = func(_ context.Context, _ string) (int, error) {
		return 0, &fakeErr{msg: "connection refused"}
	}
	res, err := s.Probe(context.Background(), "https://mail.liwei-inc.com")
	if err != nil {
		t.Fatalf("Probe error: %v", err)
	}
	if res.OK || res.Reachable {
		t.Errorf("res = %+v, want not ok / not reachable", res)
	}
}

type fakeErr struct{ msg string }

func (e *fakeErr) Error() string { return e.msg }

// TestCreateMailboxesByCount mirrors create_mailboxes with count (generates names).
func TestCreateMailboxesByCount(t *testing.T) {
	s, up := newTestService()
	// seed config with a real domain
	_, _ = s.SaveConfig(context.Background(), model.MailcodeConfigInput{
		BaseURL: "https://mail.liwei-inc.com",
		Domain:  "liwei-inc.com",
	})

	records, err := s.CreateMailboxes(context.Background(), model.MailcodeMailboxCreate{
		Count:  3,
		Prefix: "acc",
		Domain: "",
	})
	if err != nil {
		t.Fatalf("CreateMailboxes error: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("len = %d, want 3", len(records))
	}
	if len(up.calls) != 3 {
		t.Fatalf("upsert calls = %d, want 3", len(up.calls))
	}
	for i, r := range records {
		if !strings.HasSuffix(r.Email, "@liwei-inc.com") {
			t.Errorf("email[%d] = %q, want suffix @liwei-inc.com", i, r.Email)
		}
		if !strings.HasPrefix(r.Email, "acc-") {
			t.Errorf("email[%d] = %q, want prefix acc-", i, r.Email)
		}
		if !r.Imported || r.Duplicate {
			t.Errorf("record[%d] = %+v, want imported=true duplicate=false", i, r)
		}
		if !strings.Contains(r.AccessURL, "/api/mail?email=") {
			t.Errorf("accessUrl[%d] = %q, want /api/mail?email=", i, r.AccessURL)
		}
	}
}

// TestCreateMailboxesByEmails mirrors create_mailboxes with explicit emails.
func TestCreateMailboxesByEmails(t *testing.T) {
	s, up := newTestService()
	_, _ = s.SaveConfig(context.Background(), model.MailcodeConfigInput{
		BaseURL: "https://mail.liwei-inc.com",
		Domain:  "liwei-inc.com",
	})

	records, err := s.CreateMailboxes(context.Background(), model.MailcodeMailboxCreate{
		Emails: []string{"a@liwei-inc.com", "b@liwei-inc.com"},
	})
	if err != nil {
		t.Fatalf("CreateMailboxes error: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("len = %d, want 2", len(records))
	}
	if records[0].Email != "a@liwei-inc.com" || records[1].Email != "b@liwei-inc.com" {
		t.Errorf("emails = %v", records)
	}
	if len(up.calls) != 2 {
		t.Errorf("upsert calls = %d, want 2", len(up.calls))
	}
}

// TestCreateMailboxesEmpty mirrors the "no emails" 422 error.
func TestCreateMailboxesEmpty(t *testing.T) {
	s, _ := newTestService()
	_, err := s.CreateMailboxes(context.Background(), model.MailcodeMailboxCreate{})
	if err == nil {
		t.Fatalf("expected error for empty emails")
	}
	me, ok := err.(*util.HTTPError)
	if !ok {
		t.Fatalf("err type = %T, want *util.HTTPError", err)
	}
	if me.Status != 422 {
		t.Errorf("Status = %d, want 422", me.Status)
	}
}

// TestGenerateMailboxNamesFormat mirrors _generate_mailbox_names pattern.
func TestGenerateMailboxNamesFormat(t *testing.T) {
	names := generateMailboxNames(3, "acc", "liwei-inc.com")
	if len(names) != 3 {
		t.Fatalf("len = %d, want 3", len(names))
	}
	for _, n := range names {
		// acc-XXXXXXXX-XXXXXX@liwei-inc.com
		if !strings.HasPrefix(n, "acc-") || !strings.HasSuffix(n, "@liwei-inc.com") {
			t.Errorf("name = %q, wrong format", n)
		}
	}
}
