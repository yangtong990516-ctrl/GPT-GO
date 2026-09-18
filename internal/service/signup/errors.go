package signup

import "fmt"

// ErrorCategory 是错误分类，对齐 Python error_catalog.py 的 category。
type ErrorCategory string

const (
	CategoryResource   ErrorCategory = "resource"
	CategoryProxy      ErrorCategory = "proxy"
	CategoryNetwork    ErrorCategory = "network"
	CategoryEmail      ErrorCategory = "email"
	CategoryAntidetect ErrorCategory = "antidetect"
	CategoryStorage    ErrorCategory = "storage"
	CategoryUnknown    ErrorCategory = "unknown"
)

// ErrorInfo 是单个错误码的结构化描述，对齐 Python error_info() 返回值。
type ErrorInfo struct {
	Code        string        `json:"code"`
	Label       string        `json:"label"`
	Category    ErrorCategory `json:"category"`
	Description string        `json:"description"`
	Suggestion  string        `json:"suggestion"`
}

// errorCatalog 是协议注册错误目录（对齐 Python ERROR_CATALOG）。
var errorCatalog = map[string]ErrorInfo{
	"no_eligible_proxy":    {"no_eligible_proxy", "无可用代理", CategoryResource, "指定国家/组的代理池没有可用的代理（已租完或全部被隔离）", "补充该国家的动态代理，或检查代理池是否被大量占用"},
	"no_available_email":   {"no_available_email", "无可用邮箱", CategoryResource, "邮箱池中没有可用邮箱", "补充邮箱，或检查邮箱池是否已耗尽"},
	"bad_proxy_lease":      {"bad_proxy_lease", "代理租约异常", CategoryResource, "代理租约缺少 host/port，无法组装代理 URL", "检查代理配置是否完整"},
	"reserve_email_failed": {"reserve_email_failed", "预留邮箱失败", CategoryResource, "从邮箱池预留邮箱失败", "检查邮箱池状态和邮箱来源配置"},
	"warmup_failed":        {"warmup_failed", "代理被拦截", CategoryProxy, "warmup 阶段 4 次重试均未拿到 chatgpt.com 的 oai-did cookie，多为代理出口 IP 不通或被 Cloudflare 拦截", "换用干净的动态代理，当前出口 IP 大概率是脏 IP"},
	"network":              {"network", "网络异常", CategoryNetwork, "请求超时 / TLS 握手失败 / 连接被重置（curl 28/35/56/97）", "检查代理网络稳定性，切换可直连的出口"},
	"rate_limited":         {"rate_limited", "频率限制", CategoryNetwork, "触发 429 频率限制", "降低并发或放慢节奏"},
	"existing_account":     {"existing_account", "邮箱已注册", CategoryEmail, "邮箱已被识别为已有账号，OTP 静默拒发", "换用新邮箱（当前邮箱已注册过，建议从池中隔离）"},
	"sentinel":             {"sentinel", "Sentinel 求解失败", CategoryAntidetect, "Sentinel PoW 求解失败（主 token 缺失或 SO token 未算出）", "检查 SDK 版本是否漂移（旧版 token 过不了深层校验）"},
	"challenge":            {"challenge", "验证码挑战", CategoryAntidetect, "命中 Turnstile / Arkose / captcha 挑战", "当前出口 IP 触发验证码，换用更干净的代理"},
	"protocol_failed":      {"protocol_failed", "协议注册失败", CategoryUnknown, "协议注册失败（未归类的具体原因，见 error 详情）", "查看具体错误详情，按 error 文本判断根因"},
	"persist_failed":       {"persist_failed", "账号落库失败", CategoryStorage, "账号已在服务端建成，但本地落库失败（邮箱已保留，可重试）", "检查 MongoDB 可用性，账号信息可由批量补 AT 任务重建"},
	"not_trial":            {"not_trial", "非试用资格", CategoryUnknown, "注册后套餐检查未通过（非试用资格），账号已删除不入库", "该出口 IP 未命中试用资格，换代理重试"},
	"cancelled":            {"cancelled", "任务已取消", CategoryUnknown, "任务被手动取消", "无需处理"},
}

// LookupError returns the structured info for an error code, falling back to
// "unknown" (aligning with Python error_info()).
func LookupError(code string) ErrorInfo {
	if info, ok := errorCatalog[code]; ok {
		return info
	}
	return ErrorInfo{
		Code:        code,
		Label:       "未知错误",
		Category:    CategoryUnknown,
		Description: "未登记的错误码",
		Suggestion:  "查看错误详情文本",
	}
}

// RegistrationError 是协议注册错误，携带错误码 + 原始原因 + 关联邮箱。
// 对齐 Python ProtocolRegistrationError（有 code / email 属性）。
type RegistrationError struct {
	Code  string
	Email string
	Err   error
}

func (e *RegistrationError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("[%s] %s: %v", e.Code, e.Email, e.Err)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Email)
}

func (e *RegistrationError) Unwrap() error { return e.Err }

// NewRegistrationError 构造一个带错误码的注册错误。
func NewRegistrationError(code, email string, err error) *RegistrationError {
	return &RegistrationError{Code: code, Email: email, Err: err}
}
