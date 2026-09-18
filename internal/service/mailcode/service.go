// Package mailcode implements the self-hosted Mailcow + mailcode-api mailbox
// service, mirroring reference mailcode_service.py 1:1.
package mailcode

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	"gpt-go/internal/model"
	"gpt-go/internal/store"
	"gpt-go/internal/util"
)

// Constants mirroring mailcode_service.py.
const (
	configKey      = "mailcode_config"
	defaultBaseURL = "https://mail.example.com"
	defaultDomain  = "example.com"
	// createMailboxInterval mirrors the 0.2s sleep between upserts.
	createMailboxInterval = 200 * time.Millisecond
)

// EmailUpserter is the minimal contract mailcode needs from the email pool to
// insert a generated mailbox. Implemented by email.Service.UpsertMailbox.
type EmailUpserter interface {
	UpsertMailbox(ctx context.Context, email, accessURL, mailboxKind, sourceType string) (bool, error)
}

// Service mirrors mailcode_service.MailcodeService.
type Service struct {
	store   store.MailcodeStore
	upsert  EmailUpserter
	httpGet func(ctx context.Context, url string) (int, error) // injectable for tests
}

// NewService returns a mailcode Service.
func NewService(s store.MailcodeStore, up EmailUpserter) *Service {
	return &Service{store: s, upsert: up, httpGet: defaultHTTPGet}
}

// defaultHTTPGet uses the unified HTTP client (util) for the probe /health call.
func defaultHTTPGet(ctx context.Context, rawURL string) (int, error) {
	client := util.NewHTTPClient(0, "")
	req, err := client.NewRequest(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	return client.Do(req, nil)
}

// GetConfig mirrors get_config (mailcode_service.py:82).
func (s *Service) GetConfig(ctx context.Context) (model.MailcodeConfig, error) {
	doc, err := s.store.Load(ctx)
	if err != nil {
		return model.MailcodeConfig{}, err
	}
	base := doc.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	domain := doc.Domain
	if domain == "" {
		domain = defaultDomain
	}
	return model.MailcodeConfig{
		BaseURL:   base,
		Domain:    domain,
		UpdatedAt: doc.UpdatedAt,
	}, nil
}

// SaveConfig mirrors save_config (mailcode_service.py:90).
func (s *Service) SaveConfig(ctx context.Context, in model.MailcodeConfigInput) (model.MailcodeConfig, error) {
	domain := normalizeDomain(in.Domain)
	now := time.Now().UTC()
	doc := store.MailcodeConfigDocument{
		BaseURL:   strings.TrimRight(in.BaseURL, "/"),
		Domain:    domain,
		UpdatedAt: &now,
	}
	if err := s.store.Save(ctx, doc); err != nil {
		return model.MailcodeConfig{}, err
	}
	return s.GetConfig(ctx)
}

// BuildAccessURL mirrors build_access_url (mailcode_service.py:105).
// Returns base + "/api/mail?" + urlencode({"email": email}).
func BuildAccessURL(baseURL, email string) string {
	base := strings.TrimRight(baseURL, "/")
	if base == "" {
		base = defaultBaseURL
	}
	return base + "/api/mail?" + url.Values{"email": {normalizeEmail(email)}}.Encode()
}

// Probe mirrors probe (mailcode_service.py:112). It GETs base + "/health" and
// reports ok when the HTTP status is 200. On any error it returns reachable=false.
func (s *Service) Probe(ctx context.Context, baseURL string) (model.MailcodeProbeResult, error) {
	incoming := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	base := incoming
	if base == "" {
		doc, err := s.store.Load(ctx)
		if err != nil {
			return model.MailcodeProbeResult{}, err
		}
		base = strings.TrimRight(doc.BaseURL, "/")
		if base == "" {
			base = defaultBaseURL
		}
	}

	status, err := s.httpGet(ctx, base+"/health")
	if err != nil {
		return model.MailcodeProbeResult{
			OK:        false,
			Message:   err.Error(),
			Reachable: false,
		}, nil
	}
	ok := status == http.StatusOK
	message := fmt.Sprintf("mailcode-api 可达（HTTP %d）", status)
	if ok {
		message = "mailcode-api 连接正常"
	}
	return model.MailcodeProbeResult{
		OK:        ok,
		Message:   message,
		Reachable: true,
	}, nil
}

// CreateMailboxes mirrors create_mailboxes (mailcode_service.py:151).
func (s *Service) CreateMailboxes(ctx context.Context, in model.MailcodeMailboxCreate) ([]model.MailcodeMailboxRecord, error) {
	doc, err := s.store.Load(ctx)
	if err != nil {
		return nil, err
	}
	base := doc.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	domain := normalizeDomain(in.Domain)
	if domain == "" {
		domain = normalizeDomain(doc.Domain)
	}
	if domain == "" {
		domain = defaultDomain
	}

	var emails []string
	if in.Count > 0 {
		emails = generateMailboxNames(in.Count, in.Prefix, domain)
	} else {
		for _, e := range in.Emails {
			if e != "" {
				emails = append(emails, normalizeEmail(e))
			}
		}
	}
	if len(emails) == 0 {
		return nil, &util.HTTPError{Code: "mailcode_no_emails", Message: "未提供要导入的邮箱", Status: 422}
	}

	records := make([]model.MailcodeMailboxRecord, 0, len(emails))
	for idx, email := range emails {
		if idx > 0 {
			time.Sleep(createMailboxInterval)
		}
		normalized := normalizeEmail(email)
		accessURL := BuildAccessURL(base, normalized)
		inserted, uerr := s.upsert.UpsertMailbox(ctx, normalized, accessURL, "url", "mailcode")
		if uerr != nil {
			msg := uerr.Error()
			records = append(records, model.MailcodeMailboxRecord{
				Email:     normalized,
				AccessURL: "",
				Imported:  false,
				Duplicate: false,
				Error:     &msg,
			})
			continue
		}
		records = append(records, model.MailcodeMailboxRecord{
			Email:     normalized,
			AccessURL: accessURL,
			Imported:  inserted,
			Duplicate: !inserted,
			Error:     nil,
		})
	}
	return records, nil
}

// generateMailboxNames mirrors _generate_mailbox_names (mailcode_service.py:133).
// Pattern: {prefix}-{8 lowercase letters}-{6 hex}, or {8 letters}-{6 hex} without prefix.
func generateMailboxNames(count int, prefix, domain string) []string {
	domain = strings.TrimLeft(strings.ToLower(strings.TrimSpace(domain)), "@")
	seen := map[string]struct{}{}
	names := make([]string, 0, count)
	for i := 0; i < count; i++ {
		letters := randomLowercase(8)
		hexSuffix := randomHex(3) // 6 hex chars
		var local string
		if strings.TrimSpace(prefix) != "" {
			local = fmt.Sprintf("%s-%s-%s", strings.TrimSpace(prefix), letters, hexSuffix)
		} else {
			local = fmt.Sprintf("%s-%s", letters, hexSuffix)
		}
		name := local
		if domain != "" {
			name = local + "@" + domain
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names
}

// normalizeDomain mirrors the domain normalization in save_config/create_mailboxes:
// strip -> lower -> lstrip("@") -> if "@" in it, keep the part after the last "@".
func normalizeDomain(value string) string {
	d := strings.TrimLeft(strings.ToLower(strings.TrimSpace(value)), "@")
	if idx := strings.LastIndex(d, "@"); idx >= 0 {
		d = strings.TrimSpace(d[idx+1:])
	}
	return d
}

// normalizeEmail mirrors mailcode_service.normalize_email (strip+lower).
func normalizeEmail(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// randomLowercase returns n random lowercase ASCII letters, mirroring Python's
// secrets.choice over ascii_lowercase. Uses crypto/rand.Int for unbiased
// sampling (avoiding the modulo bias of raw byte % 26).
func randomLowercase(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz"
	b := make([]byte, n)
	for i := range b {
		idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(letters))))
		if err != nil {
			// crypto/rand failure is unrecoverable; fall back to byte % 26.
			var rb [1]byte
			_, _ = rand.Read(rb[:])
			idx = big.NewInt(int64(rb[0] % byte(len(letters))))
		}
		b[i] = letters[idx.Int64()]
	}
	return string(b)
}

// randomHex returns n random bytes hex-encoded (secrets.token_hex), mirroring
// Python's secrets.token_hex(3) -> 6 hex chars.
func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
