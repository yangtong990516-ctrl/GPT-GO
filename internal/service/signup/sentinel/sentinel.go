// Package sentinel 实现 Sentinel PoW 求解，用 v8go（真 V8，cgo）跑
// OpenAI sdk.js，替代 Python 的 Node/QuickJS 子进程方案。
//
// 对齐 Python _runtime/sentinel.py + sentinel_quickjs.py：
//   - 唯一有效路径是跑真实 sdk.js（纯伪造过不了深层校验，OTP silent-drop）
//   - sdk.js 依赖的 Web API 由 js/shim.js 垫片提供（crypto / navigator /
//     document / screen / performance / URL / TextEncoder 等）
//   - 三阶段求解：requirements → challenge → solve
//   - 实现见 v8_solver.go（仅 -tags v8 构建）；架构与坑见 NOTES.md
//
// 版本写死 SENTINEL_VERSION，漂移需巡检（对齐 Python SENTINEL_VERSION）。
package sentinel

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Sentinel 常量，对齐 Python sentinel_quickjs.py。
const (
	// Version 是当前 SDK 版本，旧版 token 过不了深层校验。
	Version = "20260810913b"

	// SDKURL 是 sdk.js 的下载地址（真实域名在运行时替换）。
	SDKURL = "https://sentinel.openai.com/sentinel/" + Version + "/sdk.js"

	// ReqURL 是 /sentinel/req 的 challenge 请求地址。
	ReqURL = "https://sentinel.openai.com/backend-api/sentinel/req"
)

// Flow 是 Sentinel flow 标识，对齐 Python flow 字符串。
type Flow string

const (
	FlowAuthorizeContinue      Flow = "authorize_continue"
	FlowOAuthCreateAccount     Flow = "oauth_create_account"
	FlowUsernamePasswordCreate Flow = "username_password_create"
)

// EnvPayload 是传给 sdk.js 的浏览器环境画像，对齐 Python env_payload。
type EnvPayload struct {
	DeviceID            string   `json:"device_id"`
	UserAgent           string   `json:"user_agent"`
	ScreenWidth         int      `json:"screen_width"`
	ScreenHeight        int      `json:"screen_height"`
	Language            string   `json:"language"`
	Languages           []string `json:"languages"`
	Platform            string   `json:"platform"`
	Vendor              string   `json:"vendor"`
	HardwareConcurrency int      `json:"hardware_concurrency"`
	BrowserType         string   `json:"browser_type"`
	DevicePixelRatio    float64  `json:"device_pixel_ratio"`
	MaxTouchPoints      int      `json:"max_touch_points"`
	Timezone            string   `json:"timezone"`
	DeviceMemory        *int     `json:"device_memory,omitempty"`
}

// Challenge 是 /sentinel/req 返回的 challenge，对齐 Python challenge dict。
// 字段按需解析；核心是 token 和 so 块。
type Challenge struct {
	Token string `json:"token"`

	// ProofOfWork 是 PoW 参数块：proofofwork.seed / proofofwork.difficulty / required。
	ProofOfWork *ProofOfWork `json:"proofofwork,omitempty"`

	// SO 块：so.required 决定是否需要 SO token。
	So *SoBlock `json:"so,omitempty"`

	// Turnstile（可选挑战）。
	Turnstile *Turnstile `json:"turnstile,omitempty"`

	// Raw 保存完整原始 challenge JSON（sdk.js 求解时需要完整结构透传）。
	Raw json.RawMessage `json:"-"`
}

// ProofOfWork 是 challenge.proofofwork 块（PoW seed/difficulty/required）。
type ProofOfWork struct {
	Required   bool   `json:"required"`
	Seed       string `json:"seed"`
	Difficulty int    `json:"difficulty"`
}

// SoBlock 是 challenge.so 块。
type SoBlock struct {
	Required    bool   `json:"required"`
	CollectorDx string `json:"collector_dx"`
}

// Turnstile 是 challenge.turnstile。
type Turnstile struct {
	Dx string `json:"dx"`
}

// SoRequired 判断服务端是否要求 SO token（对齐 Python so_required 判定）。
func (c *Challenge) SoRequired() bool {
	return c != nil && c.So != nil && c.So.Required
}

// Result 是求解结果。
type Result struct {
	Token   string // 主 sentinel token
	SOToken string // SO token（可能为空，取决于服务端是否要求）
}

// Solver 抽象 Sentinel PoW 求解，对齐 Python get_sentinel_token()。
type Solver interface {
	// GetToken 求解 Sentinel token（三阶段：requirements → challenge → solve）。
	// 返回主 token 和 SO token（SO token 可能为空）。
	GetToken(ctx context.Context, env EnvPayload, flow Flow) (*Result, error)
}

// DefaultUA 是默认 User-Agent，对齐 Python DEFAULT_UA。
const DefaultUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/145.0.0.0 Safari/537.36"

// IsValidTimezone 校验 IANA 时区名格式，对齐 Python 的正则校验。
func IsValidTimezone(tz string) bool {
	if tz == "" {
		return false
	}
	if tz == "UTC" {
		return true
	}
	parts := strings.Split(tz, "/")
	if len(parts) != 2 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		for _, c := range p {
			if !isAlphaOrUnderscore(c) {
				return false
			}
		}
	}
	return true
}

func isAlphaOrUnderscore(c rune) bool {
	return c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

// ErrSDK 是 sdk.js 执行失败的错误，携带阶段。
type ErrSDK struct {
	Stage string // requirements / challenge / solve
	Err   error
}

func (e *ErrSDK) Error() string {
	return fmt.Sprintf("sentinel: %s 阶段失败: %v", e.Stage, e.Err)
}

func (e *ErrSDK) Unwrap() error { return e.Err }
