// Package mailcode implements the mailcode HTTP routes (API group /api/mailcode),
// mirroring reference main.py lines 2218-2232.
package mailcode

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"gpt-go/internal/model"
	"gpt-go/internal/service/mailcode"
	"gpt-go/internal/util"
)

// Handler is the mailcode API group handler.
type Handler struct {
	svc *mailcode.Service
}

// NewHandler returns a mailcode API handler bound to the service.
func NewHandler(svc *mailcode.Service) *Handler {
	return &Handler{svc: svc}
}

// Register mounts the /api/mailcode routes onto the given group.
func (h *Handler) Register(rg *gin.RouterGroup) {
	rg.GET("/config", h.getConfig)
	rg.PUT("/config", h.saveConfig)
	rg.POST("/probe", h.probe)
	rg.POST("/create-mailboxes", h.createMailboxes)
}

// validationError builds a 422 error with a custom code/message.
func validationError(code, msg string) *util.HTTPError {
	return &util.HTTPError{Status: http.StatusUnprocessableEntity, Code: code, Message: msg}
}

// getConfig mirrors GET /api/mailcode/config (main.py:2218).
func (h *Handler) getConfig(c *gin.Context) {
	cfg, err := h.svc.GetConfig(c.Request.Context())
	if err != nil {
		util.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, cfg)
}

// saveConfig mirrors PUT /api/mailcode/config (main.py:2222).
func (h *Handler) saveConfig(c *gin.Context) {
	var payload model.MailcodeConfigInput
	if err := c.ShouldBindJSON(&payload); err != nil {
		util.WriteError(c, util.InvalidBody())
		return
	}
	// Validation mirrors Pydantic: baseUrl min 8, max 320, must start with https://.
	if len(payload.BaseURL) < 8 || len(payload.BaseURL) > 320 {
		util.WriteError(c, validationError("invalid_base_url", "baseUrl 长度必须在 8-320 之间"))
		return
	}
	if !strings.HasPrefix(payload.BaseURL, "https://") {
		util.WriteError(c, validationError("invalid_base_url", "baseUrl 必须以 https:// 开头"))
		return
	}
	if len(payload.Domain) > 128 {
		util.WriteError(c, validationError("invalid_domain", "domain 长度超过限制"))
		return
	}
	cfg, err := h.svc.SaveConfig(c.Request.Context(), payload)
	if err != nil {
		util.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, cfg)
}

// probe mirrors POST /api/mailcode/probe (main.py:2226).
func (h *Handler) probe(c *gin.Context) {
	var payload model.MailcodeProbeInput
	if err := c.ShouldBindJSON(&payload); err != nil {
		util.WriteError(c, util.InvalidBody())
		return
	}
	if len(payload.BaseURL) > 320 {
		util.WriteError(c, validationError("invalid_base_url", "baseUrl 长度超过限制"))
		return
	}
	result, err := h.svc.Probe(c.Request.Context(), payload.BaseURL)
	if err != nil {
		util.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// createMailboxes mirrors POST /api/mailcode/create-mailboxes (main.py:2230).
func (h *Handler) createMailboxes(c *gin.Context) {
	var payload model.MailcodeMailboxCreate
	if err := c.ShouldBindJSON(&payload); err != nil {
		util.WriteError(c, util.InvalidBody())
		return
	}
	// Validation mirrors Pydantic: count 0..200, emails max 200, prefix max 64, domain max 128.
	if payload.Count < 0 || payload.Count > 200 {
		util.WriteError(c, validationError("invalid_count", "count 必须在 0-200 之间"))
		return
	}
	if len(payload.Emails) > 200 {
		util.WriteError(c, validationError("invalid_emails", "emails 数量超过 200"))
		return
	}
	if len(payload.Prefix) > 64 {
		util.WriteError(c, validationError("invalid_prefix", "prefix 长度超过 64"))
		return
	}
	if len(payload.Domain) > 128 {
		util.WriteError(c, validationError("invalid_domain", "domain 长度超过 128"))
		return
	}

	records, err := h.svc.CreateMailboxes(c.Request.Context(), payload)
	if err != nil {
		util.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, records)
}
