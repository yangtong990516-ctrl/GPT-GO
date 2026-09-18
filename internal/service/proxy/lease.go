// Proxy lease: acquire/release/consume/heartbeat + candidate queries, mirroring
// probe_store.py MongoProbeStore proxy methods.
package proxy

import (
	"context"

	"gpt-go/internal/model"
)

// AllEligibleProxyCandidates mirrors all_eligible_proxy_candidates: available
// proxies (country asc, createdAt asc).
func (s *Service) AllEligibleProxyCandidates(ctx context.Context, country string) ([]model.ProxyLease, error) {
	return s.store.AllEligibleProxyCandidates(ctx, country)
}

// CountEligible mirrors count_eligible_proxies.
func (s *Service) CountEligible(ctx context.Context, country, group string) (int, error) {
	return s.store.CountEligible(ctx, country, group)
}

// AcquireProxy mirrors acquire_proxy: atomically leases an available proxy.
func (s *Service) AcquireProxy(ctx context.Context, owner string, excluded map[string]bool, leaseSeconds int, country, group string) (*model.ProxyLease, error) {
	return s.store.AcquireProxy(ctx, owner, excluded, leaseSeconds, country, group)
}

// AcquireProxyByID mirrors acquire_proxy_by_id.
func (s *Service) AcquireProxyByID(ctx context.Context, proxyID, owner string, leaseSeconds int) (*model.ProxyLease, error) {
	return s.store.AcquireProxyByID(ctx, proxyID, owner, leaseSeconds)
}

// ReleaseProxy mirrors release_proxy.
func (s *Service) ReleaseProxy(ctx context.Context, proxyID, owner string) error {
	return s.store.ReleaseProxy(ctx, proxyID, owner)
}

// ConsumeProxy mirrors consume_proxy (exclusive strategy: mark used).
func (s *Service) ConsumeProxy(ctx context.Context, proxyID, owner string) error {
	return s.store.ConsumeProxy(ctx, proxyID, owner)
}

// HeartbeatProxy mirrors heartbeat_proxy.
func (s *Service) HeartbeatProxy(ctx context.Context, proxyID, owner string) (bool, error) {
	return s.store.HeartbeatProxy(ctx, proxyID, owner)
}

// ReleaseProxyOwner mirrors release_proxy_owner.
func (s *Service) ReleaseProxyOwner(ctx context.Context, owner string) (int, error) {
	return s.store.ReleaseProxyOwner(ctx, owner)
}
