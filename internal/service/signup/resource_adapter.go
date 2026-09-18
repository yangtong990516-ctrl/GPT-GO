// resource_adapter.go 把 signup.ResourceStore 接口接到真实的邮箱池/账号池存储。
//
// 链路（你的硬需求，不变）：
//
//	邮箱池(ReserveEmails 预留) → 协议注册 → 成功落账号池(account.Create + 邮箱 consume)
//	                                          失败按错误码标记邮箱状态 / 归还可复用
//
// 实现说明：
//   - 邮箱池底层是 store.EmailStore（无原子 ReserveEmails），用「List(available) 取一个 +
//     SetStatus(reserved) 占用」实现预留；生产环境若用 Mongo，应在 store 层加 findOneAndUpdate
//     原子占位（见 SESSION-PROXY-PIPELINE.md 待办）。
//   - 账号池底层是 store.AccountStore（account.Service.Create 做 email 唯一约束）；
//     成功落库时 AccountCreate.SourceEmailID 关联邮箱记录（对齐 Python：创建后 consume 该邮箱）。
package signup

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"gpt-go/internal/model"
	"gpt-go/internal/store"
)

// ResourceAdapter 实现 ResourceStore：桥接邮箱池(EmailStore) + 账号池(AccountStore)。
type ResourceAdapter struct {
	emails  store.EmailStore
	account AccountCreator

	// mu 保护「List→SetStatus」两步预留的并发安全（进程内；多实例部署需 store 层原子占位）。
	mu sync.Mutex
}

// AccountCreator 抽象账号落库（接 account.Service.Create）。
type AccountCreator interface {
	Create(ctx context.Context, in model.AccountCreate) (model.AccountRecord, error)
}

// NewResourceAdapter 创建资源适配器。
func NewResourceAdapter(emails store.EmailStore, account AccountCreator) *ResourceAdapter {
	return &ResourceAdapter{emails: emails, account: account}
}

// ReserveEmails 预留 count 个邮箱（指定来源）。对齐 Python reserve_emails。
// 实现：List(available, source) 取前 count 个 → 逐个 SetStatus(reserved) 占用。
func (a *ResourceAdapter) ReserveEmails(ctx context.Context, count int, owner, source string) ([]ReservedEmail, error) {
	if a.emails == nil {
		return nil, fmt.Errorf("resource: 邮箱池未配置")
	}
	if count <= 0 {
		count = 1
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	docs, _, err := a.emails.List(ctx, store.EmailQuery{
		Status: string(model.EmailStatusAvailable),
		Source: source,
		Page:   1,
		Size:   count,
	})
	if err != nil {
		return nil, err
	}
	if len(docs) == 0 {
		return nil, fmt.Errorf("resource: 邮箱池无可用邮箱(source=%s)", source)
	}
	now := time.Now().UTC()
	out := make([]ReservedEmail, 0, len(docs))
	for _, d := range docs {
		// 占用：available → reserved（带 owner 备注便于排查）。
		if _, err := a.emails.SetStatus(ctx, d.ID, string(model.EmailStatusReserved), "reserved by "+owner, now, ""); err != nil {
			// 占用失败：跳过该条（可能并发被抢），继续下一条。
			continue
		}
		out = append(out, ReservedEmail{ID: d.ID, Email: d.Email, AccessURL: d.AccessURL, Source: d.SourceType})
		if len(out) >= count {
			break
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("resource: 邮箱预留失败(source=%s，并发被抢或池空)", source)
	}
	return out, nil
}

// ReleaseEmail 释放邮箱（失败时归还可复用）：reserved → available。
func (a *ResourceAdapter) ReleaseEmail(ctx context.Context, emailID, owner string) error {
	if a.emails == nil {
		return nil
	}
	_, err := a.emails.SetStatus(ctx, emailID, string(model.EmailStatusAvailable), "released by "+owner, time.Now().UTC(), "")
	return err
}

// DiscardReservedEmail 成功时从池中移除该邮箱（consume）：置 failed（已注册，不可再用）。
// 对齐 Python：注册成功后邮箱被 consume，避免同邮箱重复注册。
func (a *ResourceAdapter) DiscardReservedEmail(ctx context.Context, emailID, owner string) error {
	if a.emails == nil {
		return nil
	}
	_, err := a.emails.SetStatus(ctx, emailID, string(model.EmailStatusFailed), "consumed by "+owner+" (registered)", time.Now().UTC(), "consumed")
	return err
}

// SetEmailStatus 设置邮箱状态（含失败原因 + 错误码）。
func (a *ResourceAdapter) SetEmailStatus(ctx context.Context, emailID, status, reason, errorCode string) error {
	if a.emails == nil {
		return nil
	}
	_, err := a.emails.SetStatus(ctx, emailID, status, reason, time.Now().UTC(), errorCode)
	return err
}

// RestoreUsedProxies 把 used 代理恢复为 available（耗尽兜底）。代理恢复在 proxy store，
// 本适配器不持有 → 返回 0（由 service 层直接调 proxy store 的 RestoreUsed）。
func (a *ResourceAdapter) RestoreUsedProxies(ctx context.Context, country, group string) (int, error) {
	return 0, nil
}

// StoreAccountAccessToken 落库账号 Access Token（由 account 服务另行更新；骨架期留空实现）。
func (a *ResourceAdapter) StoreAccountAccessToken(ctx context.Context, accountID, accessToken, expiresAt string) error {
	return nil
}

// StoreAccountTotp 落库账号 TOTP 绑定（骨架期留空实现）。
func (a *ResourceAdapter) StoreAccountTotp(ctx context.Context, accountID, totpSecret, totpFactorID string) error {
	return nil
}

// DeleteAccounts 删除账号（骨架期留空实现；接 account.Service.Delete）。
func (a *ResourceAdapter) DeleteAccounts(ctx context.Context, accountIDs []string) error {
	return nil
}

// ── 成功落库（注册编排调用）────────────────────────────────────────────────

// PersistAccount 注册成功后落账号池 + consume 邮箱。
//
// in 是注册结果（email/password/session_token/access_token 等）；sourceEmailID 是
// 预留邮箱的记录 ID（consume 用）；country 是实测国家（每次注册当次值，不锁死）。
// 返回账号记录 ID。
func (a *ResourceAdapter) PersistAccount(ctx context.Context, res *AuthResultLike, sourceEmailID, country string) (string, error) {
	if a.account == nil {
		return "", fmt.Errorf("resource: 账号池未配置")
	}
	in := model.AccountCreate{
		Email:           strings.ToLower(strings.TrimSpace(res.Email)),
		ChatgptPassword: res.Password,
		// 注册产物 token：落库 accessToken + 过期时间（套餐检查/验活依赖），refresh_token 可空。
		AccessToken:          strings.TrimSpace(res.AccessToken),
		AccessTokenExpiresAt: accessTokenExpiresAt(res.AccessToken),
		// token-heal 凭证:session cookie + cookie_header(后续 AT 过期续期/验活用)。
		SessionToken: strings.TrimSpace(res.SessionToken),
		CookieHeader: strings.TrimSpace(res.CookieHeader),
	}
	if rt := strings.TrimSpace(res.RefreshToken); rt != "" {
		in.RefreshToken = &rt
	}
	if sourceEmailID != "" {
		in.SourceEmailID = &sourceEmailID // 关联邮箱记录（创建后 consume）
	}
	if country != "" {
		c := model.NormalizeCountryCode(country)
		in.RegistrationCountry = &c
	}
	rec, err := a.account.Create(ctx, in)
	if err != nil {
		return "", err
	}
	// consume 邮箱（成功 → 不可再用）。
	_ = a.DiscardReservedEmail(ctx, sourceEmailID, "signup")
	return rec.ID, nil
}

// AuthResultLike 是 PersistAccount 需要的最小注册结果（避免 import authflow 循环）。
type AuthResultLike struct {
	Email        string
	Password     string
	AccessToken  string
	RefreshToken string
	SessionToken string
	DeviceID     string
	CookieHeader string
	TotpSecret   string
}

// accessTokenExpiresAt 从 access_token 的 JWT exp claim 解析过期时间（对齐 codex
// browser_automation._decode_jwt_expires_at：不验签，仅取 exp → UTC 时间）。
// 解析失败/无 exp 返回 nil（容忍——套餐检查会自行判定 token 失效）。
func accessTokenExpiresAt(token string) *time.Time {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) < 2 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		if payload, err = base64.URLEncoding.DecodeString(parts[1]); err != nil {
			return nil
		}
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil
	}
	exp, ok := claims["exp"].(float64)
	if !ok || exp <= 0 {
		return nil
	}
	t := time.Unix(int64(exp), 0).UTC()
	return &t
}
