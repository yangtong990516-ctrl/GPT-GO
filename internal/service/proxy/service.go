// Package proxy implements the proxy-pool service layer, mirroring
// resource_service.py + probe_store.py + proxy_subscription_service.py.
//
// Sub-domains are split across files: crud.go (list/update/status/delete/
// summaries), import.go (raw-text parsing), lease.go (acquire/release/
// candidates), probe.go (connectivity/purity engine).
package proxy

import (
	"math/rand"
	"time"

	"gpt-go/internal/store"
)

// Service is the proxy-pool service, bound to a ProxyStore.
type Service struct {
	store store.ProxyStore
	// prober is the connectivity/purity probe engine (see probe.go).
	prober Prober
}

// NewService returns a Service backed by the given store, with the real
// cdn-cgi/trace purity probe engine.
func NewService(s store.ProxyStore) *Service {
	return &Service{
		store:  s,
		prober: &cdnProber{},
	}
}

// NewServiceWithProber returns a Service with an injected probe engine (tests).
func NewServiceWithProber(s store.ProxyStore, p Prober) *Service {
	svc := NewService(s)
	svc.prober = p
	return svc
}

// nowISO returns the current UTC time in RFC3339Nano (matches Python isoformat).
func nowISO() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

// random63 mirrors random.getrandbits(63): a non-negative 63-bit int.
func random63() int64 {
	return rand.Int63()
}
