// Package emails implements the email-pool HTTP routes (API group /api/emails),
// mirroring reference main.py lines 2145-2215.
package emails

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"gpt-go/internal/model"
	"gpt-go/internal/service/email"
	"gpt-go/internal/store"
	"gpt-go/internal/util"
)

// validSource mirrors the route pattern for `source`:
// ^(all|standard|mailcom_alias|mailcode|remail)$  (cloudmail 已按决策移除;
// remail 由 import-purchased-orders 落库,邮箱池主要来源,必须可筛选).
var validSource = map[string]struct{}{
	"all": {}, "standard": {}, "mailcom_alias": {}, "mailcode": {}, "remail": {},
}

// validStatus mirrors the route pattern for `status`:
// ^(all|available|reserved|failed|quarantined)$.
var validStatus = map[string]struct{}{
	"all": {}, "available": {}, "reserved": {}, "failed": {}, "quarantined": {},
}

// Handler is the email API group handler.
type Handler struct {
	svc *email.Service
}

// NewHandler returns an email API handler bound to the service.
func NewHandler(svc *email.Service) *Handler {
	return &Handler{svc: svc}
}

// Register mounts the /api/emails routes onto the given group.
func (h *Handler) Register(rg *gin.RouterGroup) {
	rg.GET("", h.list)
	rg.POST("/import", h.importEmails)
	rg.POST("/sync-mailcom-aliases", h.syncMailcomAliases)
	rg.POST("/bulk-delete", h.bulkDelete)
	rg.POST("/reset-failed", h.resetFailed)
	rg.POST("/export", h.export)
	rg.POST("/:email_id/status", h.updateStatus)
}

// validationError builds a 422 error with a custom code/message.
func validationError(code, msg string) *util.HTTPError {
	return &util.HTTPError{Status: http.StatusUnprocessableEntity, Code: code, Message: msg}
}

// list mirrors GET /api/emails (main.py:2145).
func (h *Handler) list(c *gin.Context) {
	page := queryIntDefault(c, "page", 1)
	pageSize := queryIntDefault(c, "pageSize", 10)
	q := c.Query("q")
	source := c.DefaultQuery("source", "all")
	status := c.DefaultQuery("status", "available")

	if page < 1 {
		page = 1
	}
	if !model.ValidPageSize(pageSize) {
		util.WriteError(c, validationError("invalid_page_size", "pageSize must be one of 10/20/50/100"))
		return
	}
	if _, ok := validSource[source]; !ok {
		util.WriteError(c, validationError("invalid_source", "source is invalid"))
		return
	}
	if _, ok := validStatus[status]; !ok {
		util.WriteError(c, validationError("invalid_status", "status is invalid"))
		return
	}
	if len(q) > 320 {
		util.WriteError(c, validationError("query_too_long", "q must be <= 320 chars"))
		return
	}

	result, err := h.svc.List(c.Request.Context(), page, pageSize, q, source, status)
	if err != nil {
		util.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// importEmails mirrors POST /api/emails/import (main.py:2158).
func (h *Handler) importEmails(c *gin.Context) {
	var payload model.RawImportInput
	if err := c.ShouldBindJSON(&payload); err != nil {
		util.WriteError(c, util.InvalidBody())
		return
	}
	result, err := h.svc.Import(c.Request.Context(), payload.RawText)
	if err != nil {
		util.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// syncMailcomAliases mirrors POST /api/emails/sync-mailcom-aliases (main.py:2163).
func (h *Handler) syncMailcomAliases(c *gin.Context) {
	items, status, code, msg := fetchMailcomItems(c)
	if status != http.StatusOK {
		util.WriteError(c, &util.HTTPError{Status: status, Code: code, Message: msg})
		return
	}
	result, err := h.svc.SyncMailcomAliases(c.Request.Context(), items)
	if err != nil {
		util.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// bulkDelete mirrors POST /api/emails/bulk-delete (main.py:2192).
func (h *Handler) bulkDelete(c *gin.Context) {
	var payload model.BulkIDsInput
	if err := c.ShouldBindJSON(&payload); err != nil || len(payload.IDs) == 0 || len(payload.IDs) > 10000 {
		util.WriteError(c, validationError("invalid_body", "ids must be 1..10000"))
		return
	}
	result, err := h.svc.Delete(c.Request.Context(), payload.IDs)
	if err != nil {
		util.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// resetFailed mirrors POST /api/emails/reset-failed (main.py:2197).
func (h *Handler) resetFailed(c *gin.Context) {
	var ids []string
	if c.Request.ContentLength > 0 {
		var payload model.ResetFailedEmailsInput
		if err := c.ShouldBindJSON(&payload); err != nil {
			util.WriteError(c, util.InvalidBody())
			return
		}
		ids = payload.IDs
	}
	result, err := h.svc.ResetFailed(c.Request.Context(), ids)
	if err != nil {
		util.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// updateStatus mirrors POST /api/emails/{email_id}/status (main.py:2205).
func (h *Handler) updateStatus(c *gin.Context) {
	emailID := c.Param("email_id")
	var payload model.EmailStatusUpdateInput
	if err := c.ShouldBindJSON(&payload); err != nil || !model.ValidEmailStatus(string(payload.Status)) {
		util.WriteError(c, validationError("invalid_body", "invalid status"))
		return
	}
	record, err := h.svc.SetStatus(c.Request.Context(), emailID, string(payload.Status))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			util.WriteError(c, util.ResourceNotFound("邮箱不存在"))
			return
		}
		util.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, record)
}

// export mirrors POST /api/emails/export (main.py:2210).
func (h *Handler) export(c *gin.Context) {
	var payload model.EmailExportInput
	if err := c.ShouldBindJSON(&payload); err != nil {
		util.WriteError(c, util.InvalidBody())
		return
	}
	if payload.Scope != "all" && len(payload.IDs) == 0 {
		util.WriteError(c, validationError("ids_required", "选中导出必须提供邮箱 ID"))
		return
	}
	result, err := h.svc.Export(c.Request.Context(), payload.Scope, payload.IDs)
	if err != nil {
		util.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// --- helpers ---

func queryIntDefault(c *gin.Context, key string, def int) int {
	v := c.Query(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// fetchMailcomItems calls the MailCom Hub and returns parsed items.
// It mirrors the httpx call in main.py:2163-2190, but injectable for tests.
var fetchMailcomItems = func(c *gin.Context) (items []model.MailcomItem, status int, code, msg string) {
	client := util.NewHTTPClient(10, "")
	req, err := client.NewRequest(c.Request.Context(), http.MethodGet, "http://127.0.0.1:3211/api/export/registration-items", nil)
	if err != nil {
		return nil, http.StatusBadGateway, "mailcom_hub_unavailable", "MailCom Hub 暂时不可用，请先启动本机邮箱管理器"
	}

	statusCode, body, err := client.DoRaw(req)
	if err != nil {
		return nil, http.StatusBadGateway, "mailcom_hub_unavailable", "MailCom Hub 暂时不可用，请先启动本机邮箱管理器"
	}
	if statusCode != http.StatusOK {
		return nil, http.StatusBadGateway, "mailcom_hub_invalid_response", "MailCom Hub 返回 HTTP " + strconv.Itoa(statusCode)
	}

	var payload struct {
		Items []model.MailcomItem `json:"items"`
	}
	if err := util.UnmarshalLenient(body, &payload); err != nil {
		return nil, http.StatusBadGateway, "mailcom_hub_invalid_response", "MailCom Hub 响应解析失败"
	}
	return payload.Items, http.StatusOK, "", ""
}
