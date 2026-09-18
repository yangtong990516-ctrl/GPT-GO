package sentinel

import (
	"context"
	"testing"
	"time"

	"gpt-go/internal/model"
	"gpt-go/internal/store"
)

// TestGetConfigDefault mirrors get_config returning defaults when never saved.
func TestGetConfigDefault(t *testing.T) {
	s := NewScheduler(store.NewMockSentinelStore())
	cfg, err := s.GetConfig(context.Background())
	if err != nil {
		t.Fatalf("GetConfig error: %v", err)
	}
	if !cfg.Enabled {
		t.Errorf("Enabled = %v, want true", cfg.Enabled)
	}
	if cfg.IntervalHours != 24 {
		t.Errorf("IntervalHours = %d, want 24", cfg.IntervalHours)
	}
	if cfg.Proxy != "" {
		t.Errorf("Proxy = %q, want empty", cfg.Proxy)
	}
}

// TestSaveConfig mirrors save_config: field-presence merge + interval clamp.
func TestSaveConfig(t *testing.T) {
	s := NewScheduler(store.NewMockSentinelStore())

	enabled := false
	hours := 9999 // clamp to 720
	proxy := "  http://127.0.0.1:7890  "
	cfg, err := s.SaveConfig(context.Background(), model.SentinelConfigInput{
		Enabled:       &enabled,
		IntervalHours: &hours,
		Proxy:         &proxy,
	})
	if err != nil {
		t.Fatalf("SaveConfig error: %v", err)
	}
	if cfg.Enabled {
		t.Errorf("Enabled = true, want false")
	}
	if cfg.IntervalHours != 720 {
		t.Errorf("IntervalHours = %d, want 720 (clamped)", cfg.IntervalHours)
	}
	if cfg.Proxy != "http://127.0.0.1:7890" {
		t.Errorf("Proxy = %q, want stripped", cfg.Proxy)
	}

	// Reload: stored config wins over defaults.
	cfg2, err := s.GetConfig(context.Background())
	if err != nil {
		t.Fatalf("GetConfig error: %v", err)
	}
	if cfg2.Enabled || cfg2.IntervalHours != 720 || cfg2.Proxy != "http://127.0.0.1:7890" {
		t.Errorf("reload mismatch: %+v", cfg2)
	}
}

// TestSaveConfigClampLow verifies interval clamps up to 1.
func TestSaveConfigClampLow(t *testing.T) {
	s := NewScheduler(store.NewMockSentinelStore())
	hours := 0
	cfg, err := s.SaveConfig(context.Background(), model.SentinelConfigInput{IntervalHours: &hours})
	if err != nil {
		t.Fatalf("SaveConfig error: %v", err)
	}
	if cfg.IntervalHours != 1 {
		t.Errorf("IntervalHours = %d, want 1", cfg.IntervalHours)
	}
}

// newSchedulerWithProbes 造一个注入 mock 探测（不联网）的 Scheduler。
// 探测函数做成实例字段（WithProbes），避免改全局态导致并发测试 data race。
func newSchedulerWithProbes(sdk, frame map[string]any) *Scheduler {
	return NewScheduler(store.NewMockSentinelStore()).WithProbes(
		func(string, float64) map[string]any { return sdk },
		func(string, float64) map[string]any { return frame },
	)
}

// TestRunOncePersists mirrors run_once: performs a check and stores a result.
// 探测函数注入 mock（不联网），验证 checkOnce 的状态码映射与持久化。
func TestRunOncePersists(t *testing.T) {
	s := newSchedulerWithProbes(
		map[string]any{"status_code": 200, "reachable": true, "etag": "\"abc\"", "content_length": 12345, "content_type": "application/javascript"},
		map[string]any{"discovered_version": model.SentinelVersion, "reachable": true, "status_code": 200},
	)
	res, skipped, err := s.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}
	if skipped {
		t.Fatalf("RunOnce skipped unexpectedly")
	}
	if res.ConfiguredVersion != model.SentinelVersion {
		t.Errorf("ConfiguredVersion = %q, want %q", res.ConfiguredVersion, model.SentinelVersion)
	}
	// mock 探测：reachable=true,status 200 → is_expired=false。
	if !res.Reachable {
		t.Errorf("Reachable = false, want true (mock)")
	}
	if res.StatusCode == nil || *res.StatusCode != 200 {
		t.Errorf("StatusCode = %v, want 200", res.StatusCode)
	}
	if res.IsExpired == nil || *res.IsExpired {
		t.Errorf("IsExpired = %v, want false (200)", res.IsExpired)
	}
	if res.ETag == nil || *res.ETag != "\"abc\"" {
		t.Errorf("ETag = %v, want \"abc\"", res.ETag)
	}
	if res.NewestAvailable {
		t.Errorf("NewestAvailable = true, want false（同版本不提示更新）")
	}

	latest, err := s.LatestResult(context.Background())
	if err != nil {
		t.Fatalf("LatestResult error: %v", err)
	}
	if latest == nil {
		t.Fatalf("LatestResult = nil, want a result")
	}
	if latest.ConfiguredVersion != model.SentinelVersion {
		t.Errorf("latest ConfiguredVersion = %q", latest.ConfiguredVersion)
	}
}

// TestStoreTrimKeepsLatest verifies AppendCheck trims to the latest 50.
func TestStoreTrimKeepsLatest(t *testing.T) {
	st := store.NewMockSentinelStore()
	ctx := context.Background()
	base := time.Now().UTC()
	for i := 0; i < 60; i++ {
		r := model.SentinelCheckResult{ConfiguredVersion: model.SentinelVersion}
		r.CheckedAt = base.Add(time.Duration(i) * time.Second)
		_ = st.AppendCheck(ctx, r)
	}
	latest, err := st.LatestCheck(ctx)
	if err != nil {
		t.Fatalf("LatestCheck error: %v", err)
	}
	if latest == nil {
		t.Fatalf("LatestCheck = nil")
	}
	// The newest (i=59) should survive.
	if latest.CheckedAt != base.Add(59*time.Second) {
		t.Errorf("latest CheckedAt = %v, want i=59", latest.CheckedAt)
	}
}

// TestStartStopLifecycle verifies the scheduler loop starts and stops cleanly.
func TestStartStopLifecycle(t *testing.T) {
	s := NewScheduler(store.NewMockSentinelStore())
	s.Start()
	s.Start() // idempotent: no-op when already running
	s.Stop()
	s.Stop() // idempotent
}

// TestRestartOnDisable mirrors _restart_if_needed: enabled=false stops the loop.
func TestSaveConfigDisableStops(t *testing.T) {
	s := NewScheduler(store.NewMockSentinelStore())
	s.Start()
	enabled := false
	if _, err := s.SaveConfig(context.Background(), model.SentinelConfigInput{Enabled: &enabled}); err != nil {
		t.Fatalf("SaveConfig error: %v", err)
	}
	// After disabling, the loop is stopped (Stop called); a subsequent Start
	// would re-enable. No panic and Stop is idempotent.
	s.Stop()
}

// TestCheckOnceExpired 验证：sdk.js 非 200 → is_expired=true + 错误信息。
func TestCheckOnceExpired(t *testing.T) {
	s := newSchedulerWithProbes(
		map[string]any{"status_code": 404, "reachable": true},
		map[string]any{"discovered_version": model.SentinelVersion, "reachable": true, "status_code": 200},
	)
	res, skipped, err := s.RunOnce(context.Background())
	if err != nil || skipped {
		t.Fatalf("RunOnce err=%v skipped=%v", err, skipped)
	}
	if res.IsExpired == nil || !*res.IsExpired {
		t.Errorf("IsExpired = %v, want true (404)", res.IsExpired)
	}
	if res.Error == nil {
		t.Error("Error 应有版本过期提示")
	}
}

// TestCheckOnceNewVersionDiscovered 验证：frame.html 解析到不同版本 → NewestAvailable。
func TestCheckOnceNewVersionDiscovered(t *testing.T) {
	s := newSchedulerWithProbes(
		map[string]any{"status_code": 200, "reachable": true},
		map[string]any{"discovered_version": "99999999zzzz", "reachable": true, "status_code": 200},
	)
	res, skipped, err := s.RunOnce(context.Background())
	if err != nil || skipped {
		t.Fatalf("RunOnce err=%v skipped=%v", err, skipped)
	}
	if !res.NewestAvailable {
		t.Error("NewestAvailable = false, want true（发现新版本）")
	}
	if res.NewestVersion == nil || *res.NewestVersion != "99999999zzzz" {
		t.Errorf("NewestVersion = %v, want 99999999zzzz", res.NewestVersion)
	}
}

// TestCheckOnceProbeUnreachable 验证：网络失败 → reachable=false + error（不崩溃）。
func TestCheckOnceProbeUnreachable(t *testing.T) {
	s := newSchedulerWithProbes(
		map[string]any{"status_code": nil, "reachable": false, "error": "请求 sdk.js 失败: timeout"},
		map[string]any{"reachable": false, "error": "请求 frame.html 失败: timeout"},
	)
	res, skipped, err := s.RunOnce(context.Background())
	if err != nil || skipped {
		t.Fatalf("RunOnce err=%v skipped=%v", err, skipped)
	}
	if res.Reachable {
		t.Error("Reachable = true, want false（网络失败）")
	}
	if res.Error == nil {
		t.Error("Error 应有网络错误信息")
	}
	if res.DiscoveryError == nil {
		t.Error("DiscoveryError 应有 frame.html 错误信息")
	}
	// 网络失败时 IsExpired 应保持 nil（无法判定）。
	if res.IsExpired != nil {
		t.Errorf("IsExpired = %v, want nil（status 未知不判定）", res.IsExpired)
	}
}

// TestFrameVersionRegex 验证 frame.html sv= 版本解析正则（真实探测的提取逻辑）。
func TestFrameVersionRegex(t *testing.T) {
	cases := []struct {
		body string
		want string
		ok   bool
	}{
		{`<script src="/backend-api/sentinel/frame.html?sv=20260810913b"></script>`, "20260810913b", true},
		{`window.location="frame.html?sv=abc123ef"`, "abc123ef", true},
		{`no version here`, "", false},
		{`?sv=`, "", false},
	}
	for _, c := range cases {
		m := frameVersionRe.FindStringSubmatch(c.body)
		if c.ok {
			if len(m) != 2 || m[1] != c.want {
				t.Errorf("body=%q 应解析 %q, got %v", c.body, c.want, m)
			}
		} else if len(m) == 2 {
			t.Errorf("body=%q 不应解析出版本, got %q", c.body, m[1])
		}
	}
}
