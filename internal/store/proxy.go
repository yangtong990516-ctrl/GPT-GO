// Proxy pool storage contract and in-memory mock. Mirrors probe_store.py +
// resource_service.py proxies collection.
package store

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gpt-go/internal/model"
)

// ProxyListQuery captures list_proxies filters, mirroring list_proxies params.
type ProxyListQuery struct {
	Query   string // "" == no search (regex over host/username)
	Country string // "" == no country filter
	Page    int
	Size    int
}

// ProxyStore is the proxy-pool storage contract used by the service layer.
type ProxyStore interface {
	// List returns a page of proxies (createdAt desc) plus total count.
	List(ctx context.Context, q ProxyListQuery) ([]model.ProxyDocument, int, error)
	// Upsert inserts when the identity (host/port/user/pass) is absent; returns
	// true when inserted (mirrors upsert_proxy: upserted_id or modified).
	Upsert(ctx context.Context, d model.ProxyDocument) (bool, error)
	// Update patches enabled/country/group on one proxy; returns the updated doc.
	Update(ctx context.Context, id string, enabled *bool, country, group *string) (*model.ProxyDocument, error)
	// SetStatus sets status + statusUpdatedAt (and clears usedAt when available).
	SetStatus(ctx context.Context, id, status string) (*model.ProxyDocument, error)
	// DeleteOne removes a proxy by id; returns deleted count.
	DeleteOne(ctx context.Context, id string) (int, error)
	// DeleteMany removes proxies by ids; returns deleted count.
	DeleteMany(ctx context.Context, ids []string) (int, error)
	// Clear removes all proxies; returns deleted count.
	Clear(ctx context.Context) (int, error)
	// CountrySummaries returns country → {total, enabled}.
	CountrySummaries(ctx context.Context) ([]model.ProxyCountrySummary, error)
	// GroupSummaries returns (country, group) → summary.
	GroupSummaries(ctx context.Context) ([]model.ProxyGroupSummary, error)
	// UpdateGroup patches all proxies in (country, group); returns matched/modified.
	UpdateGroup(ctx context.Context, country, group string, newCountry, newGroup *string, enabled *bool) (matched, modified int, err error)
	// DeleteGroup removes all proxies in (country, group); returns deleted count.
	DeleteGroup(ctx context.Context, country, group string) (int, error)
	// RestoreUsed resets used → available for the given country/group; returns count.
	RestoreUsed(ctx context.Context, country, group *string) (int, error)

	// AllEligibleProxyCandidates returns available proxies (country asc, createdAt asc).
	AllEligibleProxyCandidates(ctx context.Context, country string) ([]model.ProxyLease, error)
	// CountEligible returns the number of eligible proxies.
	CountEligible(ctx context.Context, country, group string) (int, error)

	// AcquireProxy atomically leases an available proxy to owner.
	AcquireProxy(ctx context.Context, owner string, excluded map[string]bool, leaseSeconds int, country, group string) (*model.ProxyLease, error)
	// AcquireProxyByID leases a specific proxy id to owner.
	AcquireProxyByID(ctx context.Context, proxyID, owner string, leaseSeconds int) (*model.ProxyLease, error)
	// ReleaseProxy releases a proxy lease held by owner.
	ReleaseProxy(ctx context.Context, proxyID, owner string) error
	// ConsumeProxy marks a used proxy as "used" (exclusive strategy).
	ConsumeProxy(ctx context.Context, proxyID, owner string) error
	// HeartbeatProxy refreshes the lease heartbeat; returns whether owned.
	HeartbeatProxy(ctx context.Context, proxyID, owner string) (bool, error)
	// ReleaseProxyOwner releases all leases held by owner.
	ReleaseProxyOwner(ctx context.Context, owner string) (int, error)

	// ProxyDocumentsForTest returns proxies eligible for connectivity testing.
	ProxyDocumentsForTest(ctx context.Context, country, group string, limit *int) ([]model.ProxyDocument, error)
	// RecordProxyTest records a probe result (available/failed) on one proxy.
	RecordProxyTest(ctx context.Context, id string, available bool, latencyMs *int, country, timezoneID string, timezoneOffsetSec *int) error
	// Count returns the number of proxies matching pred (nil pred = all).
	Count(ctx context.Context, pred func(model.ProxyDocument) bool) (int, error)
}

// MockProxyStore is an in-memory ProxyStore (mutex-guarded).
type MockProxyStore struct {
	mu  sync.Mutex
	doc map[string]*model.ProxyDocument // keyed by id
	seq int
}

// NewMockProxyStore returns an empty in-memory proxy store.
func NewMockProxyStore() *MockProxyStore {
	return &MockProxyStore{doc: map[string]*model.ProxyDocument{}}
}

func (m *MockProxyStore) nextID() string {
	m.seq++
	return "proxy-" + itoa(m.seq)
}

// List returns a page sorted createdAt desc, filtered by query/country.
func (m *MockProxyStore) List(_ context.Context, q ProxyListQuery) ([]model.ProxyDocument, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var all []model.ProxyDocument
	for _, d := range m.doc {
		if q.Query != "" {
			q := strings.ToLower(q.Query)
			if !strings.Contains(strings.ToLower(d.Host), q) && !strings.Contains(strings.ToLower(d.Username), q) {
				continue
			}
		}
		if q.Country != "" {
			if !proxyDocMatchesCountry(d, q.Country) {
				continue
			}
		}
		all = append(all, *d)
	}
	sort.SliceStable(all, func(i, j int) bool {
		return all[i].CreatedAt > all[j].CreatedAt
	})
	total := len(all)
	start := (q.Page - 1) * q.Size
	if start < 0 {
		start = 0
	}
	if start > total {
		start = total
	}
	end := start + q.Size
	if end > total {
		end = total
	}
	return all[start:end], total, nil
}

// proxyDocMatchesCountry mirrors proxy_country_filter: country field match, or
// ZZ/missing + username/host inferred pattern.
func proxyDocMatchesCountry(d *model.ProxyDocument, country string) bool {
	cc := model.NormalizeCountryCode(country)
	if d.Country == cc {
		return true
	}
	if d.Country == "" || d.Country == "ZZ" {
		return model.InferProxyCountry(d.Username, d.Host) == cc
	}
	return false
}

// Upsert inserts when identity absent; returns true when inserted.
func (m *MockProxyStore) Upsert(_ context.Context, d model.ProxyDocument) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.doc {
		if existing.Host == d.Host && existing.Port == d.Port &&
			existing.Username == d.Username && existing.Password == d.Password {
			// Update mutable fields (scheme/country/group/timezone) without
			// touching insert-only fields.
			if d.Scheme != "" {
				existing.Scheme = d.Scheme
			}
			if d.Country != "" && d.Country != "ZZ" {
				existing.Country = d.Country
			}
			if d.Group != "" {
				existing.Group = d.Group
			}
			if d.TimezoneID != "" {
				existing.TimezoneID = d.TimezoneID
			}
			if d.TimezoneOffsetSec != nil {
				existing.TimezoneOffsetSec = d.TimezoneOffsetSec
			}
			return false, nil
		}
	}
	d.ID = m.nextID()
	cp := d
	m.doc[d.ID] = &cp
	return true, nil
}

// Update patches enabled/country/group; returns updated doc or ErrNotFound.
func (m *MockProxyStore) Update(_ context.Context, id string, enabled *bool, country, group *string) (*model.ProxyDocument, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.doc[id]
	if !ok {
		return nil, ErrNotFound
	}
	if enabled != nil {
		d.Enabled = *enabled
	}
	if country != nil {
		d.Country = model.NormalizeCountryCode(*country)
	}
	if group != nil {
		d.Group = model.NormalizeProxyGroup(*group)
	}
	out := *d
	return &out, nil
}

// SetStatus sets status + statusUpdatedAt; clears usedAt when available.
func (m *MockProxyStore) SetStatus(_ context.Context, id, status string) (*model.ProxyDocument, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.doc[id]
	if !ok {
		return nil, ErrNotFound
	}
	d.Status = status
	now := time.Now().UTC().Format(time.RFC3339Nano)
	d.StatusUpdatedAt = &now
	if status == model.ProxyStatusAvailable {
		d.UsedAt = nil
	}
	out := *d
	return &out, nil
}

// DeleteOne removes a proxy by id.
func (m *MockProxyStore) DeleteOne(_ context.Context, id string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.doc[id]; !ok {
		return 0, nil
	}
	delete(m.doc, id)
	return 1, nil
}

// DeleteMany removes proxies by ids.
func (m *MockProxyStore) DeleteMany(_ context.Context, ids []string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, id := range ids {
		if _, ok := m.doc[id]; ok {
			delete(m.doc, id)
			n++
		}
	}
	return n, nil
}

// Clear removes all proxies.
func (m *MockProxyStore) Clear(_ context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := len(m.doc)
	m.doc = map[string]*model.ProxyDocument{}
	return n, nil
}

// CountrySummaries returns country → {total, enabled}.
func (m *MockProxyStore) CountrySummaries(_ context.Context) ([]model.ProxyCountrySummary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	counts := map[string][2]int{}
	for _, d := range m.doc {
		cc := effectiveCountry(d)
		v := counts[cc]
		v[0]++
		if d.Enabled {
			v[1]++
		}
		counts[cc] = v
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]model.ProxyCountrySummary, 0, len(keys))
	for _, k := range keys {
		out = append(out, model.ProxyCountrySummary{Country: k, Total: counts[k][0], Enabled: counts[k][1]})
	}
	return out, nil
}

// GroupSummaries returns (country, group) → summary.
func (m *MockProxyStore) GroupSummaries(_ context.Context) ([]model.ProxyGroupSummary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	type key struct{ c, g string }
	counts := map[key]*model.ProxyGroupSummary{}
	schemes := map[key]map[string]bool{}
	for _, d := range m.doc {
		k := key{effectiveCountry(d), model.NormalizeProxyGroup(d.Group)}
		s := counts[k]
		if s == nil {
			s = &model.ProxyGroupSummary{Country: k.c, Group: k.g}
			counts[k] = s
			schemes[k] = map[string]bool{}
		}
		s.Total++
		if d.Enabled {
			s.Enabled++
		}
		switch d.Status {
		case model.ProxyStatusAvailable:
			s.Available++
		case model.ProxyStatusUsed:
			s.Used++
		case model.ProxyStatusQuarantined:
			s.Quarantined++
		}
		schemes[k][model.NormalizeProxyScheme(d.Scheme)] = true
	}
	keys := make([]key, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].c != keys[j].c {
			return keys[i].c < keys[j].c
		}
		return keys[i].g < keys[j].g
	})
	out := make([]model.ProxyGroupSummary, 0, len(keys))
	for _, k := range keys {
		s := counts[k]
		s.Schemes = sortedKeys(schemes[k])
		out = append(out, *s)
	}
	return out, nil
}

// UpdateGroup patches all in (country, group).
func (m *MockProxyStore) UpdateGroup(_ context.Context, country, group string, newCountry, newGroup *string, enabled *bool) (int, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	matched, modified := 0, 0
	for _, d := range m.doc {
		if !proxyDocMatchesCountry(d, country) || model.NormalizeProxyGroup(d.Group) != model.NormalizeProxyGroup(group) {
			continue
		}
		matched++
		if newCountry != nil {
			d.Country = model.NormalizeCountryCode(*newCountry)
			modified++
		}
		if newGroup != nil {
			d.Group = model.NormalizeProxyGroup(*newGroup)
			modified++
		}
		if enabled != nil {
			d.Enabled = *enabled
			modified++
		}
	}
	return matched, modified, nil
}

// DeleteGroup removes all in (country, group).
func (m *MockProxyStore) DeleteGroup(_ context.Context, country, group string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for id, d := range m.doc {
		if proxyDocMatchesCountry(d, country) && model.NormalizeProxyGroup(d.Group) == model.NormalizeProxyGroup(group) {
			delete(m.doc, id)
			n++
		}
	}
	return n, nil
}

// RestoreUsed resets used → available for country/group.
func (m *MockProxyStore) RestoreUsed(_ context.Context, country, group *string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, d := range m.doc {
		if d.Status != model.ProxyStatusUsed {
			continue
		}
		if country != nil && *country != "" && !proxyDocMatchesCountry(d, *country) {
			continue
		}
		if group != nil && *group != "" && model.NormalizeProxyGroup(d.Group) != model.NormalizeProxyGroup(*group) {
			continue
		}
		d.Status = model.ProxyStatusAvailable
		d.UsedAt = nil
		now := time.Now().UTC().Format(time.RFC3339Nano)
		d.StatusUpdatedAt = &now
		n++
	}
	return n, nil
}

// AllEligibleProxyCandidates returns available proxies sorted country asc, createdAt asc.
func (m *MockProxyStore) AllEligibleProxyCandidates(_ context.Context, country string) ([]model.ProxyLease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var docs []*model.ProxyDocument
	for _, d := range m.doc {
		if !d.Enabled || d.Status != model.ProxyStatusAvailable {
			continue
		}
		if country != "" && !proxyDocMatchesCountry(d, country) {
			continue
		}
		docs = append(docs, d)
	}
	sort.SliceStable(docs, func(i, j int) bool {
		ci, cj := effectiveCountry(docs[i]), effectiveCountry(docs[j])
		if ci != cj {
			return ci < cj
		}
		if docs[i].CreatedAt != docs[j].CreatedAt {
			return docs[i].CreatedAt < docs[j].CreatedAt
		}
		return docs[i].ID < docs[j].ID
	})
	out := make([]model.ProxyLease, 0, len(docs))
	for _, d := range docs {
		out = append(out, toLease(d))
	}
	return out, nil
}

// CountEligible returns the count of eligible proxies.
func (m *MockProxyStore) CountEligible(_ context.Context, country, group string) (int, error) {
	if group == model.LocalProxyGroup {
		return 10000, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, d := range m.doc {
		if !d.Enabled || d.Status != model.ProxyStatusAvailable {
			continue
		}
		if country != "" && !proxyDocMatchesCountry(d, country) {
			continue
		}
		if group != "" && model.NormalizeProxyGroup(d.Group) != model.NormalizeProxyGroup(group) {
			continue
		}
		n++
	}
	return n, nil
}

// AcquireProxy atomically leases an available proxy (least-used + random).
func (m *MockProxyStore) AcquireProxy(_ context.Context, owner string, excluded map[string]bool, leaseSeconds int, country, group string) (*model.ProxyLease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if group == model.LocalProxyGroup {
		lease := model.ProxyLease{
			ID:      model.LocalProxyIDPrefix + owner,
			Host:    "127.0.0.1",
			Port:    7890,
			Country: strings.ToUpper(country),
			Group:   model.LocalProxyGroup,
			Scheme:  model.ProxySchemeHTTP,
		}
		return &lease, nil
	}
	now := time.Now().UTC()
	var best *model.ProxyDocument
	for _, d := range m.doc {
		if !d.Enabled || d.Status != model.ProxyStatusAvailable {
			continue
		}
		if excluded != nil && excluded[d.ID] {
			continue
		}
		if country != "" && !proxyDocMatchesCountry(d, country) {
			continue
		}
		if group != "" && model.NormalizeProxyGroup(d.Group) != model.NormalizeProxyGroup(group) {
			continue
		}
		// 排他租约：leaseOwner 为空 或 leaseUntil 已过期。
		if d.LeaseOwner != "" && d.LeaseUntil != nil && parseTime(*d.LeaseUntil).After(now) {
			continue
		}
		if best == nil {
			best = d
			continue
		}
		// activeLeaseCount asc，再 randomScore desc，再 id asc。
		if d.ActiveLeaseCount < best.ActiveLeaseCount {
			best = d
		} else if d.ActiveLeaseCount == best.ActiveLeaseCount {
			if d.RandomScore > best.RandomScore {
				best = d
			} else if d.RandomScore == best.RandomScore && d.ID < best.ID {
				best = d
			}
		}
	}
	if best == nil {
		return nil, nil
	}
	until := now.Add(time.Duration(leaseSeconds) * time.Second).Format(time.RFC3339Nano)
	best.LeaseOwner = owner
	best.LeaseUntil = &until
	best.LastSelectedAt = stringPtr(now.Format(time.RFC3339Nano))
	best.ActiveLeaseOwners = addUnique(best.ActiveLeaseOwners, owner)
	best.ActiveLeaseCount++
	lease := toLease(best)
	return &lease, nil
}

// AcquireProxyByID leases a specific id.
func (m *MockProxyStore) AcquireProxyByID(_ context.Context, proxyID, owner string, leaseSeconds int) (*model.ProxyLease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.doc[proxyID]
	if !ok || !d.Enabled || d.Status != model.ProxyStatusAvailable {
		return nil, nil
	}
	now := time.Now().UTC()
	d.LastSelectedAt = stringPtr(now.Format(time.RFC3339Nano))
	d.ActiveLeaseOwners = addUnique(d.ActiveLeaseOwners, owner)
	d.ActiveLeaseCount++
	lease := toLease(d)
	return &lease, nil
}

// ReleaseProxy releases a lease held by owner.
func (m *MockProxyStore) ReleaseProxy(_ context.Context, proxyID, owner string) error {
	if strings.HasPrefix(proxyID, model.LocalProxyIDPrefix) {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.doc[proxyID]
	if !ok {
		return nil
	}
	if contains(d.ActiveLeaseOwners, owner) {
		d.ActiveLeaseOwners = removeValue(d.ActiveLeaseOwners, owner)
		d.ActiveLeaseCount--
		d.LeaseOwner = ""
		d.LeaseUntil = nil
	}
	return nil
}

// ConsumeProxy marks a used proxy as "used".
func (m *MockProxyStore) ConsumeProxy(_ context.Context, proxyID, owner string) error {
	if strings.HasPrefix(proxyID, model.LocalProxyIDPrefix) {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.doc[proxyID]
	if !ok {
		return nil
	}
	if contains(d.ActiveLeaseOwners, owner) {
		d.Status = model.ProxyStatusUsed
		d.UsedAt = stringPtr(time.Now().UTC().Format(time.RFC3339Nano))
		d.ActiveLeaseOwners = removeValue(d.ActiveLeaseOwners, owner)
		d.ActiveLeaseCount--
		d.LeaseOwner = ""
		d.LeaseUntil = nil
	}
	return nil
}

// HeartbeatProxy refreshes lease heartbeat; returns whether owned.
func (m *MockProxyStore) HeartbeatProxy(_ context.Context, proxyID, owner string) (bool, error) {
	if strings.HasPrefix(proxyID, model.LocalProxyIDPrefix) {
		return true, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.doc[proxyID]
	if !ok || !contains(d.ActiveLeaseOwners, owner) {
		return false, nil
	}
	return true, nil
}

// ReleaseProxyOwner releases all leases held by owner.
func (m *MockProxyStore) ReleaseProxyOwner(_ context.Context, owner string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, d := range m.doc {
		if d.LeaseOwner == owner || contains(d.ActiveLeaseOwners, owner) {
			d.ActiveLeaseOwners = removeValue(d.ActiveLeaseOwners, owner)
			d.ActiveLeaseCount--
			d.LeaseOwner = ""
			d.LeaseUntil = nil
			n++
		}
	}
	return n, nil
}

// ProxyDocumentsForTest returns proxies eligible for connectivity testing.
func (m *MockProxyStore) ProxyDocumentsForTest(_ context.Context, country, group string, limit *int) ([]model.ProxyDocument, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	var docs []model.ProxyDocument
	for _, d := range m.doc {
		// 无活跃租约 且 租约已过期。
		if len(d.ActiveLeaseOwners) > 0 && d.ActiveLeaseCount > 0 {
			continue
		}
		if d.LeaseUntil != nil && parseTime(*d.LeaseUntil).After(now) {
			continue
		}
		if country != "" && !proxyDocMatchesCountry(d, country) {
			continue
		}
		if group != "" && model.NormalizeProxyGroup(d.Group) != model.NormalizeProxyGroup(group) {
			continue
		}
		docs = append(docs, *d)
	}
	sort.SliceStable(docs, func(i, j int) bool {
		return lastCheckedLess(docs[i].LastCheckedAt, docs[j].LastCheckedAt)
	})
	if limit != nil && *limit > 0 && len(docs) > *limit {
		docs = docs[:*limit]
	}
	return docs, nil
}

// RecordProxyTest records a probe result on one proxy.
func (m *MockProxyStore) RecordProxyTest(_ context.Context, id string, available bool, latencyMs *int, country, timezoneID string, timezoneOffsetSec *int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.doc[id]
	if !ok {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	d.LastCheckedAt = &now
	if available {
		d.LatencyMs = latencyMs
		if country != "" {
			d.Country = model.NormalizeCountryCode(country)
		}
		if timezoneID != "" {
			d.TimezoneID = timezoneID
		}
		if timezoneOffsetSec != nil {
			d.TimezoneOffsetSec = timezoneOffsetSec
		}
		d.Status = model.ProxyStatusAvailable
		d.ConsecutiveFailures = 0
	} else {
		d.LatencyMs = nil
		d.ConsecutiveFailures++
		if d.ConsecutiveFailures >= 3 {
			d.Status = model.ProxyStatusQuarantined
		} else if d.Status == model.ProxyStatusAvailable {
			d.Status = model.ProxyStatusAvailable
		} else {
			d.Status = model.ProxyStatusUnknown
		}
	}
	return nil
}

// Count returns the number of proxies matching pred (nil pred = all).
func (m *MockProxyStore) Count(_ context.Context, pred func(model.ProxyDocument) bool) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, d := range m.doc {
		if pred == nil || pred(*d) {
			n++
		}
	}
	return n, nil
}

// ── helpers ─────────────────────────────────────────────────────────────────

func effectiveCountry(d *model.ProxyDocument) string {
	cc := model.NormalizeCountryCode(d.Country)
	if cc == "ZZ" {
		cc = model.InferProxyCountry(d.Username, d.Host)
	}
	return cc
}

func toLease(d *model.ProxyDocument) model.ProxyLease {
	return model.ProxyLease{
		ID:                d.ID,
		Host:              d.Host,
		Port:              d.Port,
		Username:          d.Username,
		Password:          d.Password,
		Country:           effectiveCountry(d),
		Group:             model.NormalizeProxyGroup(d.Group),
		Scheme:            model.NormalizeProxyScheme(d.Scheme),
		TimezoneID:        d.TimezoneID,
		TimezoneOffsetSec: d.TimezoneOffsetSec,
	}
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

func lastCheckedLess(a, b *string) bool {
	if a == nil && b == nil {
		return false
	}
	if a == nil {
		return true
	}
	if b == nil {
		return false
	}
	return parseTime(*a).Before(parseTime(*b))
}

func stringPtr(s string) *string { return &s }

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func addUnique(list []string, v string) []string {
	if contains(list, v) {
		return list
	}
	return append(list, v)
}

func removeValue(list []string, v string) []string {
	out := list[:0]
	for _, x := range list {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

func itoa(n int) string {
	return strconv.Itoa(n)
}
