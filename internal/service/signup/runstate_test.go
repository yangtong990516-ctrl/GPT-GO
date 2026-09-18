// runstate_test.go RunTracker / RunRegistry 的单元测试（不联网、不依赖 mongo）。
//
// 验证：内存进度实时维护（requested/pending/processed/succeeded/failed/successRate）、
// 终态判定、并发 Track 无数据竞争、registry 裁剪。
package signup

import (
	"sync"
	"testing"
)

// TestTrackerLifecycle 验证 tracker 从 running 到终态的完整生命周期。
func TestTrackerLifecycle(t *testing.T) {
	tr := NewRunTracker("run-1", RunKindProtocol, 5, 2, "US", "g1", "remail")
	st := tr.Snapshot()
	if st.Status != RunStatusRunning || st.Requested != 5 || st.Pending != 5 || st.Processed != 0 {
		t.Fatalf("初始态错误: %+v", st)
	}
	// 3 成功 1 失败。
	tr.Track(true, false)
	tr.Track(true, false)
	tr.Track(true, false)
	tr.Track(false, false)
	mid := tr.Snapshot()
	if mid.Processed != 4 || mid.Succeeded != 3 || mid.Failed != 1 || mid.Pending != 1 {
		t.Fatalf("中间态错误: %+v", mid)
	}
	if mid.SuccessRate != 75.0 { // 3/4*100
		t.Fatalf("successRate 应 75.0, got %v", mid.SuccessRate)
	}
	tr.Finish(false)
	fin := tr.Snapshot()
	if fin.Status != RunStatusPartial { // 有成功有失败 → partial
		t.Fatalf("终态应 partial_success, got %s", fin.Status)
	}
	if fin.FinishedAt == nil {
		t.Fatal("finishedAt 应已设置")
	}
	if fin.Pending != 0 {
		t.Fatalf("结束后 pending 应清 0, got %d", fin.Pending)
	}
}

// TestTrackerAllSucceeded 验证全成功 → succeeded。
func TestTrackerAllSucceeded(t *testing.T) {
	tr := NewRunTracker("run-2", RunKindProtocol, 3, 1, "", "", "")
	tr.Track(true, false)
	tr.Track(true, false)
	tr.Track(true, false)
	tr.Finish(false)
	if st := tr.Snapshot(); st.Status != RunStatusSucceeded || st.SuccessRate != 100.0 {
		t.Fatalf("全成功应 succeeded/100, got %s/%v", st.Status, st.SuccessRate)
	}
}

// TestTrackerCancelled 验证取消 → cancelled。
func TestTrackerCancelled(t *testing.T) {
	tr := NewRunTracker("run-3", RunKindProtocol, 4, 1, "", "", "")
	tr.Track(true, false)
	tr.Track(false, true) // 一个取消
	tr.Finish(true)
	st := tr.Snapshot()
	if st.Status != RunStatusCancelled || !st.CancelRequested {
		t.Fatalf("应 cancelled, got %s (cancelRequested=%v)", st.Status, st.CancelRequested)
	}
	if st.Cancelled != 1 {
		t.Fatalf("cancelled 计数应 1, got %d", st.Cancelled)
	}
}

// TestTrackerConcurrentTrack 验证并发 Track 无数据竞争、计数守恒。
func TestTrackerConcurrentTrack(t *testing.T) {
	const n = 200
	tr := NewRunTracker("run-4", RunKindProtocol, n, 8, "", "", "")
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(ok bool) {
			defer wg.Done()
			tr.Track(ok, false)
		}(i%2 == 0) // 一半成功一半失败
	}
	wg.Wait()
	st := tr.Snapshot()
	if st.Processed != n || st.Succeeded+st.Failed != n || st.Pending != 0 {
		t.Fatalf("并发 Track 计数不守恒: %+v", st)
	}
}

// TestRegistryStartIdempotent 验证同 runID 复触发返回同一 tracker（幂等，不丢进度）。
func TestRegistryStartIdempotent(t *testing.T) {
	rg := NewRunRegistry()
	t1 := rg.Start("run-x", RunKindProtocol, 2, 1, "", "", "")
	t1.Track(true, false)
	t2 := rg.Start("run-x", RunKindProtocol, 2, 1, "", "", "")
	if t1 != t2 {
		t.Fatal("同 runID 复触发应返回同一 tracker（幂等）")
	}
	st, ok := rg.Snapshot("run-x")
	if !ok || st.Processed != 1 {
		t.Fatalf("幂等复触发应保留已有进度, got %+v ok=%v", st, ok)
	}
}

// TestRegistryListAndCurrent 验证 List（新→旧）与 Current（仅 running）。
func TestRegistryListAndCurrent(t *testing.T) {
	rg := NewRunRegistry()
	a := rg.Start("run-a", RunKindProtocol, 1, 1, "", "", "")
	rg.Start("run-b", RunKindProtocol, 1, 1, "", "", "")
	a.Finish(false) // run-a 完成，run-b 仍 running

	list := rg.List()
	if len(list) != 2 {
		t.Fatalf("应 2 个批次, got %d", len(list))
	}
	// 新→旧：run-b 在前。
	if list[0].RunID != "run-b" {
		t.Fatalf("List 应新→旧, 第一个应 run-b, got %s", list[0].RunID)
	}
	cur := rg.Current()
	if len(cur) != 1 || cur[0].RunID != "run-b" {
		t.Fatalf("Current 应只含 running 的 run-b, got %+v", cur)
	}
}

// TestRegistryTrimsOldFinished 验证超过 recentCap 时裁剪最旧的已完成批次（running 不删）。
func TestRegistryTrimsOldFinished(t *testing.T) {
	rg := NewRunRegistry()
	// 建 recentCap+5 个已完成批次。
	for i := 0; i < recentCap+5; i++ {
		tr := rg.Start(runIDN(i), RunKindProtocol, 1, 1, "", "", "")
		tr.Finish(false)
	}
	list := rg.List()
	if len(list) > recentCap {
		t.Fatalf("应裁剪到 <= recentCap(%d), got %d", recentCap, len(list))
	}
	// 最旧的（run-0..4）应已被裁掉。
	if _, ok := rg.Snapshot(runIDN(0)); ok {
		t.Fatal("最旧的已完成批次应已被裁剪")
	}
}

// runIDN 生成测试 runID。
func runIDN(i int) string {
	return "run-" + string(rune('a'+i%26)) + "-" + itoa(i)
}
