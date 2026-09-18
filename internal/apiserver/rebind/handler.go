// Package rebindapi 暴露换绑（邮箱换绑）HTTP API。
//
// 路由组 /api/rebind（对齐 codex-auto /api/account-rebind）：
//
//	POST /run              启动批次（账号 IDs + 代理国家/分组筛选）
//	GET  /status           当前批次快照（进度 + 账号五态）
//	POST /cancel           取消批次（进行中的账号跑完当前步骤后停止）
//	GET  /items            完成账号的换绑明细（结果矩阵用）
//	GET  /pools            可用邮箱/代理数量（前端「资源池」卡片用）
package rebindapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"gpt-go/internal/service/rebind"
	"gpt-go/internal/util"
)

// PoolCounter 抽象资源池计数（邮箱/代理）。
type PoolCounter interface {
	CountAvailableEmails(ctx context.Context) (int, error)
	CountEligibleProxies(ctx context.Context, country, group string) (int, error)
}

// Handler 是换绑 API 组 handler。
type Handler struct {
	svc   *rebind.Service
	pools PoolCounter // nil → /pools 503
}

// NewHandler 绑定换绑服务构造 API handler。
func NewHandler(svc *rebind.Service, pools PoolCounter) *Handler {
	return &Handler{svc: svc, pools: pools}
}

// Register 挂载 /api/rebind 路由。
func (h *Handler) Register(rg *gin.RouterGroup) {
	rg.POST("/run", h.run)
	rg.GET("/status", h.status)
	rg.POST("/cancel", h.cancel)
	rg.GET("/items", h.items)
	rg.GET("/pools", h.poolsHandler)
}

// runPayload 是启动批次的请求体。
type runPayload struct {
	IDs     []string          `json:"ids"`
	Country string            `json:"country"` // 代理国家筛选（空 = 任意）
	Group   string            `json:"group"`   // 代理分组筛选（空 = 任意）
	Pairs   map[string]string `json:"pairs"`   // 前端配对关系（accountID → emailID）
}

// run 启动换绑批次。
func (h *Handler) run(c *gin.Context) {
	var payload runPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		util.WriteError(c, util.InvalidBody())
		return
	}
	if len(payload.IDs) == 0 {
		util.WriteError(c, util.InvalidBody())
		return
	}
	batchID, err := h.svc.Run(c.Request.Context(), rebind.RunParams{
		AccountIDs: payload.IDs,
		Country:    strings.ToUpper(strings.TrimSpace(payload.Country)),
		Group:      strings.TrimSpace(payload.Group),
		Pairs:      payload.Pairs,
	})
	if err != nil {
		if errors.Is(err, rebind.ErrBusy) {
			c.JSON(http.StatusConflict, gin.H{"detail": gin.H{"code": "rebind_busy", "message": err.Error()}})
			return
		}
		if errors.Is(err, rebind.ErrNoAccounts) {
			c.JSON(http.StatusBadRequest, gin.H{"detail": gin.H{"code": "no_accounts", "message": err.Error()}})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"detail": gin.H{"code": "run_rejected", "message": err.Error()}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "batchId": batchID, "total": len(payload.IDs)})
}

// status 返回当前批次快照。
func (h *Handler) status(c *gin.Context) {
	b := h.svc.Status()
	if b == nil {
		c.JSON(http.StatusOK, gin.H{"running": false})
		return
	}
	c.JSON(http.StatusOK, b)
}

// cancel 取消当前批次。
func (h *Handler) cancel(c *gin.Context) {
	if !h.svc.Cancel() {
		c.JSON(http.StatusOK, gin.H{"ok": false, "message": "无运行中的批次"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// items 返回完成账号的换绑明细。
func (h *Handler) items(c *gin.Context) {
	items := h.svc.BatchItems()
	type itemView struct {
		ID          string `json:"id"`
		OldEmail    string `json:"oldEmail"`
		NewEmail    string `json:"newEmail"`
		Status      string `json:"status"`
		Error       string `json:"error,omitempty"`
		Proxy       string `json:"proxy,omitempty"`
		StartedAt   string `json:"startedAt"`
		CompletedAt string `json:"completedAt"`
	}
	out := make([]itemView, 0, len(items))
	for _, it := range items {
		out = append(out, itemView{
			ID:          it.ID,
			OldEmail:    it.OldEmail,
			NewEmail:    it.NewEmail,
			Status:      it.Status,
			Error:       it.Error,
			Proxy:       it.Proxy,
			StartedAt:   it.StartedAt.Format("2006-01-02T15:04:05Z"),
			CompletedAt: it.CompletedAt.Format("2006-01-02T15:04:05Z"),
		})
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
}

// poolsHandler 返回可用邮箱/代理数量（前端资源池卡片）。
func (h *Handler) poolsHandler(c *gin.Context) {
	if h.pools == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": gin.H{"code": "pools_unavailable", "message": "资源池未装配"}})
		return
	}
	emails, err := h.pools.CountAvailableEmails(c.Request.Context())
	if err != nil {
		util.WriteError(c, err)
		return
	}
	proxies, err := h.pools.CountEligibleProxies(c.Request.Context(),
		strings.ToUpper(c.Query("country")), c.Query("group"))
	if err != nil {
		util.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"availableEmails": emails, "eligibleProxies": proxies})
}
