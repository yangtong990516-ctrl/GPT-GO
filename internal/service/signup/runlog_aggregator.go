// 日志聚合器：把「单账号高频重复日志」合并为节流汇总，避免大批次(如 1000 个)
// 注册时 run 日志流被成千上万条交错日志打爆。
//
// 问题（实测）：N 个并发账号各发「步骤1租代理/租代理成功/步骤2预留邮箱/…」，
// N=10 就 52 条交错，N=1000 会刷几千条 —— 用户根本无法阅读。
//
// 设计（分层:默认看大局,按需下钻）:
//   - 里程碑事件(run_created/run_completed/account_failed 等) → 直通,必达;
//   - 失败事件(account_failed/email_reserve_failed/…) → 直通(要排查,不能丢),
//     但【同邮箱同事件的重复】(重试风暴)按窗口计数合并;
//   - 高频过程事件(protocol_step/proxy_acquired/email_reserved/dial_succeeded/
//     account_succeeded 等单账号例行步骤) → 聚合:不逐条发,累计计数,
//     每满 batchEvery 条 或距上次超 flushInterval,发一条 `progress` 汇总。
//
// 这样 1000 个注册的汇总流只有几十条(进度心跳 + 里程碑 + 失败),可读;
// 单账号明细仍可通过 RunLogger 直写(下钻视图)获取。
package signup

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// 可聚合的高频过程事件(单账号例行,逐条发无信息量)。
var aggregatableEvents = map[string]bool{
	"protocol_step":     true,
	"proxy_acquired":    true,
	"email_reserved":    true,
	"dial_succeeded":    true,
	"protocol_started":  true,
	"account_succeeded": true,
}

// AggregatorConfig 聚合参数。
type AggregatorConfig struct {
	// BatchEvery:每累计这么多条过程事件强制发一次汇总(即使未到时间窗)。
	BatchEvery int
	// FlushInterval:距上次汇总超过该时长且有未发事件,发一次汇总(节流心跳)。
	FlushInterval time.Duration
}

// DefaultAggregatorConfig 默认:每 25 条或每 2s 汇总一次(大批次下约几十条)。
func DefaultAggregatorConfig() AggregatorConfig {
	return AggregatorConfig{BatchEvery: 25, FlushInterval: 2 * time.Second}
}

// failureSample 记录一类重复失败的首现样本(flush 汇总时引用其文案/details)。
type failureSample struct {
	event   string
	message string
	email   string
	details map[string]any
}

// RunLogAggregator 装饰 RunLogger,拦截可聚合事件做节流汇总。
// 线程安全(注册各 worker goroutine 共享一个聚合器)。
type RunLogAggregator struct {
	log *RunLogger
	cfg AggregatorConfig

	mu        sync.Mutex
	counts    map[string]int // event -> 未发出的累计条数(过程事件)
	succeeded int            // 未发出的成功数
	lastFlush time.Time      // 上次汇总时刻
	timer     *time.Timer    // 节流心跳定时器
	closed    bool
	// 失败去重:key=event|email。failCounts 累计次数(含首现),failSamples 存首现样本;
	// 重复失败不逐条发,flush 时发一条 `...×N 次` 汇总。
	failCounts  map[string]int
	failSamples map[string]failureSample
}

// NewRunLogAggregator 包装一个 RunLogger。done 在 run 结束时调用(刷出尾巴并停表)。
func NewRunLogAggregator(log *RunLogger, cfg AggregatorConfig) *RunLogAggregator {
	if cfg.BatchEvery <= 0 {
		cfg.BatchEvery = 25
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 2 * time.Second
	}
	a := &RunLogAggregator{
		log:         log,
		cfg:         cfg,
		counts:      map[string]int{},
		failCounts:  map[string]int{},
		failSamples: map[string]failureSample{},
		lastFlush:   time.Now(),
	}
	return a
}

// Emit 入口:按事件类型分流(里程碑/失败直通,过程事件聚合)。
func (a *RunLogAggregator) Emit(level LogLevel, event, message, email string, details map[string]any) {
	if a == nil || a.log == nil {
		return
	}
	ev := sanitizeEvent(event)

	// 1) 里程碑/终态事件:直通(先刷出待发的过程汇总,保证时序可读)。
	// 注意:不在此自动 Close —— Close 的唯一时机是「批次结束」(runs.go 在 RunBatch
	// 返回后显式调用)。若在此自动 Close,任何 worker 经聚合器发出终态事件都会提前
	// 关闭聚合器、吞掉后续过程事件,与 runs.go 的显式 Close 形成双写点(H1)。
	if terminalLogEvents[ev] || ev == "run_created" {
		a.Flush()
		a.log.Emit(level, ev, message, email, details)
		return
	}

	// 2) 失败事件:首条直通(立即让用户看到根因);同 event+同邮箱(含空邮箱=系统性
	// 失败,如代理池耗尽)的重复失败不再逐条刷,累计计数,flush 时发一条 `...×N 次` 汇总。
	// 这样 1000 个同根因失败 = 1 条首现 + 1 条汇总,而非 1000 条。
	// account_cancelled 纳入去重:客户端断开时 N 个 worker 同时发 N 条取消,正是要防的刷屏。
	if level == LogError || ev == "account_cancelled" {
		key := ev + "|" + email
		a.mu.Lock()
		a.failCounts[key]++
		n := a.failCounts[key]
		if n == 1 {
			// 首现:存样本消息,直通。
			a.failSamples[key] = failureSample{message: message, details: details, email: email, event: ev}
			a.mu.Unlock()
			a.log.Emit(level, ev, message, email, details)
			return
		}
		// 重复:仅累计,达阈值则锁内取快照、解锁后汇总(I/O 移出临界区)。
		var batch flushBatch
		pending := a.totalPendingLocked()
		needFlush := pending >= a.cfg.BatchEvery || time.Since(a.lastFlush) >= a.cfg.FlushInterval
		if needFlush {
			batch = a.drainLocked()
		} else {
			a.ensureTimerLocked()
		}
		a.mu.Unlock()
		a.emitDrained(batch)
		return
	}

	// 3) 高频过程事件:聚合。
	if aggregatableEvents[ev] {
		a.mu.Lock()
		a.counts[ev]++
		if ev == "account_succeeded" {
			a.succeeded++
		}
		var batch flushBatch
		pending := a.totalPendingLocked()
		needFlush := pending >= a.cfg.BatchEvery || time.Since(a.lastFlush) >= a.cfg.FlushInterval
		if needFlush {
			batch = a.drainLocked()
		} else {
			a.ensureTimerLocked()
		}
		a.mu.Unlock()
		a.emitDrained(batch)
		return
	}

	// 4) 其余(info 级里程碑如 proxy_acquire_failed 之外的特殊事件):直通。
	a.log.Emit(level, ev, message, email, details)
}

func (a *RunLogAggregator) totalPendingLocked() int {
	n := 0
	for _, c := range a.counts {
		n += c
	}
	// 重复失败(次数>1 的部分)也算待发。
	for _, c := range a.failCounts {
		if c > 1 {
			n += c - 1
		}
	}
	return n
}

// ensureTimerLocked 起节流定时器(到点自动汇总)。调用方须持锁。
func (a *RunLogAggregator) ensureTimerLocked() {
	if a.timer != nil || a.closed {
		return
	}
	a.timer = time.AfterFunc(a.cfg.FlushInterval, func() {
		// 锁内取快照、解锁后 Emit(I/O 移出临界区,M1)。
		a.mu.Lock()
		a.timer = nil
		var batch flushBatch
		if !a.closed {
			batch = a.drainLocked()
		}
		a.mu.Unlock()
		a.emitDrained(batch)
	})
}

// flushBatch 是一次汇总要发出的全部内容(锁内收集,锁外发送)。
type flushBatch struct {
	progressMsg     string
	progressDetails map[string]any
	failures        []failureSummary
}

type failureSummary struct {
	event   string
	message string
	email   string
	details map[string]any
}

func (b flushBatch) empty() bool {
	return b.progressMsg == "" && len(b.failures) == 0
}

// drainLocked 在持锁状态收集待发内容快照并复位计数(不发日志)。调用方须持锁。
// 失败计数【保留基线】:汇总后把已发的重复数从 failCounts 减掉(归 1 = 只剩首现
// 基线),而非清零 —— 这样跨窗口同类失败会基于已有基线继续累计,不会「窗口1那次
// 永远丢、窗口2又当首现」的计数断裂(H2)。
func (a *RunLogAggregator) drainLocked() flushBatch {
	var batch flushBatch

	// ── 过程事件汇总 ──
	order := []string{"protocol_step", "proxy_acquired", "email_reserved", "dial_succeeded", "protocol_started", "account_succeeded"}
	labels := map[string]string{
		"protocol_step": "步骤", "proxy_acquired": "租代理", "email_reserved": "预留邮箱",
		"dial_succeeded": "拨号", "protocol_started": "开始注册", "account_succeeded": "注册成功",
	}
	parts := make([]string, 0, len(a.counts))
	for _, ev := range order {
		if c := a.counts[ev]; c > 0 {
			parts = append(parts, fmt.Sprintf("%s×%d", labels[ev], c))
		}
	}
	for ev, c := range a.counts { // 兜底未知可聚合事件
		if !contains(order, ev) && c > 0 {
			parts = append(parts, fmt.Sprintf("%s×%d", ev, c))
		}
	}
	if len(parts) > 0 {
		// 计数放嵌套 "breakdown"(顶层 key 安全):平铺成 proxy_acquired 等 key 会被
		// filterSensitive 误伤(proxy/token 属敏感片段)——计数是 int 非敏感值,嵌套避开。
		breakdown := make(map[string]any, len(a.counts))
		for ev, c := range a.counts {
			breakdown[ev] = c
		}
		details := map[string]any{"aggregated": true, "breakdown": breakdown}
		if a.succeeded > 0 {
			details["succeededDelta"] = a.succeeded
		}
		batch.progressMsg = "进行中: " + strings.Join(parts, " · ")
		batch.progressDetails = details
	}

	// ── 重复失败汇总(每类一条 `...×N 次`) ──
	for key, n := range a.failCounts {
		if n <= 1 {
			continue // 只首现 1 次,无重复
		}
		sample := a.failSamples[key]
		emailTag := ""
		if sample.email != "" {
			emailTag = " <" + sample.email + ">"
		}
		batch.failures = append(batch.failures, failureSummary{
			event:   sample.event,
			message: fmt.Sprintf("%s%s(同类失败累计 ×%d)", sample.message, emailTag, n),
			email:   sample.email,
			details: sample.details,
		})
		// 保留基线:已汇总的重复数清零为「首现 1 次」基线,下一窗口在此基础上继续累计。
		a.failCounts[key] = 1
	}

	// 复位过程计数(失败基线保留)。
	a.counts = map[string]int{}
	a.succeeded = 0
	a.lastFlush = time.Now()
	return batch
}

// emitDrained 锁外发送汇总内容(不持 a.mu,避免 SSE 广播阻塞 worker,M1)。
func (a *RunLogAggregator) emitDrained(b flushBatch) {
	if a == nil || a.log == nil || b.empty() {
		return
	}
	if b.progressMsg != "" {
		a.log.Emit(LogInfo, "progress", b.progressMsg, "", b.progressDetails)
	}
	for _, f := range b.failures {
		a.log.Emit(LogError, f.event, f.message, f.email, f.details)
	}
}

// Flush 立即刷出所有未发的过程汇总(里程碑前调用,保证时序)。
func (a *RunLogAggregator) Flush() {
	if a == nil {
		return
	}
	a.mu.Lock()
	b := a.drainLocked()
	a.mu.Unlock()
	a.emitDrained(b)
}

// Close 刷出尾巴并停表(run 终态调用,唯一时机=批次结束)。幂等。
func (a *RunLogAggregator) Close() {
	if a == nil {
		return
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return
	}
	a.closed = true
	if a.timer != nil {
		a.timer.Stop()
		a.timer = nil
	}
	b := a.drainLocked()
	a.mu.Unlock()
	a.emitDrained(b)
}

// StepLogger 返回适配 signup.StepLogger 的回调：步骤文本经聚合器走（protocol_step
// 是可聚合事件，会被合并为进度汇总而非逐条刷屏）。
func (a *RunLogAggregator) StepLogger() func(message string) {
	return func(message string) {
		a.Emit(LogInfo, "protocol_step", message, "", nil)
	}
}

// RunLogger 返回底层直写句柄（下钻视图/调试需逐条原始日志时用）。
func (a *RunLogAggregator) RunLogger() *RunLogger { return a.log }

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
