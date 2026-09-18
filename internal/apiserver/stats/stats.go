// Package stats implements the global stats overview HTTP route
// (GET /api/stats/overview), mirroring main.py:2485.
package stats

import (
	"net/http"

	"github.com/gin-gonic/gin"

	statssvc "gpt-go/internal/service/stats"
	"gpt-go/internal/util"
)

// Handler is the stats overview handler.
type Handler struct {
	svc *statssvc.Service
}

// NewHandler returns a stats handler bound to the service.
func NewHandler(svc *statssvc.Service) *Handler {
	return &Handler{svc: svc}
}

// Overview mirrors GET /api/stats/overview.
func (h *Handler) Overview(c *gin.Context) {
	result, err := h.svc.Overview(c.Request.Context())
	if err != nil {
		util.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}
