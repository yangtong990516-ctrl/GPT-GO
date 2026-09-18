package signup

import (
	"sync"
	"testing"
	"time"
)

// TestRunLogManySubscribersConcurrent 压测：50 订阅者并发订阅 + 高频写入 + 取消。
func TestRunLogManySubscribersConcurrent(t *testing.T) {
	hub := NewRunLogHub()
	lg := hub.Logger("stress")
	const subs = 50
	var wg sync.WaitGroup
	// 订阅者并发订阅 + 消费 + 取消。
	for i := 0; i < subs; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, ch, cancel, _ := hub.Subscribe("stress")
			defer cancel()
			// 消费一段时间。
			timeout := time.After(200 * time.Millisecond)
			for {
				select {
				case <-ch:
				case <-timeout:
					return
				}
			}
		}()
	}
	// 高频写入。
	done := make(chan struct{})
	go func() {
		for i := 0; i < 500; i++ {
			lg.Emit(LogInfo, "tick", "m", "", nil)
		}
		close(done)
	}()
	<-done
	wg.Wait()
	// 写入终态后所有剩余订阅者应被关闭。
	lg.Emit(LogSuccess, "run_completed", "done", "", nil)
}

// TestRunLogSubscribeCancelNoLeak 验证：订阅后立即取消，再写入不 panic（已注销）。
func TestRunLogSubscribeCancelNoLeak(t *testing.T) {
	hub := NewRunLogHub()
	_, ch, cancel, _ := hub.Subscribe("leak")
	cancel() // 立即取消
	// 写入不应 panic（订阅者已注销，不会向已关闭 channel 发送）。
	hub.Logger("leak").Emit(LogInfo, "after_cancel", "x", "", nil)
	// ch 已关闭。
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("cancel 后 ch 应已关闭")
		}
	default:
		t.Fatal("cancel 后 ch 应立即可读(已关闭)")
	}
}
