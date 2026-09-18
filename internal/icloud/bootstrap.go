// Package icloud wires the iCloud privacy-mail module into the gpt-go service.
//
// 该模块从独立项目 iCloud-Privacy-Mail-v2 迁移而来,统一改用 MongoDB 存储,
// 并以 /api/icloud 独立路由分组挂载到主服务。登录体系复用模块自带的
// cookie-session(auth.CookieName),默认管理员账号 admin / 1024(首次启动
// 自动 seed,若已存在管理员则跳过)。
package icloud

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/gin-gonic/gin"

	"gpt-go/internal/icloud/auth"
	"gpt-go/internal/icloud/config"
	"gpt-go/internal/icloud/httpapi"
	"gpt-go/internal/icloud/store"
)

// 默认管理员(首次启动 seed;密码哈希复用 auth.HashPassword,与 Login 校验一致)。
const (
	defaultAdminUsername = "admin"
	defaultAdminPassword = "1024"
)

// Module 持有 iCloud 模块的运行时依赖。
type Module struct {
	cfg    config.Config
	store  *store.Store
	server *httpapi.Server
	log    *slog.Logger
}

// Open 建立 iCloud 模块:连接 MongoDB、初始化 store、seed 默认管理员、组装 HTTP 服务。
//
// uri/database 复用主服务的 MongoDB 配置。返回的 Module 通过 Register 挂载路由。
func Open(uri, database string, logger *slog.Logger) (*Module, error) {
	if logger == nil {
		logger = slog.Default()
	}
	cfg := config.Default()
	st, err := store.Open(uri, database, cfg)
	if err != nil {
		return nil, fmt.Errorf("icloud: 连接 MongoDB 失败: %w", err)
	}
	m := &Module{cfg: cfg, store: st, log: logger}
	if err := m.seedDefaultAdmin(); err != nil {
		logger.Warn("icloud: seed 默认管理员失败", "错误", err)
	}
	m.server = httpapi.New(cfg, st, logger)
	return m, nil
}

// seedDefaultAdmin 在没有任何管理员时写入默认账号 admin/1024。
// 哈希复用 auth.HashPassword(与 Setup/Login 同一 pbkdf2_sha256 编码,120000 迭代),
// 但跳过 auth.Setup 的「密码至少 8 位」强度校验(Login 只走 verifyPassword,不校验强度)。
func (m *Module) seedDefaultAdmin() error {
	if _, ok := m.store.Admin(); ok {
		return nil
	}
	encoded, err := auth.HashPassword(defaultAdminPassword)
	if err != nil {
		return err
	}
	if _, err := m.store.SetupAdmin(defaultAdminUsername, encoded); err != nil {
		return err
	}
	m.log.Info("icloud: 已创建默认管理员", "账号", defaultAdminUsername)
	return nil
}

// Register 把 iCloud 模块的全部路由挂到 /api/icloud 分组。
func (m *Module) Register(engine *gin.Engine) {
	group := engine.Group("/api/icloud")
	m.server.Register(group)
}

// Auth 暴露模块的鉴权服务,供主站构建全局控制台登录门禁(整个 GPT-GO 一道密码)。
func (m *Module) Auth() *auth.Service {
	return m.server.Auth()
}

// StartBackground 启动模块的后台协程(调度器/租约回收/数据库维护/邮件回填/保活/监听)。
func (m *Module) StartBackground(ctx context.Context) {
	m.server.StartBackground(ctx)
}

// Close 释放 MongoDB 连接。
func (m *Module) Close(ctx context.Context) {
	if m.store != nil {
		_ = m.store.Close(ctx)
	}
}
