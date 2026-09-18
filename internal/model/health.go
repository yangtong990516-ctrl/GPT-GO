// Package model defines shared API models whose JSON shape must stay compatible
// with the Python backend (reference: app/backend/resource_models.py).
package model

// MongoHealth mirrors resource_models.MongoHealth.
type MongoHealth struct {
	Status           string  `json:"status"`           // "online" | "offline" | "reconnecting"
	Database         string  `json:"database"`         // database name
	Error            *string `json:"error"`            // null when no error
	NextRetrySeconds *int    `json:"nextRetrySeconds"` // null when not reconnecting
}

// HealthResponse mirrors resource_models.HealthResponse.
type HealthResponse struct {
	Status  string      `json:"status"` // "ok" | "degraded"
	Mode    string      `json:"mode"`   // always "local"
	MongoDB MongoHealth `json:"mongodb"`
}
