// Package runs 提供「批量注册运行任务」HTTP 入口（对齐 codex-auto run_protocol_batch）。
//
// 这是注册链路的【触发点】：POST /api/runs 创建一个批量注册任务，按【A 方案】
// 在每次发起时实时读 ExecutionSettings（并发数 / 同出口IP上限 / 单号超时），
// 现场组装 BatchParams + 拨号器，驱动 signup.Service.RunBatch。
//
// 为什么实时读 settings（A 方案，用户拍板）：
//   - 对齐 codex-auto：它每次 run_protocol_batch 都现读 settings 传参；
//   - 配置栏改了并发/上限后【立即生效】，无需重启；
//   - 每批新建拨号器（干净 exitIPRegistry），恰好匹配「批次内出口 IP 去重」语义。
package runs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-go/internal/model"
	"gpt-go/internal/service/signup"
)

// randomShortID 返回 8 位十六进制随机串(runID 冲突后缀)。
func randomShortID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// SettingsGetter 抽象「读当前 ExecutionSettings」（由 settingsSvc 提供）。
// 每次发起批量注册时调用，拿到最新配置（A 方案实时注入）。
type SettingsGetter interface {
	Get(ctx context.Context) (model.ExecutionSettings, error)
}

// BatchRunner 抽象「执行批量注册」（由 signup.Service 提供）。便于测试注入 mock。
type BatchRunner interface {
	RunBatch(ctx context.Context, params signup.BatchParams) (*signup.BatchResult, error)
}

// Handler 是 runs group 的 HTTP 处理器。
type Handler struct {
	// settings 读执行参数（A 方案：每次发起现读）。
	settings SettingsGetter
	// newDialer 按 exitIPCap 现场造拨号器（每批一个干净 registry）。
	newDialer func(exitIPCap, maxRedial int) *signup.SessionDialer
	// runner 执行批量注册（signup.Service）。
	runner BatchRunner
	// registry 进程内批次进度注册表（内存实时维护，见 signup.RunRegistry）。
	// 可为 nil（不跟踪进度）；装配层注入共享实例。
	registry *signup.RunRegistry
	// runStore 可选：批次落库（mongo runs collection）。nil 则 SaveRun 报未配置。
	runStore RunStore
	// logHub 进程级运行日志中心（内存环形 + SSE 订阅）。可为 nil（不写结构化日志）。
	logHub *signup.RunLogHub
}

// RunStore 抽象「批次 RunState 持久化」（mongo insert，由 store 层实现）。
// 批次结束后用户可手动触发保存——见 saveRun 接口。
type RunStore interface {
	// InsertRun 把一个批次的终态 RunState 落库（mongo runs collection 一条 insert）。
	InsertRun(ctx context.Context, st signup.RunState) error
}

// NewHandler 构造 runs 处理器。
//
//	settings   ExecutionSettings 读取器（settingsSvc）
//	newDialer  按 exitIPCap 造拨号器（装配层注入，内部 signup.NewCDNProber）
//	runner     批量注册执行器（装配好的 signup.Service）
func NewHandler(settings SettingsGetter, newDialer func(exitIPCap, maxRedial int) *signup.SessionDialer, runner BatchRunner) *Handler {
	return &Handler{settings: settings, newDialer: newDialer, runner: runner}
}

// WithRegistry 注入批次进度注册表（内存实时跟踪）。
func (h *Handler) WithRegistry(r *signup.RunRegistry) *Handler {
	h.registry = r
	return h
}

// WithRunStore 注入批次落库存储（mongo，可选）。
func (h *Handler) WithRunStore(rs RunStore) *Handler {
	h.runStore = rs
	return h
}

// WithLogHub 注入进程级运行日志中心（结构化日志 + SSE）。nil 则不写结构化日志。
func (h *Handler) WithLogHub(hub *signup.RunLogHub) *Handler {
	h.logHub = hub
	return h
}

// Register 挂载 runs 路由到 group（前缀 /api/runs）。
func (h *Handler) Register(rg *gin.RouterGroup) {
	rg.POST("", h.createRun)
	// 进度查询（内存实时）。
	rg.GET("", h.listRuns)            // 全部批次（新→旧）
	rg.GET("/current", h.currentRuns) // 进行中的批次
	rg.GET("/:runId", h.getRun)       // 单批次进度
	// 批次落库（手动触发，mongo insert）。
	rg.POST("/:runId/save", h.saveRun) // 把该批次终态落库
}

// createRunRequest 是 POST /api/runs 的请求体。并发数/超时/出口IP上限【不在此传】
// ——它们来自配置栏（ExecutionSettings），由 handler 每次现读注入（A 方案），保证配置即时生效。
// 这里只收「本批任务」特有的参数（数量、国家、组、邮箱来源等）。
type createRunRequest struct {
	// Count 目标注册数量（必填，>=1）。
	Count int `json:"count" binding:"required,min=1"`
	// Country 目标国家码（可选，会话型通道实测为准）。
	Country string `json:"country"`
	// Group 代理分组（可选）。
	Group string `json:"group"`
	// EmailSource 邮箱来源（可选，默认 mailcode）。
	EmailSource string `json:"emailSource"`
	// RunID 批次标识（可选，缺省自动生成）。
	RunID string `json:"runId"`
	// StopOnError 首错即停（可选）。
	StopOnError bool `json:"stopOnError"`
	// OTPTimeout 取 OTP 超时秒数（可选，0 用默认 120）。
	OTPTimeout int `json:"otpTimeout"`
	// LeaseSeconds 代理租约秒数（可选，0 用默认 300）。
	LeaseSeconds int `json:"leaseSeconds"`
}

// createRunResponse 是 POST /api/runs 的响应（批量执行结果汇总）。
type createRunResponse struct {
	OK        int      `json:"ok"`
	Failed    int      `json:"failed"`
	Cancelled int      `json:"cancelled"`
	Total     int      `json:"total"`
	RunID     string   `json:"runId"`
	Errors    []string `json:"errors,omitempty"`
	// Applied 回显本次实际生效的执行参数（A 方案：证明 settings 已实时注入），便于前端核对。
	Applied appliedSettings `json:"applied"`
}

// appliedSettings 回显本次批量注册实际用到的执行参数（来自配置栏实时读取）。
type appliedSettings struct {
	Concurrency             int `json:"concurrency"`
	MaxRegistrationsPerExit int `json:"maxRegistrationsPerExitIp"`
	TaskTimeoutSeconds      int `json:"taskTimeoutSeconds"`
}

// createRun 处理 POST /api/runs：实时读 settings → 组装 BatchParams → RunBatch。
//
// A 方案核心（每次发起现读配置栏）：
//   - Concurrency        ← settings.Concurrency（批量并发数）
//   - RunParams.TaskTimeoutSeconds ← settings.TaskTimeoutSeconds（单号超时）
//   - 拨号器 exitIPCap   ← settings.MaxRegistrationsPerExitIP（同出口IP并发上限）
func (h *Handler) createRun(c *gin.Context) {
	var req createRunRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": gin.H{
			"code": "invalid_request", "message": "请求体非法: " + err.Error()}})
		return
	}
	if h.runner == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": gin.H{
			"code": "signup_unavailable", "message": "注册引擎未装配"}})
		return
	}

	// ── A 方案：每次发起现读 ExecutionSettings（配置栏改动即时生效）──
	st, err := h.settings.Get(c.Request.Context())
	if err != nil {
		// 读不到配置不致命：用默认执行参数（对齐 settings_store 的 DefaultExecutionSettings）。
		st = model.DefaultExecutionSettings()
	}

	// 拨号器：按 settings.maxRegistrationsPerExitIp 现场造（每批干净 registry）。
	var dialer *signup.SessionDialer
	if h.newDialer != nil {
		dialer = h.newDialer(st.MaxRegistrationsPerExitIP, st.ProxyRetryCount)
	}

	// runID:缺省/冲突时追加随机短后缀 —— 防「秒级时间戳撞名」或「用户显式传相同
	// runId」导致两并发批次复用同一日志 buffer(日志交错、SSE 订阅者收到错批日志、
	// 两聚合器并发写同一 RunLogger,M2)。
	runID := req.RunID
	if runID == "" {
		runID = "run-" + time.Now().UTC().Format("20060102-150405")
	}
	if h.logHub != nil {
		if _, exists := h.logHub.Summary(runID); exists {
			runID = runID + "-" + randomShortID()
		}
	}

	// ── 运行日志：本批一个聚合日志器(防大批次刷屏)；里程碑/失败直通,过程事件节流汇总 ──
	// 设计:N 个并发账号共用一个聚合器,高频「步骤/租代理/预留邮箱」等合并为周期性
	// progress 汇总(N=1000 也只有几十条),失败仍逐条留痕(要排查)。详见
	// runlog_aggregator.go。底层 RunLogger 仍可通过 agg.RunLogger() 取(下钻)。
	var rlog *signup.RunLogger       // 直写句柄(终态用)
	var agg *signup.RunLogAggregator // 聚合器(Log/Step/Progress 走它)
	if h.logHub != nil {
		rlog = h.logHub.Logger(runID)
		agg = signup.NewRunLogAggregator(rlog, signup.DefaultAggregatorConfig())
		agg.Emit(signup.LogInfo, "run_created", "批量注册任务已创建", "", map[string]any{
			"requestedCount": req.Count, "concurrency": st.Concurrency,
			"country": req.Country, "group": req.Group, "emailSource": req.EmailSource,
		})
	}

	params := signup.BatchParams{
		RunParams: signup.RunParams{
			Country:            req.Country,
			Group:              req.Group,
			EmailSource:        req.EmailSource,
			RunID:              runID,
			LeaseSeconds:       req.LeaseSeconds,
			OTPTimeout:         req.OTPTimeout,
			TaskTimeoutSeconds: st.TaskTimeoutSeconds, // ← settings 注入（单号超时）
			Dialer:             dialer,                // ← settings 注入（出口IP上限）
			Log:                agg,                   // ← 聚合日志器(service 高频事件被汇总,防刷屏)
			Step:               nil,                   // 下方统一设为 agg.StepLogger()
			EnableRegistrationSecurity: st.EnableRegistrationSecurity, // ← settings 注入(密码+2FA 合一开关)
		},
		Count:       req.Count,
		Concurrency: st.Concurrency, // ← settings 注入（并发数）
		StopOnError: req.StopOnError,
		Cancel:      c.Request.Context().Done(), // 客户端断开即取消（对齐 cancel_event）
	}
	// step 文本也写入聚合日志器(protocol_step 被合并为进度汇总)。
	if agg != nil {
		params.Step = agg.StepLogger()
	}

	// ── 内存进度跟踪：批次开始建 tracker，每账号完成实时更新，结束定终态 ──
	var tracker *signup.RunTracker
	if h.registry != nil {
		tracker = h.registry.Start(runID, signup.RunKindProtocol, req.Count, st.Concurrency, req.Country, req.Group, req.EmailSource)
	}
	// 进度回调：既更新 tracker，也把账号级成败写进日志（带 email 关联）。
	params.Progress = func(item signup.BatchItemResult) {
		if tracker != nil {
			tracker.Track(item.OK, item.Cancelled)
		}
		if agg == nil {
			return
		}
		switch {
		case item.OK:
			// account_succeeded 由 service 已发(经聚合器合并)；此处不重复。
		case item.Cancelled:
			agg.Emit(signup.LogWarning, "account_cancelled", "账号注册被取消", item.Email, map[string]any{"index": item.Index})
		default:
			agg.Emit(signup.LogError, "account_failed", "账号注册失败: "+item.Error, item.Email, map[string]any{
				"index": item.Index, "code": item.Code,
			})
		}
	}

	result, err := h.runner.RunBatch(c.Request.Context(), params)
	// 批次结束定终态（无论 RunBatch 是否报错；报错视为整批失败）。
	if tracker != nil {
		cancelled := c.Request.Context().Err() != nil || err != nil
		tracker.Finish(cancelled)
	}
	// 终态:先 Close 聚合器(刷出未发的过程汇总 + 停表),再直写终态事件。
	// 终态事件用 rlog 直通(不聚合),保证 run_completed/failed 必达且触发 SSE 关闭。
	if agg != nil {
		agg.Close()
	}
	if rlog != nil {
		switch {
		case err != nil:
			rlog.Emit(signup.LogError, "run_failed", "批量注册失败: "+err.Error(), "", nil)
		case c.Request.Context().Err() != nil:
			rlog.Emit(signup.LogWarning, "run_cancelled", "批量注册被取消", "", map[string]any{
				"ok": result.OK, "failed": result.Failed, "cancelled": result.Cancelled,
			})
		default:
			rlog.Emit(signup.LogSuccess, "run_completed", "批量注册完成", "", map[string]any{
				"ok": result.OK, "failed": result.Failed, "cancelled": result.Cancelled, "total": req.Count,
			})
		}
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": gin.H{
			"code": "run_failed", "message": err.Error()}})
		return
	}

	resp := createRunResponse{
		OK:        result.OK,
		Failed:    result.Failed,
		Cancelled: result.Cancelled,
		Total:     req.Count,
		RunID:     runID,
		Applied: appliedSettings{
			Concurrency:             st.Concurrency,
			MaxRegistrationsPerExit: st.MaxRegistrationsPerExitIP,
			TaskTimeoutSeconds:      st.TaskTimeoutSeconds,
		},
	}
	for _, e := range result.Errors {
		resp.Errors = append(resp.Errors, e.Code+": "+e.Error)
	}
	c.JSON(http.StatusOK, resp)
}

// listRuns 处理 GET /api/runs：返回全部批次进度快照（新→旧）。
func (h *Handler) listRuns(c *gin.Context) {
	if h.registry == nil {
		c.JSON(http.StatusOK, gin.H{"runs": []signup.RunState{}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"runs": h.registry.List()})
}

// currentRuns 处理 GET /api/runs/current：返回仍在 running 的批次（实时进度）。
func (h *Handler) currentRuns(c *gin.Context) {
	if h.registry == nil {
		c.JSON(http.StatusOK, gin.H{"runs": []signup.RunState{}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"runs": h.registry.Current()})
}

// getRun 处理 GET /api/runs/:runId：返回单批次进度快照（含 successRate/pending/processed）。
func (h *Handler) getRun(c *gin.Context) {
	if h.registry == nil {
		c.JSON(http.StatusNotFound, gin.H{"detail": gin.H{"code": "run_tracking_disabled", "message": "批次进度跟踪未启用"}})
		return
	}
	st, ok := h.registry.Snapshot(c.Param("runId"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"detail": gin.H{"code": "run_not_found", "message": "批次不存在(可能已重启清空或未触发)"}})
		return
	}
	c.JSON(http.StatusOK, st)
}

// saveRun 处理 POST /api/runs/:runId/save：把该批次的终态 RunState 落库 mongo。
//
// 用户手动触发（你点名的「注册截止后可选保存到 mongo」）：内存态 → runs collection
// 一条 insert。批次不存在 / 未配置 runStore 返回明确错误。
func (h *Handler) saveRun(c *gin.Context) {
	if h.runStore == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": gin.H{"code": "run_store_unconfigured", "message": "批次落库存储未配置(mongo runs)"}})
		return
	}
	if h.registry == nil {
		c.JSON(http.StatusNotFound, gin.H{"detail": gin.H{"code": "run_tracking_disabled", "message": "批次进度跟踪未启用"}})
		return
	}
	runID := c.Param("runId")
	st, ok := h.registry.Snapshot(runID)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"detail": gin.H{"code": "run_not_found", "message": "批次不存在(可能已重启清空或未触发)"}})
		return
	}
	if st.Status == signup.RunStatusRunning {
		c.JSON(http.StatusConflict, gin.H{"detail": gin.H{"code": "run_not_finished", "message": "批次仍在进行中，结束后才能落库"}})
		return
	}
	if err := h.runStore.InsertRun(c.Request.Context(), st); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": gin.H{"code": "run_save_failed", "message": err.Error()}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"saved": true, "runId": runID, "status": st.Status, "successRate": st.SuccessRate})
}
