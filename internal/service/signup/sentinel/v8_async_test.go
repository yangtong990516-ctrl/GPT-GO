//go:build v8

package sentinel

import (
	"context"
	"testing"
	"time"
)

// TestV8SolverAsync_ReturnsImmediately 验证异步预热版：构造立即返回（空池），
// 不阻塞等待编译完成。
func TestV8SolverAsync_ReturnsImmediately(t *testing.T) {
	src, err := SDKSource(context.Background())
	if err != nil {
		t.Fatalf("加载 sdk.js 失败: %v", err)
	}
	start := time.Now()
	s, ready, err := NewV8SolverAsync(src, WithV8BehaviorMs(10))
	if err != nil {
		t.Fatalf("NewV8SolverAsync 失败: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	// 构造本身应近乎瞬时（远小于一次 V8 编译耗时）。给 2s 上限（机器慢时仍远快于全量编译）。
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("异步构造应近乎瞬时返回, 耗时 %v（疑似同步阻塞）", d)
	}
	if ready == nil {
		t.Fatal("ready channel 不应为 nil")
	}
}

// TestV8SolverAsync_BackgroundWarmup 验证后台预热最终完成：ready 关闭后 pool 有 worker，
// 且 solver 可正常 Solve（命中预热好的 worker，或懒编译兜底）。
func TestV8SolverAsync_BackgroundWarmup(t *testing.T) {
	src, err := SDKSource(context.Background())
	if err != nil {
		t.Fatalf("加载 sdk.js 失败: %v", err)
	}
	s, ready, err := NewV8SolverAsync(src, WithV8BehaviorMs(10))
	if err != nil {
		t.Fatalf("NewV8SolverAsync 失败: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	// 等后台预热完成（编译大 bundle，给足超时）。
	select {
	case <-ready:
	case <-time.After(60 * time.Second):
		t.Fatal("后台预热 60s 内未完成")
	}

	// 预热完成后 pool 应有 1 个 worker（Close 前）。
	s.mu.Lock()
	poolLen := len(s.pool)
	s.mu.Unlock()
	if poolLen != 1 {
		t.Fatalf("预热后 pool 应有 1 个 worker, got %d", poolLen)
	}

	// solver 可用：requirements 能跑出非空 request_p（用预热好的 worker）。
	out, err := s.execute(context.Background(), testEnv(), FlowAuthorizeContinue, "requirements", nil, "")
	if err != nil {
		t.Fatalf("预热后 execute 失败: %v", err)
	}
	if e, ok := out["error"]; ok {
		t.Fatalf("flow 报错: %v", e)
	}
	if rp, _ := out["request_p"].(string); rp == "" {
		t.Fatal("预热后应能解出非空 request_p")
	}
}

// TestV8SolverAsync_SolveBeforeWarmupDone 验证预热完成前调用也能工作（acquire 懒编译兜底），
// 不会因空池报错。这是异步版的核心保证：启动后立即可注册，只是首个稍慢。
func TestV8SolverAsync_SolveBeforeWarmupDone(t *testing.T) {
	src, err := SDKSource(context.Background())
	if err != nil {
		t.Fatalf("加载 sdk.js 失败: %v", err)
	}
	// 不等 ready，立刻用——acquire 应懒编译一个 worker（可能命中磁盘 cache）。
	s, _, err := NewV8SolverAsync(src, WithV8BehaviorMs(10))
	if err != nil {
		t.Fatalf("NewV8SolverAsync 失败: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	out, err := s.execute(context.Background(), testEnv(), FlowAuthorizeContinue, "requirements", nil, "")
	if err != nil {
		t.Fatalf("预热前 execute 不应报错（应懒编译）: %v", err)
	}
	if e, ok := out["error"]; ok {
		t.Fatalf("flow 报错: %v", e)
	}
	if rp, _ := out["request_p"].(string); rp == "" {
		t.Fatal("预热前懒编译也应解出非空 request_p")
	}
}
