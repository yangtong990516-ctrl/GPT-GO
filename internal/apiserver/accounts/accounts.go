// Package accounts implements the account-pool HTTP routes (API group /api/accounts),
// mirroring reference main.py lines 1226-1256 (basic-information CRUD subset).
package accounts

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"gpt-go/internal/model"
	accountsvc "gpt-go/internal/service/account"
	"gpt-go/internal/service/accountsecurity"
	"gpt-go/internal/service/mfacheck"
	"gpt-go/internal/service/plancheck"
	"gpt-go/internal/util"
)

// Handler is the accounts API group handler.
type Handler struct {
	svc *accountsvc.Service
	// planChecker 提供「验活 + 套餐/优惠资格检查」合并能力(批量触发),可为 nil(未装配时禁用该路由)。
	planChecker *plancheck.Service
	// mfaChecker 提供「批量查询 2FA 状态」能力,可为 nil。
	mfaChecker *mfacheck.Service
	// security 提供「补 2FA(开通 TOTP)」能力,可为 nil。
	security *accountsecurity.Service
	// makeOTP 由邮箱 accessUrl 构造 OTP provider(补 2FA 登录若走 OTP 分支用),可为 nil。
	makeOTP accountsecurity.OtpProviderFunc
}

// NewHandler returns an accounts API handler bound to the service.
func NewHandler(svc *accountsvc.Service) *Handler {
	return &Handler{svc: svc}
}

// WithPlanChecker 注入合并检查服务(批量验活+查优惠),返回自身便于链式装配。
func (h *Handler) WithPlanChecker(pc *plancheck.Service) *Handler {
	h.planChecker = pc
	return h
}

// WithMfaChecker 注入批量 2FA 状态查询服务,返回自身便于链式装配。
func (h *Handler) WithMfaChecker(mc *mfacheck.Service) *Handler {
	h.mfaChecker = mc
	return h
}

// WithSecurity 注入补 2FA(开通 TOTP)服务与 OTP provider 工厂,返回自身便于链式装配。
func (h *Handler) WithSecurity(sec *accountsecurity.Service, makeOTP accountsecurity.OtpProviderFunc) *Handler {
	h.security = sec
	h.makeOTP = makeOTP
	return h
}

// Register mounts the /api/accounts routes onto the given group.
func (h *Handler) Register(rg *gin.RouterGroup) {
	rg.GET("", h.list)
	rg.POST("", h.create)
	rg.POST("/bulk-delete", h.bulkDelete)
	// 批量「验活 + 查询优惠资格」:一次请求同时写 alive + plan 两组字段(对齐 codex check-alive/check-promotion)。
	rg.POST("/check-combined", h.checkCombined)
	// 批量「查询 2FA 状态」(对齐 codex check_2fa_status):回写 totpStatus/mfaFlagEnabled。
	rg.POST("/check-2fa", h.check2FA)
	// 补 2FA(开通 TOTP):单账号 + 批量(对齐 codex ensure-2fa / bulk-ensure-2fa)。
	rg.POST("/:id/ensure-2fa", h.ensure2FAOne)
	rg.POST("/bulk-ensure-2fa", h.ensure2FABatch)
}

// list mirrors GET /api/accounts (main.py:1226).
func (h *Handler) list(c *gin.Context) {
	page, pageSize := pageParams(c)

	filter := model.AccountListFilter{
		Query:         c.Query("q"),
		Promotion:     c.Query("promotion"),
		Country:       c.Query("country"),
		Alive:         c.Query("alive"),
		Payment:       c.Query("payment"),
		ZeroPayment:   c.Query("zero_payment"),
		PaymentStatus: c.Query("payment_status"),
	}

	result, err := h.svc.List(c.Request.Context(), filter, page, pageSize)
	if err != nil {
		util.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// create mirrors POST /api/accounts (main.py:1245, 201).
func (h *Handler) create(c *gin.Context) {
	var payload model.AccountCreate
	if err := c.ShouldBindJSON(&payload); err != nil {
		util.WriteError(c, util.InvalidBody())
		return
	}
	if err := validateCreate(payload); err != nil {
		util.WriteError(c, err)
		return
	}

	record, err := h.svc.Create(c.Request.Context(), payload)
	if err != nil {
		util.WriteError(c, err)
		return
	}
	c.JSON(http.StatusCreated, record)
}

// bulkDelete mirrors POST /api/accounts/bulk-delete (main.py:1253).
func (h *Handler) bulkDelete(c *gin.Context) {
	var payload struct {
		IDs []string `json:"ids"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		util.WriteError(c, util.InvalidBody())
		return
	}
	result, err := h.svc.Delete(c.Request.Context(), payload.IDs)
	if err != nil {
		util.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// checkCombined 是 POST /api/accounts/check-combined。
// 对一批账号做「验活 + 套餐/优惠资格检查」(一次请求写两组字段)。
// 请求:{ids:[...](≤100,必填), proxyId?:string(强制指定代理,默认复用注册代理)}
// 响应:{requested,alive,dead,failed,skipped,items:[{id,status,planStatus,errorCode,httpStatus}]}
// 对齐 codex check-alive / check-promotion 的批量语义;单号失败不影响其它。
func (h *Handler) checkCombined(c *gin.Context) {
	if h.planChecker == nil {
		util.WriteError(c, &util.HTTPError{Status: http.StatusServiceUnavailable, Code: "check_unavailable", Message: "验活/套餐检查服务未装配"})
		return
	}
	var payload struct {
		IDs     []string `json:"ids"`
		ProxyID string   `json:"proxyId"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		util.WriteError(c, util.InvalidBody())
		return
	}
	if len(payload.IDs) == 0 {
		util.WriteError(c, &util.HTTPError{Status: http.StatusUnprocessableEntity, Code: "empty_ids", Message: "ids 不能为空"})
		return
	}
	if len(payload.IDs) > 100 {
		util.WriteError(c, &util.HTTPError{Status: http.StatusUnprocessableEntity, Code: "too_many_ids", Message: "一次最多检查 100 个账号"})
		return
	}
	res, err := h.planChecker.CheckCombined(c.Request.Context(), payload.IDs, payload.ProxyID)
	if err != nil {
		util.WriteError(c, err)
		return
	}
	// 转成稳定的对外 JSON 结构(snake_case 字段名)。
	type item struct {
		ID         string `json:"id"`
		Status     string `json:"status"`
		PlanStatus string `json:"planStatus"`
		ErrorCode  string `json:"errorCode,omitempty"`
		HTTPStatus *int   `json:"httpStatus,omitempty"`
	}
	items := make([]item, len(res.Items))
	for i, it := range res.Items {
		items[i] = item{ID: it.ID, Status: it.Status, PlanStatus: it.PlanStatus, ErrorCode: it.ErrorCode, HTTPStatus: it.HTTPStatus}
	}
	c.JSON(http.StatusOK, gin.H{
		"requested": res.Requested,
		"alive":     res.Alive,
		"dead":      res.Dead,
		"failed":    res.Failed,
		"skipped":   res.Skipped,
		"items":     items,
	})
}

// check2FA 是 POST /api/accounts/check-2fa。
// 对一批账号查询 2FA 状态(用已存 accessToken 查 /me,无需重认证),回写 totpStatus/mfaFlagEnabled。
// 请求:{ids:[...](≤100,必填), proxyId?:string}
// 响应:{requested,succeeded,failed,skipped,items:[{id,status,totpStatus,mfaFlagEnabled,errorCode}]}
func (h *Handler) check2FA(c *gin.Context) {
	if h.mfaChecker == nil {
		util.WriteError(c, &util.HTTPError{Status: http.StatusServiceUnavailable, Code: "check_unavailable", Message: "2FA 状态查询服务未装配"})
		return
	}
	var payload struct {
		IDs     []string `json:"ids"`
		ProxyID string   `json:"proxyId"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		util.WriteError(c, util.InvalidBody())
		return
	}
	if len(payload.IDs) == 0 {
		util.WriteError(c, &util.HTTPError{Status: http.StatusUnprocessableEntity, Code: "empty_ids", Message: "ids 不能为空"})
		return
	}
	if len(payload.IDs) > 100 {
		util.WriteError(c, &util.HTTPError{Status: http.StatusUnprocessableEntity, Code: "too_many_ids", Message: "一次最多检查 100 个账号"})
		return
	}
	res, err := h.mfaChecker.CheckBatch(c.Request.Context(), payload.IDs, payload.ProxyID)
	if err != nil {
		util.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"requested": res.Requested,
		"succeeded": res.Succeeded,
		"failed":    res.Failed,
		"skipped":   res.Skipped,
		"items":     res.Items,
	})
}

// ensure2FAOne 处理 POST /api/accounts/:id/ensure-2fa:对单账号补 2FA(开通 TOTP)。
func (h *Handler) ensure2FAOne(c *gin.Context) {
	if h.security == nil {
		util.WriteError(c, &util.HTTPError{Status: http.StatusServiceUnavailable, Code: "security_unavailable", Message: "补 2FA 服务未装配"})
		return
	}
	id := c.Param("id")
	if id == "" {
		util.WriteError(c, util.InvalidBody())
		return
	}
	var payload struct {
		ProxyID string `json:"proxyId"`
	}
	_ = c.ShouldBindJSON(&payload) // proxyId 可空,绑定失败不致命
	res := h.security.EnsureOne(c.Request.Context(), id, payload.ProxyID, h.makeOTP)
	status := http.StatusOK
	if res.Status == "failed" {
		status = http.StatusUnprocessableEntity
	}
	c.JSON(status, res)
}

// ensure2FABatch 处理 POST /api/accounts/bulk-ensure-2fa:批量补 2FA。
func (h *Handler) ensure2FABatch(c *gin.Context) {
	if h.security == nil {
		util.WriteError(c, &util.HTTPError{Status: http.StatusServiceUnavailable, Code: "security_unavailable", Message: "补 2FA 服务未装配"})
		return
	}
	var payload struct {
		IDs      []string `json:"ids"`
		ProxyID  string   `json:"proxyId"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		util.WriteError(c, util.InvalidBody())
		return
	}
	if len(payload.IDs) == 0 {
		util.WriteError(c, &util.HTTPError{Status: http.StatusUnprocessableEntity, Code: "empty_ids", Message: "ids 不能为空"})
		return
	}
	if len(payload.IDs) > 100 {
		util.WriteError(c, &util.HTTPError{Status: http.StatusUnprocessableEntity, Code: "too_many_ids", Message: "一次最多补 100 个账号"})
		return
	}
	res := h.security.EnsureBatch(c.Request.Context(), payload.IDs, payload.ProxyID, 0, h.makeOTP)
	c.JSON(http.StatusOK, res)
}

// Stats mirrors GET /api/stats/overview (main.py:2485) — account block only.
// emails/proxies blocks return zero values (their stats are not yet migrated).
func (h *Handler) Stats(c *gin.Context) {
	acc, err := h.svc.Stats(c.Request.Context())
	if err != nil {
		util.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, model.OverviewStats{Accounts: acc})
}

// pageParams parses page/pageSize with defaults 1/10 and validation.
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

// validateCreate mirrors Pydantic field constraints on AccountCreate.
// validateCreate 只强制邮箱（手工录入的最小可用单元）；密码/TOTP/取件 URL
// 允许留空后补（paymentcheck 只需要 AccessToken，注册流才需要其余字段）。
func validateCreate(p model.AccountCreate) *util.HTTPError {
	if len(p.Email) < 3 || len(p.Email) > 320 {
		return &util.HTTPError{Status: http.StatusUnprocessableEntity, Code: "invalid_email", Message: "email 长度必须在 3-320 之间"}
	}
	if len(p.ChatgptPassword) > 1024 {
		return &util.HTTPError{Status: http.StatusUnprocessableEntity, Code: "invalid_password", Message: "chatgptPassword 长度不能超过 1024"}
	}
	if len(p.TotpSecret) > 256 {
		return &util.HTTPError{Status: http.StatusUnprocessableEntity, Code: "invalid_totp", Message: "totpSecret 长度不能超过 256"}
	}
	if len(p.EmailAccessURL) > 4096 {
		return &util.HTTPError{Status: http.StatusUnprocessableEntity, Code: "invalid_url", Message: "emailAccessUrl 长度不能超过 4096"}
	}
	if p.AccountType != "" && p.AccountType != model.AccountTypePlus && p.AccountType != model.AccountTypeFree {
		return &util.HTTPError{Status: http.StatusUnprocessableEntity, Code: "invalid_account_type", Message: "accountType 必须是 plus 或 free"}
	}
	return nil
}
