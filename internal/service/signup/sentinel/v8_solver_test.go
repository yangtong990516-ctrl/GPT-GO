//go:build v8

package sentinel

// v8go 实现的验收测试（对应交接文档 §6）。
//
// 运行方式：
//   GOTOOLCHAIN=auto CGO_ENABLED=1 go test -tags v8 ./internal/service/signup/sentinel/ -run TestV8 -v
//
// 前置：本机已拉取预编译 libv8.a（见 NOTES.md「构建」）。

import (
	"context"
	"encoding/json"
	"strings"
	"syscall"
	"testing"
	"time"
)

// testEnv 返回一份完整 EnvPayload（对齐 Python _sentinel_fp_kwargs 字段）。
func testEnv() EnvPayload {
	dm := 8
	return EnvPayload{
		DeviceID:            "test-device-00000000-0000-0000-0000-000000000000",
		UserAgent:           DefaultUA,
		ScreenWidth:         1920,
		ScreenHeight:        1080,
		Language:            "en-US",
		Languages:           []string{"en-US", "en"},
		Platform:            "Win32",
		Vendor:              "Google Inc.",
		HardwareConcurrency: 8,
		DevicePixelRatio:    1.0,
		MaxTouchPoints:      0,
		Timezone:            "America/New_York",
		DeviceMemory:        &dm,
	}
}

func newTestSolver(t *testing.T, fetcher ChallengeFetcher) *V8Solver {
	t.Helper()
	src, err := SDKSource(context.Background())
	if err != nil {
		t.Fatalf("加载 sdk.js 失败: %v", err)
	}
	s, err := NewV8Solver(src,
		WithV8Fetcher(fetcher),
		WithV8BehaviorMs(50), // 压测：行为模拟缩到 50ms
	)
	if err != nil {
		t.Fatalf("NewV8Solver 失败: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestV8Requirements_ReturnsRequestP 验收 #1：requirements 返回非空 request_p。
func TestV8Requirements_ReturnsRequestP(t *testing.T) {
	s := newTestSolver(t, nil)
	out, err := s.execute(context.Background(), testEnv(), FlowAuthorizeContinue, "requirements", nil, "")
	if err != nil {
		t.Fatalf("requirements 执行失败: %v", err)
	}
	if e, ok := out["error"]; ok {
		t.Fatalf("flow 报错: %v", e)
	}
	rp, _ := out["request_p"].(string)
	if rp == "" {
		t.Fatal("request_p 为空")
	}
	if !strings.HasPrefix(rp, "gAAAAAC") {
		t.Errorf("request_p 前缀应为 gAAAAAC: %.30s", rp)
	}
	t.Logf("request_p: %d chars, 前缀 %.20s", len(rp), rp)
}

// TestV8Solve_ReturnsToken 验收 #2：solve 阶段注入桩 challenge 返回 token。
func TestV8Solve_ReturnsToken(t *testing.T) {
	fetcher := func(ctx context.Context, env EnvPayload, flow Flow, requestP string) (*Challenge, error) {
		raw := json.RawMessage(`{"token":"stub-challenge-token","proofofwork":{"required":true,"seed":"stub-seed","difficulty":1},"so":{"required":false,"collector_dx":""}}`)
		var c Challenge
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, err
		}
		c.Raw = raw
		return &c, nil
	}
	s := newTestSolver(t, fetcher)
	res, err := s.GetToken(context.Background(), testEnv(), FlowAuthorizeContinue)
	if err != nil {
		t.Fatalf("GetToken 失败: %v", err)
	}
	if res.Token == "" {
		t.Fatal("token 为空")
	}
	// solve 的 token 是 JSON 串，含 challenge token 与 flow。
	if !strings.Contains(res.Token, "stub-challenge-token") {
		t.Errorf("token 应包含 challenge token: %.80s", res.Token)
	}
	t.Logf("token: %.80s | so_token: %q", res.Token, res.SOToken)
}

// TestV8SoTokenOptional_WhenServerNotRequire 验收 #2b：so.required=false 时 soToken="" 且不报错。
func TestV8SoTokenOptional_WhenServerNotRequire(t *testing.T) {
	fetcher := func(ctx context.Context, env EnvPayload, flow Flow, requestP string) (*Challenge, error) {
		raw := json.RawMessage(`{"token":"stub-challenge-token","proofofwork":{"required":true,"seed":"s","difficulty":1},"so":{"required":false}}`)
		var c Challenge
		_ = json.Unmarshal(raw, &c)
		c.Raw = raw
		return &c, nil
	}
	s := newTestSolver(t, fetcher)
	res, err := s.GetToken(context.Background(), testEnv(), FlowAuthorizeContinue)
	if err != nil {
		t.Fatalf("so 未要求时不应报错: %v", err)
	}
	if res.SOToken != "" {
		t.Errorf("so.required=false 时 soToken 应为空串，得到 %q", res.SOToken)
	}
}

// TestV8CodeCache_SkipsReparse 验收 #4：第二次编译命中缓存且不被拒绝。
func TestV8CodeCache_SkipsReparse(t *testing.T) {
	src, err := SDKSource(context.Background())
	if err != nil {
		t.Fatalf("加载 sdk.js 失败: %v", err)
	}
	patched := patchSDKV8(string(src))

	// 清掉磁盘缓存，第一次编译生成 cache。
	dir, _ := codeCacheDir(Version)
	if dir != "" {
		_ = removeFile(dir + "/sdk.v8cache")
	}

	iso1 := newIsolate()
	defer iso1.Dispose()
	t0 := time.Now()
	script1, err := iso1.CompileUnboundScript(patched, "sdk.js", compileOptsEmpty())
	if err != nil {
		t.Fatalf("compile#1 失败: %v", err)
	}
	cold := time.Since(t0)
	cache := script1.CreateCodeCache()
	if cache == nil || len(cache.Bytes) == 0 {
		t.Fatal("CreateCodeCache 返回空")
	}

	iso2 := newIsolate()
	defer iso2.Dispose()
	t0 = time.Now()
	if _, err := iso2.CompileUnboundScript(patched, "sdk.js", compileOptsCached(cache)); err != nil {
		t.Fatalf("compile#2 失败: %v", err)
	}
	warm := time.Since(t0)

	if cache.Rejected {
		t.Error("第二次编译 cache 被拒绝（Rejected=true）")
	}
	t.Logf("cold=%v warm=%v (%.1fx) cache=%d bytes rejected=%v",
		cold, warm, float64(cold)/float64(warm), len(cache.Bytes), cache.Rejected)
}

// TestV8NoLeak_RepeatedSolve 验收 #5：连续 N 次求解，RSS 增长有界。
func TestV8NoLeak_RepeatedSolve(t *testing.T) {
	if testing.Short() {
		t.Skip("short 模式跳过泄漏压测")
	}
	const N = 200
	const rssGrowthLimitMB = 50.0

	s := newTestSolver(t, nil)
	env := testEnv()

	base := rssMB(t)
	t0 := time.Now()
	for i := 0; i < N; i++ {
		out, err := s.execute(context.Background(), env, FlowAuthorizeContinue, "requirements", nil, "")
		if err != nil {
			t.Fatalf("iter %d 失败: %v", i, err)
		}
		rp, _ := out["request_p"].(string)
		if rp == "" {
			t.Fatalf("iter %d request_p 为空", i)
		}
		if i%50 == 0 {
			t.Logf("iter %3d | rss %.1f MB | elapsed %v", i, rssMB(t), time.Since(t0).Round(time.Millisecond))
		}
	}
	growth := rssMB(t) - base
	t.Logf("DONE %d iters in %v | rss growth %.1f MB", N, time.Since(t0).Round(time.Millisecond), growth)
	if growth > rssGrowthLimitMB {
		t.Errorf("RSS 增长 %.1f MB 超过阈值 %.1f MB（疑似泄漏）", growth, rssGrowthLimitMB)
	}
}

// TestV8Timezone 验收 #3：注入时区后沙箱内 Intl 与 Date 一致。
func TestV8Timezone(t *testing.T) {
	// 注意：Date.toString() 的时区由进程级 TZ env 决定（V8/ICU 在 Isolate 创建时读取）。
	// 本测试进程未设 TZ 时只验证 Intl patch 生效；CI 可设 TZ=America/New_York 跑全量。
	s := newTestSolver(t, nil)
	w, err := s.acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer s.release(w)
	vctx := newV8Context(w.iso)
	defer vctx.Close()

	in := buildInput(testEnv(), FlowAuthorizeContinue, "requirements", nil, "", 0)
	inJSON, _ := json.Marshal(in)
	mustRun(t, vctx, "globalThis.__input = "+string(inJSON)+";", "input.js")
	mustRun(t, vctx, shimJS, "shim.js")

	v, err := vctx.RunScript(`Intl.DateTimeFormat().resolvedOptions().timeZone`, "tz.js")
	if err != nil {
		t.Fatal(err)
	}
	if v.String() != "America/New_York" {
		t.Errorf("Intl resolvedOptions().timeZone = %q, want America/New_York", v.String())
	}
	t.Logf("Intl timeZone=%s | Date=%s", v, mustRunVal(t, vctx, `new Date().toString()`, "d.js"))
}

// ─── 测试辅助 ───

func rssMB(t *testing.T) float64 {
	t.Helper()
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		t.Fatalf("getrusage: %v", err)
	}
	return float64(ru.Maxrss) / 1024 / 1024
}
