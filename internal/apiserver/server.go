// Package apiserver implements the HTTP API layer, mirroring reference
// app/backend/main.py. Each resource/module registers its routes here; all
// shared helpers come from internal/util.
package apiserver

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	mongodriver "go.mongodb.org/mongo-driver/mongo"
	"go.uber.org/zap"

	"gpt-go/internal/apiserver/accounts"
	consoleauth "gpt-go/internal/apiserver/auth"
	"gpt-go/internal/apiserver/emails"
	"gpt-go/internal/apiserver/mailcode"
	paymentcheckapi "gpt-go/internal/apiserver/paymentcheck"
	"gpt-go/internal/apiserver/proxy"
	rebindapi "gpt-go/internal/apiserver/rebind"
	"gpt-go/internal/apiserver/remail"
	"gpt-go/internal/apiserver/runlogs"
	"gpt-go/internal/apiserver/runs"
	"gpt-go/internal/apiserver/sentinel"
	"gpt-go/internal/apiserver/settings"
	"gpt-go/internal/apiserver/stats"
	"gpt-go/internal/config"
	"gpt-go/internal/icloud"
	icloudhttp "gpt-go/internal/icloud/httpapi"
	"gpt-go/internal/model"
	"gpt-go/internal/service/account"
	"gpt-go/internal/service/accountsecurity"
	"gpt-go/internal/service/email"
	mailcodesvc "gpt-go/internal/service/mailcode"
	"gpt-go/internal/service/mfacheck"
	"gpt-go/internal/service/paymentcheck"
	"gpt-go/internal/service/plancheck"
	proxysvc "gpt-go/internal/service/proxy"
	"gpt-go/internal/service/rebind"
	remailsvc "gpt-go/internal/service/remail"
	sentinelsvc "gpt-go/internal/service/sentinel"
	settingssvc "gpt-go/internal/service/settings"
	"gpt-go/internal/service/signup"
	"gpt-go/internal/service/signup/otp"
	statssvc "gpt-go/internal/service/stats"
	"gpt-go/internal/store"
	"gpt-go/internal/store/mongo"
	"gpt-go/internal/util"
)

// Server wires the router and dependencies together (mirrors create_app()).
type Server struct {
	cfg           *config.Config
	mongo         *mongo.Manager
	emailStore    store.EmailStore
	emailSvc      *email.Service
	mailcodeStore store.MailcodeStore
	mailcodeSvc   *mailcodesvc.Service
	remailStore   store.RemailStore
	remailSvc     *remailsvc.Service
	accountStore  store.AccountStore
	accountSvc    *account.Service
	sentinelStore store.SentinelStore
	sentinelSched *sentinelsvc.Scheduler
	proxyStore    store.ProxyStore
	proxySvc      *proxysvc.Service
	settingsStore *store.SettingsStore
	settingsSvc   *settingssvc.Service
	statsSvc      *statssvc.Service
	// paymentChecker 是支付类型检测服务（全局互斥批次；/api/payment-check 路由组）。
	paymentChecker *paymentcheck.Service
	// planChecker 是「验活 + 套餐/优惠资格检查」合并服务（/api/accounts/check-combined 批量触发；
	// 注册成功后也用它做后台单号检查）。对齐 codex check_combined。
	planChecker *plancheck.Service
	// mfaChecker 是「批量查询 2FA 状态」服务（/api/accounts/check-2fa 批量触发）。
	mfaChecker *mfacheck.Service
	// security 是「补 2FA(开通 TOTP)」服务（/api/accounts/{id}/ensure-2fa + /bulk-ensure-2fa）。
	security *accountsecurity.Service
	// rebinder 是邮箱换绑服务（全局互斥批次；/api/rebind 路由组）。
	rebinder *rebind.Service
	// signup 是装配好的纯协议注册引擎（见 signup_wiring.go），由 runs 触发批量注册。
	signup *signupEngine
	// runRegistry 是批次进度的进程内注册表（内存实时跟踪，runs API 查询/落库用）。
	runRegistry *signup.RunRegistry
	// logHub 是进程级运行日志中心（内存环形 + SSE 订阅）：注册/plan检查事件实时写入，
	// runlogs API 提供历史查询 + SSE 实时推送。
	logHub *signup.RunLogHub
	// icloud 是 iCloud 邮箱管理模块（独立 /api/icloud 分组 + MongoDB 存储 + cookie 登录）。
	icloud *icloud.Module
}

// NewServer returns a Server bound to config and Mongo manager.
func NewServer(cfg *config.Config, m *mongo.Manager, mongoClient *mongodriver.Client) *Server {
	s := &Server{cfg: cfg, mongo: m}

	// ── 业务 store 装配:优先 Mongo 持久化,连接缺失/未实现则回退内存 Mock ──
	// mongoClient 为 nil(连接失败)时整体回退内存(重启即失),并记录警告。
	var (
		emailStore    store.EmailStore
		mailcodeStore store.MailcodeStore
		remailStore   store.RemailStore
		accountStore  store.AccountStore
		sentinelStore store.SentinelStore
		proxyStore    store.ProxyStore
	)
	if mongoClient != nil {
		base := store.NewStoreBase(mongoClient, cfg.MongoDB.Database)
		ms, err := store.BuildMongoStoresContext(context.Background(), base)
		if err != nil {
			util.Logger().Warn("mongo store 装配失败,回退内存存储", zap.Error(err))
		} else {
			emailStore, mailcodeStore, remailStore = ms.Email, ms.Mailcode, ms.Remail
			accountStore, sentinelStore, proxyStore = ms.Account, ms.Sentinel, ms.Proxy
		}
	}
	// 任一 store 未装配(Mongo 不可用或未实现)时回退内存 Mock。
	if emailStore == nil {
		emailStore = store.NewMockEmailStore()
	}
	if mailcodeStore == nil {
		mailcodeStore = store.NewMockMailcodeStore()
	}
	if remailStore == nil {
		remailStore = store.NewMockRemailStore()
	}
	if accountStore == nil {
		accountStore = store.NewMockAccountStore()
	}
	if sentinelStore == nil {
		sentinelStore = store.NewMockSentinelStore()
	}
	if proxyStore == nil {
		proxyStore = store.NewMockProxyStore()
	}
	s.emailStore = emailStore
	s.emailSvc = email.NewService(s.emailStore)
	s.mailcodeStore = mailcodeStore
	s.mailcodeSvc = mailcodesvc.NewService(s.mailcodeStore, s.emailSvc)
	s.remailStore = remailStore
	s.remailSvc = remailsvc.NewService(s.remailStore, s.emailSvc)
	s.accountStore = accountStore
	s.accountSvc = account.NewService(s.accountStore)
	s.sentinelStore = sentinelStore
	s.proxyStore = proxyStore
	s.proxySvc = proxysvc.NewService(s.proxyStore)
	// Execution settings use the file-backed store (mirrors settings_store.py).
	s.settingsStore = store.NewSettingsStore(store.DefaultSettingsPath())
	s.settingsSvc = settingssvc.NewService(s.settingsStore)
	// Stats overview aggregates account/email/proxy counts.
	s.statsSvc = statssvc.NewService(s.accountStore, s.emailStore, s.proxyStore)
	// Wire the proxy pool into sentinel: sentinel's _resolve_proxy uses the
	// pool's all_eligible_proxy_candidates() when no fixed proxy is configured.
	s.sentinelSched = sentinelsvc.NewSchedulerWithProxy(s.sentinelStore, proxyProviderAdapter{s.proxySvc})
	// 演示种子数据已默认关闭(避免假数据污染真实库);需要开箱演示时可用
	// GPT_GO_SEED_DEMO=1 显式开启。
	if os.Getenv("GPT_GO_SEED_DEMO") == "1" {
		seedMockDemoData(s)
	}
	// 批次进度注册表（内存实时跟踪注册 run 的进度，runs API 查询/落库用）。
	s.runRegistry = signup.NewRunRegistry()
	// 运行日志中心（内存环形 + SSE），注册/plan检查事件实时写入。
	s.logHub = signup.NewRunLogHub()
	// 装配纯协议注册引擎（地基+solver+authflow+资源/代理轮转+OTP），供 runs 触发。
	s.wireSignup(context.Background())
	// iCloud 邮箱管理模块：MongoDB 存储 + /api/icloud 独立分组 + cookie 登录。
	// 连接失败不阻断主服务启动（记录日志,模块路由不可用）。
	if mod, err := icloud.Open(cfg.MongoDB.URI, cfg.MongoDB.Database, nil); err != nil {
		util.Logger().Warn("icloud module init failed", zap.Error(err))
	} else {
		s.icloud = mod
	}
	return s
}

// proxyProviderAdapter adapts proxysvc.Service to sentinelsvc.ProxyProvider,
// dropping the optional country filter (sentinel asks for all eligible).
type proxyProviderAdapter struct {
	svc *proxysvc.Service
}

func (a proxyProviderAdapter) AllEligibleProxyCandidates(ctx context.Context) ([]model.ProxyLease, error) {
	return a.svc.AllEligibleProxyCandidates(ctx, "")
}

// buildEnsure2FAOTPFactory 返回补 2FA 用的 OTP provider 工厂
// (accountsecurity.OtpProviderFunc:accessURL → otp.Provider)。
//
// 本轮【有密码分支】密码登录直达、不走 OTP,故工厂的 provider 通常用不到;
// 仅在账号被路由到 passwordless/OTP 分支时兜底取码(用邮箱 accessUrl,与注册同源)。
// accessUrl 为空或构造失败返回 nil(登录密码分支 Mail 可为 nil)。
func (s *Server) buildEnsure2FAOTPFactory() accountsecurity.OtpProviderFunc {
	return func(accessURL string) otp.Provider {
		accessURL = strings.TrimSpace(accessURL)
		if accessURL == "" {
			return nil
		}
		settings := map[string]any{}
		if s.mailcodeSvc != nil {
			if cfg, err := s.mailcodeSvc.GetConfig(context.Background()); err == nil && cfg.BaseURL != "" {
				settings["baseURL"] = cfg.BaseURL
			}
		}
		// mailcode provider:accessUrl 返回邮件原文,正则提码(与注册兜底同源)。
		p, err := otp.New(context.Background(), "mailcode", settings, map[string]any{
			"email": "", "accessUrl": accessURL,
		})
		if err != nil {
			return nil
		}
		return p
	}
}

// withSettingsAndStats wires the settings and stats services from the account/
// email/proxy stores already assigned to s. Used by the test constructor hooks.
func (s *Server) withSettingsAndStats() *Server {
	s.settingsStore = store.NewSettingsStore(store.DefaultSettingsPath())
	s.settingsSvc = settingssvc.NewService(s.settingsStore)
	s.statsSvc = statssvc.NewService(s.accountStore, s.emailStore, s.proxyStore)
	// 批次进度注册表（nil 防护：自定义构造函数统一走这里补齐）。
	if s.runRegistry == nil {
		s.runRegistry = signup.NewRunRegistry()
	}
	// 运行日志中心（nil 防护：自定义构造函数统一走这里补齐）。
	if s.logHub == nil {
		s.logHub = signup.NewRunLogHub()
	}
	return s
}

// NewServerWithEmailStore is a test/constructor hook that injects a custom
// email store (used by the email pool tests).
func NewServerWithEmailStore(cfg *config.Config, m *mongo.Manager, es store.EmailStore) *Server {
	emailSvc := email.NewService(es)
	ss := store.NewMockSentinelStore()
	ps := store.NewMockProxyStore()
	return (&Server{
		cfg:           cfg,
		mongo:         m,
		emailStore:    es,
		emailSvc:      emailSvc,
		mailcodeStore: store.NewMockMailcodeStore(),
		mailcodeSvc:   mailcodesvc.NewService(store.NewMockMailcodeStore(), emailSvc),
		remailStore:   store.NewMockRemailStore(),
		remailSvc:     remailsvc.NewService(store.NewMockRemailStore(), emailSvc),
		accountStore:  store.NewMockAccountStore(),
		accountSvc:    account.NewService(store.NewMockAccountStore()),
		sentinelStore: ss,
		sentinelSched: sentinelsvc.NewScheduler(ss),
		proxyStore:    ps,
		proxySvc:      proxysvc.NewService(ps),
	}).withSettingsAndStats()
}

// NewServerWithStores is a test/constructor hook that injects custom stores.
func NewServerWithStores(cfg *config.Config, m *mongo.Manager, es store.EmailStore, ms store.MailcodeStore) *Server {
	emailSvc := email.NewService(es)
	ss := store.NewMockSentinelStore()
	ps := store.NewMockProxyStore()
	return (&Server{
		cfg:           cfg,
		mongo:         m,
		emailStore:    es,
		emailSvc:      emailSvc,
		mailcodeStore: ms,
		mailcodeSvc:   mailcodesvc.NewService(ms, emailSvc),
		remailStore:   store.NewMockRemailStore(),
		remailSvc:     remailsvc.NewService(store.NewMockRemailStore(), emailSvc),
		accountStore:  store.NewMockAccountStore(),
		accountSvc:    account.NewService(store.NewMockAccountStore()),
		sentinelStore: ss,
		sentinelSched: sentinelsvc.NewScheduler(ss),
		proxyStore:    ps,
		proxySvc:      proxysvc.NewService(ps),
	}).withSettingsAndStats()
}

// NewServerWithAllStores is a test/constructor hook that injects email, mailcode,
// remail, and account stores.
func NewServerWithAllStores(cfg *config.Config, m *mongo.Manager, es store.EmailStore, ms store.MailcodeStore, rs store.RemailStore) *Server {
	emailSvc := email.NewService(es)
	ss := store.NewMockSentinelStore()
	ps := store.NewMockProxyStore()
	return (&Server{
		cfg:           cfg,
		mongo:         m,
		emailStore:    es,
		emailSvc:      emailSvc,
		mailcodeStore: ms,
		mailcodeSvc:   mailcodesvc.NewService(ms, emailSvc),
		remailStore:   rs,
		remailSvc:     remailsvc.NewService(rs, emailSvc),
		accountStore:  store.NewMockAccountStore(),
		accountSvc:    account.NewService(store.NewMockAccountStore()),
		sentinelStore: ss,
		sentinelSched: sentinelsvc.NewScheduler(ss),
		proxyStore:    ps,
		proxySvc:      proxysvc.NewService(ps),
	}).withSettingsAndStats()
}

// NewServerWithAccountStore is a test/constructor hook that injects a custom
// account store (used by the account pool tests).
func NewServerWithAccountStore(cfg *config.Config, m *mongo.Manager, es store.EmailStore, as store.AccountStore) *Server {
	emailSvc := email.NewService(es)
	ss := store.NewMockSentinelStore()
	ps := store.NewMockProxyStore()
	return (&Server{
		cfg:           cfg,
		mongo:         m,
		emailStore:    es,
		emailSvc:      emailSvc,
		mailcodeStore: store.NewMockMailcodeStore(),
		mailcodeSvc:   mailcodesvc.NewService(store.NewMockMailcodeStore(), emailSvc),
		remailStore:   store.NewMockRemailStore(),
		remailSvc:     remailsvc.NewService(store.NewMockRemailStore(), emailSvc),
		accountStore:  as,
		accountSvc:    account.NewService(as),
		sentinelStore: ss,
		sentinelSched: sentinelsvc.NewScheduler(ss),
		proxyStore:    ps,
		proxySvc:      proxysvc.NewService(ps),
	}).withSettingsAndStats()
}

// Handler returns the configured gin engine with all routes registered.
func (s *Server) Handler() *gin.Engine {
	engine := gin.New()
	engine.Use(gin.Recovery())

	engine.GET("/api/health", s.health)

	// 全局控制台登录门禁:除登录/健康/iCloud 公开API外,所有 /api/* 需全局会话 cookie。
	// 会话与密码由 icloud 模块的 auth.Service 托管(Mongo),整个控制台一道密码。
	if s.icloud != nil && os.Getenv("GPT_GO_AUTH_DISABLED") == "" {
		authHandler := consoleauth.NewHandler(s.icloud.Auth())
		// 登录/登出/状态/改密码路由自身不走门禁。
		authHandler.Register(engine.Group("/api/auth"))
		// 全局中间件:保护主站各业务 API。
		// 排除 /api/auth(登录)、/api/health(健康)、/api/icloud(iCloud 模块自带
		// protected 中间件读同一 cookie,且其 /v1 公开 API 用 API key 鉴权)。
		guard := consoleauth.Guard(s.icloud.Auth(), "/api/auth", "/api/health", "/api/icloud")
		engine.Use(guard)
	}

	// Global stats overview (CONTRACT §4.5): account + email + proxy counts.
	engine.GET("/api/stats/overview", stats.NewHandler(s.statsSvc).Overview)

	// Execution settings (CONTRACT §4.5): local JSON file.
	settings.NewHandler(s.settingsSvc).Register(engine.Group("/api/settings"))

	// Account pool group (CONTRACT §4.5).
	accounts.NewHandler(s.accountSvc).
		WithPlanChecker(s.planChecker).
		WithMfaChecker(s.mfaChecker).
		WithSecurity(s.security, s.buildEnsure2FAOTPFactory()).
		Register(engine.Group("/api/accounts"))

	// Email pool group (CONTRACT §4.5).
	emails.NewHandler(s.emailSvc).Register(engine.Group("/api/emails"))

	// Mailcode group (CONTRACT §4.5).
	mailcode.NewHandler(s.mailcodeSvc).Register(engine.Group("/api/mailcode"))

	// Remail group (CONTRACT §4.5).
	remail.NewHandler(s.remailSvc).Register(engine.Group("/api/remail"))

	// Sentinel version-check group (CONTRACT §4.5).
	sentinel.NewHandler(s.sentinelSched).Register(engine.Group("/api/sentinel"))

	// Proxy pool group (CONTRACT §4.5).
	proxy.NewHandler(s.proxySvc).WithSettings(s.settingsSvc).Register(engine.Group("/api/proxies"))

	// Payment-check group（支付类型检测，独立功能页）：全局互斥批次 + 账号并发 + 线路串行。
	if s.paymentChecker != nil {
		paymentcheckapi.NewHandler(s.paymentChecker, s.proxySvc).Register(engine.Group("/api/payment-check"))
	}
	// Rebind group（邮箱换绑，独立功能页）：全局互斥批次 + 账号并发 + 邮箱预留。
	if s.rebinder != nil {
		rebindapi.NewHandler(s.rebinder, rebindPoolCounter{s.emailStore, s.proxySvc}).Register(engine.Group("/api/rebind"))
	}

	// Runs group（批量注册触发点，CONTRACT §4.5）：A 方案实时注入 settings。
	// newDialer 按 settings.maxRegistrationsPerExitIp 现场造拨号器（每批干净 registry）。
	if s.signup != nil && s.signup.Svc != nil {
		// 批次进度：内存 RunRegistry 实时跟踪（注册中可查），结束后可选手动落库 mongo。
		// runStore 用 config.mongodb 的 uri/database 建（懒连接，首次 save 才连）。
		h := runs.NewHandler(
			s.settingsSvc,
			func(exitIPCap, maxRedial int) *signup.SessionDialer {
				return signup.NewSessionDialer(s.signup.prober,
					signup.WithExitIPCap(exitIPCap),
					signup.WithMaxRedial(maxRedial))
			},
			s.signup.Svc,
		).WithRegistry(s.runRegistry).WithLogHub(s.logHub)
		if s.cfg != nil {
			h = h.WithRunStore(NewRunStore(s.cfg.MongoDB.URI, s.cfg.MongoDB.Database))
		}
		h.Register(engine.Group("/api/runs"))
	}

	// Run-logs group（运行日志，CONTRACT 扩展）：REST 历史查询 + SSE 实时推送。
	// hub 由 logHub 提供（注册/plan检查事件实时写入）。
	runlogs.NewHandler(s.logHub).Register(engine.Group("/api/run-logs"))

	// iCloud 邮箱管理模块（独立 /api/icloud 分组 + cookie 登录 + MongoDB 存储）。
	if s.icloud != nil {
		s.icloud.Register(engine)
	}

	// Serve the built frontend (web/dist) as a single-page app. API routes are
	// registered above; any other path falls back to index.html so the React
	// router can handle client-side navigation.
	mountStatic(engine)

	return engine
}

// mountStatic serves web/dist when it exists (production build). During
// frontend development the Vite dev server (with /api proxy) is used instead,
// so a missing dist directory is fine.
func mountStatic(engine *gin.Engine) {
	dist := os.Getenv("GPT_GO_WEB_DIST")
	if dist == "" {
		dist = filepath.Join("web", "dist")
	}
	index := filepath.Join(dist, "index.html")
	if _, err := os.Stat(index); err != nil {
		return // no built frontend present
	}
	// 带内容 hash 的静态资产(/assets/index-<hash>.js)可长缓存:hash 变即新文件,不怕旧缓存。
	engine.Static("/assets", filepath.Join(dist, "assets"))
	engine.NoRoute(func(c *gin.Context) {
		// iCloud 模块未知接口:返回该模块统一的 {success:false,code} 包壳。
		if strings.HasPrefix(c.Request.URL.Path, "/api/icloud/") {
			icloudhttp.HandleNotFound(c)
			return
		}
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.JSON(http.StatusNotFound, gin.H{"detail": gin.H{"code": "not_found", "message": "接口不存在"}})
			return
		}
		// index.html 不缓存:它引用带 hash 的 js,html 一旦拿到最新版,就会去拉对应的新 hash js,
		// 从此避免「前端已重新构建但浏览器还在用旧页面」的问题(用户无需手动强刷)。
		c.Header("Cache-Control", "no-cache, must-revalidate")
		c.File(index)
	})
}

// health implements GET /api/health, mirroring main.py health():
//
//	status == "ok" when mongodb.status == "online", otherwise "degraded".
func (s *Server) health(c *gin.Context) {
	health := s.mongo.Health()
	status := "degraded"
	if health.Status == mongo.StatusOnline {
		status = "ok"
	}
	c.JSON(http.StatusOK, model.HealthResponse{
		Status:  status,
		Mode:    "local",
		MongoDB: health,
	})
}

// StartBackground launches the sentinel version-check loop. It mirrors main.py
// startup: start the scheduler, then stop it if the stored config is disabled.
func (s *Server) StartBackground() {
	// Start unconditionally, then honor the enabled flag (mirrors main.py:326-333).
	s.sentinelSched.Start()
	cfg, err := s.sentinelSched.GetConfig(context.Background())
	if err != nil || !cfg.Enabled {
		s.sentinelSched.Stop()
	}
	// iCloud 模块后台协程（调度器/租约回收/数据库维护/邮件回填/保活/监听）。
	if s.icloud != nil && os.Getenv("GPT_GO_ICLOUD_DISABLE_BG") == "" {
		s.icloud.StartBackground(context.Background())
	}
}

// StopBackground stops the sentinel version-check loop (graceful shutdown).
func (s *Server) StopBackground() {
	s.sentinelSched.Stop()
	// 释放注册引擎持有的 V8 Isolate（v8go 对象不被 Go GC 回收，需显式 Close 防泄漏）。
	if s.signup != nil {
		s.signup.Close()
	}
	// 关闭 iCloud 模块的 MongoDB 连接。
	if s.icloud != nil {
		s.icloud.Close(context.Background())
	}
}
