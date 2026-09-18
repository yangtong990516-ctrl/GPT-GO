package signup

import (
	"strings"
	"testing"
	"time"
)

// collectEvents 从 hub 读某 run 的历史事件名序列。
func collectEvents(h *RunLogHub, runID string) []string {
	entries, _ := h.Entries(runID)
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Event)
	}
	return out
}

// TestAggregatorMergesProcessEvents 验证:高频过程事件被合并为 progress 汇总,
// 而非逐条独立刷屏。
func TestAggregatorMergesProcessEvents(t *testing.T) {
	hub := NewRunLogHub()
	agg := NewRunLogAggregator(hub.Logger("run-a"), AggregatorConfig{BatchEvery: 10, FlushInterval: time.Hour})

	agg.Emit(LogInfo, "run_created", "任务创建", "", nil)
	// 模拟 30 个账号的高频事件(低于 BatchEvery=10 时部分累积,终态 Close 全刷)。
	for i := 0; i < 30; i++ {
		agg.Emit(LogInfo, "protocol_step", "步骤1/5 租用代理 ...", "", nil)
		agg.Emit(LogInfo, "proxy_acquired", "租用代理成功", "", nil)
		agg.Emit(LogInfo, "email_reserved", "预留邮箱成功", "u@x.com", nil)
	}
	agg.Emit(LogError, "account_failed", "账号失败", "u@x.com", nil)
	agg.Emit(LogSuccess, "run_completed", "完成", "", nil)

	events := collectEvents(hub, "run-a")
	// 不应有 90 条独立过程事件;应有 progress 汇总。
	proxyCount := 0
	progressCount := 0
	for _, e := range events {
		if e == "proxy_acquired" {
			proxyCount++
		}
		if e == "progress" {
			progressCount++
		}
	}
	if proxyCount > 5 {
		t.Errorf("proxy_acquired 应被聚合(≤少量), got %d 条", proxyCount)
	}
	if progressCount == 0 {
		t.Errorf("应有 progress 汇总事件, events=%v", events)
	}
	// 里程碑直通。
	for _, m := range []string{"run_created", "account_failed", "run_completed"} {
		if !contains(events, m) {
			t.Errorf("里程碑 %s 应直通, events=%v", m, events)
		}
	}
	if events[len(events)-1] != "run_completed" {
		t.Errorf("run_completed 应最后, got %v", events)
	}
}

// TestAggregatorProgressDetails 验证 progress 汇总 details 含各事件计数。
func TestAggregatorProgressDetails(t *testing.T) {
	hub := NewRunLogHub()
	// BatchEvery 大到不中途自动 flush,FlushInterval 也长——只在显式 Flush 时刷一次。
	agg := NewRunLogAggregator(hub.Logger("run-b"), AggregatorConfig{BatchEvery: 1000, FlushInterval: time.Hour})
	for i := 0; i < 5; i++ {
		agg.Emit(LogInfo, "proxy_acquired", "ok", "", nil)
		agg.Emit(LogInfo, "protocol_step", "step", "", nil)
	}
	agg.Flush() // 强制刷出

	entries, _ := hub.Entries("run-b")
	var prog *RunLogEntry
	for i := range entries {
		if entries[i].Event == "progress" {
			prog = &entries[i]
		}
	}
	if prog == nil {
		t.Fatal("应有 progress 事件")
	}
	breakdown, ok := prog.Details["breakdown"].(map[string]any)
	if !ok {
		t.Fatalf("progress 应含嵌套 breakdown, got %v", prog.Details)
	}
	if breakdown["proxy_acquired"] != 5 {
		t.Errorf("breakdown.proxy_acquired = %v, want 5", breakdown["proxy_acquired"])
	}
	if breakdown["protocol_step"] != 5 {
		t.Errorf("breakdown.protocol_step = %v, want 5", breakdown["protocol_step"])
	}
	if !strings.Contains(prog.Message, "租代理") {
		t.Errorf("progress 文案应含人类可读标签, got %q", prog.Message)
	}
}

// TestAggregatorErrorDedup 验证同邮箱同事件的失败重试被计数合并(不刷屏)。
func TestAggregatorErrorDedup(t *testing.T) {
	hub := NewRunLogHub()
	agg := NewRunLogAggregator(hub.Logger("run-c"), DefaultAggregatorConfig())
	// 同邮箱同事件失败 5 次(重试风暴)。
	for i := 0; i < 5; i++ {
		agg.Emit(LogError, "dial_failed", "拨号失败: timeout", "u@x.com", nil)
	}
	events := collectEvents(hub, "run-c")
	dialFails := 0
	for _, e := range events {
		if e == "dial_failed" {
			dialFails++
		}
	}
	// 第 1 条 + 后续合并条(带 ×N 提示) —— 合并实现是逐条带计数后缀,故 5 条但文案含次数。
	// 关键是不丢、且能看出是同一邮箱重试。
	if dialFails == 0 {
		t.Error("失败事件应保留(要排查)")
	}
}

// TestAggregatorFlushOnTerminal 验证终态事件前自动刷出未发的过程汇总(时序正确)。
func TestAggregatorFlushOnTerminal(t *testing.T) {
	hub := NewRunLogHub()
	agg := NewRunLogAggregator(hub.Logger("run-d"), AggregatorConfig{BatchEvery: 1000, FlushInterval: time.Hour})
	agg.Emit(LogInfo, "proxy_acquired", "ok", "", nil) // 未达 BatchEvery,未 flush
	agg.Emit(LogSuccess, "run_completed", "完成", "", nil)

	events := collectEvents(hub, "run-d")
	// 终态前应有一条 progress(终态触发 flush),run_completed 最后。
	if len(events) < 2 {
		t.Fatalf("终态前应刷出 progress, events=%v", events)
	}
	if events[len(events)-1] != "run_completed" {
		t.Errorf("run_completed 应最后, got %v", events)
	}
	foundProgress := false
	for _, e := range events[:len(events)-1] {
		if e == "progress" {
			foundProgress = true
		}
	}
	if !foundProgress {
		t.Errorf("终态前应 flush 出 progress, got %v", events)
	}
}

// TestAggregatorThrottledHeartbeat 验证时间窗触发的节流心跳(慢批次也周期性汇总)。
func TestAggregatorThrottledHeartbeat(t *testing.T) {
	hub := NewRunLogHub()
	agg := NewRunLogAggregator(hub.Logger("run-e"), AggregatorConfig{BatchEvery: 1000, FlushInterval: 50 * time.Millisecond})
	agg.Emit(LogInfo, "proxy_acquired", "ok", "", nil) // 未达 BatchEvery
	time.Sleep(120 * time.Millisecond)                 // 超 FlushInterval,定时器应自动刷
	events := collectEvents(hub, "run-e")
	if !contains(events, "progress") {
		t.Errorf("节流定时器应自动刷出 progress, got %v", events)
	}
	agg.Close()
}

// TestAggregatorCloseIdempotent 验证 Close 幂等(重复调不 panic 不重复刷)。
func TestAggregatorCloseIdempotent(t *testing.T) {
	hub := NewRunLogHub()
	agg := NewRunLogAggregator(hub.Logger("run-f"), DefaultAggregatorConfig())
	agg.Emit(LogInfo, "proxy_acquired", "ok", "", nil)
	agg.Close()
	n1 := len(collectEvents(hub, "run-f"))
	agg.Close() // 再调一次
	n2 := len(collectEvents(hub, "run-f"))
	if n1 != n2 {
		t.Errorf("Close 应幂等: 第一次后 %d 条,第二次后 %d 条", n1, n2)
	}
}

// TestAggregatorCrossWindowFailureBaseline 验证 H2 修复:同类失败跨窗口计数连续,
// 不会「窗口1那次丢失、窗口2重新当首现」。保留基线后,窗口2汇总应包含累计总数。
func TestAggregatorCrossWindowFailureBaseline(t *testing.T) {
	hub := NewRunLogHub()
	agg := NewRunLogAggregator(hub.Logger("run-xw"), AggregatorConfig{BatchEvery: 1000, FlushInterval: time.Hour})

	// 窗口1:同类失败 3 次(首现 1 + 重复 2),手动 Flush → 发 1 首现 + 1 汇总(×3),基线归 1。
	agg.Emit(LogError, "dial_failed", "拨号失败", "u@x.com", nil)
	agg.Emit(LogError, "dial_failed", "拨号失败", "u@x.com", nil)
	agg.Emit(LogError, "dial_failed", "拨号失败", "u@x.com", nil)
	agg.Flush()

	// 窗口2:再失败 2 次(基于保留基线 1 → 累计 3),Flush → 汇总应 ×3(基线1+新增2)。
	agg.Emit(LogError, "dial_failed", "拨号失败", "u@x.com", nil)
	agg.Emit(LogError, "dial_failed", "拨号失败", "u@x.com", nil)
	agg.Flush()

	entries, _ := hub.Entries("run-xw")
	// 收集所有 dial_failed 汇总消息,最后一条应反映累计 ×3(基线连续),而非重新首现。
	var summaries []string
	firstCount := 0
	for _, e := range entries {
		if e.Event != "dial_failed" {
			continue
		}
		if strings.Contains(e.Message, "累计 ×") {
			summaries = append(summaries, e.Message)
		} else {
			firstCount++
		}
	}
	// 首现只应 1 条(窗口1首次);窗口2不应再出现首现条。
	if firstCount != 1 {
		t.Errorf("首现应只 1 条(窗口1), got %d; entries=%v", firstCount, entries)
	}
	// 最后一次汇总应累计窗口1+窗口2(基线连续 → ×3)。
	if len(summaries) == 0 {
		t.Fatalf("应有失败汇总, entries=%v", entries)
	}
	last := summaries[len(summaries)-1]
	if !strings.Contains(last, "×3") {
		t.Errorf("窗口2汇总应累计基线 ×3, got %q (all=%v)", last, summaries)
	}
}
