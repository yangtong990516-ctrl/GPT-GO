// Package proxy implements the proxy-pool HTTP routes (API group /api/proxies),
// mirroring main.py:2297-2483.
package proxy

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"gpt-go/internal/model"
	proxysvc "gpt-go/internal/service/proxy"
	"gpt-go/internal/util"
)

// SettingsGetter 抽象「读当前 ExecutionSettings」(由 settingsSvc 提供)。
type SettingsGetter interface {
	Get(ctx context.Context) (model.ExecutionSettings, error)
}

// Handler is the proxies API group handler.
type Handler struct {
	svc      *proxysvc.Service
	settings SettingsGetter // 测速并发(ExecutionSettings.proxyCheckConcurrency)
}

// NewHandler returns a proxies API handler bound to the service.
func NewHandler(svc *proxysvc.Service) *Handler {
	return &Handler{svc: svc}
}

// WithSettings 注入 ExecutionSettings 读取器(测速并发用;可选,缺省 16)。
func (h *Handler) WithSettings(g SettingsGetter) *Handler {
	h.settings = g
	return h
}

// Register mounts the /api/proxies routes onto the given group.
func (h *Handler) Register(rg *gin.RouterGroup) {
	rg.GET("", h.list)
	rg.POST("/import", h.importProxies)
	rg.GET("/countries", h.countries)
	rg.POST("/test", h.test)
	rg.GET("/groups", h.groups)
	rg.PATCH("/groups", h.updateGroup)
	rg.DELETE("/groups", h.deleteGroup)
	rg.POST("/bulk-delete", h.bulkDelete)
	rg.DELETE("", h.clear)
	rg.POST("/restore-used", h.restoreUsed)
	rg.PATCH("/:proxy_id", h.update)
	rg.POST("/:proxy_id/status", h.updateStatus)
	rg.DELETE("/:proxy_id", h.delete)
}

// list mirrors GET /api/proxies (main.py:2297).
func (h *Handler) list(c *gin.Context) {
	page, pageSize := pageParams(c)
	q := c.Query("q")
	country := c.Query("country")
	result, err := h.svc.ListProxies(c.Request.Context(), page, pageSize, q, country)
	if err != nil {
		util.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// importProxies mirrors POST /api/proxies/import (main.py:2328).
func (h *Handler) importProxies(c *gin.Context) {
	var payload model.ProxyImportInput
	if err := c.ShouldBindJSON(&payload); err != nil {
		util.WriteError(c, util.InvalidBody())
		return
	}
	result, err := h.svc.ImportProxies(c.Request.Context(), payload.RawText, payload.Country, payload.Group)
	if err != nil {
		util.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// countries mirrors GET /api/proxies/countries (main.py:2405).
func (h *Handler) countries(c *gin.Context) {
	result, err := h.svc.CountrySummaries(c.Request.Context())
	if err != nil {
		util.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// test mirrors POST /api/proxies/test (main.py:2410).
func (h *Handler) test(c *gin.Context) {
	var payload model.ProxyTestInput
	if err := c.ShouldBindJSON(&payload); err != nil {
		util.WriteError(c, util.InvalidBody())
		return
	}
	timeout := payload.TimeoutSeconds
	if timeout <= 0 {
		timeout = 8
	}
	// 测速并发:现读 ExecutionSettings.proxyCheckConcurrency(缺省 16)。
	concurrency := 16
	if h.settings != nil {
		if st, serr := h.settings.Get(c.Request.Context()); serr == nil && st.ProxyCheckConcurrency > 0 {
			concurrency = st.ProxyCheckConcurrency
		}
	}
	result, err := h.svc.TestStoredProxies(c.Request.Context(), payload.Country, payload.Group, timeout, concurrency)
	if err != nil {
		util.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// groups mirrors GET /api/proxies/groups (main.py:2423).
func (h *Handler) groups(c *gin.Context) {
	result, err := h.svc.GroupSummaries(c.Request.Context())
	if err != nil {
		util.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// updateGroup mirrors PATCH /api/proxies/groups (main.py:2428).
func (h *Handler) updateGroup(c *gin.Context) {
	var payload model.ProxyGroupUpdate
	if err := c.ShouldBindJSON(&payload); err != nil {
		util.WriteError(c, util.InvalidBody())
		return
	}
	result, err := h.svc.UpdateGroup(c.Request.Context(), payload)
	if err != nil {
		util.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// deleteGroup mirrors DELETE /api/proxies/groups (main.py:2439).
func (h *Handler) deleteGroup(c *gin.Context) {
	country := c.Query("country")
	group := c.Query("group")
	result, err := h.svc.DeleteGroup(c.Request.Context(), country, group)
	if err != nil {
		util.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// bulkDelete mirrors POST /api/proxies/bulk-delete (main.py:2447).
func (h *Handler) bulkDelete(c *gin.Context) {
	var payload model.BulkIdsInput
	if err := c.ShouldBindJSON(&payload); err != nil {
		util.WriteError(c, util.InvalidBody())
		return
	}
	result, err := h.svc.DeleteProxies(c.Request.Context(), payload.IDs)
	if err != nil {
		util.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// clear mirrors DELETE /api/proxies (main.py:2452).
func (h *Handler) clear(c *gin.Context) {
	result, err := h.svc.ClearProxies(c.Request.Context())
	if err != nil {
		util.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// restoreUsed mirrors POST /api/proxies/restore-used (main.py:2474).
func (h *Handler) restoreUsed(c *gin.Context) {
	var payload model.RestoreUsedProxiesInput
	if err := c.ShouldBindJSON(&payload); err != nil {
		util.WriteError(c, util.InvalidBody())
		return
	}
	result, err := h.svc.RestoreUsed(c.Request.Context(), payload)
	if err != nil {
		util.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// update mirrors PATCH /api/proxies/{proxy_id} (main.py:2457).
func (h *Handler) update(c *gin.Context) {
	id := c.Param("proxy_id")
	var payload model.ProxyUpdate
	if err := c.ShouldBindJSON(&payload); err != nil {
		util.WriteError(c, util.InvalidBody())
		return
	}
	if payload.Enabled == nil && payload.Country == nil && payload.Group == nil {
		util.WriteError(c, util.InvalidBody())
		return
	}
	result, err := h.svc.UpdateProxy(c.Request.Context(), id, payload)
	if err != nil {
		util.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// updateStatus mirrors POST /api/proxies/{proxy_id}/status (main.py:2467).
func (h *Handler) updateStatus(c *gin.Context) {
	id := c.Param("proxy_id")
	var payload model.ProxyStatusUpdateInput
	if err := c.ShouldBindJSON(&payload); err != nil {
		util.WriteError(c, util.InvalidBody())
		return
	}
	result, err := h.svc.SetProxyStatus(c.Request.Context(), id, payload.Status)
	if err != nil {
		util.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// delete mirrors DELETE /api/proxies/{proxy_id} (main.py:2480).
func (h *Handler) delete(c *gin.Context) {
	id := c.Param("proxy_id")
	result, err := h.svc.DeleteProxy(c.Request.Context(), id)
	if err != nil {
		util.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// pageParams binds page (ge=1, default 1) and pageSize (10/20/50/100, default 10).
func pageParams(c *gin.Context) (int, int) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "10"))
	if !model.ValidPageSize(pageSize) {
		pageSize = 10
	}
	return page, pageSize
}
