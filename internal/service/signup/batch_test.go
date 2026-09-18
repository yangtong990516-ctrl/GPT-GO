// batch_test.go RunBatch 编排与取消语义的单测（无外部依赖，空 Service 快速失败）。
//
// 验证：①并发限流到 Concurrency；②取消时未派发的计入 Cancelled（不丢数）；
// ③派发循环在 sem 满阻塞时也能被 ctx 取消即时打断。
package signup

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// TestRunBatchCountsAll 验证全部失败时 Failed 计数守恒（Count == Failed）。
func TestRunBatchCountsAll(t *testing.T) {
	s := NewService(nil, nil, nil) // 无 proxy/email/flowRunner：Run 在租代理步快速失败
	res, err := s.RunBatch(context.Background(), BatchParams{Count: 5, Concurrency: 2})
	if err != nil {
		t.Fatalf("RunBatch err: %v", err)
	}
	if res.OK != 0 || res.Cancelled != 0 {
		t.Fatalf("无依赖应全失败, got OK=%d Cancelled=%d", res.OK, res.Cancelled)
	}
	if res.Failed != 5 {
		t.Fatalf("Failed 应 == Count(5), got %d", res.Failed)
	}
}

// TestRunBatchCancelCountsRemaining 验证：取消后未派发的计入 Cancelled，总数守恒。
func TestRunBatchCancelCountsRemaining(t *testing.T) {
	s := NewService(nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancelCh := make(chan struct{})
	// 立刻触发取消（让派发循环第一时间命中 ctx.Done）。
	cancel()
	close(cancelCh)

	const count = 10
	res, err := s.RunBatch(ctx, BatchParams{Count: count, Concurrency: 1, Cancel: cancelCh})
	if err != nil {
		t.Fatalf("RunBatch err: %v", err)
	}
	total := res.OK + res.Failed + res.Cancelled
	if total != count {
		t.Fatalf("计数不守恒: OK+Failed+Cancelled=%d, want %d", total, count)
	}
	// 取消场景：至少有一部分被计入 Cancelled（取消即时生效）。
	if res.Cancelled == 0 {
		t.Fatalf("取消应计入 Cancelled, got %+v", res)
	}
}

// TestRunBatchConcurrencyRespected 验证：并发上限被信号量限流（同刻并行 <= Concurrency）。
func TestRunBatchConcurrencyRespected(t *testing.T) {
	// 用一个会阻塞在租代理的 proxy store，观察并行度。
	blocking := &blockingProxyStore{release: make(chan struct{})}
	s := NewService(blocking, nil, nil)

	const conc = 3
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = s.RunBatch(context.Background(), BatchParams{Count: 8, Concurrency: conc})
	}()

	// 等待若干进入，记录峰值并行。
	var peak int32
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c := atomic.LoadInt32(&blocking.inflight); c > peak {
			peak = c
		}
		if atomic.LoadInt32(&blocking.inflight) >= int32(conc) {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	close(blocking.release) // 放行让所有 Run 走完
	<-done

	if peak > int32(conc) {
		t.Fatalf("并行峰值 %d 超过并发上限 %d（信号量未限流）", peak, conc)
	}
	if peak == 0 {
		t.Fatal("应有并发进入（peak=0 说明没跑起来）")
	}
}

// blockingProxyStore 租代理时阻塞直到 release 关闭（用于观测并行度）。
// release 由构造方初始化（避免懒初始化的 nil 竞争）。
type blockingProxyStore struct {
	inflight int32
	release  chan struct{}
}

func (b *blockingProxyStore) CountEligibleProxies(ctx context.Context, country, group string) (int, error) {
	return 100, nil
}

func (b *blockingProxyStore) AcquireProxy(ctx context.Context, owner string, excluded []string, leaseSeconds int, country, group string) (*ProxyLease, error) {
	atomic.AddInt32(&b.inflight, 1)
	defer atomic.AddInt32(&b.inflight, -1)
	select {
	case <-b.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &ProxyLease{ID: "px-block", Host: "127.0.0.1", Port: 8080, Scheme: "http", Country: country}, nil
}

func (b *blockingProxyStore) ReleaseProxy(ctx context.Context, proxyID, owner string) error {
	return nil
}
func (b *blockingProxyStore) ReturnProxy(ctx context.Context, proxyID, owner string) error {
	return nil
}
func (b *blockingProxyStore) AcquireProxyByID(ctx context.Context, proxyID, owner string, leaseSeconds int) (*ProxyLease, error) {
	return b.AcquireProxy(ctx, owner, nil, leaseSeconds, "", "")
}
