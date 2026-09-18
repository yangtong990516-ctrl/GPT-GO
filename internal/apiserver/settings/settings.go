// Package settings implements the execution-settings HTTP routes
// (API group /api/settings), mirroring main.py lines 1197-1224.
package settings

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"gpt-go/internal/model"
	settingssvc "gpt-go/internal/service/settings"
	"gpt-go/internal/util"
)

// Handler is the settings API group handler.
type Handler struct {
	svc *settingssvc.Service
}

// NewHandler returns a settings API handler bound to the service.
func NewHandler(svc *settingssvc.Service) *Handler {
	return &Handler{svc: svc}
}

// Register mounts the /api/settings routes onto the given group.
func (h *Handler) Register(rg *gin.RouterGroup) {
	rg.GET("/execution", h.getExecution)
	rg.PUT("/execution", h.putExecution)
}

// getExecution mirrors GET /api/settings/execution (main.py:1197).
func (h *Handler) getExecution(c *gin.Context) {
	result, err := h.svc.Get(c.Request.Context())
	if err != nil {
		util.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// putExecution mirrors PUT /api/settings/execution (main.py:1207).
func (h *Handler) putExecution(c *gin.Context) {
	var payload model.ExecutionSettingsInput
	if err := c.ShouldBindJSON(&payload); err != nil {
		util.WriteError(c, util.InvalidBody())
		return
	}
	result, err := h.svc.Update(c.Request.Context(), payload)
	if err != nil {
		util.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}
