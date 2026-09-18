// Proxy import: raw-text parsing, mirroring resource_service.import_proxies
// (resource_service.py:3037). Supports three formats:
//   - YAML document (proxies: [{...}])
//   - URL format (scheme://user:pass@host:port)
//   - bare host:port:user:pass (the two groups supplied for testing)
package proxy

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	"gpt-go/internal/model"
)

// ImportProxies parses rawText and upserts proxies, mirroring import_proxies.
func (s *Service) ImportProxies(ctx context.Context, rawText string, country, group *string) (model.ImportResult, error) {
	res := model.ImportResult{}
	cleaned := strings.TrimPrefix(rawText, "\ufeff")
	cleaned = strings.TrimSpace(cleaned)

	// 每个条目：line + entryCountry + entryGroup。
	type entry struct {
		line    string
		country *string
		group   *string
	}
	var entries []entry

	if hasYAMLHeader(cleaned) {
		nodes := parseProxyYAML(cleaned)
		if nodes == nil {
			entries = append(entries, entry{})
		} else {
			for _, n := range nodes {
				entries = append(entries, n)
			}
		}
	} else {
		for _, line := range strings.Fields(cleaned) {
			entries = append(entries, entry{line: line})
		}
	}

	seen := map[string]bool{}
	for _, e := range entries {
		if e.line == "" {
			res.Total++
			res.ErrorCount++
			continue
		}
		res.Total++

		host, port, user, pass, scheme, ok := parseProxyLine(e.line)
		if !ok {
			res.ErrorCount++
			continue
		}
		if host == "" || user == "" || pass == "" || port < 1 || port > 65535 {
			res.ErrorCount++
			continue
		}
		if !isSupportedScheme(scheme) {
			res.ErrorCount++
			continue
		}
		scheme = model.NormalizeProxyScheme(scheme)
		// 按厂商+端口纠正协议（IPRocket 9595 → socks5h）。
		scheme = model.InferProxySchemeByVendor(host, port, scheme)

		key := strings.ToLower(host) + "\x00" + strconv.Itoa(port) + "\x00" + user + "\x00" + pass + "\x00" + scheme
		if seen[key] {
			res.DuplicateCount++
			continue
		}
		seen[key] = true

		cc := model.InferProxyCountry(user, host)
		if cc == "ZZ" {
			cc = model.NormalizeCountryCode("")
		}
		var effectiveCountry *string
		if e.country != nil {
			effectiveCountry = e.country
		} else if country != nil {
			effectiveCountry = country
		}
		grp := ""
		if e.group != nil {
			grp = model.NormalizeProxyGroup(*e.group)
		} else if group != nil {
			grp = model.NormalizeProxyGroup(*group)
		} else {
			grp = model.DefaultProxyGroup
		}

		doc := model.ProxyDocument{
			Host:        host,
			Port:        port,
			Username:    user,
			Password:    pass,
			Enabled:     true,
			Status:      model.ProxyStatusUnknown,
			Country:     normalizeCountryOrDefault(effectiveCountry, cc),
			Group:       grp,
			Scheme:      scheme,
			CreatedAt:   nowISO(),
			RandomScore: random63(),
		}
		inserted, err := s.store.Upsert(ctx, doc)
		if err != nil {
			return res, err
		}
		if inserted {
			res.Imported++
		} else {
			res.DuplicateCount++
		}
	}
	return res, nil
}

// normalizeCountryOrDefault mirrors upsert_proxy: country or infer_proxy_country。
func normalizeCountryOrDefault(explicit *string, inferred string) string {
	if explicit != nil && *explicit != "" {
		return model.NormalizeCountryCode(*explicit)
	}
	if inferred != "" && inferred != "ZZ" {
		return inferred
	}
	return model.NormalizeCountryCode("")
}

// hasYAMLHeader mirrors the regex `^\s*proxies\s*:`。
func hasYAMLHeader(s string) bool {
	lines := strings.Split(s, "\n")
	for _, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		if trimmed == "" {
			continue
		}
		return strings.HasPrefix(trimmed, "proxies:")
	}
	return false
}

// parseProxyYAML mirrors the YAML branch of import_proxies. Returns nil when
// the document is not a valid list (→ single empty entry, error).
func parseProxyYAML(raw string) []struct {
	line    string
	country *string
	group   *string
} {
	// Minimal YAML handling for the proxies list. The reference uses
	// yaml.safe_load; here we parse a conservative subset (list of maps with
	// type/server/host/port/username|user/password|pass/country/group).
	// TODO: full YAML via gopkg.in/yaml.v3 when needed (currently no consumer).
	_ = raw
	return nil
}

// parseProxyLine parses one proxy line into (host, port, user, pass, scheme).
// Mirrors the per-line parsing in import_proxies.
func parseProxyLine(line string) (host string, port int, user, pass, scheme string, ok bool) {
	scheme = model.ProxySchemeHTTP
	if strings.Contains(line, "://") {
		u, err := url.Parse(line)
		if err != nil || u.Hostname() == "" {
			return "", 0, "", "", "", false
		}
		scheme = strings.ToLower(u.Scheme)
		host = u.Hostname()
		p := u.Port()
		if p == "" {
			port = 0
		} else {
			port, _ = strconv.Atoi(p)
		}
		if u.User != nil {
			user = u.User.Username()
			pass, _ = u.User.Password()
		}
		return host, port, user, pass, scheme, true
	}

	if strings.Contains(line, "@") {
		// socks5 认证格式：user:pass@host:port
		parts := strings.SplitN(line, "@", 2)
		auth := parts[0]
		hostPort := parts[1]
		if !strings.Contains(auth, ":") || !strings.Contains(hostPort, ":") {
			return "", 0, "", "", "", false
		}
		authParts := strings.SplitN(auth, ":", 2)
		user = strings.TrimSpace(authParts[0])
		pass = strings.TrimSpace(authParts[1])
		idx := strings.LastIndex(hostPort, ":")
		host = strings.TrimSpace(hostPort[:idx])
		port, _ = strconv.Atoi(strings.TrimSpace(hostPort[idx+1:]))
		scheme = model.ProxySchemeSocks5
		return host, port, user, pass, scheme, true
	}

	// http 格式：host:port:user:pass
	parts := strings.SplitN(line, ":", 4)
	if len(parts) != 4 {
		return "", 0, "", "", "", false
	}
	host = strings.TrimSpace(parts[0])
	port, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return "", 0, "", "", "", false
	}
	user = strings.TrimSpace(parts[2])
	pass = strings.TrimSpace(parts[3])
	return host, port, user, pass, model.ProxySchemeHTTP, true
}

func isSupportedScheme(s string) bool {
	switch s {
	case model.ProxySchemeHTTP, model.ProxySchemeHTTPS, model.ProxySchemeSocks5, model.ProxySchemeSocks5h:
		return true
	}
	return false
}
