// Proxy CRUD: list/update/status/delete/summaries/groups/restore, mirroring
// resource_service.py proxy methods.
package proxy

import (
	"context"
	"strings"

	"gpt-go/internal/model"
	"gpt-go/internal/store"
)

// ListProxies mirrors list_proxies: regex search over host/username + country
// filter, createdAt desc, paginated.
func (s *Service) ListProxies(ctx context.Context, page, pageSize int, query, country string) (model.Page[model.ProxyRecord], error) {
	docs, total, err := s.store.List(ctx, store.ProxyListQuery{
		Query:   query,
		Country: country,
		Page:    page,
		Size:    pageSize,
	})
	if err != nil {
		return model.Page[model.ProxyRecord]{}, err
	}
	items := make([]model.ProxyRecord, 0, len(docs))
	for _, d := range docs {
		items = append(items, proxyRecord(d))
	}
	return model.Page[model.ProxyRecord]{
		Items:    items,
		Total:    total,
		Page:     page,
		PageSize: model.PageSize(pageSize),
	}, nil
}

// UpdateProxy mirrors update_proxy: patch enabled/country/group, 404 when absent.
func (s *Service) UpdateProxy(ctx context.Context, id string, in model.ProxyUpdate) (model.ProxyRecord, error) {
	d, err := s.store.Update(ctx, id, in.Enabled, in.Country, in.Group)
	if err != nil {
		return model.ProxyRecord{}, err
	}
	return proxyRecord(*d), nil
}

// SetProxyStatus mirrors set_proxy_status.
func (s *Service) SetProxyStatus(ctx context.Context, id, status string) (model.ProxyRecord, error) {
	d, err := s.store.SetStatus(ctx, id, status)
	if err != nil {
		return model.ProxyRecord{}, err
	}
	return proxyRecord(*d), nil
}

// DeleteProxy mirrors delete_proxy.
func (s *Service) DeleteProxy(ctx context.Context, id string) (model.DeleteResult, error) {
	n, err := s.store.DeleteOne(ctx, id)
	if err != nil {
		return model.DeleteResult{}, err
	}
	return model.DeleteResult{Deleted: n}, nil
}

// DeleteProxies mirrors delete_proxies (bulk-delete).
func (s *Service) DeleteProxies(ctx context.Context, ids []string) (model.DeleteResult, error) {
	n, err := s.store.DeleteMany(ctx, ids)
	if err != nil {
		return model.DeleteResult{}, err
	}
	return model.DeleteResult{Deleted: n}, nil
}

// ClearProxies mirrors clear_proxies.
func (s *Service) ClearProxies(ctx context.Context) (model.DeleteResult, error) {
	n, err := s.store.Clear(ctx)
	if err != nil {
		return model.DeleteResult{}, err
	}
	return model.DeleteResult{Deleted: n}, nil
}

// CountrySummaries mirrors proxy_country_summaries.
func (s *Service) CountrySummaries(ctx context.Context) ([]model.ProxyCountrySummary, error) {
	return s.store.CountrySummaries(ctx)
}

// GroupSummaries mirrors proxy_group_summaries.
func (s *Service) GroupSummaries(ctx context.Context) ([]model.ProxyGroupSummary, error) {
	return s.store.GroupSummaries(ctx)
}

// UpdateGroup mirrors update_proxy_group.
func (s *Service) UpdateGroup(ctx context.Context, in model.ProxyGroupUpdate) (model.ProxyGroupUpdateResult, error) {
	matched, modified, err := s.store.UpdateGroup(ctx, in.Country, in.Group, in.NewCountry, in.NewGroup, in.Enabled)
	if err != nil {
		return model.ProxyGroupUpdateResult{}, err
	}
	return model.ProxyGroupUpdateResult{Matched: matched, Modified: modified}, nil
}

// DeleteGroup mirrors delete_proxy_group.
func (s *Service) DeleteGroup(ctx context.Context, country, group string) (model.DeleteResult, error) {
	n, err := s.store.DeleteGroup(ctx, country, group)
	if err != nil {
		return model.DeleteResult{}, err
	}
	return model.DeleteResult{Deleted: n}, nil
}

// RestoreUsed mirrors restore_used_proxies.
func (s *Service) RestoreUsed(ctx context.Context, in model.RestoreUsedProxiesInput) (model.RestoreUsedResult, error) {
	n, err := s.store.RestoreUsed(ctx, in.Country, in.Group)
	if err != nil {
		return model.RestoreUsedResult{}, err
	}
	return model.RestoreUsedResult{Restored: n}, nil
}

// proxyRecord mirrors _proxy_record: maps a document to the API record, with
// country fallback (ZZ → infer from username/host) and scheme normalization.
func proxyRecord(d model.ProxyDocument) model.ProxyRecord {
	cc := model.NormalizeCountryCode(d.Country)
	if cc == "ZZ" {
		cc = model.InferProxyCountry(d.Username, d.Host)
	}
	scheme := strings.ToLower(d.Scheme)
	switch scheme {
	case model.ProxySchemeHTTP, model.ProxySchemeHTTPS, model.ProxySchemeSocks5, model.ProxySchemeSocks5h:
		// keep
	default:
		scheme = model.ProxySchemeHTTP
	}
	return model.ProxyRecord{
		ID:            d.ID,
		Host:          d.Host,
		Port:          d.Port,
		Username:      d.Username,
		Password:      d.Password,
		Enabled:       d.Enabled,
		Status:        d.Status,
		LatencyMs:     d.LatencyMs,
		LastCheckedAt: d.LastCheckedAt,
		Country:       cc,
		Group:         model.NormalizeProxyGroup(d.Group),
		Scheme:        scheme,
	}
}
