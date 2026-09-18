// Package humanize 人类行为节奏模拟 —— 让协议请求的时序更像真实浏览器操作。
//
// 对齐 codex-auto protocol_signup/humanize.py（这是注册成功率的命脉之一）：
// 真实浏览器注册时，页面导航 / XHR / 输入 / 点击之间存在人类固有的、不可精确预测的
// 延迟，且分布符合对数正态（反应时间分布）。纯协议链路若"连发"（请求间几乎零间隔），
// 会在时序特征上被风控一眼识别为脚本——尤其高并发下，OTP 秒取秒提交被服务端作废，
// 表现为 wrong_email_otp_code。
//
// 两层：
//  1. Pause(url)      —— 传输层：按 URL 域名/路径在每次请求前插入随机延迟。
//  2. ActionPause(kind) —— 动作层：关键人类动作（收验证码后输入、填表单等）之间插入
//     更长的、动作语义明确的停顿。
package humanize

import (
	"fmt"
	"math"
	"math/rand"
	"strings"
	"time"
)

// ── 传输层节奏档位（毫秒）：(mean, sigma, minJitter, maxJitter) ────────────
// 对齐 Python _NAV/_XHR/_FAST（单位换算 s→ms）。
var (
	navBucket  = rhythmBucket{900, 450, 300, 600}
	xhrBucket  = rhythmBucket{450, 250, 150, 300}
	fastBucket = rhythmBucket{180, 100, 50, 150}
)

type rhythmBucket struct{ mean, sigma, minJitter, maxJitter float64 }

// navMarkers 整页导航类 URL（较长延迟）。
var navMarkers = []string{
	"/authorize", "/email-otp/send", "/passwordless/send-otp", "/signin/openai",
	"/create-account", "/email-verification", "auth.openai.com/login",
}

// xhrMarkers XHR/fetch 类 URL（中等延迟）。
var xhrMarkers = []string{
	"/api/accounts/authorize/continue", "/api/accounts/user/register",
	"/api/accounts/create_account", "/api/accounts/email-otp/validate",
	"/api/auth/csrf", "/api/auth/session", "/sentinel/", "/backend-api/sentinel",
	"/api/accounts/email-otp/resend",
}

// actionDelays 动作层停顿（毫秒）：(min, max)。对齐 Python _ACTION_DELAYS。
var actionDelays = map[string][2]float64{
	// 收到邮箱验证码后，模拟人切回页面、看清 6 位数字再输入（最关键）。
	"otp_input": {2500, 8000},
	// 填写姓名 / 生日等表单字段。
	"form": {1800, 5000},
	// Sentinel / Turnstile / PoW 挑战，给 SDK 运行和 UI 等待留时间。
	"challenge": {800, 2400},
	// 注册完成后进入应用、拉取 session 的过渡。
	"post_auth": {1500, 4000},
}

// bucket 按 URL 选传输层节奏档。
func bucket(url string) rhythmBucket {
	u := strings.ToLower(url)
	for _, m := range xhrMarkers {
		if strings.Contains(u, m) {
			return xhrBucket
		}
	}
	for _, m := range navMarkers {
		if strings.Contains(u, m) {
			return navBucket
		}
	}
	return fastBucket
}

// sampleLogNormal 对数正态采样（对齐 Python _sample_log_normal）。
func sampleLogNormal(rng *rand.Rand, mean, sigma float64) float64 {
	if mean < 1e-6 {
		mean = 1e-6
	}
	mu := math.Log(mean) - (sigma*sigma)/2.0
	return math.Exp(rng.NormFloat64()*sigma + mu)
}

// Pause 传输层节奏：在发起请求前调用，按 URL 采样一个延迟并 sleep。
// rng 为 nil 时用全局源（线程安全由调用方保证；注册每协程持独立 rng 更佳）。
func Pause(rng *rand.Rand, url string) {
	b := bucket(url)
	base := sampleLogNormal(rngOrGlobal(rng), b.mean, b.sigma)
	jitter := b.minJitter + rngOrGlobal(rng).Float64()*(b.maxJitter-b.minJitter)
	sleepMs(base + jitter)
}

// ActionPause 动作层停顿：关键人类动作之间插入较长停顿。
// kind ∈ otp_input / form / challenge / post_auth。
func ActionPause(rng *rand.Rand, kind string) {
	d, ok := actionDelays[kind]
	if !ok {
		return
	}
	delay := d[0] + rngOrGlobal(rng).Float64()*(d[1]-d[0])
	sleepMs(delay)
}

// OTPInputPause 收码后输入验证码的停顿（最常用，语义化封装）。
func OTPInputPause(rng *rand.Rand) { ActionPause(rng, "otp_input") }

// DatadogTraceHeaders 生成 Datadog RUM 追踪头（对齐 codex _datadog_trace_headers）。
// codex 全请求注入这组头，注释明确「避免 OTP silent-drop」+ 状态机连续性。
func DatadogTraceHeaders(rng *rand.Rand) map[string]string {
	r := rngOrGlobal(rng)
	tid := fmt.Sprintf("%016x", r.Uint64())
	sid := fmt.Sprintf("%d", r.Int63())
	pid := fmt.Sprintf("%d", r.Int63())
	tsHex := fmt.Sprintf("%08x", time.Now().Unix())
	return map[string]string{
		"traceparent":                fmt.Sprintf("00-0000000000000000%s-%016x-01", tid, r.Uint64()),
		"x-datadog-trace-id":         sid,
		"x-datadog-parent-id":        pid,
		"x-datadog-sampling-priority": "1",
		"x-datadog-origin":           "rum",
		"x-datadog-tags":             fmt.Sprintf("_dd.p.id=%s,_dd.p.tid=%s00000000,_dd.b.sr=1", tid, tsHex),
	}
}

// ── 内部工具 ──

var globalRng = rand.New(rand.NewSource(time.Now().UnixNano()))

func rngOrGlobal(rng *rand.Rand) *rand.Rand {
	if rng != nil {
		return rng
	}
	return globalRng
}

func sleepMs(ms float64) {
	if ms <= 0 {
		return
	}
	if ms > 8000 { // 全局上限（对齐 HUMAN_RHYTHM_MAX 默认 4000ms，动作层单独允许更长）
		ms = 8000
	}
	time.Sleep(time.Duration(ms * float64(time.Millisecond)))
}
