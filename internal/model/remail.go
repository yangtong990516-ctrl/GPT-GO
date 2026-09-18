// Package model defines the remail (ReMail 接码平台) API models.
// JSON field names are camelCase to match Pydantic (reference: resource_models.py:944-1024).
package model

import "time"

// RemailConfig mirrors resource_models.RemailConfig.
type RemailConfig struct {
	APIKey      string     `json:"apiKey"`
	ProjectID   *int       `json:"projectId"` // null when unset
	EmailSuffix string     `json:"emailSuffix"`
	BaseURL     string     `json:"baseUrl"`
	UpdatedAt   *time.Time `json:"updatedAt"` // null when never saved
}

// RemailConfigInput mirrors resource_models.RemailConfigInput.
type RemailConfigInput struct {
	APIKey      string `json:"apiKey"`
	ProjectID   int    `json:"projectId"`
	EmailSuffix string `json:"emailSuffix"`
	BaseURL     string `json:"baseUrl"`
}

// RemailMailboxCreate mirrors resource_models.RemailMailboxCreate.
type RemailMailboxCreate struct {
	Count       int    `json:"count"`       // 1..1000
	EmailSuffix string `json:"emailSuffix"` // optional override
}

// RemailImportRequest mirrors resource_models.RemailImportRequest.
type RemailImportRequest struct {
	Limit       int    `json:"limit"`       // 1..100
	MaxOrders   int    `json:"maxOrders"`   // 0 = unlimited
	ProductType string `json:"productType"` // microsoft/icloud/gmail/... empty = any
	EmailSuffix string `json:"emailSuffix"` // filter by suffix
	OnlyIcloud  bool   `json:"onlyIcloud"`  // legacy: true == productType=icloud
}

// RemailWallet mirrors resource_models.RemailWallet.
type RemailWallet struct {
	OK              bool   `json:"ok"`
	Message         string `json:"message"`
	ConsumerBalance string `json:"consumerBalance"`
	TotalRecharged  string `json:"totalRecharged"`
	HistoricalSpend string `json:"historicalSpend"`
}

// RemailProbeInput mirrors resource_models.RemailProbeInput.
type RemailProbeInput struct {
	APIKey string `json:"apiKey"`
}

// RemailProbeResult mirrors resource_models.RemailProbeResult.
type RemailProbeResult struct {
	OK        bool   `json:"ok"`
	Message   string `json:"message"`
	Reachable bool   `json:"reachable"`
}

// RemailOrderResult mirrors resource_models.RemailOrderResult.
type RemailOrderResult struct {
	OrderNo          string `json:"orderNo"`
	DeliveryEmail    string `json:"deliveryEmail"`
	ServiceToken     string `json:"serviceToken"`
	VerificationCode string `json:"verificationCode"`
	Status           string `json:"status"`
}

// RemailMailboxRecord mirrors resource_models.RemailMailboxRecord.
type RemailMailboxRecord struct {
	Email     string  `json:"email"`
	AccessURL string  `json:"accessUrl"`
	Imported  bool    `json:"imported"`
	Duplicate bool    `json:"duplicate"`
	OrderNo   string  `json:"orderNo"`
	Error     *string `json:"error"` // null when no error
}
