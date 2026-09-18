// Package runlogs 提供「注册运行日志」HTTP 入口：REST 历史查询 + SSE 实时推送。
//
// 对齐并超越 codex-auto /api/run-logs：
//   - codex 只有 REST list/get（jsonl 文件 + 前端轮询）；本实现新增【SSE 实时流】，
//     注册过程逐条推送（Go channel 驱动），前端实时滚动，无需轮询。
//   - 数据来自 signup.RunLogHub（内存环形缓冲），零磁盘 IO。
//
// 路由（前缀 /api/run-logs）：
//
//	GET /runs                  全部 run 日志摘要（新→旧）
//	GET /runs/:runId           某 run 的历史日志（时间序全量）
//	GET /runs/:runId/stream    SSE 实时流（历史回放 + 增量推送 + 终态自动结束）
package runlogs

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-go/internal/service/signup"
)

// Handler 是 runlogs group 的 HTTP 处理器。
type Handler struct {
	hub *signup.RunLogHub
}

// NewHandler 构造 runlogs 处理器。
func NewHandler(hub *signup.RunLogHub) *Handler { return &Handler{hub: hub} }

// Register 挂载路由到 group（前缀 /api/run-logs）。
func (h *Handler) Register(rg *gin.RouterGroup) {
	rg.GET("/runs", h.listRuns)
	rg.GET("/runs/:runId", h.getRun)
	rg.GET("/runs/:runId/stream", h.streamRun)
}

// runSummaryJSON 是 list 响应元素（对齐 codex RunLogSummary）。
type runSummaryJSON struct {
	RunID      string    `json:"runId"`
	StartedAt  time.Time `json:"startedAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
	EntryCount int       `json:"entryCount"`
	LastEvent  string    `json:"lastEvent"`
	Terminal   bool      `json:"terminal"`
}

// listRuns 处理 GET /api/run-logs/runs：全部 run 日志摘要（新→旧）。
func (h *Handler) listRuns(c *gin.Context) {
	if h.hub == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": gin.H{
			"code": "run_log_unavailable", "message": "运行日志未装配"}})
		return
	}
	sums := h.hub.ListSummaries()
	out := make([]runSummaryJSON, 0, len(sums))
	for _, s := range sums {
		out = append(out, runSummaryJSON{
			RunID: s.RunID, StartedAt: s.StartedAt, UpdatedAt: s.UpdatedAt,
			EntryCount: s.EntryCount, LastEvent: s.LastEvent, Terminal: s.Terminal,
		})
	}
	c.JSON(http.StatusOK, out)
}

// runLogFileJSON 是 get 响应（摘要 + 全量日志，对齐 codex RunLogFile）。
type runLogFileJSON struct {
	runSummaryJSON
	Entries []signup.RunLogEntry `json:"entries"`
}

// getRun 处理 GET /api/run-logs/runs/:runId：某 run 的历史日志（时间序全量）。
func (h *Handler) getRun(c *gin.Context) {
	if h.hub == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": gin.H{
			"code": "run_log_unavailable", "message": "运行日志未装配"}})
		return
	}
	runID := c.Param("runId")
	sum, ok := h.hub.Summary(runID)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"detail": gin.H{
			"code": "run_log_not_found", "message": "任务日志不存在: " + runID}})
		return
	}
	entries, _ := h.hub.Entries(runID)
	if entries == nil {
		entries = []signup.RunLogEntry{}
	}
	c.JSON(http.StatusOK, runLogFileJSON{
		runSummaryJSON: runSummaryJSON{
			RunID: sum.RunID, StartedAt: sum.StartedAt, UpdatedAt: sum.UpdatedAt,
			EntryCount: sum.EntryCount, LastEvent: sum.LastEvent, Terminal: sum.Terminal,
		},
		Entries: entries,
	})
}

// streamRun 处理 GET /api/run-logs/runs/:runId/stream：SSE 实时流。
//
// 协议（W3C SSE）：
//   - 先发一个 `event: history`，data 为历史快照数组（保证新连接看到完整上下文）；
//   - 之后每条新日志发 `event: log`，data 为单条 RunLogEntry；
//   - run 到终态：发完 `event: log`(终态帧) 后发 `event: done` 并关闭连接；
//   - 周期发 `: ping` 注释（保活，防代理/浏览器断连）；
//   - 客户端断开（ctx.Done）即退出（goroutine 随连接生命周期，无泄漏）。
func (h *Handler) streamRun(c *gin.Context) {
	if h.hub == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": gin.H{
			"code": "run_log_unavailable", "message": "运行日志未装配"}})
		return
	}
	runID := c.Param("runId")

	// SSE 响应头（必须在订阅前设好）。
	w := c.Writer
	header := w.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache")
	header.Set("Connection", "keep-alive")
	header.Set("X-Accel-Buffering", "no") // 禁 nginx 缓冲，保证实时
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		// 不支持 flush 的 writer（极少见）：直接报错。
		c.JSON(http.StatusInternalServerError, gin.H{"detail": gin.H{
			"code": "sse_unsupported", "message": "响应不支持流式推送"}})
		return
	}

	history, ch, cancel, isTerminal := h.hub.Subscribe(runID)
	defer cancel()

	// 1) 历史回放（一个事件带全量，前端一次性渲染）。
	writeSSE(w, "history", history)
	flusher.Flush()

	// 已终态：发 done 即结束（不再挂实时）。
	if isTerminal {
		writeSSE(w, "done", map[string]string{"runId": runID})
		flusher.Flush()
		return
	}

	// 2) 实时增量 + 保活 ping。
	ctx := c.Request.Context()
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			// 客户端断开：退出（cancel 由 defer 触发，从 hub 注销订阅）。
			return
		case e, ok := <-ch:
			if !ok {
				// channel 关闭 = run 终态：发 done 结束。
				writeSSE(w, "done", map[string]string{"runId": runID})
				flusher.Flush()
				return
			}
			writeSSE(w, "log", e)
			flusher.Flush()
		case <-ping.C:
			// 保活注释（SSE 协议：冒号开头为注释，不触发事件）。
			_, _ = fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

// writeSSE 写一条 SSE 帧：event 名 + JSON data。多行 data 逐行加 "data: " 前缀。
func writeSSE(w http.ResponseWriter, event string, payload any) {
	var data []byte
	switch v := payload.(type) {
	case []byte:
		data = v
	case string:
		data = []byte(v)
	default:
		data, _ = json.Marshal(payload)
	}
	_, _ = fmt.Fprintf(w, "event: %s\n", event)
	// JSON 单行（Marshal 不产出换行），但防御性按行写。
	for _, line := range strings.Split(string(data), "\n") {
		_, _ = fmt.Fprintf(w, "data: %s\n", line)
	}
	_, _ = fmt.Fprint(w, "\n")
}
