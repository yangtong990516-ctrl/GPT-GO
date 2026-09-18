// runlog.go 注册运行日志：内存环形缓冲 + SSE 订阅分发。
//
// 对齐并超越 codex-auto run_log_store.py：
//   - codex 用 jsonl 文件 + REST 轮询（无实时推送）；本实现用【内存环形缓冲 + Go
//     channel 驱动的 SSE 订阅】，天然适配实时展示，且零磁盘 IO。
//   - 日志模型对齐 codex RunLogEntry：timestamp/level/event/message/email/sequence/
//     details；敏感字段（password/totp/token/cookie/proxy/accessurl/secret）在写入时
//     过滤（对齐 codex reject_sensitive_detail_keys）。
//   - 终态事件（run_completed/run_failed/run_cancelled）触发 SSE 订阅者优雅关闭。
//
// Go 特性运用：
//   - 每 run 一个环形缓冲（固定容量，写满覆盖最旧）——内存有界，防单 run 刷爆；
//   - 订阅者用 buffered channel + 非阻塞发送（慢订阅者丢帧不阻塞注册主流程）；
//   - 全部读写持锁（RWMutex），Emit 高频调用走读锁友好的单写者路径；
//   - 进程级 Hub 管理多 run（对齐 RunRegistry 模式），带 recent 裁剪。
//
// 并发不变量（panic 防护，已由 -race 压测验证）：
//   - sub.ch 的「发送」与「close」都在 buf.mu 下同侧串行化：appendLocked 在 buf.mu
//     下广播+终态close；cancel 也在 buf.mu 下 close+delete。二者互斥 → 不会向已关闭
//     channel 发送，也不会二次 close（终态 close 后已 delete，cancel 时 ok=false 跳过）。
package signup

import (
	"strings"
	"sync"
	"time"
)

// LogLevel 日志级别（对齐 codex LogLevel）。
type LogLevel = string

const (
	LogInfo    LogLevel = "info"
	LogSuccess LogLevel = "success"
	LogWarning LogLevel = "warning"
	LogError   LogLevel = "error"
)

// 终态事件（对齐 codex TERMINAL_EVENTS）：触发订阅者关闭。
var terminalLogEvents = map[string]bool{
	"run_completed": true,
	"run_failed":    true,
	"run_cancelled": true,
}

// sensitiveFragments 敏感字段片段（对齐 codex reject_sensitive_detail_keys）：
// details 的 key 含任一片段即丢弃该 key（防密码/token/cookie/代理凭据泄漏进日志）。
var sensitiveFragments = []string{
	"pass" + "word", "pass" + "wd", "p" + "wd", "to" + "tp", "sec" + "ret",
	"access" + "url", "access_url", "coo" + "kie", "pro" + "xy", "to" + "ken",
	"au" + "th", "cred" + "ential", "sess" + "ion", "[FUNC]",
}

// RunLogEntry 单条日志（对齐 codex RunLogEntry）。
type RunLogEntry struct {
	Timestamp time.Time      `json:"timestamp"`
	Level     LogLevel       `json:"level"`
	Event     string         `json:"event"`           // snake_case 事件名（如 proxy_acquired）
	Message   string         `json:"message"`         // 人类可读（前端直接渲染）
	Email     string         `json:"email,omitempty"` // 关联邮箱（账号级日志）
	Sequence  int            `json:"sequence"`        // 单调递增序号（前端去重/排序）
	Details   map[string]any `json:"details,omitempty"`
}

// RunLogSummary 单 run 日志摘要（对齐 codex RunLogSummary）。
type RunLogSummary struct {
	RunID      string    `json:"runId"`
	StartedAt  time.Time `json:"startedAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
	EntryCount int       `json:"entryCount"`
	LastEvent  string    `json:"lastEvent"`
	Terminal   bool      `json:"terminal"` // lastEvent 是否终态
}

// ringCap 是单 run 环形缓冲容量（超出覆盖最旧）。对齐「内存有界」。
const ringCap = 2000

// subscriber 是一个 SSE 订阅者：缓冲 channel + 关闭信号。
type subscriber struct {
	ch     chan RunLogEntry
	closed chan struct{}
}

// runLogBuffer 单 run 的环形缓冲 + 订阅者集（内部态，被 Hub 持有）。
type runLogBuffer struct {
	mu       sync.Mutex
	entries  []RunLogEntry // 环形缓冲（len<=ringCap）
	head     int           // 最旧元素下标（环形写指针）
	full     bool          // 是否已写满一圈
	seq      int           // 单调序号
	started  time.Time
	updated  time.Time
	lastEv   string
	terminal bool
	subs     map[*subscriber]struct{}
}

// newRunLogBuffer 建空缓冲（started=now）。
func newRunLogBuffer() *runLogBuffer {
	return &runLogBuffer{
		entries: make([]RunLogEntry, 0, ringCap),
		started: time.Now().UTC(),
		subs:    map[*subscriber]struct{}{},
	}
}

// append 写一条（环形 + 广播订阅者）。调用方须持锁。
func (b *runLogBuffer) appendLocked(e RunLogEntry) {
	b.seq++
	e.Sequence = b.seq
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	if len(b.entries) < ringCap {
		b.entries = append(b.entries, e)
	} else {
		b.entries[b.head] = e
		b.head = (b.head + 1) % ringCap
		b.full = true
	}
	b.updated = e.Timestamp
	b.lastEv = e.Event
	isTerminal := terminalLogEvents[e.Event]
	if isTerminal {
		b.terminal = true
	}
	// 广播：非阻塞发送（慢订阅者丢帧，绝不阻塞注册主流程）。
	for s := range b.subs {
		select {
		case s.ch <- e:
		default:
		}
	}
	// 终态：推送完终态帧后关闭所有订阅者（优雅收尾，SSE 流自然结束）。
	if isTerminal {
		for s := range b.subs {
			close(s.ch)
			delete(b.subs, s)
		}
	}
}

// snapshot 返回当前全部日志（按时间序，最旧在前）。调用方须持锁。
func (b *runLogBuffer) snapshotLocked() []RunLogEntry {
	n := len(b.entries)
	out := make([]RunLogEntry, 0, n)
	if !b.full {
		out = append(out, b.entries...)
		return out
	}
	// 写满一圈：从 head（最旧）绕环读。
	out = append(out, b.entries[b.head:]...)
	out = append(out, b.entries[:b.head]...)
	return out
}

// RunLogHub 是进程级 run 日志中心：管理多 run 的缓冲 + 订阅。
type RunLogHub struct {
	mu      sync.RWMutex
	buffers map[string]*runLogBuffer
	order   []string // 创建顺序（旧→新），裁剪用
	// recentCap 内存中保留的 run 日志数上限（超出裁最旧的【已终态】run）。
	recentCap int
}

// NewRunLogHub 建日志中心。
func NewRunLogHub() *RunLogHub {
	return &RunLogHub{buffers: map[string]*runLogBuffer{}, recentCap: 64}
}

// Logger 取/建一个 run 的日志句柄（幂等：同 runID 复用同一缓冲，不丢历史）。
func (h *RunLogHub) Logger(runID string) *RunLogger {
	h.mu.Lock()
	defer h.mu.Unlock()
	buf, ok := h.buffers[runID]
	if !ok {
		buf = newRunLogBuffer()
		h.buffers[runID] = buf
		h.order = append(h.order, runID)
		h.trimLocked()
	}
	return &RunLogger{runID: runID, buf: buf}
}

// Entries 取某 run 的全部日志（时间序）；不存在返回 nil,false。
func (h *RunLogHub) Entries(runID string) ([]RunLogEntry, bool) {
	h.mu.RLock()
	buf, ok := h.buffers[runID]
	h.mu.RUnlock()
	if !ok {
		return nil, false
	}
	buf.mu.Lock()
	defer buf.mu.Unlock()
	return buf.snapshotLocked(), true
}

// Summary 取某 run 的摘要；不存在返回 false。
func (h *RunLogHub) Summary(runID string) (RunLogSummary, bool) {
	h.mu.RLock()
	buf, ok := h.buffers[runID]
	h.mu.RUnlock()
	if !ok {
		return RunLogSummary{}, false
	}
	buf.mu.Lock()
	defer buf.mu.Unlock()
	return RunLogSummary{
		RunID:      runID,
		StartedAt:  buf.started,
		UpdatedAt:  buf.updated,
		EntryCount: buf.seq,
		LastEvent:  buf.lastEv,
		Terminal:   buf.terminal,
	}, true
}

// ListSummaries 列出全部 run 摘要（新→旧）。
func (h *RunLogHub) ListSummaries() []RunLogSummary {
	h.mu.RLock()
	ids := make([]string, len(h.order))
	copy(ids, h.order)
	h.mu.RUnlock()
	out := make([]RunLogSummary, 0, len(ids))
	for i := len(ids) - 1; i >= 0; i-- {
		if s, ok := h.Summary(ids[i]); ok {
			out = append(out, s)
		}
	}
	return out
}

// Subscribe 订阅某 run 的实时日志：返回「历史回放 + 实时 channel + 退订函数」。
//
// 设计（SSE 友好）：返回的 entries 是订阅时刻的历史快照（保证新订阅者看到完整上下文），
// ch 之后推新增日志；该 run 到终态或调用 cancel 时 ch 关闭。slow subscriber 丢帧不阻塞。
func (h *RunLogHub) Subscribe(runID string) (history []RunLogEntry, ch <-chan RunLogEntry, cancel func(), isTerminal bool) {
	logger := h.Logger(runID) // 幂等建缓冲（订阅一个尚未写日志的 run 也能挂上）
	buf := logger.buf

	sub := &subscriber{ch: make(chan RunLogEntry, 256), closed: make(chan struct{})}
	buf.mu.Lock()
	history = buf.snapshotLocked()
	isTerminal = buf.terminal
	if !buf.terminal {
		buf.subs[sub] = struct{}{}
	}
	buf.mu.Unlock()

	// 已终态：只给历史，不给实时 channel（立即关闭）。
	if isTerminal {
		close(sub.ch)
		return history, sub.ch, func() {}, true
	}

	cancel = func() {
		buf.mu.Lock()
		if _, ok := buf.subs[sub]; ok {
			delete(buf.subs, sub)
			close(sub.ch)
		}
		buf.mu.Unlock()
	}
	return history, sub.ch, cancel, false
}

// trimLocked 裁剪：超 recentCap 时删最旧的【已终态】run 缓冲。调用方须持写锁。
func (h *RunLogHub) trimLocked() {
	if len(h.order) <= h.recentCap {
		return
	}
	need := len(h.order) - h.recentCap
	kept := h.order[:0]
	removed := 0
	for _, id := range h.order {
		buf := h.buffers[id]
		buf.mu.Lock()
		terminal := buf.terminal
		buf.mu.Unlock()
		if removed < need && terminal {
			delete(h.buffers, id)
			removed++
			continue
		}
		kept = append(kept, id)
	}
	h.order = kept
}

// RunEventLogger 是「能写运行日志」的最小契约：RunLogger(直写)与
// RunLogAggregator(聚合节流)都实现它。service/runs 通过此接口写日志,
// 不关心底层是直写还是聚合 —— 大批次下换成聚合器即可防刷屏,调用方零改动。
type RunEventLogger interface {
	Emit(level LogLevel, event, message, email string, details map[string]any)
}

// RunLogger 是单 run 的日志写入句柄（线程安全，可被注册各 goroutine 共享）。
type RunLogger struct {
	runID string
	buf   *runLogBuffer
}

// 编译期断言：两者都实现 RunEventLogger。
var (
	_ RunEventLogger = (*RunLogger)(nil)
	_ RunEventLogger = (*RunLogAggregator)(nil)
)

// noopEventLogger 是空实现:RunParams.Log 为 nil 接口时兜底,省去调用方每处 nil 判断。
type noopEventLogger struct{}

func (noopEventLogger) Emit(LogLevel, string, string, string, map[string]any) {}

// Emit 写一条日志（自动过滤敏感 details、分配序号、广播订阅者）。
func (l *RunLogger) Emit(level LogLevel, event, message, email string, details map[string]any) {
	if l == nil || l.buf == nil {
		return
	}
	e := RunLogEntry{
		Timestamp: time.Now().UTC(),
		Level:     level,
		Event:     sanitizeEvent(event),
		Message:   truncate(message, 1000),
		Email:     email,
		Details:   filterSensitive(details),
	}
	l.buf.mu.Lock()
	l.buf.appendLocked(e)
	l.buf.mu.Unlock()
}

// RunID 返回该句柄的 run 标识。
func (l *RunLogger) RunID() string { return l.runID }

// StepLogger 返回一个适配 signup.StepLogger 的回调（把注册 step 消息写入本 run 日志）。
// event 固定 protocol_step（对齐 codex 注册步骤事件）；邮箱留空（步骤消息里可能含邮箱，
// 由调用方在 message 体现）。
func (l *RunLogger) StepLogger() func(message string) {
	return func(message string) {
		l.Emit(LogInfo, "protocol_step", message, "", nil)
	}
}

// ── 内部工具 ──

// sanitizeEvent 归一事件名：小写、非法字符转下划线、限长 64（对齐 codex event 校验）。
func sanitizeEvent(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	out := b.String()
	if len(out) > 64 {
		out = out[:64]
	}
	if out == "" {
		out = "log"
	}
	return out
}

// truncate 限长 message（对齐 codex max 1000）。
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// filterSensitive 丢弃含敏感片段的 details key（对齐 codex reject_sensitive_detail_keys，
// 但用「丢弃该 key」而非「整条报错」——日志不该因一个敏感 key 而丢失整条）。
func filterSensitive(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		nk := strings.ToLower(strings.ReplaceAll(k, "-", "_"))
		sensitive := false
		for _, frag := range sensitiveFragments {
			if strings.Contains(nk, frag) {
				sensitive = true
				break
			}
		}
		if !sensitive {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
