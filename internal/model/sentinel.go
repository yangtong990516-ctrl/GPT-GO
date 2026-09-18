// Package model defines the sentinel (CDK SDK) version-check API models.
// JSON field names are camelCase to match the Python contract
// (reference: sentinel_version_scheduler.py + main.py:473-529).
package model

import "time"

// Sentinel constants mirroring sentinel_quickjs.py:48-50.
const (
	// SentinelVersion is the hardcoded CDK SDK version currently bundled.
	SentinelVersion = "20260810913b"
	// sentinelOrigin is the Sentinel CDN origin (host without scheme/path).
	sentinelOrigin = "https://sentinel.openai.com"
)

// SentinelSDKURL returns the sdk.js URL for the bundled version.
func SentinelSDKURL() string {
	return sentinelOrigin + "/sentinel/" + SentinelVersion + "/sdk.js"
}

// SentinelReqURL returns the backend req endpoint.
func SentinelReqURL() string {
	return sentinelOrigin + "/backend-api/sentinel/req"
}

// SentinelFrameURL returns the frame.html endpoint (authoritative latest version).
func SentinelFrameURL() string {
	return sentinelOrigin + "/backend-api/sentinel/frame.html"
}

// SentinelConfig mirrors sentinel_settings document fields (enabled /
// interval_hours / proxy). The runtime switch is stored in Mongo and hot-reloaded
// via PUT /api/sentinel/config — no restart required (project decision).
type SentinelConfig struct {
	Enabled       bool   `json:"enabled" bson:"enabled"`
	IntervalHours int    `json:"interval_hours" bson:"interval_hours"`
	Proxy         string `json:"proxy" bson:"proxy"`
}

// DefaultSentinelConfig returns the built-in defaults, mirroring
// sentinel_version_scheduler.DEFAULT_CONFIG. These apply only until a config is
// first saved; after that the stored document wins.
func DefaultSentinelConfig() SentinelConfig {
	return SentinelConfig{Enabled: true, IntervalHours: 24, Proxy: ""}
}

// SentinelConfigInput mirrors the PUT /api/sentinel/config payload. Pointers
// distinguish "field absent" from "field present with zero value", mirroring
// Python's `if "enabled" in payload and payload["enabled"] is not None`.
type SentinelConfigInput struct {
	Enabled       *bool   `json:"enabled"`
	IntervalHours *int    `json:"interval_hours"`
	Proxy         *string `json:"proxy"`
}

// SentinelCheckResult mirrors the document persisted to sentinel_version_checks
// (sentinel_version_scheduler._check_once). It carries the full detection result
// including the Step-1 "newest version" enhancement fields.
type SentinelCheckResult struct {
	// Core fields
	ConfiguredVersion string    `json:"configured_version" bson:"configured_version"`
	IsExpired         *bool     `json:"is_expired" bson:"is_expired"`
	Reachable         bool      `json:"reachable" bson:"reachable"`
	ProxyUsed         bool      `json:"proxy_used" bson:"proxy_used"`
	CheckedAt         time.Time `json:"checked_at" bson:"checked_at"`
	Error             *string   `json:"error" bson:"error"`
	Version           string    `json:"version" bson:"version"`
	URL               string    `json:"url" bson:"url"`
	StatusCode        *int      `json:"status_code" bson:"status_code"`
	ETag              *string   `json:"etag" bson:"etag"`
	LastModified      *string   `json:"last_modified" bson:"last_modified"`
	ContentLength     *int      `json:"content_length" bson:"content_length"`
	ContentType       *string   `json:"content_type" bson:"content_type"`
	// Step-1 enhancement fields
	NewestAvailable   bool    `json:"newest_available" bson:"newest_available"`
	NewestVersion     *string `json:"newest_version" bson:"newest_version"`
	DiscoveryMethod   string  `json:"discovery_method" bson:"discovery_method"`
	NewestDownloaded  bool    `json:"newest_downloaded" bson:"newest_downloaded"`
	DiscoveryError    *string `json:"discovery_error" bson:"discovery_error"`
	NewestDownloadErr *string `json:"newest_download_error" bson:"newest_download_error"`
}

// SentinelVersionInfo mirrors the GET /api/sentinel/version response body
// (main.py:496-529). It is a projection of the latest check result over the
// bundled constants.
type SentinelVersionInfo struct {
	Version           string  `json:"version"`
	URL               *string `json:"url"`
	ETag              *string `json:"etag"`
	LastModified      *string `json:"last_modified"`
	ContentLength     *int    `json:"content_length"`
	Reachable         bool    `json:"reachable"`
	ConfiguredVersion string  `json:"configured_version"`
	IsExpired         *bool   `json:"is_expired"`
	LastCheckedAt     *string `json:"last_checked_at"`
	ProxyUsed         bool    `json:"proxy_used"`
	CheckError        *string `json:"check_error"`
}
