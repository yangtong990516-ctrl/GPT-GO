package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// handleUpdateStatus 复刻自原 net/http 实现。
func (s *Server) handleUpdateStatus(c *gin.Context) {
	status := s.updates.Check(c.Request.Context(), c.Request.URL.Query().Get("force") == "1")
	writeJSON(c, http.StatusOK, map[string]any{
		"success": true,
		"data":    status,
	})
}
