// Package paymentcheck implements the payment-methods-check HTTP routes
// (API group /api/payment-check), mirroring the payment-check design doc.
//
// 路由：
//
//	POST /api/payment-check/run        启动批次（只收账号 ID，AT 从后端账号库读）
//	GET  /api/payment-check/status     批次实时状态（进度 + 每账号状态机）
//	POST /api/payment-check/cancel     取消当前批次
//	GET  /api/payment-check/routes-config   读取区域线路配置（环境变量）
//	GET  /api/payment-check/accounts/:id/routes  单账号线路明细
package paymentcheck

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"gpt-go/internal/model"
	"gpt-go/internal/service/paymentcheck"
	"gpt-go/internal/util"
)

// ProxyLister 抽象代理池查询（供前端线路点选器拉可选代理）。
type ProxyLister interface {
	ListProxies(ctx context.Context, page, pageSize int, query, country string) (model.Page[model.ProxyRecord], error)
}

// Handler is the payment-check API group handler.
type Handler struct {
	svc     *paymentcheck.Service
	proxies ProxyLister // nil → /proxies 接口 503
}

// NewHandler returns a payment-check API handler bound to the service.
func NewHandler(svc *paymentcheck.Service, proxies ProxyLister) *Handler {
	return &Handler{svc: svc, proxies: proxies}
}

// Register mounts the /api/payment-check routes onto the given group.
func (h *Handler) Register(rg *gin.RouterGroup) {
	rg.POST("/run", h.run)
	rg.GET("/status", h.status)
	rg.POST("/cancel", h.cancel)
	rg.GET("/routes-config", h.routesConfig)
	rg.GET("/proxies", h.listProxies)
	rg.GET("/accounts/:id/routes", h.accountRoutes)
	rg.GET("/items", h.batchItems)
}

// runPayload 是启动批次的请求体（对齐文档 §1：API 只接收账号 ID 或裸 token）。
type runPayload struct {
	IDs        []string    `json:"ids"`        // 账号池模式：账号 ID 列表
	Tokens     []tokenSpec `json:"tokens"`     // 粘贴 AT 模式：裸 token 列表（不落库）
	Routes     []routeSpec `json:"routes"`     // 点选模式：结构化线路（含代理 URL 列表）
	RoutesText string      `json:"routesText"` // 备选：直接提交「国家|币种|语言|代理」文本
	WriteBack  *bool       `json:"writeBack"`  // 默认 true（tokens 模式强制 false）
}

// tokenSpec 是一条粘贴的 AT（djblook 式用法：只给 token，可选备注名）。
type tokenSpec struct {
	AccessToken string `json:"accessToken"`
	Label       string `json:"label"` // 展示名（如邮箱），仅用于结果矩阵标识，可空
}

type routeSpec struct {
	Country  string   `json:"country"`
	Currency string   `json:"currency"`
	Locale   string   `json:"locale"`
	Proxies  []string `json:"proxies"`
}

// run mirrors POST /api/payment-check/run.
func (h *Handler) run(c *gin.Context) {
	var payload runPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		util.WriteError(c, util.InvalidBody())
		return
	}
	if len(payload.IDs) == 0 && len(payload.Tokens) == 0 {
		util.WriteError(c, util.InvalidBody())
		return
	}

	routes, err := resolveRoutes(payload)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": gin.H{"code": "invalid_routes", "message": err.Error()}})
		return
	}
	writeBack := true
	if payload.WriteBack != nil {
		writeBack = *payload.WriteBack
	}

	params := paymentcheck.RunParams{Routes: routes, WriteBack: writeBack}
	total := 0
	switch {
	case len(payload.Tokens) > 0:
		// 粘贴 AT 模式：token 不落库（writeBack 强制 false）。
		for _, t := range payload.Tokens {
			params.Tokens = append(params.Tokens, paymentcheck.TokenInput{
				AccessToken: strings.TrimSpace(t.AccessToken),
				Label:       strings.TrimSpace(t.Label),
			})
		}
		params.WriteBack = false
		total = len(params.Tokens)
	default:
		params.AccountIDs = payload.IDs
		total = len(payload.IDs)
	}

	batchID, err := h.svc.Run(c.Request.Context(), params)
	if err != nil {
		if errors.Is(err, paymentcheck.ErrBusy) {
			c.JSON(http.StatusConflict, gin.H{"detail": gin.H{"code": "payment_check_busy", "message": err.Error()}})
			return
		}
		if errors.Is(err, paymentcheck.ErrNoRoutes) {
			c.JSON(http.StatusBadRequest, gin.H{"detail": gin.H{"code": "no_proxy_routes", "message": err.Error()}})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"detail": gin.H{"code": "run_rejected", "message": err.Error()}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "batchId": batchID, "total": total})
}

// resolveRoutes 解析请求中的线路：优先结构化 routes，其次 routesText，
// 最后回退到环境变量 PAYMENT_PROXY_ROUTES（含 MOMO_* 旧名迁移）。
// resolveRoutes 统一把结构化 routes[] 和 routesText 两种入参都转成文本再过
// ParseRoutesText——确保两条路径走同一套国家/币种/语言校验，不出现"结构化
// 入参绕过校验"的旁路。
func resolveRoutes(payload runPayload) ([]paymentcheck.Route, error) {
	if len(payload.Routes) > 0 {
		var b strings.Builder
		for _, r := range payload.Routes {
			if len(r.Proxies) == 0 {
				return nil, errors.New("线路 " + r.Country + " 未选择代理")
			}
			for _, p := range r.Proxies {
				fmt.Fprintf(&b, "%s|%s|%s|%s\n", r.Country, r.Currency, r.Locale, p)
			}
		}
		return paymentcheck.ParseRoutesText(b.String())
	}
	text := payload.RoutesText
	if text == "" {
		text = paymentcheck.DefaultRoutesText()
	}
	return paymentcheck.ParseRoutesText(text)
}

// status mirrors GET /api/payment-check/status.
func (h *Handler) status(c *gin.Context) {
	b := h.svc.Status()
	if b == nil {
		c.JSON(http.StatusOK, gin.H{"running": false})
		return
	}
	c.JSON(http.StatusOK, b)
}

// cancel mirrors POST /api/payment-check/cancel.
func (h *Handler) cancel(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"ok": h.svc.Cancel()})
}

// listProxies mirrors GET /api/payment-check/proxies：返回代理池可用代理，
// 供前端线路点选器按国家分组展示（djblook 式点选，免手写代理串）。
func (h *Handler) listProxies(c *gin.Context) {
	if h.proxies == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": gin.H{"code": "proxy_pool_unavailable", "message": "代理池未装配"}})
		return
	}
	page, err := h.proxies.ListProxies(c.Request.Context(), 1, 500, "", c.Query("country"))
	if err != nil {
		util.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, page)
}

// routesConfig mirrors GET /api/payment-check/routes-config：返回当前生效的
// 区域线路配置文本（供前端配置区预填）。
func (h *Handler) routesConfig(c *gin.Context) {
	text := paymentcheck.DefaultRoutesText()
	routes, err := paymentcheck.ParseRoutesText(text)
	enabled := 0
	if err == nil {
		enabled = len(paymentcheck.EnabledRoutes(routes))
	}
	c.JSON(http.StatusOK, gin.H{
		"routesText":    text,
		"routeCount":    len(routes),
		"enabledRoutes": enabled,
		"parseError":    errString(err),
	})
}

// accountRoutes mirrors GET /api/payment-check/accounts/:id/routes.
func (h *Handler) accountRoutes(c *gin.Context) {
	routes, ok := h.svc.AccountRoutes(c.Param("id"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"detail": gin.H{"code": "not_found", "message": "该账号暂无本批次线路明细"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"routes": routes})
}

// batchItems mirrors GET /api/payment-check/items：返回本批次全部完成账号的
// 线路明细（粘贴 AT 模式的前端结果矩阵从这里拉取）。
func (h *Handler) batchItems(c *gin.Context) {
	items := h.svc.BatchItems()
	type itemView struct {
		ID          string                                `json:"id"`
		Label       string                                `json:"label"`
		Status      string                                `json:"status"`
		Methods     []string                              `json:"methods"`
		ZeroMethods []string                              `json:"zeroMethods"`
		Routes      map[string]model.PaymentRouteResult `json:"routes"`
	}
	out := make([]itemView, 0, len(items))
	for _, it := range items {
		out = append(out, itemView{
			ID: it.ID, Label: it.Email, Status: it.Status,
			Methods: it.Methods, ZeroMethods: it.ZeroMethods, Routes: it.Routes,
		})
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
