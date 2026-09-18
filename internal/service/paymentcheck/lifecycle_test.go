package paymentcheck

import (
	"context"
	"testing"
	"time"

	"gpt-go/internal/service/signup"
)

// TestBatchLifecycleCancel 端到端生命周期：启动 → running 标记 → 取消 → canceled 落状态。
func TestBatchLifecycleCancel(t *testing.T) {
	svc := New(nil, nil)
	svc.proxies = &stubProxyStore{block: true}

	id, err := svc.Run(context.Background(), RunParams{
		Tokens: []TokenInput{{AccessToken: "tok-x", Label: "u1"}},
		Routes: []Route{{Country: "VN", Currency: "VND", Locale: "vi-VN", Proxies: []string{"http://h:1"}}},
	})
	if err != nil {
		t.Fatalf("Run 应成功: %v", err)
	}
	if id == "" {
		t.Fatal("批次 ID 非空")
	}

	// 等到 running
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		st := svc.Status()
		if st != nil && st.Accounts["tok-0"] == "running" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	st := svc.Status()
	if st == nil {
		t.Fatal("Status 非 nil")
	}
	if st.Accounts["tok-0"] != "running" {
		t.Fatalf("应标记 running, got %s", st.Accounts["tok-0"])
	}

	// 取消
	if !svc.Cancel() {
		t.Fatal("Cancel 应返回 true")
	}
	// 等批次结束
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		st = svc.Status()
		if st != nil && !st.Running {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	st = svc.Status()
	if st.Running {
		t.Fatal("取消后批次应停止")
	}
	if !st.Canceled {
		t.Fatal("批次应标记 Canceled")
	}
	// 取消后应有 Item（含 canceled 线路状态）
	items := svc.BatchItems()
	if len(items) == 0 {
		t.Fatal("取消后应有 Item 落状态")
	}
}

// stubProxyStore 用于测试：可控的代理源（阻塞直到 ctx 取消）。
type stubProxyStore struct{ block bool }

func (s *stubProxyStore) CountEligibleProxies(context.Context, string, string) (int, error) {
	return 1, nil
}
func (s *stubProxyStore) AcquireProxy(ctx context.Context, owner string, excluded []string, lease int, country, group string) (*signup.ProxyLease, error) {
	if s.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &signup.ProxyLease{ID: "p1", Host: "h", Port: 1}, nil
}
func (s *stubProxyStore) ReleaseProxy(context.Context, string, string) error { return nil }
func (s *stubProxyStore) ReturnProxy(context.Context, string, string) error  { return nil }
