// Package sentinel implements the Sentinel (CDK SDK) version-expiry checker,
// mirroring sentinel_version_scheduler.py. It exposes config read/write (runtime
// switch, hot-reloaded via PUT /api/sentinel/config) plus a background loop that
// periodically probes the bundled SDK version.
//
// The remote network probe is a stub for now (see check.go TODO); the config,
// persistence, scheduling, and API contract are fully implemented.
package sentinel

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"gpt-go/internal/model"
	"gpt-go/internal/store"
)

const (
	// minIntervalHours / maxIntervalHours mirror save_config clamping [1,720].
	minIntervalHours = 1
	maxIntervalHours = 720

	// probeTimeoutSeconds is the per-probe timeout, mirroring the 20.0 default.
	probeTimeoutSeconds = 20.0
)

// Scheduler runs the periodic Sentinel SDK version check. It owns a background
// goroutine (see Start/Stop) and serializes individual checks with a dedicated
// mutex (mirroring Python's asyncio.Lock run-once semantics).
type Scheduler struct {
	store  store.SentinelStore
	proxys ProxyProvider

	// probeSDK / probeFrame 是网络探测函数（默认真实 httpcloak 探测，测试可注入
	// mock 不联网）。做成实例字段而非 package 级变量——避免多 scheduler / 并发
	// 测试互相改全局态导致 data race。
	probeSDK   func(proxy string, timeoutSeconds float64) map[string]any
	probeFrame func(proxy string, timeoutSeconds float64) map[string]any

	// runMu serializes individual checks: RunOnce skips (TryLock fails) when a
	// check is already in flight — mirrors asyncio.Lock.locked() in run_once.
	runMu sync.Mutex

	// stateMu guards the loop lifecycle fields below.
	stateMu sync.Mutex
	cancel  context.CancelFunc
	done    chan struct{} // closed when the run loop exits
	wg      sync.WaitGroup
}

// NewScheduler returns a Scheduler backed by the given store. The proxy provider
// defaults to a mock returning no candidates (TODO: real proxy pool at T-104).
func NewScheduler(s store.SentinelStore) *Scheduler {
	return &Scheduler{
		store:      s,
		proxys:     mockProxyProvider{},
		probeSDK:   realCheckSDKStatus,
		probeFrame: realFetchLatestVersion,
	}
}

// NewSchedulerWithProxy returns a Scheduler with an injected proxy provider
// (used by tests and, later, by the real proxy pool).
func NewSchedulerWithProxy(s store.SentinelStore, p ProxyProvider) *Scheduler {
	return &Scheduler{
		store:      s,
		proxys:     p,
		probeSDK:   realCheckSDKStatus,
		probeFrame: realFetchLatestVersion,
	}
}

// WithProbes 注入网络探测函数（测试用 mock 不联网；nil 各项保留默认真实探测）。
func (sc *Scheduler) WithProbes(sdk, frame func(proxy string, timeoutSeconds float64) map[string]any) *Scheduler {
	if sdk != nil {
		sc.probeSDK = sdk
	}
	if frame != nil {
		sc.probeFrame = frame
	}
	return sc
}

// GetConfig reads the stored config, falling back to defaults when never saved.
// Mirrors get_config: stored document wins; otherwise DefaultSentinelConfig.
func (sc *Scheduler) GetConfig(ctx context.Context) (model.SentinelConfig, error) {
	cfg, ok, err := sc.store.LoadConfig(ctx)
	if err != nil {
		return model.DefaultSentinelConfig(), err
	}
	if !ok {
		return model.DefaultSentinelConfig(), nil
	}
	return cfg, nil
}

// SaveConfig merges the input over the current config (field presence semantics),
// persists it, and restarts the loop to apply enabled/interval changes.
// Mirrors save_config + _restart_if_needed.
func (sc *Scheduler) SaveConfig(ctx context.Context, in model.SentinelConfigInput) (model.SentinelConfig, error) {
	cfg, err := sc.GetConfig(ctx)
	if err != nil {
		return cfg, err
	}
	if in.Enabled != nil {
		cfg.Enabled = *in.Enabled
	}
	if in.IntervalHours != nil {
		cfg.IntervalHours = clampInt(*in.IntervalHours, minIntervalHours, maxIntervalHours)
	}
	if in.Proxy != nil {
		cfg.Proxy = strings.TrimSpace(*in.Proxy)
	}
	if err := sc.store.SaveConfig(ctx, cfg); err != nil {
		return cfg, err
	}
	sc.restartIfNeeded(ctx)
	return cfg, nil
}

// LatestResult returns the most recent detection result (or nil when none),
// mirroring latest_result.
func (sc *Scheduler) LatestResult(ctx context.Context) (*model.SentinelCheckResult, error) {
	return sc.store.LatestCheck(ctx)
}

// RunOnce performs a single check. It mirrors run_once: when another check is
// already running, it returns skipped=true without waiting.
func (sc *Scheduler) RunOnce(ctx context.Context) (result *model.SentinelCheckResult, skipped bool, err error) {
	if !sc.runMu.TryLock() {
		return nil, true, nil
	}
	defer sc.runMu.Unlock()

	res := sc.checkOnce(ctx)
	if err := sc.store.AppendCheck(ctx, res); err != nil {
		return &res, false, err
	}
	return &res, false, nil
}

// checkOnce assembles the detection result and persists it, mirroring
// _check_once + _persist. The actual network probe is currently a stub.
func (sc *Scheduler) checkOnce(ctx context.Context) model.SentinelCheckResult {
	proxy := sc.resolveProxy(ctx)
	res := model.SentinelCheckResult{
		ConfiguredVersion: model.SentinelVersion,
		Version:           model.SentinelVersion,
		URL:               model.SentinelSDKURL(),
		IsExpired:         nil,
		Reachable:         false,
		ProxyUsed:         proxy != "",
		CheckedAt:         time.Now().UTC(),
		Error:             nil,
		NewestAvailable:   false,
		NewestVersion:     nil,
		DiscoveryMethod:   "frame.html-sv-parse",
		NewestDownloaded:  false,
	}

	status := sc.probeSDK(proxy, probeTimeoutSeconds)
	if code, ok := status["status_code"].(int); ok {
		res.StatusCode = &code
	}
	if reachable, ok := status["reachable"].(bool); ok {
		res.Reachable = reachable
	}
	if etag, ok := status["etag"].(string); ok && etag != "" {
		res.ETag = &etag
	}
	if lm, ok := status["last_modified"].(string); ok && lm != "" {
		res.LastModified = &lm
	}
	if cl, ok := status["content_length"].(int); ok {
		res.ContentLength = &cl
	}
	if ct, ok := status["content_type"].(string); ok && ct != "" {
		res.ContentType = &ct
	}
	if e, ok := status["error"].(string); ok && e != "" {
		res.Error = &e
	}

	// is_expired from status_code (200 -> false, other non-nil -> true).
	if res.StatusCode != nil {
		expired := *res.StatusCode != 200
		res.IsExpired = &expired
		if expired && res.Error == nil {
			msg := "sdk.js 返回 HTTP " + strconv.Itoa(*res.StatusCode) + "，版本可能已过期"
			res.Error = &msg
		}
	}

	// Step-1: discover the online latest version and compare.
	disc := sc.probeFrame(proxy, probeTimeoutSeconds)
	if dv, ok := disc["discovered_version"].(string); ok {
		dv = strings.TrimSpace(dv)
		if dv == "" {
			msg := "frame.html 未能解析版本"
			if de, ok := disc["error"].(string); ok && de != "" {
				msg = de
			}
			res.DiscoveryError = &msg
		} else if !strings.EqualFold(dv, model.SentinelVersion) {
			res.NewestAvailable = true
			res.NewestVersion = &dv
			// Auto-download is not implemented (deferred with the network probe).
			res.NewestDownloaded = false
		}
	} else if de, ok := disc["error"].(string); ok && de != "" {
		res.DiscoveryError = &de
	}

	return res
}

// resolveProxy mirrors _resolve_proxy: configured fixed proxy wins, then the
// first eligible proxy-pool candidate, then direct (empty).
func (sc *Scheduler) resolveProxy(ctx context.Context) string {
	cfg, err := sc.GetConfig(ctx)
	if err == nil && cfg.Proxy != "" {
		return cfg.Proxy
	}
	candidates, err := sc.proxys.AllEligibleProxyCandidates(ctx)
	if err != nil || len(candidates) == 0 {
		return ""
	}
	return model.ProxyURL(candidates[0].Host, candidates[0].Port, candidates[0].Username, candidates[0].Password, candidates[0].Scheme)
}

// Start launches the background check loop (no-op if already running). It
// mirrors start: clears the stop signal and spawns the run task.
func (sc *Scheduler) Start() {
	sc.stateMu.Lock()
	defer sc.stateMu.Unlock()
	if sc.cancel != nil {
		return // already running
	}
	ctx, cancel := context.WithCancel(context.Background())
	sc.cancel = cancel
	sc.done = make(chan struct{})
	sc.wg.Add(1)
	go func() {
		defer sc.wg.Done()
		defer close(sc.done)
		sc.runLoop(ctx)
	}()
}

// Stop cancels the loop and waits for the goroutine to exit. Idempotent.
func (sc *Scheduler) Stop() {
	sc.stateMu.Lock()
	cancel := sc.cancel
	sc.cancel = nil
	done := sc.done
	sc.stateMu.Unlock()

	if cancel != nil {
		cancel()
		<-done
		sc.wg.Wait()
	}
}

// restartIfNeeded mirrors _restart_if_needed: start or stop according to enabled.
func (sc *Scheduler) restartIfNeeded(ctx context.Context) {
	cfg, err := sc.GetConfig(ctx)
	if err != nil {
		cfg = model.DefaultSentinelConfig()
	}
	if cfg.Enabled {
		sc.Start()
	} else {
		sc.Stop()
	}
}

// runLoop runs an immediate check then sleeps per interval until cancelled.
// Mirrors _run: run once immediately, then wait interval; stop exits cleanly.
func (sc *Scheduler) runLoop(ctx context.Context) {
	for {
		// Run immediately, then wait for the interval (or cancellation).
		sc.RunOnce(ctx)

		interval := sc.currentInterval(ctx)
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// currentInterval returns the configured interval as a duration, mirroring
// _current_interval_seconds (hours * 3600).
func (sc *Scheduler) currentInterval(ctx context.Context) time.Duration {
	cfg, err := sc.GetConfig(ctx)
	if err != nil {
		cfg = model.DefaultSentinelConfig()
	}
	h := cfg.IntervalHours
	if h < minIntervalHours {
		h = minIntervalHours
	}
	return time.Duration(h) * time.Hour
}

// clampInt bounds v to [lo, hi], mirroring max(1, min(720, hours)).
func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
