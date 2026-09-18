//go:build v8

package sentinel

// 并发安全测试：批量注册场景就是高并发调 GetToken，这是硬需求。
//
// 覆盖的并发风险面：
//  1. 池竞争：并发 acquire/release 同一批 worker（race detector 验证）
//  2. worker 耗尽：并发数 > 池大小，acquire 新建 worker 的路径
//  3. worker 重建：压垮 maxSolvesPerIsolate 上限，Dispose 与新建并发交错
//  4. V8 Isolate 线程亲和：同一 Isolate 在不同 goroutine 间传递（acquire 取出 /
//     release 归还后可能被另一个 goroutine 拿到）——v8go 允许"同一时间一个线程，
//     不同时间不同线程"，本测试验证该假设
//  5. 并发期间 Close：Close 与在途 GetToken 竞态（Close 后 acquire 必须报错，
//     在途的 release 必须安全销毁 worker 而不是归还已关闭的池）

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestV8ConcurrentSolve 并发压测：N 个 goroutine 同时求解，验证池的并发安全。
func TestV8ConcurrentSolve(t *testing.T) {
	s := newTestSolver(t, nil)
	env := testEnv()
	const C = 8 // 并发数（> 池中 worker 数，迫使 acquire 新建 + 复用）
	var wg sync.WaitGroup
	errs := make(chan error, C*4)
	for i := 0; i < C; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 4; j++ {
				out, err := s.execute(context.Background(), env, FlowAuthorizeContinue, "requirements", nil, "")
				if err != nil {
					errs <- err
					return
				}
				if rp, _ := out["request_p"].(string); rp == "" {
					errs <- fmt.Errorf("request_p 为空")
					return
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("并发求解失败: %v", err)
	}
}

// TestV8ConcurrentFullToken 真实批量场景：高并发跑完整三阶段 GetToken。
func TestV8ConcurrentFullToken(t *testing.T) {
	if testing.Short() {
		t.Skip("short 模式跳过完整并发压测")
	}
	fetcher := func(ctx context.Context, env EnvPayload, flow Flow, requestP string) (*Challenge, error) {
		raw := json.RawMessage(`{"token":"stub-challenge-token","proofofwork":{"required":true,"seed":"s","difficulty":1},"so":{"required":false}}`)
		var c Challenge
		_ = json.Unmarshal(raw, &c)
		c.Raw = raw
		return &c, nil
	}
	s := newTestSolver(t, fetcher)
	env := testEnv()

	const C = 12      // 并发 goroutine 数
	const perG = 3    // 每个 goroutine 求解次数
	var okCount int64 // 成功计数
	var wg sync.WaitGroup
	errs := make(chan error, C*perG)
	t0 := time.Now()
	for i := 0; i < C; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < perG; j++ {
				res, err := s.GetToken(context.Background(), env, FlowAuthorizeContinue)
				if err != nil {
					errs <- fmt.Errorf("g%d iter%d: %w", id, j, err)
					return
				}
				if res.Token == "" {
					errs <- fmt.Errorf("g%d iter%d: token 为空", id, j)
					return
				}
				if !strings.Contains(res.Token, "stub-challenge-token") {
					errs <- fmt.Errorf("g%d iter%d: token 不含 challenge: %.40s", id, j, res.Token)
					return
				}
				atomic.AddInt64(&okCount, 1)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("%v", err)
	}
	el := time.Since(t0)
	total := int64(C * perG)
	t.Logf("并发完成: %d/%d 成功 | %v 总耗时 | 平均 %v/次", okCount, total, el.Round(time.Millisecond), (el / time.Duration(total)).Round(time.Millisecond))
	if okCount != total {
		t.Errorf("成功数 %d != 期望 %d", okCount, total)
	}
}

// TestV8ConcurrentWorkerChurn 压垮 worker 复用上限：强制频繁 Dispose+重建，
// 验证重建路径与在途求解的并发交错。
func TestV8ConcurrentWorkerChurn(t *testing.T) {
	s := newTestSolver(t, nil)
	env := testEnv()
	// 每个 worker 用 maxSolvesPerIsolate(20) 次后重建。
	// 用足够多的迭代确保至少触发几轮全池重建。
	const C = 6
	const perG = 12 // 6*12=72 次求解，约 3-4 轮全池重建
	var wg sync.WaitGroup
	errs := make(chan error, C*perG)
	for i := 0; i < C; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < perG; j++ {
				out, err := s.execute(context.Background(), env, FlowAuthorizeContinue, "requirements", nil, "")
				if err != nil {
					errs <- fmt.Errorf("g%d iter%d: %w", id, j, err)
					return
				}
				if rp, _ := out["request_p"].(string); rp == "" {
					errs <- fmt.Errorf("g%d iter%d: request_p 为空", id, j)
					return
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("%v", err)
	}
}

// TestV8ConcurrentClose 并发期间关闭 solver：Close 后新 acquire 必须报错，
// 在途的 release 必须安全销毁 worker（不能归还已关闭的池导致 use-after-close）。
func TestV8ConcurrentClose(t *testing.T) {
	s := newTestSolver(t, nil)
	env := testEnv()

	// 起一批在途求解（故意拉长：solve 阶段有行为模拟 sleep）。
	var wg sync.WaitGroup
	const C = 4
	started := make(chan struct{})
	for i := 0; i < C; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-started
			// 这些调用会在 Close 前后交错完成；允许返回"solver 已关闭"错误，
			// 但不允许 panic 或 data race。
			_, _ = s.execute(context.Background(), env, FlowAuthorizeContinue, "requirements", nil, "")
		}()
	}
	close(started)
	// 让在途求解先跑起来再 Close。
	time.Sleep(20 * time.Millisecond)
	if err := s.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	wg.Wait()

	// Close 后再调用必须返回明确错误，不能 panic。
	if _, err := s.GetToken(context.Background(), env, FlowAuthorizeContinue); err == nil {
		t.Error("Close 后 GetToken 应返回错误")
	} else if !strings.Contains(err.Error(), "已关闭") {
		t.Errorf("Close 后错误应明确: %v", err)
	}
	// 二次 Close 幂等。
	if err := s.Close(); err != nil {
		t.Errorf("二次 Close: %v", err)
	}
}
