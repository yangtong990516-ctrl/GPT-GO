// Package remail implements the remail HTTP routes (API group /api/remail),
// mirroring reference main.py lines 2235-2256.
package remail

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"gpt-go/internal/model"
	remailsvc "gpt-go/internal/service/remail"
	"gpt-go/internal/util"
)

// Handler is the remail API group handler.
type Handler struct {
	svc *remailsvc.Service
}

// NewHandler returns a remail API handler bound to the service.
func NewHandler(svc *remailsvc.Service) *Handler {
	return &Handler{svc: svc}
}

// Register mounts the /api/remail routes onto the given group.
func (h *Handler) Register(rg *gin.RouterGroup) {
	rg.GET("/config", h.getConfig)
	rg.PUT("/config", h.saveConfig)
	rg.POST("/probe", h.probe)
	rg.POST("/create-mailboxes", h.createMailboxes)
	rg.POST("/import-purchased-orders", h.importPurchasedOrders)
	rg.GET("/wallet", h.wallet)
}

// validationError builds a 422 error with a custom code/message.
func validationError(code, msg string) *util.HTTPError {
	return &util.HTTPError{Status: http.StatusUnprocessableEntity, Code: code, Message: msg}
}

// getConfig mirrors GET /api/remail/config (main.py:2235).
func (h *Handler) getConfig(c *gin.Context) {
	cfg, err := h.svc.GetConfig(c.Request.Context())
	if err != nil {
		util.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, cfg)
}

// saveConfig mirrors PUT /api/remail/config (main.py:2239).
func (h *Handler) saveConfig(c *gin.Context) {
	var payload model.RemailConfigInput
	if err := c.ShouldBindJSON(&payload); err != nil {
		util.WriteError(c, util.InvalidBody())
		return
	}
	// Validation mirrors Pydantic: apiKey 8..320, projectId >=1, emailSuffix 1..128, baseUrl https.
	if len(payload.APIKey) < 8 || len(payload.APIKey) > 320 {
		util.WriteError(c, validationError("invalid_api_key", "apiKey 长度必须在 8-320 之间"))
		return
	}
	if payload.ProjectID < 1 {
		util.WriteError(c, validationError("invalid_project_id", "projectId 必须 >= 1"))
		return
	}
	if len(payload.EmailSuffix) < 1 || len(payload.EmailSuffix) > 128 {
		util.WriteError(c, validationError("invalid_suffix", "emailSuffix 长度必须在 1-128 之间"))
		return
	}
	if !strings.HasPrefix(payload.BaseURL, "https://") {
		util.WriteError(c, validationError("invalid_base_url", "baseUrl 必须以 https:// 开头"))
		return
	}
	cfg, err := h.svc.SaveConfig(c.Request.Context(), payload)
	if err != nil {
		util.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, cfg)
}

// probe mirrors POST /api/remail/probe (main.py:2243).
func (h *Handler) probe(c *gin.Context) {
	var payload model.RemailProbeInput
	if err := c.ShouldBindJSON(&payload); err != nil {
		util.WriteError(c, util.InvalidBody())
		return
	}
	if len(payload.APIKey) > 320 {
		util.WriteError(c, validationError("invalid_api_key", "apiKey 长度超过限制"))
		return
	}
	result, err := h.svc.Probe(c.Request.Context(), payload.APIKey)
	if err != nil {
		util.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// createMailboxes mirrors POST /api/remail/create-mailboxes (main.py:2247).
func (h *Handler) createMailboxes(c *gin.Context) {
	var payload model.RemailMailboxCreate
	if err := c.ShouldBindJSON(&payload); err != nil {
		util.WriteError(c, util.InvalidBody())
		return
	}
	if payload.Count < 1 || payload.Count > 1000 {
		util.WriteError(c, validationError("invalid_count", "count 必须在 1-1000 之间"))
		return
	}
	if len(payload.EmailSuffix) > 128 {
		util.WriteError(c, validationError("invalid_suffix", "emailSuffix 长度超过 128"))
		return
	}
	records, err := h.svc.CreateMailboxes(c.Request.Context(), payload)
	if err != nil {
		util.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, records)
}

// importPurchasedOrders mirrors POST /api/remail/import-purchased-orders (main.py:2251).
func (h *Handler) importPurchasedOrders(c *gin.Context) {
	var payload model.RemailImportRequest
	if err := c.ShouldBindJSON(&payload); err != nil {
		util.WriteError(c, util.InvalidBody())
		return
	}
	// Validation mirrors Pydantic: limit 1..100, maxOrders 0..2000, productType <=32, emailSuffix <=128.
	if payload.Limit < 1 || payload.Limit > 100 {
		util.WriteError(c, validationError("invalid_limit", "limit 必须在 1-100 之间"))
		return
	}
	if payload.MaxOrders < 0 || payload.MaxOrders > 2000 {
		util.WriteError(c, validationError("invalid_max_orders", "maxOrders 必须在 0-2000 之间"))
		return
	}
	if len(payload.ProductType) > 32 {
		util.WriteError(c, validationError("invalid_product_type", "productType 长度超过 32"))
		return
	}
	if len(payload.EmailSuffix) > 128 {
		util.WriteError(c, validationError("invalid_suffix", "emailSuffix 长度超过 128"))
		return
	}
	records, err := h.svc.ImportPurchasedOrders(c.Request.Context(), payload)
	if err != nil {
		util.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, records)
}

// wallet mirrors GET /api/remail/wallet (main.py:2255).
func (h *Handler) wallet(c *gin.Context) {
	w, err := h.svc.GetBalance(c.Request.Context())
	if err != nil {
		util.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, w)
}
