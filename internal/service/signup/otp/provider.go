// provider.go 邮箱 Provider 抽象 + 注册表工厂（对齐 Python mail_providers/base.py）。
//
// ────────────────────────────────────────────────────────────
// 两个正交的能力维度（务必分清，混用会踩坑，base.py 血泪教训）
// ────────────────────────────────────────────────────────────
//
//	Pooled     号是"买来的、有限的、废了要换下一个"
//	           → 决定调用方的 fast-fail / mark_dead 行为
//
//	Ephemeral  地址是不是"每次都新造一个"
//	           → 决定 OpenAI 把它当新号还是老号
//	           → Ephemeral=false 的固定地址可能被路由到
//	             page_type='login_password'，需要密码才能过
//
//	这两个维度独立，四种组合都真实存在：
//	    provider            Pooled  Ephemeral   说明
//	    ─────────────────── ─────── ─────────  ──────────────────────
//	    Outlook 接码池        true    false      导入一批固定号，用完换
//	    CF catch-all         false   true       自己造随机地址，无限
//	    Gmail / 通用 IMAP     true    false      同 Outlook
//	    iCloud relay 中转     false   false      固定地址但无密码 ⚠️
//
//	⚠️ 最后一行是 iCloud 失败根因：Pooled=false 避开号池逻辑，
//	   但 Ephemeral=false 意味着 OpenAI 当老号处理 → 要密码 → 401。
package otp

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// Capabilities 是 provider 的能力声明（对齐 base.py 的类属性）。
type Capabilities struct {
	// Kind 唯一标识（等于号池里存的 mail_source）。
	Kind string
	// DisplayName 是给 UI/下拉框用的显示名。
	DisplayName string
	// Pooled 是否从号池 claim（见包注释的能力维度表）。
	Pooled bool
	// Ephemeral 地址是否每次新建（见能力维度表）。
	Ephemeral bool
	// AcceptsExistingAccount：「OpenAI 说这邮箱已注册」算不算失败。
	//   false（默认）想注册新号却撞上老号 → 这号没用了，标记失败
	//   true          本来就是买的老号，走 passwordless_login 拿 token 才正常，不该判失败
	AcceptsExistingAccount bool
}

// Provider 是所有邮箱 provider 的接口（对齐 base.py MailProvider）。
//
// 实现者只需实现 WaitForOTP；CreateMailbox / PeekOTP / MarkDead 按需覆盖。
type Provider interface {
	// Caps 返回能力声明（Kind/Pooled/Ephemeral 等）。
	Caps() Capabilities

	// WaitForOTP 阻塞等待 OTP，拿到返回 6 位码，超时返回 *Error(Kind="timeout")。
	//
	//   emailAddr    要收码的邮箱地址
	//   timeoutSeconds 最长等待秒数
	//   issuedAfter  【防串号时间窗】只接受这个时间点之后到达的邮件，
	//                避免读到上一轮遗留的旧验证码。为 0 表示不过滤。
	//                实现者务必尊重这个参数（见 authflow：发码前取时间戳）。
	WaitForOTP(ctx context.Context, emailAddr string, timeoutSeconds int, issuedAfterUnix int64) (string, error)

	// CreateMailbox 返回本次注册要用的邮箱地址（可选）。
	//   Ephemeral=true  → 每次调用造一个新地址
	//   Ephemeral=false → 返回已持有的固定地址（默认返回 ""/nil，由号池提供）
	CreateMailbox(ctx context.Context) (string, error)

	// PeekOTP 非破坏性预读收件箱（可选，省一封重复码，对齐 base.py peek_otp）。
	//
	// 为什么要有：get_auth_url 带 login_hint 时 OpenAI 会【抢跑发码】，
	// 一轮 challenge 能收 3 封【码完全一样】的信。先 peek 一下，命中就不用再发。
	//
	// 三条铁律（缺一不可）：
	//   1. 不阻塞死等 —— 探一次就走（waitSeconds 通常 0）。
	//   2. 拿不到返回 "",nil（不抛异常），让调用方走原来的发码路径。
	//   3. 非破坏性 —— 不得把看过的邮件记进 seen 集合，否则紧接着的
	//      WaitForOTP 就再也看不见那几封信了。
	//
	// 默认实现返回 "",nil（保持原有「先发再等」行为），provider 想省信就覆盖。
	PeekOTP(ctx context.Context, emailAddr string, issuedAfterUnix int64, waitSeconds float64) (string, error)

	// MarkDead 标记本号废掉（Pooled provider 实现；非池化默认无操作）。
	MarkDead(reason string)

	// Exhausted 本号是否已判定不可用（收不到码 / 凭证失效）。
	// 调用方据此决定超时后要不要换号重试。
	Exhausted() bool
}

// ────────────────────────────────────────────────────────────
//  注册表 + 工厂（对齐 base.py register / create_mail_provider）
// ────────────────────────────────────────────────────────────

// Factory 是 provider 构造器：从「全局配置 + 号池记录」构造实例。
//
//	settings  全局配置（api_url / token / domain ...）
//	account   Pooled provider 从号池 claim 到的那一行（map 形式，含 accessUrl 等）；
//	          非池化 provider 传 nil
type Factory func(ctx context.Context, settings map[string]any, account map[string]any) (Provider, error)

var (
	registryMu sync.RWMutex
	registry   = map[string]Factory{}
)

// Register 注册一个 provider 工厂（可在各 provider 文件的 init() 里调）。
// kind 重复注册会 panic（开发期暴露冲突，不静默覆盖）。
func Register(kind string, f Factory) {
	key := normalizeKind(kind)
	if key == "" || key == "base" {
		panic("otp.Register: kind 必须非空且非 base")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, dup := registry[key]; dup {
		panic(fmt.Sprintf("otp.Register: kind=%q 已注册", key))
	}
	registry[key] = f
}

// New 是 provider 的唯一构造入口（对齐 base.py create_mail_provider）。
// 未知 kind 返回致命错误（不静默回退，避免「选了新邮箱却跳到默认」）。
func New(ctx context.Context, kind string, settings, account map[string]any) (Provider, error) {
	key := normalizeKind(kind)
	registryMu.RLock()
	f, ok := registry[key]
	registryMu.RUnlock()
	if !ok {
		return nil, NewError(
			fmt.Sprintf("未知邮箱来源 %q（已注册: %v）", kind, ListKinds()),
			true, "unknown_kind", nil)
	}
	return f(ctx, settings, account)
}

// ListKinds 列出所有已注册 provider 的 kind（排序，便于 UI 展示与错误提示）。
func ListKinds() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// normalizeKind 规范化 kind（小写、去空白），保证注册/查找一致。
func normalizeKind(kind string) string {
	b := []byte(kind)
	j := 0
	for _, c := range b {
		if c == ' ' || c == '\t' {
			continue
		}
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[j] = c
		j++
	}
	return string(b[:j])
}
