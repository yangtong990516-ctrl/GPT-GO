// Package email implements the email-pool business logic, mirroring the
// reference resource_service.py email methods 1:1.
package email

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"gpt-go/internal/model"
	"gpt-go/internal/store"
	"gpt-go/internal/util"
)

// Constants mirroring resource_service.py / mailbox_client.py.
const (
	mailcomWebmailURL = "https://www.mail.com/int/"
)

var (
	emailPattern          = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
	urlImportEmailPattern = regexp.MustCompile(`^[^@]+@[^@\s]+\.[^@\s]+$`)
)

// api798Hosts mirrors API798_HOSTS = frozenset({"api798.com", "www.api798.com"}).
var api798Hosts = map[string]struct{}{"api798.com": {}, "www.api798.com": {}}

// Service is the email-pool service, mirroring ResourceService email methods.
type Service struct {
	store store.EmailStore
}

// NewService returns a Service backed by the given store.
func NewService(s store.EmailStore) *Service {
	return &Service{store: s}
}

// List mirrors list_emails (resource_service.py:1506).
func (s *Service) List(ctx context.Context, page, pageSize int, query, source, status string) (model.Page[model.EmailRecord], error) {
	// Mirror list_emails: "all" status/source means "no filter".
	if status == "all" {
		status = ""
	}
	if source == "all" {
		source = ""
	}
	q := store.EmailQuery{
		Status: status,
		Source: source,
		Query:  strings.ToLower(strings.TrimSpace(query)),
		Page:   page,
		Size:   pageSize,
	}
	docs, total, err := s.store.List(ctx, q)
	if err != nil {
		return model.Page[model.EmailRecord]{}, err
	}
	items := make([]model.EmailRecord, 0, len(docs))
	for i := range docs {
		items = append(items, emailRecord(docs[i]))
	}
	return model.Page[model.EmailRecord]{
		Items:    items,
		Total:    total,
		Page:     page,
		PageSize: model.PageSize(pageSize),
	}, nil
}

// SetStatus mirrors set_email_status (resource_service.py:1539).
func (s *Service) SetStatus(ctx context.Context, id, status string) (model.EmailRecord, error) {
	doc, err := s.store.SetStatus(ctx, id, status, "", time.Now().UTC(), "")
	if err != nil {
		return model.EmailRecord{}, err
	}
	return emailRecord(*doc), nil
}

// ResetFailed mirrors reset_failed_emails (resource_service.py:1583).
func (s *Service) ResetFailed(ctx context.Context, ids []string) (model.DeleteResult, error) {
	count, err := s.store.ResetFailed(ctx, ids)
	if err != nil {
		return model.DeleteResult{}, err
	}
	return model.DeleteResult{Deleted: count}, nil
}

// Delete mirrors delete_emails (resource_service.py:1675).
func (s *Service) Delete(ctx context.Context, ids []string) (model.DeleteResult, error) {
	count, err := s.store.Delete(ctx, ids)
	if err != nil {
		return model.DeleteResult{}, err
	}
	return model.DeleteResult{Deleted: count}, nil
}

// Import mirrors import_emails (resource_service.py:2934).
func (s *Service) Import(ctx context.Context, rawText string) (model.ImportResult, error) {
	total, imported, duplicates, errors := 0, 0, 0, 0
	seen := map[string]struct{}{}

	for _, rawLine := range strings.Split(strings.TrimPrefix(rawText, "\ufeff"), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		total++

		parts := strings.SplitN(line, "----", 2)
		if len(parts) != 2 {
			errors++
			continue
		}
		emailAddr := strings.TrimSpace(parts[0])
		credential := strings.TrimSpace(parts[1])
		key := normalizeEmail(emailAddr)

		isURL := validURL(credential)
		isMailcomPassword := credential != "" && !strings.Contains(credential, "://") && len(credential) <= 1024

		if !(emailPattern.MatchString(emailAddr) || (isURL && urlImportEmailPattern.MatchString(emailAddr))) ||
			!(isURL || isMailcomPassword) {
			errors++
			continue
		}
		if _, dup := seen[key]; dup {
			duplicates++
			continue
		}
		seen[key] = struct{}{}

		var inserted bool
		var err error
		if isURL {
			inserted, err = s.upsert(ctx, key, credential, "url", "", "", "")
		} else {
			inserted, err = s.upsert(ctx, key, mailcomWebmailURL, "mailcom_imap", "", "", credential)
		}
		if err != nil {
			return model.ImportResult{}, err
		}
		if inserted {
			imported++
		} else {
			duplicates++
		}
	}

	return model.ImportResult{
		Total:          total,
		Imported:       imported,
		DuplicateCount: duplicates,
		ErrorCount:     errors,
	}, nil
}

// SyncMailcomAliases mirrors sync_mailcom_aliases (resource_service.py:2984).
// The HTTP call to the MailCom Hub is injected via fetchItems so the service
// stays testable without a live hub.
func (s *Service) SyncMailcomAliases(ctx context.Context, items []model.MailcomItem) (model.ImportResult, error) {
	total, imported, duplicates, errors := 0, 0, 0, 0
	seen := map[string]struct{}{}

	for _, item := range items {
		if !item.IsAlias {
			continue
		}
		total++
		emailAddr := strings.TrimSpace(item.Email)
		parentEmail := strings.TrimSpace(item.AccountEmail)
		accessURL := strings.TrimSpace(item.AccessURL)
		key := normalizeEmail(emailAddr)

		validLocalURL := isValidMailcomURL(accessURL)
		if !emailPattern.MatchString(emailAddr) ||
			!emailPattern.MatchString(parentEmail) ||
			!validLocalURL {
			errors++
			continue
		}

		expectedURL := "http://127.0.0.1:3211/api/mail/latest?" + url.Values{"email": {emailAddr}}.Encode()

		if _, dup := seen[key]; dup {
			duplicates++
			continue
		}
		seen[key] = struct{}{}

		inserted, err := s.upsert(ctx, key, expectedURL, "url", "mailcom_alias", parentEmail, "")
		if err != nil {
			return model.ImportResult{}, err
		}
		if inserted {
			imported++
		} else {
			duplicates++
		}
	}

	return model.ImportResult{
		Total:          total,
		Imported:       imported,
		DuplicateCount: duplicates,
		ErrorCount:     errors,
	}, nil
}

// Export mirrors export_emails (resource_service.py:3222).
func (s *Service) Export(ctx context.Context, scope string, ids []string) (model.TextExport, error) {
	var exportIDs []string
	if scope != "all" {
		exportIDs = ids
	}
	docs, err := s.store.ListForExport(ctx, exportIDs)
	if err != nil {
		return model.TextExport{}, err
	}

	lines := make([]string, 0, len(docs))
	for _, d := range docs {
		lines = append(lines, fmt.Sprintf("%s----%s", d.Email, d.AccessURL))
	}
	content := strings.Join(lines, "\n")
	filename := fmt.Sprintf("emails-%d-mail-links-%s.txt", len(docs), exportTimestamp(time.Now()))

	return model.TextExport{
		Content:  content,
		Filename: filename,
		Count:    len(docs),
		Format:   nil,
	}, nil
}

// --- helpers (mirror resource_service.py / mailbox_client.py) ---

// UpsertMailbox is the public entry point for other modules (e.g. mailcode) to
// insert a mailbox into the email pool. It mirrors resource_service.upsert_email.
func (s *Service) UpsertMailbox(ctx context.Context, email, accessURL, mailboxKind, sourceType string) (bool, error) {
	return s.upsert(ctx, email, accessURL, mailboxKind, sourceType, "", "")
}

// upsert mirrors upsert_email (resource_service.py:1604).
func (s *Service) upsert(ctx context.Context, email, accessURL, mailboxKind, sourceType, parentEmail, mailboxPassword string) (bool, error) {
	normalized := normalizeEmail(email)

	// If the email is already registered (in accounts), skip insertion.
	registered, err := s.store.RegisteredEmails(ctx)
	if err != nil {
		return false, err
	}
	for _, r := range registered {
		if r == normalized {
			return false, nil
		}
	}

	doc := store.EmailDocument{
		ID:              newID(),
		Email:           email,
		EmailNormalized: normalized,
		AccessURL:       accessURL,
		ImportedAt:      time.Now().UTC(),
		Status:          string(model.EmailStatusAvailable),
		MailboxKind:     mailboxKind,
		SourceType:      normalizeSourceType(sourceType),
		RandomScore:     random63(),
	}
	if sourceType == string(model.EmailSourceMailcomAlias) && parentEmail != "" {
		doc.ParentEmail = normalizeEmail(parentEmail)
	}
	if mailboxKind == "mailcom_imap" && mailboxPassword != "" {
		doc.MailboxPassword = mailboxPassword
	}

	return s.store.Upsert(ctx, doc)
}
func normalizeSourceType(sourceType string) string {
	switch sourceType {
	case string(model.EmailSourceMailcomAlias), string(model.EmailSourceMailcode), string(model.EmailSourceRemail):
		return sourceType
	default:
		return string(model.EmailSourceManual)
	}
}

// emailRecord mirrors _email_record (resource_service.py:2538).
func emailRecord(d store.EmailDocument) model.EmailRecord {
	var parentEmail *string
	if d.ParentEmail != "" {
		v := d.ParentEmail
		parentEmail = &v
	}

	status := model.EmailStatus(d.Status)
	if !model.ValidEmailStatus(d.Status) {
		status = model.EmailStatusAvailable
	}

	var statusReason *string
	if d.StatusReason != "" {
		v := d.StatusReason
		statusReason = &v
	}

	return model.EmailRecord{
		ID:              d.ID,
		Email:           d.Email,
		AccessURL:       directMailboxAccessURL(d.AccessURL, d.Email),
		ImportedAt:      d.ImportedAt,
		SourceType:      model.EmailSourceType(normalizeSourceType(d.SourceType)),
		ParentEmail:     parentEmail,
		Status:          status,
		StatusReason:    statusReason,
		StatusUpdatedAt: d.StatusUpdatedAt,
	}
}

// DirectMailboxAccessURL is the exported form of directMailboxAccessURL, used by
// other modules (e.g. account pool) that also rewrite api798 mailbox URLs on
// output. See direct_mailbox_access_url (mailbox_client.py:94).
func DirectMailboxAccessURL(value, email string) string {
	return directMailboxAccessURL(value, email)
}

// directMailboxAccessURL mirrors direct_mailbox_access_url (mailbox_client.py:94).
func directMailboxAccessURL(value, email string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return value
	}
	host := strings.ToLower(parsed.Hostname())
	if _, ok := api798Hosts[host]; !ok {
		return value
	}
	path := strings.TrimSuffix(parsed.Path, "/")
	if path != "/get_code" && path != "/latest" {
		return value
	}

	query := parsed.Query()
	requestedEmails := query["email"]
	if len(requestedEmails) != 1 || !strings.EqualFold(strings.TrimSpace(requestedEmails[0]), strings.TrimSpace(email)) {
		return value
	}

	suppliedCodes := query["auth_code"]
	configuredCode := strings.TrimSpace(util.EnvOrDefault("API798_AUTH_CODE", ""))
	authCode := configuredCode
	if len(suppliedCodes) == 1 && strings.TrimSpace(suppliedCodes[0]) != "" {
		authCode = strings.TrimSpace(suppliedCodes[0])
	}
	if authCode == "" {
		return value
	}

	return (&url.URL{
		Scheme:   parsed.Scheme,
		Host:     parsed.Host,
		Path:     "/latest",
		RawQuery: "email=" + queryEscape(strings.TrimSpace(requestedEmails[0])) + "&auth_code=" + queryEscape(authCode),
	}).String()
}

// queryEscape mirrors urllib.parse.urlencode's quote_plus behavior for these
// values: it percent-encodes '@' (as %40) and other reserved characters while
// preserving key order (callers build the query string in insertion order).
func queryEscape(s string) string {
	return url.QueryEscape(s)
}

// normalizeEmail mirrors normalize_email (resource_service.py:148).
func normalizeEmail(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	for _, prefix := range []string{"邮箱：", "邮箱:", "email：", "email:"} {
		if strings.HasPrefix(normalized, prefix) {
			normalized = strings.TrimSpace(normalized[len(prefix):])
			break
		}
	}
	return normalized
}

// validURL mirrors valid_url (resource_service.py:177).
func validURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil {
		return false
	}
	return (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}

// isValidMailcomURL mirrors the valid_local_url check in sync_mailcom_aliases.
func isValidMailcomURL(accessURL string) bool {
	parsed, err := url.Parse(accessURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	port := parsed.Port()
	if port == "" {
		port = "80"
	}
	return parsed.Scheme == "http" &&
		(host == "127.0.0.1" || host == "localhost") &&
		port == "3211" &&
		strings.TrimSuffix(parsed.Path, "/") == "/api/mail/latest"
}

// exportTimestamp mirrors export_timestamp (resource_service.py:247).
func exportTimestamp(now time.Time) string {
	return now.Format("20060102-150405")
}

func newID() string {
	// UUID v4, 128 bits. Sufficient for unique document ids.
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func random63() int64 {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return int64(binary.BigEndian.Uint64(b) >> 1) // 63 bits
}
