// Package model defines the mailcode (自建 Mailcow + mail.example.com) API models.
// JSON field names are camelCase to match Pydantic (reference: resource_models.py:833-886).
package model

import "time"

// MailcodeConfig mirrors resource_models.MailcodeConfig.
type MailcodeConfig struct {
	BaseURL   string     `json:"baseUrl"`
	Domain    string     `json:"domain"`
	UpdatedAt *time.Time `json:"updatedAt"` // null when never saved
}

// MailcodeConfigInput mirrors resource_models.MailcodeConfigInput.
type MailcodeConfigInput struct {
	BaseURL string `json:"baseUrl"`
	Domain  string `json:"domain"`
}

// MailcodeMailboxCreate mirrors resource_models.MailcodeMailboxCreate.
type MailcodeMailboxCreate struct {
	Emails []string `json:"emails"`
	Count  int      `json:"count"`  // generate count mailboxes (0 = use emails)
	Prefix string   `json:"prefix"` // prefix for generated names
	Domain string   `json:"domain"` // optional domain override
}

// MailcodeProbeInput mirrors resource_models.MailcodeProbeInput.
type MailcodeProbeInput struct {
	BaseURL string `json:"baseUrl"`
}

// MailcodeProbeResult mirrors resource_models.MailcodeProbeResult.
type MailcodeProbeResult struct {
	OK        bool   `json:"ok"`
	Message   string `json:"message"`
	Reachable bool   `json:"reachable"`
}

// MailcodeMailboxRecord mirrors resource_models.MailcodeMailboxRecord.
type MailcodeMailboxRecord struct {
	Email     string  `json:"email"`
	AccessURL string  `json:"accessUrl"`
	Imported  bool    `json:"imported"`
	Duplicate bool    `json:"duplicate"`
	Error     *string `json:"error"` // null when no error
}
