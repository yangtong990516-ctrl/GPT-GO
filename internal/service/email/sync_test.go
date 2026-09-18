package email

import (
	"context"
	"testing"

	"gpt-go/internal/model"
	"gpt-go/internal/store"
)

// TestSyncMailcomAliases mirrors sync_mailcom_aliases (resource_service.py:2984).
func TestSyncMailcomAliases(t *testing.T) {
	ctx := context.Background()
	s := NewService(store.NewMockEmailStore())

	items := []model.MailcomItem{
		{IsAlias: true, Email: "alias1@x.com", AccountEmail: "mother@x.com", AccessURL: "http://127.0.0.1:3211/api/mail/latest"},
		{IsAlias: true, Email: "alias2@x.com", AccountEmail: "mother@x.com", AccessURL: "http://127.0.0.1:3211/api/mail/latest"},
		{IsAlias: false, Email: "skip@x.com", AccountEmail: "mother@x.com", AccessURL: "http://127.0.0.1:3211/api/mail/latest"}, // skipped
		{IsAlias: true, Email: "bad@", AccountEmail: "mother@x.com", AccessURL: "http://127.0.0.1:3211/api/mail/latest"},        // invalid email -> error
	}

	result, err := s.SyncMailcomAliases(ctx, items)
	if err != nil {
		t.Fatalf("SyncMailcomAliases error: %v", err)
	}
	if result.Total != 3 {
		t.Errorf("Total = %d, want 3 (isAlias!=true skipped)", result.Total)
	}
	if result.Imported != 2 {
		t.Errorf("Imported = %d, want 2", result.Imported)
	}
	if result.ErrorCount != 1 {
		t.Errorf("ErrorCount = %d, want 1", result.ErrorCount)
	}
	if result.DuplicateCount != 0 {
		t.Errorf("DuplicateCount = %d, want 0", result.DuplicateCount)
	}
}

// TestDirectMailboxAccessURL mirrors direct_mailbox_access_url (mailbox_client.py:94).
func TestDirectMailboxAccessURL(t *testing.T) {
	cases := []struct {
		name  string
		value string
		email string
		env   string // API798_AUTH_CODE
		want  string
	}{
		{
			name:  "non-api798 host unchanged",
			value: "https://mail.example.com/get_code?email=a@x.com",
			email: "a@x.com",
			want:  "https://mail.example.com/get_code?email=a@x.com",
		},
		{
			name:  "api798 host wrong path unchanged",
			value: "https://api798.com/other?email=a@x.com",
			email: "a@x.com",
			want:  "https://api798.com/other?email=a@x.com",
		},
		{
			name:  "api798 host email mismatch unchanged",
			value: "https://api798.com/latest?email=b@x.com",
			email: "a@x.com",
			want:  "https://api798.com/latest?email=b@x.com",
		},
		{
			name:  "api798 host no auth code unchanged",
			value: "https://api798.com/latest?email=a@x.com",
			email: "a@x.com",
			env:   "",
			want:  "https://api798.com/latest?email=a@x.com",
		},
		{
			name:  "api798 host with env auth code rewrites",
			value: "https://api798.com/latest?email=a@x.com",
			email: "a@x.com",
			env:   "SECRET",
			want:  "https://api798.com/latest?email=a%40x.com&auth_code=SECRET",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("API798_AUTH_CODE", tc.env)
			got := directMailboxAccessURL(tc.value, tc.email)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
