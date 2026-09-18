//go:build v8

package sentinel

// v8_solver 用 robomotionio/v8go（真 V8，cgo）跑 sdk.js 解 Sentinel PoW，
// 替代 goja 解释器与 Python 的 Node 子进程方案。
//
// 关键设计（详见 NOTES.md）：
//   - sdk.js 进程内编译一次，CreateCodeCache 落盘，后续 Isolate 用 CachedData 直编。
//   - Isolate 池：并发安全；每个 Isolate 跑 maxSolvesPerIsolate 次后重建，
//     配合显式 ctx.Close/iso.Dispose 压住 V8 堆外内存（v8go 对象不被 Go GC 回收）。
//   - 无内建事件循环：js/shim.js 提供 FIFO 定时器队列 + __drainTimers，
//     Go 侧 pump 循环交替 PerformMicrotaskCheckpoint 与到期定时器抽干。
//   - 时区：依赖进程级 TZ env（V8/ICU 在 Isolate 创建时读取），
//     进程内只能全局一个时区；跨 script 共享状态一律挂 globalThis.__*。

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	v8 "github.com/robomotionio/v8go"
)

//go:embed js/shim.js
var shimJS string

//go:embed js/flow.js
var flowJS string

const (
	// maxSolvesPerIsolate 是单个 Isolate 复用的求解次数上限，
	// 超过后重建 Isolate+Context（内存治理，见 NOTES.md）。
	maxSolvesPerIsolate = 20

	// pumpDeadline 是单次求解事件循环的墙钟上限。
	pumpDeadline = 30 * time.Second
)

// V8Solver 实现 Solver 接口（并发安全）。
type V8Solver struct {
	sdkSource string // patch 后的 sdk.js 源码
	fetcher   ChallengeFetcher

	// 行为模拟时长（毫秒）；<=0 用 sdk 默认 4200。
	behaviorMs int

	mu     sync.Mutex     // 保护 pool 与 closed
	pool   []*v8Worker    //
	wg     sync.WaitGroup // 跟踪 in-flight 求解（Close 等待）
	closed bool
}

// v8Worker 持有一个 Isolate + 预编译脚本 + 复用次数计数。
//
// 并发不变式（v8go 线程模型：一个 Isolate 同一时间只能被一个线程访问）：
// 一个 worker 被 acquire 出池后即被调用方独占，直到 release 才回到池中——
// 同一 worker 绝不同时属于两个 goroutine。inUse 是该不变式的运行时断言。
type v8Worker struct {
	iso       *v8.Isolate
	script    *v8.UnboundScript
	uses      int         // 仅独占期间（release 内）修改，无竞争
	fromCache bool        // 是否用磁盘 code cache 编译
	inUse     atomic.Bool // 独占断言：acquire 置位 / release 复位
}

// dispose 强制销毁 worker（标记不可复用 + Dispose）。
func (w *v8Worker) dispose() { w.iso.Dispose() }

// V8SolverOption 是构造选项。
type V8SolverOption func(*V8Solver)

// WithV8Fetcher 注入 ChallengeFetcher（生产用 authflow 的 coreChallengeFetcher——与主线
// TLS 会话同源，见 authflow/wire.go；测试用 mock）。
func WithV8Fetcher(f ChallengeFetcher) V8SolverOption {
	return func(s *V8Solver) { s.fetcher = f }
}

// WithV8BehaviorMs 覆盖行为模拟时长（提速/压测用）。
func WithV8BehaviorMs(ms int) V8SolverOption {
	return func(s *V8Solver) { s.behaviorMs = ms }
}

// SetFetcher 运行期替换 ChallengeFetcher（并发安全）。
//
// 用途（authflow 装配）：core.Session 是每次注册才创建的（不能在 NewV8Solver 时定死
// fetcher），故每次注册前用本方法注入「复用当前注册 TLS 会话」的 fetcher——保证
// challenge 请求与 sdk.js 画像三路同源（见 authflow/wire.go）。
func (s *V8Solver) SetFetcher(f ChallengeFetcher) {
	s.mu.Lock()
	s.fetcher = f
	s.mu.Unlock()
}

// NewV8Solver 创建 v8go 求解器（同步预热版）。sdkSource 是原始（未 patch）sdk.js。
//
// 同步预热：编译一次 + 生成 code cache 落盘后才返回。首次无磁盘 cache 时会阻塞
// （V8 全量编译大 bundle，数百 ms~数秒）。服务装配请改用 NewV8SolverAsync（启动不阻塞）；
// 本函数保留给需要「构造即可用」的场景与测试。
func NewV8Solver(sdkSource []byte, opts ...V8SolverOption) (*V8Solver, error) {
	s, err := newV8SolverNoWarmup(sdkSource, opts...)
	if err != nil {
		return nil, err
	}
	// 预热：编译一次 + 生成 code cache 落盘。
	if err := s.warmup(); err != nil {
		return nil, fmt.Errorf("sentinel: v8 预热失败: %w", err)
	}
	return s, nil
}

// NewV8SolverAsync 创建 v8go 求解器（异步预热版，服务装配用）。
//
// 启动不阻塞：立即返回一个【空池】solver，同时后台 goroutine 预编译首个 worker 入池
// （编译结果同时落盘 code cache，加速后续）。首个注册若赶在预热完成前，acquire 会
// 懒编译一个 worker（有 cache 走快路径，无 cache 一次性全量编译）——不报错，仅慢一点。
//
// 返回 (solver, ready)：ready 在后台预热完成（或失败）时关闭，可用于观测/测试等待。
func NewV8SolverAsync(sdkSource []byte, opts ...V8SolverOption) (*V8Solver, <-chan struct{}, error) {
	s, err := newV8SolverNoWarmup(sdkSource, opts...)
	if err != nil {
		return nil, nil, err
	}
	ready := make(chan struct{})
	go func() {
		defer close(ready)
		if err := s.warmup(); err != nil {
			anchorMissLogger.Printf("sentinel: WARN 后台 v8 预热失败（首个注册将懒编译）: %v", err)
		}
	}()
	return s, ready, nil
}

// newV8SolverNoWarmup 构造 solver（不预热，pool 为空）。NewV8Solver/Async 共用。
func newV8SolverNoWarmup(sdkSource []byte, opts ...V8SolverOption) (*V8Solver, error) {
	if len(sdkSource) == 0 {
		return nil, fmt.Errorf("sentinel: sdk.js 源码为空")
	}
	s := &V8Solver{
		sdkSource: patchSDKV8(string(sdkSource)),
	}
	for _, o := range opts {
		o(s)
	}
	return s, nil
}

// patchSDKV8 对 sdk.js 做字符串 patch。
//
// 锚点标识符与 20260810913b 版 bundle 对应（sdk 版本漂移时可能失效，
// 失效时 anchorMiss 告警，见 NOTES.md「SDK 版本漂移」）：
//
//	"var SentinelSDK="                          → 挂 globalThis（必中）
//	"var E=new O;"                              → 暴露实例 __debugP（兜底 getRequirementsToken 用）
//	sessionObserverToken 尾部的 EXPOSE 锚点      → 暴露 __debug_me（保留扩展位）
//	"const se=5e3,"                             → 预取间隔改读 __SENTINEL_INIT_MS（提速；
//	                                              Python 注释：跳过 sdk se=5000 下个token
//	                                              预取定时器空等，无风控影响）
func patchSDKV8(src string) string {
	patches := [][2]string{
		{"var SentinelSDK=", "globalThis.SentinelSDK="},
		{"var E=new O;", "var E=new O;globalThis.__debugP=E;"},
		{
			"return o?r?.[n(5)]?me({so:o,c:r[n(5)]},t):o:null},t.timing=function(){if(ie)throw new Error(Hn(54));return Ae},t.token=je,t}({});",
			"return o?r?.[n(5)]?me({so:o,c:r[n(5)]},t):o:null},t.timing=function(){if(ie)throw new Error(Hn(54));return Ae},t.token=je,t.__debug_me=me,t}({});",
		},
		{"const se=5e3,", "const se=+(globalThis.__SENTINEL_INIT_MS||5e3),"},
	}
	for i, p := range patches {
		if !strings.Contains(src, p[0]) {
			anchorMissLogger.Printf("sentinel: WARN sdk patch anchor #%d 未命中（sdk 版本漂移？）: %.40q", i, p[0])
			continue
		}
		src = replaceOne(src, p[0], p[1])
	}
	return src
}

// warmup 预编译 sdk 并落盘 code cache。
//
// 并发安全：后台预热 goroutine 与首个注册的 acquire 可能并发操作 s.pool，
// 故入池必须持 s.mu（acquire/release 同锁）。编译本身（newWorker）在锁外做，
// 不阻塞并发 acquire 的懒编译。
func (s *V8Solver) warmup() error {
	w, err := s.newWorker()
	if err != nil {
		return err
	}
	// 若本次是全量编译（无有效磁盘缓存），生成并落盘 cache。
	if !w.fromCache {
		cache := w.script.CreateCodeCache()
		if cache != nil && len(cache.Bytes) > 0 {
			_ = saveCodeCache(Version, s.sdkSource, cache.Bytes)
		}
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		w.iso.Dispose() // 预热期间服务关停：丢弃刚编译的 worker，防 Isolate 泄漏
		return nil
	}
	s.pool = append(s.pool, w)
	s.mu.Unlock()
	return nil
}

// newWorker 新建 Isolate + 编译脚本（优先磁盘 cache）。
func (s *V8Solver) newWorker() (*v8Worker, error) {
	cached, _ := loadCodeCache(Version, s.sdkSource)
	iso := v8.NewIsolate()
	if cached != nil {
		script, err := iso.CompileUnboundScript(s.sdkSource, "sdk.js", v8.CompileOptions{CachedData: cached})
		if err == nil && !cached.Rejected {
			return &v8Worker{iso: iso, script: script, fromCache: true}, nil
		}
		// cache 被拒/编译失败：删除坏文件（避免下次继续走慢路径），回退全量编译。
		removeCodeCache(Version)
	}
	script, err := iso.CompileUnboundScript(s.sdkSource, "sdk.js", v8.CompileOptions{})
	if err != nil {
		iso.Dispose()
		return nil, fmt.Errorf("编译 sdk.js 失败: %w", err)
	}
	return &v8Worker{iso: iso, script: script, fromCache: false}, nil
}

// acquire 取一个可用 worker（不足则新建）。
// 返回的 worker 被调用方独占（inUse 断言），直到配对的 release 调用。
func (s *V8Solver) acquire() (*v8Worker, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, fmt.Errorf("sentinel: solver 已关闭")
	}
	var w *v8Worker
	if n := len(s.pool); n > 0 {
		w = s.pool[n-1]
		s.pool = s.pool[:n-1]
	} else {
		var err error
		w, err = s.newWorker()
		if err != nil {
			return nil, err
		}
	}
	s.wg.Add(1)
	if !w.inUse.CompareAndSwap(false, true) {
		panic("sentinel: BUG worker 被并发持有（并发不变式破坏）")
	}
	return w, nil
}

// release 归还 worker；超过复用上限或标记不可复用则销毁。
// 只能在配对的 acquire 之后调用一次（worker 独占期间）。
func (s *V8Solver) release(w *v8Worker) {
	defer s.wg.Done()
	if !w.inUse.CompareAndSwap(true, false) {
		panic("sentinel: BUG release 了未独占的 worker")
	}
	w.uses++
	// TerminateExecution 过的 worker（watchdog 会把 uses 顶到上限）不可复用。
	if w.uses >= maxSolvesPerIsolate {
		w.dispose()
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		w.dispose()
		return
	}
	s.pool = append(s.pool, w)
	s.mu.Unlock()
}

// GetToken 求解 Sentinel token（三阶段：requirements → challenge → solve）。
func (s *V8Solver) GetToken(ctx context.Context, env EnvPayload, flow Flow) (*Result, error) {
	// 0) 入口校验：env 关键字段为零值会导致所有账号指纹趋同（风控聚类风险），
	//    必须 fail-fast，不能在 shim 里静默兜底成默认值。
	if err := validateEnv(env); err != nil {
		return nil, &ErrSDK{Stage: "requirements", Err: err}
	}

	// 1) requirements：拿 request_p。
	requestP, err := s.runStage(ctx, env, flow, "requirements", nil, "")
	if err != nil {
		return nil, &ErrSDK{Stage: "requirements", Err: err}
	}
	if requestP == "" {
		return nil, &ErrSDK{Stage: "requirements", Err: fmt.Errorf("request_p 为空")}
	}

	// 2) challenge：用主线会话发 /sentinel/req。
	//    fetcher 取局部拷贝（与 SetFetcher 并发安全：注册前注入的 core 会话 fetcher）。
	s.mu.Lock()
	fetcher := s.fetcher
	s.mu.Unlock()
	if fetcher == nil {
		return nil, &ErrSDK{Stage: "challenge", Err: fmt.Errorf("challenge fetcher 未配置")}
	}
	challenge, err := fetcher(ctx, env, flow, requestP)
	if err != nil {
		return nil, &ErrSDK{Stage: "challenge", Err: err}
	}
	// 残缺 challenge 注入沙箱会产出"结构正常但服务端必拒"的 token，
	// 错误延迟到服务端才暴露——必须在 challenge 阶段就拦住（对齐 goja 版的校验）。
	if challenge == nil || challenge.Token == "" {
		return nil, &ErrSDK{Stage: "challenge", Err: fmt.Errorf("challenge token 为空")}
	}

	// 3) solve：注入 challenge 跑 token() + sessionObserverToken()。
	token, soToken, err := s.runSolve(ctx, env, flow, requestP, challenge)
	if err != nil {
		return nil, &ErrSDK{Stage: "solve", Err: err}
	}
	if challenge.SoRequired() && soToken == "" {
		return nil, &ErrSDK{Stage: "solve", Err: fmt.Errorf("服务端要求 SO token 但求解结果为空")}
	}
	return &Result{Token: token, SOToken: soToken}, nil
}

// validateEnv 校验 EnvPayload 关键字段（零值会导致指纹趋同，fail-fast）。
func validateEnv(env EnvPayload) error {
	if env.UserAgent == "" {
		return fmt.Errorf("env.UserAgent 为空")
	}
	if env.ScreenWidth <= 0 || env.ScreenHeight <= 0 {
		return fmt.Errorf("env.Screen 尺寸非法: %dx%d", env.ScreenWidth, env.ScreenHeight)
	}
	if env.DeviceID == "" {
		return fmt.Errorf("env.DeviceID 为空")
	}
	return nil
}

// runStage 跑一个纯 JS 阶段（requirements）。返回 request_p。
func (s *V8Solver) runStage(ctx context.Context, env EnvPayload, flow Flow, action string, challenge *Challenge, requestP string) (string, error) {
	out, err := s.execute(ctx, env, flow, action, challenge, requestP)
	if err != nil {
		return "", err
	}
	if e, ok := out["error"]; ok {
		return "", fmt.Errorf("%v", e)
	}
	rp, _ := out["request_p"].(string)
	return rp, nil
}

// runSolve 跑 solve 阶段，返回 (token, soToken)。
func (s *V8Solver) runSolve(ctx context.Context, env EnvPayload, flow Flow, requestP string, challenge *Challenge) (string, string, error) {
	out, err := s.execute(ctx, env, flow, "solve", challenge, requestP)
	if err != nil {
		return "", "", err
	}
	if e, ok := out["error"]; ok {
		return "", "", fmt.Errorf("%v", e)
	}
	token, _ := out["token"].(string)
	soToken, _ := out["so_token"].(string)
	// 兜底路径：token() 不可用时返回 final_p（对齐 Python，上层按需使用）。
	if token == "" {
		if fp, ok := out["final_p"].(string); ok && fp != "" {
			token = fp
		}
	}
	if token == "" {
		return "", "", fmt.Errorf("solve 未产出 token/final_p")
	}
	return token, soToken, nil
}

// execute 在一个 worker 上跑完整的 shim → sdk → flow → pump 流程。
func (s *V8Solver) execute(ctx context.Context, env EnvPayload, flow Flow, action string, challenge *Challenge, requestP string) (map[string]any, error) {
	w, err := s.acquire()
	if err != nil {
		return nil, err
	}
	defer s.release(w)

	// 每次求解新建 Context（轻量；Isolate 复用，Context 用完即关防泄漏）。
	vctx := v8.NewContext(w.iso)
	defer vctx.Close()

	// watchdog：ctx 取消时强制中断该 Isolate 的 JS 执行。
	// RunScript/pump 是同步 cgo 调用，sdk 同步死循环时 pump 的 ctx 检查进不去——
	// 只有另一个 goroutine 调 TerminateExecution 才能打断。
	// 注意：TerminateExecution 后该 Isolate 基本残废，release 时会随 uses 上限兜底销毁；
	// 这里立即标记不可复用（提前到上限），避免下一个求解拿到残废 Isolate。
	stop := context.AfterFunc(ctx, func() {
		w.iso.TerminateExecution()
		w.uses = maxSolvesPerIsolate // 标记不可复用（release 时销毁）
	})
	defer stop()

	// 组装 input（对齐 Python _sentinel_fp_kwargs + cjs input）。
	input := buildInput(env, flow, action, challenge, requestP, s.behaviorMs)
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}

	run := func(src, name string) error {
		if _, err := vctx.RunScript(src, name); err != nil {
			if je, ok := err.(*v8.JSError); ok {
				return fmt.Errorf("%s: %s\n%s", name, je.Message, je.StackTrace)
			}
			return fmt.Errorf("%s: %w", name, err)
		}
		return nil
	}

	if err := run("globalThis.__input = "+string(inputJSON)+";", "input.js"); err != nil {
		return nil, err
	}
	if err := run(shimJS, "shim.js"); err != nil {
		return nil, err
	}
	if _, err := w.script.Run(vctx); err != nil {
		return nil, fmt.Errorf("sdk.js 执行失败: %w", err)
	}
	if err := run(flowJS, "flow.js"); err != nil {
		return nil, err
	}

	// 事件循环泵：微任务 + 定时器交叉抽干，直到 __output 就绪 / 超时 / ctx 取消。
	// behaviorMs 传入用于识别并跳过 sdk prefetch 长定时器（见 pump）。
	if err := pump(ctx, vctx, s.behaviorMs); err != nil {
		return nil, err
	}

	outVal, err := vctx.RunScript("JSON.stringify(globalThis.__output || null)", "out.js")
	if err != nil {
		return nil, err
	}
	if outVal.String() == "null" {
		return nil, fmt.Errorf("事件循环结束但无输出（promise 链断裂）")
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(outVal.String()), &out); err != nil {
		return nil, fmt.Errorf("解析输出失败: %w", err)
	}
	return out, nil
}

// pump 驱动 v8 事件循环直到 __output 就绪。
//
// 每轮：PerformMicrotaskCheckpoint（抽干微任务）→ __drainTimers（执行到期定时器）。
// 退出条件：
//  1. __output 就绪（正常路径）
//  2. 连续 idleRoundsToExit 轮「无定时器且无微任务产出」——真空闲兜底
//  3. 墙钟 pumpDeadline（防 sdk 死循环/超长等待）
//  4. ctx 取消（watchdog 会 TerminateExecution 打断同步阻塞）
//
// 关于 sdk prefetch 长定时器：sdk 在 token 算出后会注册一个 se(默认 5s) 的
// 「下个 token 预取」定时器。它的回调只刷新缓存的 proof，对本次 token 无影响
// （Python 注释：输出后立即退出，跳过该空等，无风控影响）。但我们的事件循环
// 模型下它会和行为模拟的短 sleep 在同一个 timer 队列里互相推后空转（实测
// behavior=50ms 的 solve 从 2.6s 拖到 7.1s）。因此 pump 每轮清掉延迟超过
// skipLongTimerAfterMs 的定时器——保留行为模拟的短 sleep，跳过 prefetch 空等。
func pump(ctx context.Context, vctx *v8.Context, behaviorMs int) error {
	deadline := time.Now().Add(pumpDeadline)
	const idleRoundsToExit = 50 // 保守阈值：50 轮 × ~2ms ≈ 100ms 真空闲才判定结束
	// 长定时器阈值：behavior 时长 + 余量。behavior 的单次 sleep 最长 ~220ms，
	// 任何超过该阈值的定时器都是 sdk 的 prefetch/超时兜底，可安全跳过。
	skipAfterMs := int64(behaviorMs) + 3000
	if skipAfterMs < 3000 {
		skipAfterMs = 3000
	}
	idle := 0
	for round := 0; ; round++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("事件循环超时（%v）", pumpDeadline)
		}
		vctx.PerformMicrotaskCheckpoint()
		st, err := vctx.RunScript(fmt.Sprintf(`(() => {
			if (globalThis.__output) return {ready:true};
			// 跳过 sdk prefetch 等长定时器（对本次 token 无影响的空等）
			const __now = Date.now();
			for (const t of globalThis.__timers) { if (t.at - __now > %d) t.cleared = true; }
			const r = globalThis.__drainTimers(__now);
			return {ready:false, pending:!r.done, nextDelayMs:r.nextDelayMs, ran:r.ran};
		})()`, skipAfterMs), "pump.js")
		if err != nil {
			return err
		}
		obj := st.Object()
		ready, _ := obj.Get("ready")
		if ready.Boolean() {
			return nil
		}
		pending, _ := obj.Get("pending")
		ran, _ := obj.Get("ran")
		if !pending.Boolean() && ran.Integer() == 0 {
			idle++
			if idle >= idleRoundsToExit {
				return nil // 真空闲：无更多任务
			}
			time.Sleep(2 * time.Millisecond)
			continue
		}
		idle = 0
		delayV, _ := obj.Get("nextDelayMs")
		delay := time.Duration(delayV.Integer()) * time.Millisecond
		if delay < 0 {
			delay = 0
		}
		if delay > 2*time.Second {
			delay = 2 * time.Second
		}
		if delay > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
	}
}

// buildInput 组装注入 JS 沙箱的 input（对齐 Python cjs 的 input 结构）。
func buildInput(env EnvPayload, flow Flow, action string, challenge *Challenge, requestP string, behaviorMs int) map[string]any {
	in := map[string]any{
		"action":               action,
		"flow":                 string(flow),
		"timezone":             env.Timezone,
		"device_id":            env.DeviceID,
		"user_agent":           env.UserAgent,
		"screen_width":         env.ScreenWidth,
		"screen_height":        env.ScreenHeight,
		"language":             env.Language,
		"languages":            env.Languages,
		"platform":             env.Platform,
		"vendor":               env.Vendor,
		"hardware_concurrency": env.HardwareConcurrency,
		"device_pixel_ratio":   env.DevicePixelRatio,
		"max_touch_points":     env.MaxTouchPoints,
	}
	if env.DeviceMemory != nil {
		in["device_memory"] = *env.DeviceMemory
	}
	if action == "solve" {
		if behaviorMs > 0 {
			in["behavior_duration_ms"] = behaviorMs
		}
		in["request_p"] = requestP
		if challenge != nil && len(challenge.Raw) > 0 {
			var raw any
			if json.Unmarshal(challenge.Raw, &raw) == nil {
				in["challenge"] = raw
			}
		}
		if _, ok := in["challenge"]; !ok && challenge != nil {
			// 无 Raw 时按结构体序列化（测试桩路径）。
			in["challenge"] = map[string]any{
				"token":       challenge.Token,
				"proofofwork": challenge.ProofOfWork,
				"so":          challenge.So,
				"turnstile":   challenge.Turnstile,
			}
		}
	}
	return in
}

// Close 释放全部 Isolate。等待所有 in-flight 求解完成后再返回。
// Close 后 acquire 返回"solver 已关闭"错误；幂等。
func (s *V8Solver) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	pool := s.pool
	s.pool = nil
	s.mu.Unlock()

	// 先销毁池中存货，再等 in-flight 的 release 把它们各自的 worker 销毁。
	for _, w := range pool {
		w.dispose()
	}
	s.wg.Wait()
	return nil
}
