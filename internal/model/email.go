// Package model defines shared API models. JSON field names are camelCase to
// match the Python backend's Pydantic serialization (reference: resource_models.py).
package model

import "time"

// EmailStatus mirrors resource_models.EmailStatus (str Enum).
type EmailStatus string

const (
	EmailStatusAvailable   EmailStatus = "available"
	EmailStatusReserved    EmailStatus = "reserved"
	EmailStatusFailed      EmailStatus = "failed"
	EmailStatusQuarantined EmailStatus = "quarantined"
)

// ValidEmailStatus reports whether s is a legal EmailStatus value.
func ValidEmailStatus(s string) bool {
	switch EmailStatus(s) {
	case EmailStatusAvailable, EmailStatusReserved, EmailStatusFailed, EmailStatusQuarantined:
		return true
	}
	return false
}

// EmailSourceType mirrors EmailSourceType (Literal).
type EmailSourceType string

const (
	EmailSourceManual       EmailSourceType = "manual"
	EmailSourceMailcomAlias EmailSourceType = "mailcom_alias"
	EmailSourceMailcode     EmailSourceType = "mailcode"
	EmailSourceRemail       EmailSourceType = "remail"
)

// EmailRecord mirrors resource_models.EmailRecord.
type EmailRecord struct {
	ID              string          `json:"id"`
	Email           string          `json:"email"`
	AccessURL       string          `json:"accessUrl"`
	ImportedAt      time.Time       `json:"importedAt"`
	SourceType      EmailSourceType `json:"sourceType"`
	ParentEmail     *string         `json:"parentEmail"`
	Status          EmailStatus     `json:"status"`
	StatusReason    *string         `json:"statusReason"`
	StatusUpdatedAt *time.Time      `json:"statusUpdatedAt"`
}

// EmailStatusUpdateInput mirrors EmailStatusUpdateInput.
type EmailStatusUpdateInput struct {
	Status EmailStatus `json:"status"`
}

// EmailExportInput mirrors EmailExportInput.
type EmailExportInput struct {
	Scope string   `json:"scope"` // "single" | "selected" | "all"
	IDs   []string `json:"ids"`
}

// RawImportInput mirrors RawImportInput.
type RawImportInput struct {
	RawText string `json:"rawText"`
}

// BulkIdsInput mirrors BulkIdsInput.
type BulkIDsInput struct {
	IDs []string `json:"ids"`
}

// ResetFailedEmailsInput mirrors ResetFailedEmailsInput.
type ResetFailedEmailsInput struct {
	IDs []string `json:"ids"` // nil means reset all failed
}

// ImportResult mirrors ImportResult.
type ImportResult struct {
	Total          int `json:"total"`
	Imported       int `json:"imported"`
	DuplicateCount int `json:"duplicateCount"`
	ErrorCount     int `json:"errorCount"`
}

// DeleteResult mirrors DeleteResult.
type DeleteResult struct {
	Deleted int `json:"deleted"`
}

// TextExport mirrors TextExport.
type TextExport struct {
	Content             string  `json:"content"`
	Filename            string  `json:"filename"`
	Count               int     `json:"count"`
	Format              *string `json:"format"`
	SkippedMissingCount int     `json:"skippedMissingCount"`
	SkippedExpiredCount int     `json:"skippedExpiredCount"`
}

// PageSize mirrors PageSize = Literal[10, 20, 50, 100].
type PageSize int

const (
	PageSize10  PageSize = 10
	PageSize20  PageSize = 20
	PageSize50  PageSize = 50
	PageSize100 PageSize = 100
)

// ValidPageSize reports whether n is an allowed page size.
func ValidPageSize(n int) bool {
	switch PageSize(n) {
	case PageSize10, PageSize20, PageSize50, PageSize100:
		return true
	}
	return false
}

// Page mirrors Page[T].
type Page[T any] struct {
	Items    []T      `json:"items"`
	Total    int      `json:"total"`
	Page     int      `json:"page"`
	PageSize PageSize `json:"pageSize"`
}

// MailcomItem is one registration item exported by the MailCom Hub
// (/api/export/registration-items), consumed by sync_mailcom_aliases.
// Field types mirror Python's `str(item.get(...) or "")`: a missing key
// decodes to "" rather than producing a "<nil>" string.
type MailcomItem struct {
	IsAlias      bool   `json:"isAlias"`
	Email        string `json:"email"`
	AccountEmail string `json:"accountEmail"`
	AccessURL    string `json:"accessUrl"`
}
