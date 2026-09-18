package signup

import (
	"context"

	"gpt-go/internal/service/signup/otp"
)

// ProxyLease 是代理租约，对齐 Python probe_store 的 ProxyLease。
// 字段对齐 model.ProxyRecord 的租约视图。
type ProxyLease struct {
	ID       string
	Host     string
	Port     int
	Username string
	Password string
	Scheme   string
	Country  string
	Group    string
}

// ProxyURL 组装代理 URL（对齐 Python _proxy_url_from_lease）。
func (l *ProxyLease) ProxyURL() string {
	if l == nil || l.Host == "" || l.Port == 0 {
		return ""
	}
	scheme := l.Scheme
	if scheme == "" {
		scheme = "socks5"
	}
	userinfo := ""
	if l.Username != "" {
		userinfo = l.Username
		if l.Password != "" {
			userinfo += ":" + l.Password
		}
		userinfo += "@"
	}
	return scheme + "://" + userinfo + l.Host + ":" + itoa(l.Port)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// ReservedEmail 是预留邮箱结果，对齐 Python reserve_emails 返回的元素。
type ReservedEmail struct {
	ID    string
	Email string
	// AccessURL 是该邮箱的取件/接码入口（邮箱池记录里的 accessUrl）。
	// 用于按本邮箱现场构造专属 MailProvider（见 MailFactory）——并发批量注册时
	// 每个协程绑定自己邮箱的 accessUrl，杜绝「共享 mailbox 串号」。
	AccessURL string
	// Source 是该邮箱的来源类型（= 邮箱池 store 的 SourceType：
	// mailcode / remail / mailcom_alias / standard...）。装配层据此选 OTP provider
	// 类型（remail→jsoncode 取 verificationCode 字段；其余→mailcode 邮件原文提码）。
	Source string
}

// ProxyStore 抽象代理池租约操作，对齐 Python 里 run_protocol_registration 用到的
// store 方法（probe_store）。真实实现接 internal/service/proxy 的租约服务。
type ProxyStore interface {
	// CountEligibleProxies 统计指定国家/组的可用代理数。
	CountEligibleProxies(ctx context.Context, country, group string) (int, error)
	// AcquireProxy 租一个代理，排除 excludedIDs。
	AcquireProxy(ctx context.Context, owner string, excludedIDs []string, leaseSeconds int, country, group string) (*ProxyLease, error)
	// AcquireProxyByID 按 ID 租指定代理（套餐检查等需固定代理的场景）。
	AcquireProxyByID(ctx context.Context, proxyID, owner string, leaseSeconds int) (*ProxyLease, error)
	// ReleaseProxy 释放（隔离）代理。
	ReleaseProxy(ctx context.Context, proxyID, owner string) error
	// ReturnProxy 归还（复用）代理。
	ReturnProxy(ctx context.Context, proxyID, owner string) error
}

// ResourceStore 抽象资源层操作，对齐 Python resource_service 用到的方法。
// 真实实现接 internal/service/{account,email} 的 store。
type ResourceStore interface {
	// ReserveEmails 预留 count 个邮箱（指定来源）。
	ReserveEmails(ctx context.Context, count int, owner, source string) ([]ReservedEmail, error)
	// ReleaseEmail 释放邮箱（失败时归还可复用）。
	ReleaseEmail(ctx context.Context, emailID, owner string) error
	// DiscardReservedEmail 丢弃预留邮箱（成功时从池中移除）。
	DiscardReservedEmail(ctx context.Context, emailID, owner string) error
	// SetEmailStatus 设置邮箱状态（含失败原因 + 错误码）。
	SetEmailStatus(ctx context.Context, emailID, status, reason, errorCode string) error
	// RestoreUsedProxies 把 used 代理恢复为 available。
	RestoreUsedProxies(ctx context.Context, country, group string) (int, error)
	// StoreAccountAccessToken 落库账号 Access Token。
	StoreAccountAccessToken(ctx context.Context, accountID, accessToken string, expiresAt string) error
	// StoreAccountTotp 落库账号 TOTP 绑定。
	StoreAccountTotp(ctx context.Context, accountID, totpSecret, totpFactorID string) error
	// DeleteAccounts 删除账号。
	DeleteAccounts(ctx context.Context, accountIDs []string) error
}

// MailProvider 是「取邮箱验证码」的统一抽象（= otp.Provider 的别名）。
//
// 【接口升级说明】原定义为单方法 FetchOTP（无防串号），已升级为 otp.Provider：
// 新增 issuedAfter 防串号 + PeekOTP 预读 + 能力声明，见 internal/service/signup/otp。
// 用类型别名保持向后兼容（service.go / wire.go 等处的 MailProvider 引用自动指向新接口）。
//
// 注意：实现方需满足 otp.Provider 全接口（Caps/WaitForOTP/CreateMailbox/PeekOTP/MarkDead/Exhausted），
// 推荐直接用 otp.New(kind,...) 工厂构造（如 otp 的 mailcode provider）。
type MailProvider = otp.Provider

// MailFactory 按「预留邮箱」现场构造专属 MailProvider（对齐 codex-auto MailBridgeProvider
// 的 __init__：access_url 绑定到那一个 reserved_email）。
//
// 为什么用工厂而不是共享实例（并发串号的根因修复）：
//
//	原设计 Service 持有一个共享 mailbox，批量并发注册时多个协程复用它，
//	其内部 accessUrl 是单值——A 邮箱的码会被 B 协程读到（extract.go 防串号注释
//	正是警告这个）。改为工厂后，Run 在 reserveEmail 拿到带 AccessURL 的预留邮箱，
//	现场造一个绑定该 accessUrl 的专属 provider，并发下互不干扰。
//
// 装配层实现：按 EmailSource 调 otp.New(source, settings, {"email","accessUrl"})。
type MailFactory func(ctx context.Context, email ReservedEmail) (MailProvider, error)

// ── SMSController 已移除（剔除 SMS）──
// 当前 GPT-GO 只做邮箱验证码，不涉及短信接码（add-phone 风控旁路）。
// 若未来需要 SMS，参照 otp 包的模式另建独立 provider 体系，不与邮箱 OTP 合并。

// StepLogger 是步骤日志回调，对齐 Python log_callback（把步骤推送到运行日志）。
type StepLogger func(message string)

// ProgressCallback 是单账号完成回调，对齐 Python progress_callback。
type ProgressCallback func(result BatchItemResult)
