// proxy_adapter.go 把 proxy.Service（真实代理池）适配到 signup.ProxyStore 接口。
//
// 为什么需要它：proxy.Service 的签名与 signup.ProxyStore 不一致——
//   - 方法名：CountEligible vs CountEligibleProxies；
//   - excluded 类型：map[string]bool vs []string；
//   - 租约类型：*model.ProxyLease vs *signup.ProxyLease；
//   - proxy.Service 只有 ReleaseProxy（释放租约），没有 ReturnProxy（归还复用）。
//
// 本适配器做单向桥接，让装配层能直接把 proxySvc 传给 signup.NewService。
//
// 语义对齐（codex-auto）：
//   - ReturnProxy（归还复用）→ 映射到 ReleaseProxy（释放租约回 available）；
//   - ConsumeProxy（用后即焚）→ 保留，供「标记 used」场景（当前会话型通道按
//     「出口 IP 去重」防复用，通道本身归还复用，故主链路不 consume）。
package signup

import (
	"context"

	"gpt-go/internal/model"
	"gpt-go/internal/service/proxy"
)

// ProxyServiceAdapter 实现 signup.ProxyStore：桥接 proxy.Service。
type ProxyServiceAdapter struct {
	svc *proxy.Service
}

// NewProxyServiceAdapter 创建代理适配器。
func NewProxyServiceAdapter(svc *proxy.Service) *ProxyServiceAdapter {
	return &ProxyServiceAdapter{svc: svc}
}

// 编译期断言：ProxyServiceAdapter 必须实现 signup.ProxyStore。
// 若 proxy.Service 签名变更导致失配，这里立即编译失败（防装配期才发现）。
var _ ProxyStore = (*ProxyServiceAdapter)(nil)

// CountEligibleProxies 统计可用代理数（映射 CountEligible）。
func (a *ProxyServiceAdapter) CountEligibleProxies(ctx context.Context, country, group string) (int, error) {
	return a.svc.CountEligible(ctx, country, group)
}

// AcquireProxy 租一个代理（excludedIDs []string → map[string]bool；租约类型转换）。
func (a *ProxyServiceAdapter) AcquireProxy(ctx context.Context, owner string, excludedIDs []string, leaseSeconds int, country, group string) (*ProxyLease, error) {
	var excluded map[string]bool
	if len(excludedIDs) > 0 {
		excluded = make(map[string]bool, len(excludedIDs))
		for _, id := range excludedIDs {
			excluded[id] = true
		}
	}
	lease, err := a.svc.AcquireProxy(ctx, owner, excluded, leaseSeconds, country, group)
	if err != nil || lease == nil {
		return nil, err
	}
	return leaseFromModel(lease), nil
}

// AcquireProxyByID 按 ID 租指定代理（透传；套餐检查等需固定代理的场景）。
func (a *ProxyServiceAdapter) AcquireProxyByID(ctx context.Context, proxyID, owner string, leaseSeconds int) (*ProxyLease, error) {
	lease, err := a.svc.AcquireProxyByID(ctx, proxyID, owner, leaseSeconds)
	if err != nil || lease == nil {
		return nil, err
	}
	return leaseFromModel(lease), nil
}

// ReleaseProxy 释放（隔离）代理（透传）。
func (a *ProxyServiceAdapter) ReleaseProxy(ctx context.Context, proxyID, owner string) error {
	return a.svc.ReleaseProxy(ctx, proxyID, owner)
}

// ReturnProxy 归还（复用）代理 → 映射到 ReleaseProxy（释放租约回 available）。
//
// 语义：codex-auto 的「归还复用」就是让代理回到 available 池供下次租用，
// 这正是 proxy.Service.ReleaseProxy 的行为（清除 leaseOwner/leaseUntil）。
func (a *ProxyServiceAdapter) ReturnProxy(ctx context.Context, proxyID, owner string) error {
	return a.svc.ReleaseProxy(ctx, proxyID, owner)
}

// leaseFromModel 把 model.ProxyLease 转成 signup.ProxyLease（字段一一对应）。
func leaseFromModel(l *model.ProxyLease) *ProxyLease {
	if l == nil {
		return nil
	}
	return &ProxyLease{
		ID:       l.ID,
		Host:     l.Host,
		Port:     l.Port,
		Username: l.Username,
		Password: l.Password,
		Scheme:   l.Scheme,
		Country:  l.Country,
		Group:    l.Group,
	}
}
