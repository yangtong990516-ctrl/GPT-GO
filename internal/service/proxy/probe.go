// Proxy connectivity + purity probe engine, mirroring
// proxy_subscription_service._probe_proxy. It uses a single external request to
// chatgpt.com/cdn-cgi/trace (returns ip/loc/sliver) plus a local GeoLite2 lookup,
// then grades purity from the Cloudflare "sliver" risk score.
package proxy

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"sync"
	"strings"
	"time"

	"gpt-go/internal/model"
)

// ProbeResult mirrors proxy_subscription_service.ProxyProbeResult.
type ProbeResult struct {
	ProxyURL          string
	Country           string
	LatencyMs         int
	Purity            string // "clean" | "dirty"
	PurityReason      string
	TimezoneID        string
	TimezoneOffsetSec *int
}

// Prober probes one proxy URL for connectivity + country/timezone/purity.
type Prober interface {
	Probe(ctx context.Context, proxyURL string, timeoutSeconds float64) (*ProbeResult, error)
}

// NewService wires the real cdnProber by default (see service.go NewService).

// cdnProber is the real probe engine. It mirrors _probe_proxy exactly:
//  1. GET https://chatgpt.com/cdn-cgi/trace through the proxy (3s connect
//     timeout) and parse ip / loc / sliver.
//  2. Local GeoLite2 lookup for country + IANA timezone (zero online requests),
//     falling back to the trace loc field when the database is missing.
//  3. Purity from sliver: none/tier1 → clean; datacenter/cloud/hosting/vpn/
//     proxy/tier2-5/tunnel → dirty; missing sliver → clean (retry upstream).
type cdnProber struct {
	client *http.Client
}

var (
	traceIP     = regexp.MustCompile(`(?m)^ip=([^\s]+)\s*$`)
	traceLoc    = regexp.MustCompile(`(?m)^loc=([A-Za-z]{2})\s*$`)
	traceSliver = regexp.MustCompile(`(?m)^sliver=([^\s]+)\s*$`)
)

// dirtyMarks mirrors _probe_proxy dirty_marks.
var dirtyMarks = []string{
	"datacenter", "cloud", "hosting", "vpn", "proxy",
	"tier2", "tier3", "tier4", "tier5", "tunnel",
}

// Probe performs one proxy purity probe.
func (p *cdnProber) Probe(ctx context.Context, proxyURL string, timeoutSeconds float64) (*ProbeResult, error) {
	started := time.Now()

	// Build an http.Client routed through the proxy (per-call, mirrors httpx
	// AsyncClient with trust_env=False).
	proxyParsed, err := url.Parse(proxyURL)
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{
		Proxy: http.ProxyURL(proxyParsed),
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   time.Duration(timeoutSeconds) * time.Second,
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://chatgpt.com/cdn-cgi/trace", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "curl/8.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	text := string(body)

	sliver := matchGroup(traceSliver, text)
	egressIP := matchGroup(traceIP, text)
	traceCountry := matchGroup(traceLoc, text)

	// Local GeoLite2 (falls back to trace loc when DB missing).
	country := strings.ToUpper(traceCountry)
	var geo *GeoInfo
	if egressIP != "" {
		geo = lookupGeoIP(egressIP)
	}
	if geo != nil && geo.CountryCode != "" {
		country = strings.ToUpper(geo.CountryCode)
	}
	if !isTwoLetter(country) || country == "ZZ" {
		return nil, errCountryUnidentified
	}

	// ── Purity grading ──
	purity, purityReason := gradePurity(sliver)

	// Timezone only on clean proxies (dirty ones never participate).
	timezoneID := ""
	var tzOffsetSec *int
	if purity == "clean" && geo != nil && geo.TimezoneID != "" {
		timezoneID = geo.TimezoneID
		if off := tzOffset(geo.TimezoneID); off != nil {
			tzOffsetSec = off
		}
	}

	latency := int(time.Since(started).Milliseconds())
	if latency < 1 {
		latency = 1
	}

	return &ProbeResult{
		ProxyURL:          proxyURL,
		Country:           country,
		LatencyMs:         latency,
		Purity:            purity,
		PurityReason:      purityReason,
		TimezoneID:        timezoneID,
		TimezoneOffsetSec: tzOffsetSec,
	}, nil
}

// EgressResult 是拨号器需要的出口实测结果（国家动态实测，不锁死）。
type EgressResult struct {
	EgressIP   string // 实测出口 IP（同批次去重用）
	Country    string // 实测国家码（每次拨号当次有效）
	TimezoneID string // 出口 IP 的 IANA 时区（喂 core 时区/语言联动）
}

// ProbeEgress 经 proxyURL 探测出口（国家 + IP + 时区），供会话型拨号器做
// 「同国家 + 不同 IP」校验。与 Probe 同源（同一次 chatgpt.com/cdn-cgi/trace），
// 但返回出口 IP（Probe 只用于纯净度分级，丢了 egressIP）。
func (p *cdnProber) ProbeEgress(ctx context.Context, proxyURL string, timeoutSeconds float64) (*EgressResult, error) {
	proxyParsed, err := url.Parse(proxyURL)
	if err != nil {
		return nil, err
	}
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyParsed)},
		Timeout:   time.Duration(timeoutSeconds) * time.Second,
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://chatgpt.com/cdn-cgi/trace", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "curl/8.0")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	text := string(body)
	egressIP := matchGroup(traceIP, text)
	country := strings.ToUpper(matchGroup(traceLoc, text))
	// GeoIP 校准国家 + 解时区（国家动态实测：以出口 IP 的 GeoIP 为准，trace loc 兜底）。
	tz := ""
	if egressIP != "" {
		if geo := lookupGeoIP(egressIP); geo != nil {
			if geo.CountryCode != "" {
				country = strings.ToUpper(geo.CountryCode)
			}
			tz = geo.TimezoneID
		}
	}
	if !isTwoLetter(country) || country == "ZZ" {
		return nil, errCountryUnidentified
	}
	return &EgressResult{EgressIP: egressIP, Country: country, TimezoneID: tz}, nil
}

// ProbeEgressOn 是包级便捷入口：用默认 cdnProber 探测出口（拨号器适配用）。
func ProbeEgressOn(ctx context.Context, proxyURL string, timeoutSeconds float64) (country, egressIP, timezoneID string, err error) {
	res, err := (&cdnProber{}).ProbeEgress(ctx, proxyURL, timeoutSeconds)
	if err != nil {
		return "", "", "", err
	}
	return res.Country, res.EgressIP, res.TimezoneID, nil
}

// tzOffset resolves an IANA timezone's current UTC offset in seconds (nil when
// the zone is unknown).
func tzOffset(iana string) *int {
	loc, err := time.LoadLocation(iana)
	if err != nil {
		return nil
	}
	_, offset := time.Now().In(loc).Zone()
	return &offset
}

func matchGroup(re *regexp.Regexp, s string) string {
	m := re.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// gradePurity mirrors _probe_proxy purity grading:
//   - ""                 → clean (sliver-unknown, upstream retries)
//   - none / xxx-tier1   → clean (residential, normal risk tier)
//   - datacenter/cloud/hosting/vpn/proxy/tier2-5/tunnel → dirty
func gradePurity(sliver string) (purity, reason string) {
	low := strings.ToLower(sliver)
	if low == "" {
		return "clean", "sliver-unknown"
	}
	for _, m := range dirtyMarks {
		if strings.Contains(low, m) {
			return "dirty", "cloudflare-sliver=" + sliver
		}
	}
	return "clean", ""
}

func isTwoLetter(s string) bool {
	return len(s) == 2 && s[0] >= 'A' && s[0] <= 'Z' && s[1] >= 'A' && s[1] <= 'Z'
}

// errCountryUnidentified mirrors the ValueError raised when the country cannot
// be identified.
var errCountryUnidentified = &probeIdentError{msg: "proxy country could not be identified"}

type probeIdentError struct{ msg string }

func (e *probeIdentError) Error() string { return e.msg }

// TestStoredProxies mirrors test_stored_proxies: probes eligible proxies and
// aggregates the result. Failed/dirty proxies are recorded as failed.
// concurrency 来自 ExecutionSettings.proxyCheckConcurrency(1-32);<1 时按 1(串行)。
// 原为串行 for 循环,逐个 Probe 慢;改为带并发上限的 worker 池,结果写库各自独立。
func (s *Service) TestStoredProxies(ctx context.Context, country, group *string, timeoutSeconds float64, concurrency int) (model.ProxyTestResult, error) {
	docs, err := s.store.ProxyDocumentsForTest(ctx, deref(country), deref(group), nil)
	if err != nil {
		return model.ProxyTestResult{}, err
	}

	result := model.ProxyTestResult{Tested: len(docs)}
	if concurrency < 1 {
		concurrency = 1
	}
	if concurrency > len(docs) {
		concurrency = len(docs)
	}

	var (
		mu        sync.Mutex
		usable    []*ProbeResult
		countries = map[string]int{}
	)
	probeOne := func(d model.ProxyDocument) {
		scheme := model.NormalizeProxyScheme(d.Scheme)
		proxyURL := model.ProxyURL(d.Host, d.Port, d.Username, d.Password, scheme)
		probe, perr := s.prober.Probe(ctx, proxyURL, timeoutSeconds)
		if perr != nil || probe == nil {
			_ = s.store.RecordProxyTest(ctx, d.ID, false, nil, "", "", nil)
			return
		}
		if probe.Purity != "clean" {
			_ = s.store.RecordProxyTest(ctx, d.ID, false, nil, "", "", nil)
			return
		}
		_ = s.store.RecordProxyTest(ctx, d.ID, true, intPtr(probe.LatencyMs), probe.Country, probe.TimezoneID, probe.TimezoneOffsetSec)
		mu.Lock()
		usable = append(usable, probe)
		countries[probe.Country]++
		mu.Unlock()
	}

	// worker 池:concurrency 个 goroutine 消费 docs 通道。
	jobs := make(chan model.ProxyDocument)
	var wg sync.WaitGroup
	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for d := range jobs {
				probeOne(d)
			}
		}()
	}
	for _, d := range docs {
		jobs <- d
	}
	close(jobs)
	wg.Wait()

	result.Available = len(usable)
	result.Failed = len(docs) - len(usable)
	if len(usable) > 0 {
		sum := 0
		for _, p := range usable {
			sum += p.LatencyMs
		}
		avg := sum / len(usable)
		result.AverageLatencyMs = &avg
	}
	keys := make([]string, 0, len(countries))
	for k := range countries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		result.Countries = append(result.Countries, map[string]any{"country": k, "count": countries[k]})
	}
	return result, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func intPtr(v int) *int { return &v }
