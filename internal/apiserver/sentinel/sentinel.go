// Package sentinel implements the Sentinel (CDK SDK) version-check HTTP routes
// (API group /api/sentinel), mirroring main.py:473-529.
//
// Note: unlike the other resource modules (which use camelCase via Pydantic),
// the sentinel endpoints return snake_case field names — the Python source
// passes Mongo documents and internal dicts through verbatim. This is preserved
// 1:1.
package sentinel

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"gpt-go/internal/model"
	sentinelsvc "gpt-go/internal/service/sentinel"
	"gpt-go/internal/util"
)

// Handler is the sentinel API group handler.
type Handler struct {
	sched *sentinelsvc.Scheduler
}

// NewHandler returns a sentinel API handler bound to the scheduler.
func NewHandler(sched *sentinelsvc.Scheduler) *Handler {
	return &Handler{sched: sched}
}

// Register mounts the /api/sentinel routes onto the given group.
func (h *Handler) Register(rg *gin.RouterGroup) {
	rg.GET("/config", h.getConfig)
	rg.PUT("/config", h.saveConfig)
	rg.GET("/version", h.version)
}

// configResponse mirrors the get_config response shape. error is present only
// on failure (main.py:479 returns defaults + "error" with HTTP 200).
type configResponse struct {
	model.SentinelConfig
	Error string `json:"error,omitempty"`
}

// getConfig mirrors GET /api/sentinel/config (main.py:473).
func (h *Handler) getConfig(c *gin.Context) {
	cfg, err := h.sched.GetConfig(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusOK, configResponse{
			SentinelConfig: model.DefaultSentinelConfig(),
			Error:          err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, configResponse{SentinelConfig: cfg})
}

// saveConfig mirrors PUT /api/sentinel/config (main.py:481).
func (h *Handler) saveConfig(c *gin.Context) {
	var payload model.SentinelConfigInput
	if err := c.ShouldBindJSON(&payload); err != nil {
		util.WriteError(c, util.InvalidBody())
		return
	}
	cfg, err := h.sched.SaveConfig(c.Request.Context(), payload)
	if err != nil {
		// main.py raises HTTPException(500, detail=str(exc)); WriteError maps a
		// non-HTTPError to 500 internal_error. To preserve the raw detail, write
		// it directly.
		c.JSON(http.StatusInternalServerError, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, configResponse{SentinelConfig: cfg})
}

// version mirrors GET /api/sentinel/version (main.py:489). It projects the
// latest persisted check over the bundled constants.
func (h *Handler) version(c *gin.Context) {
	info := model.SentinelVersionInfo{
		Version:           model.SentinelVersion,
		ConfiguredVersion: model.SentinelVersion,
		Reachable:         false,
		ProxyUsed:         false,
	}
	last, err := h.sched.LatestResult(c.Request.Context())
	if err == nil && last != nil {
		info.Version = last.Version
		if info.Version == "" {
			info.Version = model.SentinelVersion
		}
		info.URL = &last.URL
		info.ETag = last.ETag
		info.LastModified = last.LastModified
		info.ContentLength = last.ContentLength
		info.Reachable = last.Reachable
		info.IsExpired = last.IsExpired
		info.ProxyUsed = last.ProxyUsed
		if !last.CheckedAt.IsZero() {
			iso := last.CheckedAt.UTC().Format("2006-01-02T15:04:05.000000+00:00")
			info.LastCheckedAt = &iso
		}
		if last.Error != nil {
			info.CheckError = last.Error
		}
	}
	c.JSON(http.StatusOK, info)
}
