// Package httpapi 是 iCloud 隐私邮箱模块的 HTTP 层，基于 gin 框架实现。
// 全部 handler 逻辑逐行复刻自原项目 iCloud-Privacy-Mail-v2/internal/httpapi
// 的 net/http ServeMux 实现，仅将路由与请求/响应处理迁移到 gin。
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-go/internal/icloud/apple"
	"gpt-go/internal/icloud/auth"
	"gpt-go/internal/icloud/buildinfo"
	"gpt-go/internal/icloud/config"
	"gpt-go/internal/icloud/domain"
	mailboxservice "gpt-go/internal/icloud/mailbox"
	"gpt-go/internal/icloud/mailwatcher"
	"gpt-go/internal/icloud/protocol"
	"gpt-go/internal/icloud/scheduler"
	"gpt-go/internal/icloud/serverchan"
	"gpt-go/internal/icloud/store"
	"gpt-go/internal/icloud/updatecheck"
)

// adminContextKey 是 protected 中间件写入 gin.Context 的管理员键。
// 复刻自原 net/http 实现的 adminContextKey（原写入 request context）。
const adminContextKey = "admin"

// appleKeepAliveTarget 复刻自原 net/http 实现。
type appleKeepAliveTarget struct {
	CheckedAt time.Time
	NextAt    time.Time
	Interval  time.Duration
}

// Server 字段布局与原 server.go 相同（去掉 mux，gin 路由由 Register 注册）。
type Server struct {
	cfg                 config.Config
	runtimeCtx          context.Context
	store               *store.Store
	auth                *auth.Service
	apple               *apple.Service
	mailbox             *mailboxservice.Service
	scheduler           *scheduler.Service
	watcher             *mailwatcher.Service
	updates             *updatecheck.Service
	serverChan          serverchan.Sender
	log                 *slog.Logger
	keepAliveMu         sync.RWMutex
	keepAliveNextAt     time.Time
	keepAliveInterval   time.Duration
	keepAliveState      func(context.Context, domain.LoginState) (domain.LoginState, error)
	keepAliveTargets    map[string]appleKeepAliveTarget
	keepAliveIntervalFn func(time.Duration, int) time.Duration
}

// New 组装全部 service，复刻自原 net/http 实现的 New。
func New(cfg config.Config, state *store.Store, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{
		cfg:              cfg,
		runtimeCtx:       context.Background(),
		store:            state,
		auth:             auth.NewService(state, time.Duration(cfg.SessionTTLHours)*time.Hour),
		apple:            apple.NewService(cfg, state),
		mailbox:          mailboxservice.NewService(cfg, state),
		updates:          updatecheck.New(cfg.UpdateEnabled, cfg.UpdateRepository),
		serverChan:       serverchan.NewClient(),
		log:              logger,
		keepAliveTargets: make(map[string]appleKeepAliveTarget),
	}
	state.SetChangeLogLimit(cfg.DatabaseChangeLogLimit)
	s.scheduler = scheduler.NewService(state, s.mailbox)
	s.watcher = mailwatcher.NewService(cfg, state, s.mailbox, logger)
	s.keepAliveState = s.apple.KeepAliveState
	s.keepAliveIntervalFn = randomAppleKeepAliveInterval
	return s
}

// Auth 暴露模块的鉴权服务,供主站做全局控制台门禁(整个 GPT-GO 共用一道登录)。
func (s *Server) Auth() *auth.Service {
	return s.auth
}

// Register 把全部原路由注册到 gin 分组。rg 由外部以 /api/icloud 创建，
// 因此这里注册的路径已去掉原 /api 前缀。复刻自原 routes()。
func (s *Server) Register(rg *gin.RouterGroup) {
	// 安全响应头中间件:复刻原 ServeHTTP 的四个头(nosniff/frame/referrer/no-store)。
	rg.Use(secureHeaders())

	// ---- 公开路由（不鉴权）----
	rg.GET("/health", s.handleHealth)
	rg.GET("/auth/status", s.handleAuthStatus)
	rg.POST("/auth/setup", s.handleAuthSetup)
	rg.POST("/auth/login", s.handleAuthLogin)

	// ---- 公共 API 子分组（/api/icloud/v1/...），复刻自原 p2.go 与 mailbox_lease.go ----
	v1 := rg.Group("/v1")
	{
		v1.GET("/health", s.handlePublicHealth)
		v1.POST("/mailboxes/claim", s.handlePublicClaimMailbox)
		v1.POST("/mailboxes/lookup", s.handlePublicLookupMailboxes)
		v1.GET("/mailboxes/:email/code", s.handlePublicMailboxCode)
		v1.POST("/mailboxes/:email/commit", s.handlePublicMailboxLeaseCommitCompat)
		v1.POST("/mailboxes/:email/release", s.handlePublicMailboxLeaseReleaseCompat)
		v1.POST("/mailboxes/:email/renew", s.handlePublicMailboxLeaseRenewCompat)
		v1.GET("/mailbox-leases/:lease_id", s.handlePublicMailboxLease)
		v1.POST("/mailbox-leases/:lease_id/commit", s.handlePublicMailboxLeaseCommit)
		v1.POST("/mailbox-leases/:lease_id/release", s.handlePublicMailboxLeaseRelease)
		v1.POST("/mailbox-leases/:lease_id/renew", s.handlePublicMailboxLeaseRenew)
		v1.POST("/mailbox-leases/:lease_id/note", s.handlePublicMailboxLeaseNote)
		v1.GET("/public-code/status", s.handlePublicCodePageStatus)
		v1.GET("/public-code", s.handlePublicCodePageLookup)
		v1.GET("/public-code/messages", s.handlePublicCodePageMessages)
		v1.GET("/public-code/messages/:messageID", s.handlePublicCodePageMessage)
	}

	// ---- session 鉴权分组（protected middleware），复刻自原 protected 包装的全部路由 ----
	protected := rg.Group("", s.protected())
	{
		protected.POST("/auth/logout", s.handleAuthLogout)
		protected.GET("/dashboard", s.handleDashboard)
		protected.GET("/apple-accounts", s.handleAppleAccounts)
		protected.GET("/apple-accounts/:id", s.handleAppleAccount)
		protected.DELETE("/apple-accounts/:id", s.handleAppleAccountDelete)
		protected.POST("/apple-accounts/login/start", s.handleAppleLoginStart)
		protected.POST("/apple-accounts/login/2fa", s.handleAppleLogin2FA)
		protected.POST("/apple-accounts/:id/check", s.handleAppleAccountCheck)
		protected.POST("/apple-accounts/:id/imap", s.handleAppleAccountSaveIMAP)
		protected.POST("/apple-accounts/:id/mailboxes", s.handleCreatePrivacyMailbox)
		protected.POST("/apple-accounts/:id/mailboxes/sync", s.handleSyncPrivacyMailboxes)
		protected.GET("/apple-mail/cleanup/status", s.handleAppleMailCleanupStatus)
		protected.POST("/apple-mail/cleanup", s.handleAppleMailCleanupStart)
		protected.POST("/apple-mail/cleanup/cancel", s.handleAppleMailCleanupCancel)
		protected.GET("/mailboxes", s.handleMailboxes)
		protected.POST("/mailboxes", s.handleImportMailbox)
		protected.POST("/mailboxes/resolve", s.handleMailboxResolve)
		protected.GET("/mailboxes/sync-messages/status", s.handleExistingMailboxMessagesSyncStatus)
		protected.POST("/mailboxes/sync-messages", s.handleExistingMailboxMessagesSync)
		protected.POST("/mailboxes/remote-clean", s.handleMailboxesRemoteClean)
		protected.GET("/mailboxes/:id", s.handleMailbox)
		protected.POST("/mailboxes/:id/status", s.handleMailboxStatus)
		protected.POST("/mailboxes/:id/sync", s.handleMailboxSync)
		protected.POST("/mailboxes/:id/remote-clean", s.handleMailboxRemoteClean)
		protected.DELETE("/mailboxes/:id", s.handleMailboxDelete)
		protected.GET("/mailboxes/:id/messages", s.handleMailboxMessages)
		protected.GET("/mailboxes/:id/messages/:messageID", s.handleMailboxMessage)
		protected.GET("/mailboxes/:id/code", s.handleMailboxCode)
		protected.GET("/tasks", s.handleTasks)
		protected.GET("/settings", s.handleSettings)
		protected.PUT("/settings", s.handleSaveSettings)
		protected.POST("/server-chan/test", s.handleServerChanTest)
		protected.GET("/update/status", s.handleUpdateStatus)
		protected.GET("/create-settings", s.handleCreateSettings)
		protected.PUT("/create-settings", s.handleSaveCreateSettings)
		protected.GET("/events", s.handleEvents)
		protected.GET("/realtime", s.handleRealtime)
		protected.GET("/database/status", s.handleDatabaseStatus)
		protected.POST("/database/backup", s.handleDatabaseBackup)
		protected.POST("/database/check", s.handleDatabaseCheck)
		protected.POST("/database/optimize", s.handleDatabaseOptimize)
		protected.POST("/events/clear", s.handleClearEvents)
		protected.GET("/runtime/export", s.handleExportRuntime)
		protected.GET("/runtime/export-mailbox-apis", s.handleExportMailboxAPIs)
		protected.GET("/runtime/export-mailbox-emails", s.handleExportMailboxEmails)
		protected.GET("/scheduler/status", s.handleSchedulerStatus)
		protected.POST("/scheduler/start", s.handleSchedulerStart)
		protected.POST("/scheduler/stop", s.handleSchedulerStop)
		protected.POST("/scheduler/logs/clear", s.handleSchedulerClearLogs)
	}
}

// HandleNotFound 复刻原 handleUnknownAPI:未知 /api/icloud/* 返回本模块统一包壳,
// 供主 engine 的 NoRoute 在识别到 icloud 前缀时调用(避免返回主站 {detail} 风格)。
func HandleNotFound(c *gin.Context) {
	writeError(c, http.StatusNotFound, "api_not_found", "接口不存在")
}

// secureHeaders 复刻原 ServeHTTP 的安全响应头中间件。
func secureHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Cache-Control", "no-store")
		c.Next()
	}
}

// StartBackground 启动与 HTTP 服务共用生命周期的后台任务。复刻自原实现。
func (s *Server) StartBackground(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.runtimeCtx = ctx
	s.scheduler.Resume(ctx)
	go s.runMailboxLeaseReaper(ctx)
	go s.runDatabaseMaintenance(ctx)
	go s.runMessageContentBackfill(ctx)
	s.startAccountLoginStateNotifications(ctx)
	if s.cfg.AppleAccountKeepAliveEnabled {
		go s.runAppleKeepAlive(ctx)
	}
	if s.cfg.MailWatcherEnabled {
		go s.watcher.Run(ctx)
	}
}

// runMessageContentBackfill 复刻自原 net/http 实现。
func (s *Server) runMessageContentBackfill(ctx context.Context) {
	result, err := s.mailbox.BackfillMessageContent(ctx)
	if result.Total == 0 {
		return
	}
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn("旧邮件完整正文补全未全部完成", "总数", result.Total, "已补全", result.Updated, "失败", result.Failed, "错误", err)
		}
		return
	}
	s.log.Info("旧邮件完整正文补全完成", "总数", result.Total, "已补全", result.Updated)
}

// runMailboxLeaseReaper 复刻自原 net/http 实现。
func (s *Server) runMailboxLeaseReaper(ctx context.Context) {
	interval := time.Duration(s.cfg.PublicMailboxLeaseSweepSeconds) * time.Second
	if interval < 5*time.Second {
		interval = 30 * time.Second
	}
	reap := func() {
		count, err := s.store.ExpireMailboxLeases(time.Now())
		if err != nil {
			s.log.Warn("回收过期邮箱租约失败", "错误", err)
			return
		}
		if count > 0 {
			s.log.Info("已回收过期邮箱租约", "数量", count)
		}
	}
	reap()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reap()
		}
	}
}

// runAppleKeepAlive 复刻自原 net/http 实现。
func (s *Server) runAppleKeepAlive(ctx context.Context) {
	baseInterval := time.Duration(s.cfg.AppleAccountKeepAliveMS) * time.Millisecond
	if baseInterval <= 0 {
		baseInterval = 3 * time.Minute
	}
	scanInterval := appleKeepAliveScanInterval(baseInterval)
	defer s.setAppleKeepAliveSchedule(time.Time{}, 0)
	s.log.Info("Apple 登录态保活已启动", "基础间隔", baseInterval, "扫描间隔", scanInterval, "每轮随机", fmt.Sprintf("±%d%%", s.cfg.AppleAccountKeepAliveJitterPercent))
	s.keepAliveAppleRound(ctx, baseInterval)
	ticker := time.NewTicker(scanInterval)
	defer ticker.Stop()
	for {
		s.setAppleKeepAliveSchedule(time.Now().Add(scanInterval), baseInterval)
		select {
		case <-ctx.Done():
			s.log.Info("Apple 登录态保活已停止")
			return
		case <-ticker.C:
			s.keepAliveAppleRound(ctx, baseInterval)
		}
	}
}

// appleKeepAliveScanInterval 复刻自原 net/http 实现。
func appleKeepAliveScanInterval(base time.Duration) time.Duration {
	if base <= 0 {
		base = 3 * time.Minute
	}
	interval := base / 4
	if interval > 30*time.Second {
		return 30 * time.Second
	}
	if interval < 5*time.Second {
		return 5 * time.Second
	}
	return interval
}

// keepAliveAppleRound 复刻自原 net/http 实现。
func (s *Server) keepAliveAppleRound(ctx context.Context, baseInterval time.Duration) {
	if ctx.Err() != nil || !s.store.Settings().EnableAppleKeepAlive {
		return
	}
	keepAliveState := s.keepAliveState
	if keepAliveState == nil {
		keepAliveState = s.apple.KeepAliveState
	}
	now := time.Now()
	for _, session := range s.store.ICloudSessions() {
		if ctx.Err() != nil {
			return
		}
		state, ok := protocol.LoginStateForKind(session, domain.LoginStateAppleAccount)
		if !ok || !appleKeepAliveEligible(state) {
			continue
		}
		nextAt, _ := s.appleKeepAliveTargetForSession(session, state, baseInterval)
		if now.Before(nextAt) {
			continue
		}
		accountLabel := strings.TrimSpace(session.AppleID)
		if accountLabel == "" {
			accountLabel = session.AccountID
		}
		s.recordAppleKeepAliveEvent("info", fmt.Sprintf("开始 Apple 登录态保活：%s", accountLabel))
		callCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
		next, err := keepAliveState(callCtx, state)
		cancel()
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			code, _, _ := protocol.ErrorDetails(err)
			if code == "apple_account_auth_failed" {
				s.clearAppleKeepAliveTarget(session)
				state.LastCheckedAt = time.Now()
				state.LastCheckOK = false
				state.LastStatusMessage = "Apple Account 登录态已失效：" + err.Error()
				session = protocol.WithLoginState(session, state)
				message := fmt.Sprintf("Apple 登录态保活失败：%s；登录态已失效，需要重新登录；%s", accountLabel, err.Error())
				if _, saveErr := s.store.SaveICloudSessionWithEvent(session, "error", message); saveErr != nil {
					s.log.Warn("保存 Apple 登录态失效结果失败", "账号", session.AccountID, "错误", saveErr)
				}
			} else {
				s.recordAppleKeepAliveEvent("warning", fmt.Sprintf("Apple 登录态保活临时失败：%s；%s", accountLabel, err.Error()))
			}
			s.log.Warn("Apple 登录态保活失败", "账号", session.AccountID, "Apple ID", session.AppleID, "错误", err)
			continue
		}
		next.Kind = domain.LoginStateAppleAccount
		next.LastCheckedAt = time.Now()
		next.LastCheckOK = true
		next.LastStatusMessage = "Apple Account 登录态保活成功"
		session = protocol.WithLoginState(session, next)
		nextInterval := s.resetAppleKeepAliveTarget(session, next, baseInterval)
		message := fmt.Sprintf("Apple 登录态保活成功：%s；下次目标间隔 %s", accountLabel, nextInterval.Round(time.Second))
		if _, err := s.store.SaveICloudSessionWithEvent(session, "info", message); err != nil {
			s.log.Warn("保存 Apple 登录态保活结果失败", "账号", session.AccountID, "错误", err)
			continue
		}
		s.log.Info("Apple 登录态保活成功", "账号", session.AccountID, "Apple ID", session.AppleID, "下次目标间隔", nextInterval)
	}
}

// recordAppleKeepAliveEvent 复刻自原 net/http 实现。
func (s *Server) recordAppleKeepAliveEvent(level, message string) {
	if err := s.store.RecordEvent(level, "apple", message); err != nil {
		s.log.Warn("保存 Apple 登录态保活运行记录失败", "错误", err)
	}
}

// appleKeepAliveEligible 复刻自原 net/http 实现。
func appleKeepAliveEligible(state domain.LoginState) bool {
	if strings.TrimSpace(state.Scnt) == "" || strings.TrimSpace(state.APIKey) == "" || len(state.Cookies) == 0 {
		return false
	}
	return state.LastCheckedAt.IsZero() || state.LastCheckOK
}

// randomAppleKeepAliveInterval 复刻自原 net/http 实现。
func randomAppleKeepAliveInterval(base time.Duration, jitterPercent int) time.Duration {
	if base <= 0 {
		base = 3 * time.Minute
	}
	if jitterPercent <= 0 {
		return base
	}
	if jitterPercent > 50 {
		jitterPercent = 50
	}
	spread := int64(base) * int64(jitterPercent) / 100
	if spread <= 0 {
		return base
	}
	offset := rand.Int64N(spread*2+1) - spread
	interval := base + time.Duration(offset)
	if interval < 30*time.Second {
		return 30 * time.Second
	}
	return interval
}

// appleKeepAliveTargetForSession 复刻自原 net/http 实现。
func (s *Server) appleKeepAliveTargetForSession(session domain.ICloudSession, state domain.LoginState, baseInterval time.Duration) (time.Time, time.Duration) {
	key := appleKeepAliveSessionKey(session)
	s.keepAliveMu.Lock()
	defer s.keepAliveMu.Unlock()
	target, ok := s.keepAliveTargets[key]
	if ok && target.CheckedAt.Equal(state.LastCheckedAt) {
		return target.NextAt, target.Interval
	}
	interval := s.nextAppleKeepAliveInterval(baseInterval)
	nextAt := time.Now()
	if !state.LastCheckedAt.IsZero() {
		nextAt = state.LastCheckedAt.Add(interval)
	}
	target = appleKeepAliveTarget{CheckedAt: state.LastCheckedAt, NextAt: nextAt, Interval: interval}
	s.keepAliveTargets[key] = target
	return target.NextAt, target.Interval
}

// resetAppleKeepAliveTarget 复刻自原 net/http 实现。
func (s *Server) resetAppleKeepAliveTarget(session domain.ICloudSession, state domain.LoginState, baseInterval time.Duration) time.Duration {
	interval := s.nextAppleKeepAliveInterval(baseInterval)
	s.keepAliveMu.Lock()
	s.keepAliveTargets[appleKeepAliveSessionKey(session)] = appleKeepAliveTarget{
		CheckedAt: state.LastCheckedAt,
		NextAt:    state.LastCheckedAt.Add(interval),
		Interval:  interval,
	}
	s.keepAliveMu.Unlock()
	return interval
}

// clearAppleKeepAliveTarget 复刻自原 net/http 实现。
func (s *Server) clearAppleKeepAliveTarget(session domain.ICloudSession) {
	s.keepAliveMu.Lock()
	delete(s.keepAliveTargets, appleKeepAliveSessionKey(session))
	s.keepAliveMu.Unlock()
}

// nextAppleKeepAliveInterval 复刻自原 net/http 实现。
func (s *Server) nextAppleKeepAliveInterval(baseInterval time.Duration) time.Duration {
	intervalFn := s.keepAliveIntervalFn
	if intervalFn == nil {
		intervalFn = randomAppleKeepAliveInterval
	}
	return intervalFn(baseInterval, s.cfg.AppleAccountKeepAliveJitterPercent)
}

// appleKeepAliveSessionKey 复刻自原 net/http 实现。
func appleKeepAliveSessionKey(session domain.ICloudSession) string {
	if accountID := strings.TrimSpace(session.AccountID); accountID != "" {
		return accountID
	}
	if appleID := strings.ToLower(strings.TrimSpace(session.AppleID)); appleID != "" {
		return appleID
	}
	return "default"
}

// setAppleKeepAliveSchedule 复刻自原 net/http 实现。
func (s *Server) setAppleKeepAliveSchedule(nextAt time.Time, interval time.Duration) {
	s.keepAliveMu.Lock()
	s.keepAliveNextAt = nextAt
	s.keepAliveInterval = interval
	s.keepAliveMu.Unlock()
}

// appleKeepAliveSchedule 复刻自原 net/http 实现。
func (s *Server) appleKeepAliveSchedule() (time.Time, time.Duration) {
	s.keepAliveMu.RLock()
	defer s.keepAliveMu.RUnlock()
	return s.keepAliveNextAt, s.keepAliveInterval
}

// protected 是 session 鉴权的 gin middleware，复刻自原 protected 包装逻辑：
// 读 auth.CookieName cookie → s.auth.Authenticate → 失败 401 {success:false,code:auth_required}。
func (s *Server) protected() gin.HandlerFunc {
	return func(c *gin.Context) {
		cookie, err := c.Request.Cookie(auth.CookieName)
		if err != nil || strings.TrimSpace(cookie.Value) == "" {
			writeError(c, http.StatusUnauthorized, "auth_required", "请先登录")
			c.Abort()
			return
		}
		admin, ok := s.auth.Authenticate(cookie.Value)
		if !ok {
			writeError(c, http.StatusUnauthorized, "auth_required", "登录态已失效，请重新登录")
			c.Abort()
			return
		}
		// 复刻原 context.WithValue(adminContextKey, admin)，改为 gin 的 c.Set。
		c.Set(adminContextKey, admin)
		c.Next()
	}
}

// handleHealth 复刻自原 net/http 实现。
func (s *Server) handleHealth(c *gin.Context) {
	current := buildinfo.Current()
	writeJSON(c, http.StatusOK, map[string]any{
		"success": true,
		"data": map[string]any{
			"status":  "ok",
			"version": current.Version,
			"commit":  current.Commit,
		},
	})
}

// handleAuthStatus 复刻自原 net/http 实现。
func (s *Server) handleAuthStatus(c *gin.Context) {
	data := map[string]any{"setup_required": false, "authenticated": false}
	if _, ok := s.store.Admin(); !ok {
		data["setup_required"] = true
		writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": data})
		return
	}
	if cookie, err := c.Request.Cookie(auth.CookieName); err == nil {
		if admin, ok := s.auth.Authenticate(cookie.Value); ok {
			data["authenticated"] = true
			data["admin"] = publicAdmin(admin)
		}
	}
	writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": data})
}

// handleAuthSetup 复刻自原 net/http 实现。
func (s *Server) handleAuthSetup(c *gin.Context) {
	var body credentialPayload
	if err := decodeJSON(c, &body); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	result, err := s.auth.Setup(body.Username, body.Password)
	if err != nil {
		writeError(c, http.StatusBadRequest, "setup_failed", err.Error())
		return
	}
	s.setSessionCookie(c, result)
	s.notifyAdminLogin(c, result.Admin)
	writeJSON(c, http.StatusCreated, map[string]any{"success": true, "data": map[string]any{"admin": publicAdmin(result.Admin)}})
}

// handleAuthLogin 复刻自原 net/http 实现。
func (s *Server) handleAuthLogin(c *gin.Context) {
	var body credentialPayload
	if err := decodeJSON(c, &body); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	result, err := s.auth.Login(body.Username, body.Password)
	if err != nil {
		writeError(c, http.StatusUnauthorized, "login_failed", err.Error())
		return
	}
	s.setSessionCookie(c, result)
	s.notifyAdminLogin(c, result.Admin)
	writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": map[string]any{"admin": publicAdmin(result.Admin)}})
}

// handleAuthLogout 复刻自原 net/http 实现。
func (s *Server) handleAuthLogout(c *gin.Context) {
	if cookie, err := c.Request.Cookie(auth.CookieName); err == nil {
		if err := s.auth.Logout(cookie.Value); err != nil {
			s.log.Warn("退出登录态清理失败", "错误", err)
		}
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     auth.CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	writeJSON(c, http.StatusOK, map[string]any{"success": true})
}

// handleDashboard 复刻自原 net/http 实现。
func (s *Server) handleDashboard(c *gin.Context) {
	writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": s.store.Dashboard()})
}

// handleAppleAccounts 复刻自原 net/http 实现。
func (s *Server) handleAppleAccounts(c *gin.Context) {
	writeJSON(c, http.StatusOK, map[string]any{
		"success": true,
		"data": map[string]any{
			"items":        s.apple.Accounts(),
			"module_ready": true,
		},
	})
}

// handleAppleAccount 复刻自原 net/http 实现。原 {id} → :id，取参用 c.Param("id")。
func (s *Server) handleAppleAccount(c *gin.Context) {
	account, err := s.apple.Account(c.Param("id"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": map[string]any{"account": account}})
}

// handleAppleAccountDelete 复刻自原 net/http 实现。
func (s *Server) handleAppleAccountDelete(c *gin.Context) {
	accountID := strings.TrimSpace(c.Param("id"))
	state := s.scheduler.Snapshot()
	if state.Running {
		for _, runningAccountID := range state.AccountIDs {
			if runningAccountID == accountID {
				writeError(c, http.StatusConflict, "scheduler_running", "该账号正在参与自动创建，请先停止定时创建后再删除")
				return
			}
		}
	}
	deleted, err := s.store.DeleteAppleAccount(accountID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": map[string]any{"deleted": deleted}})
}

// handleAppleLoginStart 复刻自原 net/http 实现。
func (s *Server) handleAppleLoginStart(c *gin.Context) {
	var body struct {
		Flow            protocol.AuthFlow `json:"flow"`
		AppleID         string            `json:"apple_id"`
		Password        string            `json:"password"`
		TwoFactorMethod string            `json:"two_factor_method"`
	}
	if err := decodeJSON(c, &body); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	result, err := s.apple.StartLogin(c.Request.Context(), apple.LoginRequest{
		Flow: body.Flow, AppleID: body.AppleID, Password: body.Password, TwoFactorMethod: body.TwoFactorMethod,
	})
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": result})
}

// handleAppleLogin2FA 复刻自原 net/http 实现。
func (s *Server) handleAppleLogin2FA(c *gin.Context) {
	var body struct {
		PendingID   string          `json:"pending_id"`
		Code        string          `json:"code"`
		PhoneNumber json.RawMessage `json:"phone_number,omitempty"`
	}
	if err := decodeJSON(c, &body); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	result, err := s.apple.Submit2FA(c.Request.Context(), body.PendingID, body.Code, body.PhoneNumber)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": result})
}

// handleAppleAccountCheck 复刻自原 net/http 实现。
func (s *Server) handleAppleAccountCheck(c *gin.Context) {
	account, err := s.apple.Check(c.Request.Context(), c.Param("id"))
	if err != nil {
		writeJSON(c, http.StatusBadGateway, map[string]any{"success": false, "code": "session_check_failed", "message": err.Error(), "data": map[string]any{"account": account}})
		return
	}
	writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": map[string]any{"account": account}})
}

// handleAppleAccountSaveIMAP 复刻自原 net/http 实现。
func (s *Server) handleAppleAccountSaveIMAP(c *gin.Context) {
	var body struct {
		Email       string `json:"email"`
		AppPassword string `json:"app_password"`
	}
	if err := decodeJSON(c, &body); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	account, err := s.apple.SaveIMAP(c.Request.Context(), c.Param("id"), body.Email, body.AppPassword)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	s.watcher.Wake("")
	writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": map[string]any{"account": account}})
}

// handleCreatePrivacyMailbox 复刻自原 net/http 实现。
func (s *Server) handleCreatePrivacyMailbox(c *gin.Context) {
	var body struct {
		Label   string `json:"label"`
		Note    string `json:"note"`
		Channel string `json:"channel"`
	}
	if err := decodeJSON(c, &body); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	accountID := c.Param("id")
	mailbox, err := s.mailbox.Create(c.Request.Context(), accountID, body.Label, body.Note, body.Channel)
	if err != nil {
		s.scheduler.RecordManualFailure(accountID, body.Label, err)
		writeServiceError(c, err)
		return
	}
	schedulerState := s.scheduler.RecordManualSuccess(accountID, mailbox)
	mailbox.APIToken = ""
	writeJSON(c, http.StatusCreated, map[string]any{"success": true, "data": map[string]any{"mailbox": mailbox, "scheduler": schedulerState}})
}

// handleSyncPrivacyMailboxes 复刻自原 net/http 实现。
func (s *Server) handleSyncPrivacyMailboxes(c *gin.Context) {
	items, err := s.mailbox.SyncRemote(c.Request.Context(), c.Param("id"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	for i := range items {
		items[i].APIToken = ""
	}
	writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": map[string]any{"items": items, "count": len(items)}})
}

// handleMailboxes 复刻自原 net/http 实现。
func (s *Server) handleMailboxes(c *gin.Context) {
	query := c.Request.URL.Query()
	page := parsePositiveInt(query.Get("page"), 1)
	pageSize := parsePositiveInt(query.Get("page_size"), 0)
	result := s.store.Mailboxes(query.Get("q"), query.Get("status"), query.Get("account_id"), page, pageSize)
	writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": result})
}

// handleMailboxResolve 复刻自原 net/http 实现。
func (s *Server) handleMailboxResolve(c *gin.Context) {
	var body struct {
		Emails []string `json:"emails"`
	}
	if err := decodeJSON(c, &body); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	type resolvedMailbox struct {
		ID        string `json:"id"`
		Email     string `json:"email"`
		AccountID string `json:"account_id"`
	}
	items := make([]resolvedMailbox, 0, len(body.Emails))
	missing := make([]string, 0)
	seen := make(map[string]struct{}, len(body.Emails))
	for _, value := range body.Emails {
		email := strings.ToLower(strings.TrimSpace(value))
		if email == "" {
			continue
		}
		if _, exists := seen[email]; exists {
			continue
		}
		seen[email] = struct{}{}
		mailbox, ok := s.store.FindMailboxByEmail(email)
		if !ok {
			missing = append(missing, email)
			continue
		}
		items = append(items, resolvedMailbox{ID: mailbox.ID, Email: mailbox.Email, AccountID: mailbox.AccountID})
	}
	if len(seen) == 0 {
		writeError(c, http.StatusBadRequest, "email_list_empty", "请至少输入一个邮箱地址")
		return
	}
	writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": map[string]any{"items": items, "missing": missing}})
}

// handleMailbox 复刻自原 net/http 实现。
func (s *Server) handleMailbox(c *gin.Context) {
	mailbox, ok := s.store.FindMailboxByID(c.Param("id"))
	if !ok {
		writeError(c, http.StatusNotFound, "mailbox_not_found", "邮箱不存在")
		return
	}
	writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": map[string]any{"mailbox": mailbox}})
}

// handleMailboxStatus 复刻自原 net/http 实现。
func (s *Server) handleMailboxStatus(c *gin.Context) {
	var body struct {
		APIActive    *bool   `json:"api_active"`
		ICloudActive *bool   `json:"icloud_active"`
		Status       string  `json:"status"`
		Note         *string `json:"note"`
	}
	if err := decodeJSON(c, &body); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if body.Status != "" && !validMailboxStatus(body.Status) {
		writeError(c, http.StatusBadRequest, "invalid_status", "邮箱状态不正确")
		return
	}
	mailbox, err := s.store.SetMailboxStatus(c.Param("id"), body.APIActive, body.ICloudActive, body.Status, body.Note)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	mailbox.APIToken = ""
	writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": map[string]any{"mailbox": mailbox}})
}

// handleMailboxSync 复刻自原 net/http 实现。
func (s *Server) handleMailboxSync(c *gin.Context) {
	result, err := s.mailbox.SyncMailboxMessages(c.Request.Context(), c.Param("id"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	data := struct {
		mailboxservice.MailboxMessageSyncBatchResult
		Synced int `json:"synced"`
	}{MailboxMessageSyncBatchResult: result, Synced: result.SyncedMessages}
	writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": data})
}

// handleExistingMailboxMessagesSync 复刻自原 net/http 实现。
func (s *Server) handleExistingMailboxMessagesSync(c *gin.Context) {
	job, err := s.mailbox.StartExistingMailboxMessageSync(s.runtimeCtx)
	if err != nil {
		if strings.Contains(err.Error(), "正在运行") {
			writeError(c, http.StatusConflict, "message_sync_running", err.Error())
			return
		}
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusAccepted, map[string]any{"success": true, "data": map[string]any{"job": job}})
}

// handleExistingMailboxMessagesSyncStatus 复刻自原 net/http 实现。
func (s *Server) handleExistingMailboxMessagesSyncStatus(c *gin.Context) {
	writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": map[string]any{"job": s.mailbox.ExistingMailboxMessageSyncStatus()}})
}

// handleImportMailbox 复刻自原 net/http 实现。
func (s *Server) handleImportMailbox(c *gin.Context) {
	var body struct {
		AccountID string `json:"account_id"`
		Email     string `json:"email"`
		Label     string `json:"label"`
		Note      string `json:"note"`
	}
	if err := decodeJSON(c, &body); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	mailbox, created, err := s.mailbox.ImportLocal(body.AccountID, body.Email, body.Label, body.Note)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(c, status, map[string]any{"success": true, "data": map[string]any{"mailbox": mailbox, "created": created}})
}

// handleMailboxRemoteClean 复刻自原 net/http 实现。
func (s *Server) handleMailboxRemoteClean(c *gin.Context) {
	options := mailboxservice.RemoteCleanupOptions{MoveSynced: true, EmptyTrash: true}
	if c.Request.ContentLength != 0 {
		if err := decodeJSON(c, &options); err != nil {
			writeError(c, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
	}
	result, err := s.mailbox.CleanRemoteMessages(c.Request.Context(), c.Param("id"), options)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": map[string]any{"cleanup": result}})
}

// handleAppleMailCleanupStatus 复刻自原 net/http 实现。
func (s *Server) handleAppleMailCleanupStatus(c *gin.Context) {
	writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": map[string]any{"job": s.mailbox.AppleMailCleanupStatus()}})
}

// handleAppleMailCleanupStart 复刻自原 net/http 实现。
func (s *Server) handleAppleMailCleanupStart(c *gin.Context) {
	var body struct {
		AccountIDs []string `json:"account_ids"`
		Scope      string   `json:"scope"`
		Strategy   string   `json:"strategy"`
		PurgeLocal *bool    `json:"purge_local"`
	}
	if c.Request.ContentLength != 0 {
		if err := decodeJSON(c, &body); err != nil {
			writeError(c, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
	}
	purgeLocal := true
	if body.PurgeLocal != nil {
		purgeLocal = *body.PurgeLocal
	}
	job, err := s.mailbox.StartAppleMailCleanup(s.runtimeCtx, mailboxservice.AppleMailCleanupRequest{
		AccountIDs: body.AccountIDs,
		Scope:      body.Scope,
		Strategy:   body.Strategy,
		PurgeLocal: purgeLocal,
	})
	if err != nil {
		if strings.Contains(err.Error(), "正在运行") {
			writeError(c, http.StatusConflict, "apple_mail_cleanup_running", err.Error())
			return
		}
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusAccepted, map[string]any{"success": true, "data": map[string]any{"job": job}})
}

// handleAppleMailCleanupCancel 复刻自原 net/http 实现。
func (s *Server) handleAppleMailCleanupCancel(c *gin.Context) {
	job := s.mailbox.CancelAppleMailCleanup("已手动取消全部 Apple 邮件清理任务")
	writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": map[string]any{"job": job}})
}

// handleMailboxesRemoteClean 复刻自原 net/http 实现。
func (s *Server) handleMailboxesRemoteClean(c *gin.Context) {
	options := mailboxservice.RemoteCleanupOptions{MoveSynced: true, EmptyTrash: true}
	if c.Request.ContentLength != 0 {
		if err := decodeJSON(c, &options); err != nil {
			writeError(c, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
	}
	if strings.TrimSpace(options.AccountID) != "" {
		if _, ok := s.store.FindAppleAccount(options.AccountID); !ok {
			writeError(c, http.StatusNotFound, "account_not_found", "Apple 账号不存在")
			return
		}
	}
	result, err := s.mailbox.CleanRemoteMailboxes(c.Request.Context(), options)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": result})
}

// handleMailboxDelete 复刻自原 net/http 实现。
func (s *Server) handleMailboxDelete(c *gin.Context) {
	var err error
	if parseBool(c.Request.URL.Query().Get("local_only")) {
		err = s.mailbox.DeleteLocal(c.Param("id"))
	} else {
		err = s.mailbox.DeleteCompletely(c.Request.Context(), c.Param("id"))
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, map[string]any{"success": true})
}

// handleMailboxMessages 复刻自原 net/http 实现。
func (s *Server) handleMailboxMessages(c *gin.Context) {
	if _, ok := s.store.FindMailboxByID(c.Param("id")); !ok {
		writeError(c, http.StatusNotFound, "mailbox_not_found", "邮箱不存在")
		return
	}
	items := s.store.MessagesForMailbox(c.Param("id"))
	writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": map[string]any{"items": items}})
}

// handleMailboxMessage 复刻自原 net/http 实现。原 {messageID} → :messageID。
func (s *Server) handleMailboxMessage(c *gin.Context) {
	message, err := s.mailbox.MessageContent(c.Request.Context(), c.Param("id"), c.Param("messageID"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": map[string]any{"message": message}})
}

// handleMailboxCode 复刻自原 net/http 实现。
func (s *Server) handleMailboxCode(c *gin.Context) {
	after, err := parseRFC3339(c.Request.URL.Query().Get("after"))
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_after", err.Error())
		return
	}
	result, err := s.mailbox.Code(c.Request.Context(), c.Param("id"), after, c.Request.URL.Query().Get("keyword"), parseBool(c.Request.URL.Query().Get("allow_stale")))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": result})
}

// handleTasks 复刻自原 net/http 实现。
func (s *Server) handleTasks(c *gin.Context) {
	settings := s.store.Settings()
	schedulerState := s.scheduler.Snapshot()
	watcherSnapshot := s.watcher.Snapshot()
	watcherStatus := "idle"
	watcherDescription := "后台监听尚未开启"
	if !s.cfg.MailWatcherEnabled {
		watcherDescription = "config.json 已关闭后台邮件监听能力"
	} else if settings.EnableMailWatcher {
		switch {
		case !watcherSnapshot.Running:
			watcherStatus = "starting"
			watcherDescription = "后台监听正在启动"
		case watcherSnapshot.GroupCount == 0:
			watcherStatus = "waiting"
			watcherDescription = "已开启，但没有找到同时具备 IMAP 登录态和可用邮箱的账号"
		case watcherSnapshot.LastError != "":
			watcherStatus = "failed"
			watcherDescription = "IMAP 同步异常：" + watcherSnapshot.LastError
		case watcherSnapshot.ConnectedWorkerCount == 0 && watcherSnapshot.LastIdleError != "":
			watcherStatus = "failed"
			watcherDescription = "IMAP IDLE 连接异常：" + watcherSnapshot.LastIdleError
		default:
			watcherStatus = "running"
			watcherDescription = fmt.Sprintf("正在监听 %d 个账号分组，IDLE 已连接 %d/%d，已同步 %d 封邮件", watcherSnapshot.GroupCount, watcherSnapshot.ConnectedWorkerCount, watcherSnapshot.WorkerCount, watcherSnapshot.SyncedMessages)
		}
	}
	keepAliveStatus := "idle"
	if s.cfg.AppleAccountKeepAliveEnabled && settings.EnableAppleKeepAlive {
		keepAliveStatus = "running"
	}
	keepAliveNextAt, keepAliveInterval := s.appleKeepAliveSchedule()
	var keepAliveNextAtPointer *time.Time
	if keepAliveStatus == "running" && !keepAliveNextAt.IsZero() {
		nextAt := keepAliveNextAt
		keepAliveNextAtPointer = &nextAt
	}
	items := []domain.Task{
		{ID: "data-store", Name: "本地数据存储", Description: "保存账号、邮箱、设置和 Apple 登录态", Status: "completed", Progress: 100, Module: "data"},
		{ID: "apple-account", Name: "Apple 账号管理", Description: "Apple Account、iCloud Web、2FA 和登录态检测", Status: "completed", Progress: 100, Module: "apple"},
		{ID: "mailbox-create", Name: "隐私邮箱闭环", Description: "创建、同步、收信、取码、停用和 Apple 远程删除", Status: "completed", Progress: 100, Module: "mailbox"},
		{ID: "imap-watcher", Name: "后台邮件监听", Description: watcherDescription, Status: watcherStatus, Progress: 100, Module: "imap"},
		{ID: "apple-keepalive", Name: "Apple 登录态保活", Description: "周期刷新 Apple Account 管理态", Status: keepAliveStatus, Progress: 100, Module: "apple", NextRunAt: keepAliveNextAtPointer, ScheduledIntervalSeconds: int(keepAliveInterval.Seconds()), JitterPercent: s.cfg.AppleAccountKeepAliveJitterPercent},
		{ID: "scheduler", Name: "定时创建", Description: "按所选账号周期创建隐私邮箱", Status: schedulerState.Status, Progress: 100, Module: "scheduler"},
		{ID: "public-api", Name: "公共取号 API", Description: "健康检查、取号、查询、邮箱取码和 АРI keys", Status: enabledTaskStatus(settings.EnablePublicMailboxAPI), Progress: 100, Module: "api"},
		{ID: "export", Name: "本地数据导出", Description: "运行数据、邮箱地址和取码 API 导出", Status: "completed", Progress: 100, Module: "export"},
	}
	writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": map[string]any{"items": items, "scheduler": schedulerState}})
}

// enabledTaskStatus 复刻自原 net/http 实现。
func enabledTaskStatus(enabled bool) string {
	if enabled {
		return "running"
	}
	return "idle"
}

// handleSettings 复刻自原 net/http 实现。
// 差异说明：原 runtime 里的 database_backup_dir / database_backup_retention_count 字段
// 来自已删除的 sqlite 配置，这里改用 backupDir() 与常量 backupRetentionCount。
func (s *Server) handleSettings(c *gin.Context) {
	settings := s.serverChanSettings()
	apiKeySource := ""
	if strings.TrimSpace(settings.PublicAPIKey) != "" {
		apiKeySource = "system_settings"
	} else if strings.TrimSpace(s.cfg.APIKey) != "" {
		apiKeySource = "config"
	}
	writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"settings":   settings,
		"data_path":  s.store.Path(),
		"local_only": true,
		"runtime": map[string]any{
			"database_status":                  s.store.DatabaseStatus(),
			"database_backup_dir":              backupDir(),
			"database_backup_retention_count":  backupRetentionCount,
			"database_message_retention_days":  s.cfg.DatabaseMessageRetentionDays,
			"mail_watcher_available":           s.cfg.MailWatcherEnabled,
			"mail_watcher_poll_ms":             s.cfg.MailWatcherPollMS,
			"mail_watcher_web_poll_ms":         s.cfg.MailWatcherWebPollMS,
			"mail_watcher_fetch_limit":         s.cfg.MailWatcherFetchLimit,
			"mail_watcher_initial_fetch_limit": s.cfg.MailWatcherInitialFetchLimit,
			"mail_watcher_lookback_hours":      s.cfg.MailWatcherLookbackHours,
			"mail_watcher_status":              s.watcher.Snapshot(),
			"apple_keep_alive_available":       s.cfg.AppleAccountKeepAliveEnabled,
			"apple_keep_alive_ms":              s.cfg.AppleAccountKeepAliveMS,
			"apple_keep_alive_jitter_percent":  s.cfg.AppleAccountKeepAliveJitterPercent,
			"api_configured":                   s.globalAPIKey() != "",
			"api_key_source":                   apiKeySource,
			"config_api_key_configured":        strings.TrimSpace(s.cfg.APIKey) != "",
			"public_base_url":                  s.cfg.PublicBaseURL,
			"server_chan_configured":           strings.TrimSpace(settings.ServerChanSendKey) != "",
			"server_chan_send_key_masked":      maskServerChanSendKey(settings.ServerChanSendKey),
		},
	}})
}

// handleSaveSettings 复刻自原 net/http 实现。
func (s *Server) handleSaveSettings(c *gin.Context) {
	var body struct {
		domain.Settings
		ClearServerChanSendKey bool `json:"clear_server_chan_send_key"`
	}
	body.Settings = s.store.Settings()
	if err := decodeJSON(c, &body); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	settings := body.Settings
	if strings.TrimSpace(settings.ServerChanSendKey) == "" && !body.ClearServerChanSendKey {
		settings.ServerChanSendKey = s.serverChanSettings().ServerChanSendKey
	}
	if err := validateServerChanSettings(settings); err != nil {
		writeError(c, http.StatusBadRequest, "server_chan_settings_invalid", err.Error())
		return
	}
	saved, err := s.store.SaveSettings(settings)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "settings_save_failed", err.Error())
		return
	}
	s.watcher.Wake("")
	writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"settings": saved,
		"runtime": map[string]any{
			"server_chan_configured":      strings.TrimSpace(saved.ServerChanSendKey) != "",
			"server_chan_send_key_masked": maskServerChanSendKey(saved.ServerChanSendKey),
		},
	}})
}

// handleCreateSettings 复刻自原 net/http 实现。
func (s *Server) handleCreateSettings(c *gin.Context) {
	writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": map[string]any{"settings": s.store.CreateSettings()}})
}

// handleSaveCreateSettings 复刻自原 net/http 实现。
func (s *Server) handleSaveCreateSettings(c *gin.Context) {
	var settings domain.CreateSettings
	if err := decodeJSON(c, &settings); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	saved, err := s.store.SaveCreateSettings(settings)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": map[string]any{"settings": saved}})
}

// handleEvents 复刻自原 net/http 实现。
func (s *Server) handleEvents(c *gin.Context) {
	writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": map[string]any{"items": s.store.Dashboard().Events}})
}

// handleClearEvents 复刻自原 net/http 实现。
func (s *Server) handleClearEvents(c *gin.Context) {
	if err := s.store.ClearEvents(); err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": map[string]any{"items": []domain.Event{}}})
}

// setSessionCookie 复刻自原 net/http 实现（写 cookie 改用 c.Writer）。
func (s *Server) setSessionCookie(c *gin.Context, result auth.LoginResult) {
	maxAge := int(time.Until(result.ExpiresAt).Seconds())
	if maxAge < 1 {
		maxAge = 1
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     auth.CookieName,
		Value:    result.Token,
		Path:     "/",
		MaxAge:   maxAge,
		Expires:  result.ExpiresAt,
		HttpOnly: true,
		Secure:   s.cfg.SecureCookie,
		SameSite: http.SameSiteStrictMode,
	})
}

// credentialPayload 复刻自原 net/http 实现。
type credentialPayload struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// publicAdmin 复刻自原 net/http 实现。
func publicAdmin(admin domain.Admin) map[string]any {
	return map[string]any{
		"id":            admin.ID,
		"username":      admin.Username,
		"created_at":    formatTime(admin.CreatedAt),
		"last_login_at": formatTime(admin.LastLoginAt),
	}
}

// formatTime 复刻自原 net/http 实现。
func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339)
}

// decodeJSON 复刻自原 net/http 实现：保留「只能一个 JSON 对象」与 1MB 限制。
// gin 版改用 io.LimitReader(c.Request.Body, (1<<20)+1) + json.Decoder，与原版逐行一致。
func decodeJSON(c *gin.Context, target any) error {
	if c.Request.Body == nil {
		return errors.New("请求体为空")
	}
	decoder := json.NewDecoder(io.LimitReader(c.Request.Body, (1<<20)+1))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("请求体只能包含一个 JSON 对象")
	}
	return nil
}

// writeJSON 复刻自原 net/http 实现，响应包壳 {success:true,data:...} 由调用方组装。
func writeJSON(c *gin.Context, status int, value any) {
	c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	c.Writer.WriteHeader(status)
	_ = json.NewEncoder(c.Writer).Encode(value)
}

// writeError 复刻自原 net/http 实现：{success:false,code,message}。
func writeError(c *gin.Context, status int, code, message string) {
	writeJSON(c, status, map[string]any{"success": false, "code": code, "message": message})
}

// writeServiceError 复刻自原 net/http 实现：protocol.ErrorDetails，retryable→502，
// 含 not_found/不存在→404，否则 400。
func writeServiceError(c *gin.Context, err error) {
	code, message, retryable := protocol.ErrorDetails(err)
	status := http.StatusBadRequest
	if retryable {
		status = http.StatusBadGateway
	}
	if strings.Contains(strings.ToLower(code), "not_found") || strings.Contains(message, "不存在") {
		status = http.StatusNotFound
	}
	writeJSON(c, status, map[string]any{"success": false, "code": code, "message": message, "retryable": retryable})
}

// validMailboxStatus 复刻自原 net/http 实现。
func validMailboxStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case domain.StatusActive, domain.StatusAvailable, domain.StatusReserved, domain.StatusUsed, domain.StatusFailed, domain.StatusDisabled:
		return true
	default:
		return false
	}
}

// parseBool 复刻自原 net/http 实现。
func parseBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// parseRFC3339 复刻自原 net/http 实现。
func parseRFC3339(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, errors.New("after 必须是 RFC3339 时间")
	}
	return parsed, nil
}

// parsePositiveInt 复刻自原 net/http 实现。
func parsePositiveInt(value string, fallback int) int {
	var parsed int
	if _, err := fmt.Sscanf(strings.TrimSpace(value), "%d", &parsed); err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}
