// proxyreturn_test.go 代理租约归还的 owner 配对测试（P0 级并发/资源正确性修复验证）。
//
// 修复前 bug：acquire 用 owner=RunID 租约，归还却传 owner="" → proxy store 按
// (proxyID, owner) 配对释放失败 → 租约泄漏（代理被 leaseUntil 卡到过期）；且
// defer + cleanupOnSuccess/cleanupOnFailure 三处归还 → ActiveLeaseCount 双重递减成负。
// 修复后：唯一归还点 defer safeReturnProxy(owner=RunID) 配对释放，代理回 available。
package signup

import (
	"context"
	"testing"

	"gpt-go/internal/model"
	proxysvc "gpt-go/internal/service/proxy"
	"gpt-go/internal/store"
)

// seedProxyPool 造一个含 1 个可用代理的真实池（mock store + service 适配器）。
func seedProxyPool(t *testing.T) (*store.MockProxyStore, *ProxyServiceAdapter) {
	t.Helper()
	ps := store.NewMockProxyStore()
	if _, err := ps.Upsert(context.Background(), model.ProxyDocument{
		ID: "px-1", Host: "1.2.3.4", Port: 8080, Scheme: model.ProxySchemeHTTP,
		Enabled: true, Status: model.ProxyStatusAvailable, Country: "US",
	}); err != nil {
		t.Fatalf("seed proxy: %v", err)
	}
	return ps, NewProxyServiceAdapter(proxysvc.NewService(ps))
}

// TestRunReleasesProxyPaired 验证：注册（无论成败）后代理租约被配对释放回 available，
// ActiveLeaseCount 归 0（不双重递减成负、不泄漏）。
func TestRunReleasesProxyPaired(t *testing.T) {
	ps, adapter := seedProxyPool(t)
	// 无 email/flowRunner：Run 在预留邮箱步失败 → 走 cleanupOnFailure → defer 归还代理。
	s := NewService(adapter, nil, nil)

	_, err := s.Run(context.Background(), RunParams{RunID: "run-x", Country: "US", LeaseSeconds: 300})
	if err == nil {
		t.Fatal("无邮箱池应失败")
	}

	// 代理应已释放：activeLeaseCount 归 0、leaseOwner 清空、状态仍 available。
	docs, _, lerr := ps.List(context.Background(), store.ProxyListQuery{Page: 1, Size: 10})
	if lerr != nil || len(docs) == 0 {
		t.Fatalf("List: %v", lerr)
	}
	d := docs[0]
	if d.ActiveLeaseCount != 0 {
		t.Fatalf("ActiveLeaseCount 应归 0（配对释放，不双重递减成负/不泄漏）, got %d", d.ActiveLeaseCount)
	}
	if len(d.ActiveLeaseOwners) != 0 {
		t.Fatalf("ActiveLeaseOwners 应清空, got %v", d.ActiveLeaseOwners)
	}
	if d.Status != model.ProxyStatusAvailable {
		t.Fatalf("代理应回 available, got %s", d.Status)
	}
}

// TestRunProxyReleasedOnlyOnce 验证：多次 Run 复用同一代理（释放后可再租），
// 证明释放真实生效（若泄漏，第二次租不到——leaseUntil 卡住）。
func TestRunProxyReleasedOnlyOnce(t *testing.T) {
	ps, adapter := seedProxyPool(t)

	// 连续 3 次租还：每次租到→归还。若前次泄漏未配对释放，后续租不到同一代理。
	for i := 0; i < 3; i++ {
		lease, err := adapter.AcquireProxy(context.Background(), "run-loop", nil, 300, "US", "")
		if err != nil || lease == nil {
			t.Fatalf("第 %d 次租代理失败（可能前次泄漏未释放）: err=%v lease=%v", i, err, lease)
		}
		if err := adapter.ReturnProxy(context.Background(), lease.ID, "run-loop"); err != nil {
			t.Fatalf("第 %d 次归还失败: %v", i, err)
		}
	}
	docs, _, _ := ps.List(context.Background(), store.ProxyListQuery{Page: 1, Size: 10})
	if docs[0].ActiveLeaseCount != 0 {
		t.Fatalf("循环租还后 ActiveLeaseCount 应 0, got %d", docs[0].ActiveLeaseCount)
	}
}
