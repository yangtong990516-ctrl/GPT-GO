// runlog_test.go 运行日志 store 的单测（不联网、不起 HTTP）。
//
// 验证：①Emit/历史/序号；②环形缓冲写满覆盖最旧（内存有界）；③SSE 订阅（历史回放
// + 实时 + 终态关闭 + 慢订阅者丢帧不阻塞）；④敏感字段过滤；⑤并发 Emit 无 race、
// 序号唯一单调。
package signup

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestRunLogEmitAndHistory 验证基本写入 + 历史读取 + 序号单调。
func TestRunLogEmitAndHistory(t *testing.T) {
	hub := NewRunLogHub()
	lg := hub.Logger("run-1")
	lg.Emit(LogInfo, "proxy_acquired", "租到代理", "", nil)
	lg.Emit(LogSuccess, "email_reserved", "预留邮箱", "a@x.com", map[string]any{"source": "remail"})

	entries, ok := hub.Entries("run-1")
	if !ok || len(entries) != 2 {
		t.Fatalf("应 2 条, got %d ok=%v", len(entries), ok)
	}
	if entries[0].Sequence != 1 || entries[1].Sequence != 2 {
		t.Fatalf("序号应 1,2, got %d,%d", entries[0].Sequence, entries[1].Sequence)
	}
	if entries[1].Email != "a@x.com" || entries[1].Level != LogSuccess {
		t.Fatalf("字段错误: %+v", entries[1])
	}
	if entries[1].Details["source"] != "remail" {
		t.Fatalf("details 未保留: %+v", entries[1].Details)
	}
}

// TestRunLogRingBuffer 验证环形缓冲：写满后覆盖最旧，总数封顶，顺序正确。
func TestRunLogRingBuffer(t *testing.T) {
	hub := NewRunLogHub()
	lg := hub.Logger("run-ring")
	// 写 ringCap+100 条。
	total := ringCap + 100
	for i := 0; i < total; i++ {
		lg.Emit(LogInfo, "tick", fmt.Sprintf("m%d", i), "", nil)
	}
	entries, _ := hub.Entries("run-ring")
	if len(entries) != ringCap {
		t.Fatalf("环形应封顶 %d, got %d", ringCap, len(entries))
	}
	// 最旧的应是第 100 条（0..99 被覆盖），最新是 total-1。
	if entries[0].Message != "m100" {
		t.Fatalf("最旧应 m100, got %s", entries[0].Message)
	}
	if entries[len(entries)-1].Message != fmt.Sprintf("m%d", total-1) {
		t.Fatalf("最新应 m%d, got %s", total-1, entries[len(entries)-1].Message)
	}
	// 序号连续递增（首条序号 = total-ringCap+1）。
	if entries[0].Sequence != total-ringCap+1 {
		t.Fatalf("首条序号应 %d, got %d", total-ringCap+1, entries[0].Sequence)
	}
	// 摘要 entryCount 是总写入数（非缓冲长度）。
	sum, _ := hub.Summary("run-ring")
	if sum.EntryCount != total {
		t.Fatalf("摘要 entryCount 应 %d, got %d", total, sum.EntryCount)
	}
}

// TestRunLogSubscribeLiveAndTerminal 验证订阅：历史回放 + 实时 + 终态自动关闭。
func TestRunLogSubscribeLiveAndTerminal(t *testing.T) {
	hub := NewRunLogHub()
	lg := hub.Logger("run-sub")
	lg.Emit(LogInfo, "start", "开始", "", nil)

	history, ch, cancel, isTerm := hub.Subscribe("run-sub")
	defer cancel()
	if isTerm {
		t.Fatal("未终态应 isTerminal=false")
	}
	if len(history) != 1 || history[0].Message != "开始" {
		t.Fatalf("历史回放应含 start, got %+v", history)
	}
	// 实时推送。
	lg.Emit(LogInfo, "step2", "第二步", "", nil)
	select {
	case e := <-ch:
		if e.Message != "第二步" {
			t.Fatalf("实时应收到 第二步, got %s", e.Message)
		}
	case <-time.After(time.Second):
		t.Fatal("实时日志超时未收到")
	}
	// 终态：订阅者应被关闭（channel close）。
	lg.Emit(LogSuccess, "run_completed", "完成", "", nil)
	// 收到终态帧后 channel 应关闭。
	gotTerminal := false
	timeout := time.After(time.Second)
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				if !gotTerminal {
					t.Fatal("channel 关闭前应收到 run_completed 帧")
				}
				return // 正常关闭
			}
			if e.Event == "run_completed" {
				gotTerminal = true
			}
		case <-timeout:
			t.Fatal("终态后 channel 未关闭")
		}
	}
}

// TestRunLogSubscribeAfterTerminal 验证：订阅一个已终态的 run → 只给历史，channel 立即关。
func TestRunLogSubscribeAfterTerminal(t *testing.T) {
	hub := NewRunLogHub()
	lg := hub.Logger("run-done")
	lg.Emit(LogInfo, "start", "开始", "", nil)
	lg.Emit(LogError, "run_failed", "失败", "", nil)

	history, ch, _, isTerm := hub.Subscribe("run-done")
	if !isTerm {
		t.Fatal("已终态应 isTerminal=true")
	}
	if len(history) != 2 {
		t.Fatalf("历史应 2 条, got %d", len(history))
	}
	// channel 应已关闭。
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("已终态订阅 channel 应立即关闭")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("已终态 channel 未关闭")
	}
}

// TestRunLogSensitiveFilter 验证敏感 details key 被过滤（密码/token/cookie/代理等）。
func TestRunLogSensitiveFilter(t *testing.T) {
	hub := NewRunLogHub()
	lg := hub.Logger("run-sec")
	lg.Emit(LogInfo, "test", "msg", "", map[string]any{
		"source":       "remail", // 保留
		"country":      "US",     // 保留
		"password":     "x",      // 滤
		"accessToken":  "y",      // 滤（含 token）
		"cookieHeader": "z",      // 滤
		"proxyUrl":     "p",      // 滤
		"totpSecret":   "t",      // 滤
	})
	entries, _ := hub.Entries("run-sec")
	d := entries[0].Details
	if _, ok := d["source"]; !ok {
		t.Fatal("非敏感 source 应保留")
	}
	for _, k := range []string{"password", "accessToken", "cookieHeader", "proxyUrl", "totpSecret"} {
		if _, ok := d[k]; ok {
			t.Fatalf("敏感 key %s 应被过滤", k)
		}
	}
}

// TestRunLogConcurrentEmit 验证并发 Emit 无 race、序号唯一且单调。
func TestRunLogConcurrentEmit(t *testing.T) {
	hub := NewRunLogHub()
	lg := hub.Logger("run-conc")
	const workers = 16
	const perWorker = 50
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				lg.Emit(LogInfo, "tick", fmt.Sprintf("w%d-%d", id, i), "", nil)
			}
		}(w)
	}
	wg.Wait()
	sum, _ := hub.Summary("run-conc")
	if sum.EntryCount != workers*perWorker {
		t.Fatalf("总序号应 %d, got %d", workers*perWorker, sum.EntryCount)
	}
	// 缓冲内序号应严格递增（环形已按写入序排列）。
	entries, _ := hub.Entries("run-conc")
	for i := 1; i < len(entries); i++ {
		if entries[i].Sequence <= entries[i-1].Sequence {
			t.Fatalf("序号应严格递增: %d 后 %d", entries[i-1].Sequence, entries[i].Sequence)
		}
	}
}

// TestRunLogSlowSubscriberNoBlock 验证：慢订阅者（不消费）不阻塞 Emit（非阻塞发送丢帧）。
func TestRunLogSlowSubscriberNoBlock(t *testing.T) {
	hub := NewRunLogHub()
	lg := hub.Logger("run-slow")
	_, ch, cancel, _ := hub.Subscribe("run-slow")
	defer cancel()
	_ = ch // 故意不消费（慢订阅者）
	// 大量 Emit 应立即返回（不阻塞）。
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			lg.Emit(LogInfo, "tick", "m", "", nil)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("慢订阅者导致 Emit 阻塞（应非阻塞丢帧）")
	}
}

// TestRunLogTrimOldTerminal 验证：超 recentCap 时裁剪最旧的已终态 run。
func TestRunLogTrimOldTerminal(t *testing.T) {
	hub := NewRunLogHub()
	hub.recentCap = 8
	// 建 12 个终态 run。
	for i := 0; i < 12; i++ {
		lg := hub.Logger(fmt.Sprintf("run-t%d", i))
		lg.Emit(LogInfo, "run_completed", "done", "", nil)
	}
	sums := hub.ListSummaries()
	if len(sums) > 8 {
		t.Fatalf("应裁剪到 <=8, got %d", len(sums))
	}
	// 最旧的 run-t0 应被裁掉。
	if _, ok := hub.Entries("run-t0"); ok {
		t.Fatal("最旧的已终态 run 应被裁剪")
	}
}
