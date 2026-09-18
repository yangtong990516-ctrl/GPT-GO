// Package otp 提供「取验证码」的通用抽象层（仅邮箱，已剔除 SMS）。
//
// 设计目标（对齐 codex-auto mail_providers/base.py）：
//
//	加一种新邮箱来源 = 新建 1 个文件 + 注册表加 1 行，
//	调用方（authflow / 登录 / 改密 / 任何需要邮箱验证的模块）一行不动。
//
// ────────────────────────────────────────────────────────────
// 为什么独立成 group（signup/otp）：
//   - 「取验证码」是横切关注点：注册、已有账号登录、密码重置、改绑邮箱……都要用。
//   - 收敛到这一个包，避免每个业务各写一份取码/防误判正则，降低重复实现。
//   - 其它模块直接 import 本包 + 注册对应 provider 即可复用。
//
// ────────────────────────────────────────────────────────────
package otp

// Error 是 provider 统一异常（对齐 Python MailProviderError）。
//
// 带 Fatal 标志，替代「按错误字符串嗅探」的脆弱做法：
//
//	Fatal=true   号本身废了（凭证失效 / 被封 / 收件链路不可用）
//	             → 调用方应把该邮箱标记为永久失败（不再复用）
//	Fatal=false  环境/网络问题，号是无辜的
//	             → 调用方应把邮箱放回池子（可复用）
type Error struct {
	// Message 是人类可读的错误描述。
	Message string
	// Fatal 标记该邮箱是否已不可用（true=号废了，false=环境/网络问题）。
	Fatal bool
	// Kind 是错误分类（如 "mailbox" / "cancelled" / "timeout"），便于上层按类分流。
	Kind string
	// Err 是底层错误（可选，便于 errors.Unwrap 链式排查）。
	Err error
}

// Error 实现 error 接口。
func (e *Error) Error() string {
	if e.Err != nil {
		return e.Message + ": " + e.Err.Error()
	}
	return e.Message
}

// Unwrap 暴露底层错误（供 errors.Is/As）。
func (e *Error) Unwrap() error { return e.Err }

// NewError 构造一个 provider 错误。
func NewError(message string, fatal bool, kind string, err error) *Error {
	return &Error{Message: message, Fatal: fatal, Kind: kind, Err: err}
}

// IsFatal 判断 err 是否为「邮箱已废」的致命错误（供上层决定 mark_dead vs 放回池）。
// 非 *Error 的错误一律按 Fatal=false 处理（保守：不轻易判死一个号）。
func IsFatal(err error) bool {
	if e, ok := err.(*Error); ok {
		return e.Fatal
	}
	return false
}
