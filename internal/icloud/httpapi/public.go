package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-go/internal/icloud/buildinfo"
	"gpt-go/internal/icloud/domain"
	mailboxservice "gpt-go/internal/icloud/mailbox"
	"gpt-go/internal/icloud/scheduler"
	"gpt-go/internal/icloud/store"
)

// publicCodePageContextKey 复刻自原 net/http 实现（原写入 request context，
// gin 版改为 c.Get/Set 同一个字符串键）。
const publicCodePageContextKey = "public-code-page"

// handlePublicHealth 复刻自原 net/http 实现。
func (s *Server) handlePublicHealth(c *gin.Context) {
	current := buildinfo.Current()
	if s.globalAPIKey() != "" && !s.authorizedGlobalAPI(c) {
		writeError(c, http.StatusUnauthorized, "invalid_api_key", "АРI keys 错误")
		return
	}
	active := false
	for _, session := range s.store.ICloudSessions() {
		if session.IsICloudPlus && session.CanCreateHME && len(session.Cookies) > 0 {
			active = true
			break
		}
	}
	writeJSON(c, http.StatusOK, map[string]any{
		"success": true,
		"data": map[string]any{
			"service":                    "icloud-privacy-mail-v2",
			"version":                    current.Version,
			"commit":                     current.Commit,
			"api_active":                 s.store.Settings().EnablePublicMailboxAPI && s.globalAPIKey() != "",
			"icloud_active":              active,
			"lease_api_version":          1,
			"lease_ttl_seconds":          s.cfg.PublicMailboxLeaseTTLMinutes * 60,
			"lease_max_ttl_seconds":      s.cfg.PublicMailboxLeaseMaxTTLMinutes * 60,
			"mailbox_note_api_supported": true,
			"time":                       time.Now().Format(time.RFC3339),
		},
	})
}

// handlePublicClaimMailbox 复刻自原 net/http 实现。
func (s *Server) handlePublicClaimMailbox(c *gin.Context) {
	if !s.store.Settings().EnablePublicMailboxAPI {
		writeError(c, http.StatusForbidden, "public_api_disabled", "公共取号 API 尚未开启")
		return
	}
	if !s.authorizedGlobalAPI(c) {
		writeError(c, http.StatusUnauthorized, "global_api_key_required", "自动取号需要提交全局 АРI keys")
		return
	}
	var body struct {
		Project    string `json:"project"`
		Purpose    string `json:"purpose"`
		RequestID  string `json:"request_id"`
		Note       string `json:"note"`
		TTLSeconds int    `json:"ttl_seconds"`
	}
	if c.Request.ContentLength != 0 {
		if err := decodeJSON(c, &body); err != nil {
			writeError(c, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
	}
	mailbox, lease, created, err := s.store.ClaimMailboxLease(
		body.Project,
		body.Purpose,
		body.RequestID,
		body.Note,
		s.mailboxLeaseTTL(body.TTLSeconds),
		time.Now(),
	)
	if err != nil {
		if errors.Is(err, store.ErrNoAvailableMailbox) {
			writeError(c, http.StatusOK, "no_available_mailbox", err.Error())
			return
		}
		writeMailboxLeaseError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"mailbox":    s.publicMailbox(c, mailbox, true),
		"lease":      s.publicMailboxLease(lease),
		"created":    created,
		"idempotent": !created,
	}})
}

// handlePublicLookupMailboxes 复刻自原 net/http 实现。
func (s *Server) handlePublicLookupMailboxes(c *gin.Context) {
	if !s.store.Settings().EnablePublicMailboxAPI {
		writeError(c, http.StatusForbidden, "public_api_disabled", "公共取号 API 尚未开启")
		return
	}
	if !s.authorizedGlobalAPI(c) {
		writeError(c, http.StatusUnauthorized, "global_api_key_required", "查询邮箱需要提交全局 АРI keys")
		return
	}
	var body struct {
		Emails []string `json:"emails"`
	}
	if err := decodeJSON(c, &body); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if len(body.Emails) == 0 || len(body.Emails) > 500 {
		writeError(c, http.StatusBadRequest, "invalid_email_count", "邮箱数量必须是 1-500")
		return
	}
	seen := map[string]bool{}
	items := make([]map[string]any, 0, len(body.Emails))
	missing := make([]string, 0)
	for _, raw := range body.Emails {
		email := strings.ToLower(strings.TrimSpace(raw))
		if email == "" || seen[email] {
			continue
		}
		seen[email] = true
		mailbox, ok := s.store.FindMailboxByEmail(email)
		if !ok {
			missing = append(missing, email)
			continue
		}
		items = append(items, s.publicMailbox(c, mailbox, true))
	}
	writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": map[string]any{"items": items, "missing": missing}})
}

// handlePublicMailboxCode 是 GET /v1/mailboxes/:email/code 的入口，
// 复刻自原 net/http 实现。原版的 fromPublicPage 通过 request context 传递，
// gin 版重构为读取 c 中的 public-code-page 标记后调用公共实现。
func (s *Server) handlePublicMailboxCode(c *gin.Context) {
	fromPublicPage, _ := c.Get(publicCodePageContextKey)
	fromPage, _ := fromPublicPage.(bool)
	s.servePublicMailboxCode(c, fromPage)
}

// servePublicMailboxCode 复刻自原 handlePublicMailboxCode 的完整长轮询逻辑
// （CachedCodeWithQuery / syncDone 协程 / deadline / poll），fromPublicPage 由调用方显式给出。
func (s *Server) servePublicMailboxCode(c *gin.Context, fromPublicPage bool) {
	settings := s.store.Settings()
	if fromPublicPage {
		if !settings.EnablePublicCodePage {
			writeError(c, http.StatusForbidden, "public_code_page_disabled", "公共验证码页面尚未开启")
			return
		}
	} else if !settings.EnablePublicMailboxAPI {
		writeError(c, http.StatusForbidden, "public_api_disabled", "公共取号 API 尚未开启")
		return
	}
	// 原代码用 url.PathUnescape(r.PathValue("email"))；gin 的 c.Param 已解码，直接使用。
	email := c.Param("email")
	mailbox, ok := s.store.FindMailboxByEmail(email)
	if !ok {
		writeError(c, http.StatusNotFound, "mailbox_not_found", "邮箱不存在")
		return
	}
	if !fromPublicPage && !s.authorizedMailboxAPI(c, mailbox) {
		writeError(c, http.StatusUnauthorized, "invalid_api_key", "АРI keys 错误")
		return
	}
	if !mailbox.APIActive || mailbox.Status == domain.StatusDisabled {
		writeError(c, http.StatusForbidden, "api_disabled", "邮箱 API 已停用")
		return
	}
	if !mailbox.ICloudActive {
		writeError(c, http.StatusForbidden, "icloud_inactive", "邮箱在 iCloud 中已停用")
		return
	}
	after, err := parseRFC3339(c.Request.URL.Query().Get("after"))
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_after", err.Error())
		return
	}
	waitMS := s.cfg.PublicFastSyncWaitMS
	if raw := strings.TrimSpace(c.Request.URL.Query().Get("wait_ms")); raw != "" {
		if parsed, parseErr := strconv.Atoi(raw); parseErr == nil {
			waitMS = parsed
		}
	}
	if waitMS < 0 {
		waitMS = 0
	}
	if waitMS > 30000 {
		waitMS = 30000
	}
	keyword := strings.TrimSpace(c.Request.URL.Query().Get("keyword"))
	if keyword == "" {
		keyword = "AI Platform"
	}
	allowStale := parseBool(c.Request.URL.Query().Get("allow_stale"))
	cacheOnly := parseBool(c.Request.URL.Query().Get("cache"))
	peekOnly := parseBool(c.Request.URL.Query().Get("peek")) || parseBool(c.Request.URL.Query().Get("preview"))
	query := mailboxservice.CodeQuery{
		After:         after,
		Keyword:       keyword,
		SkipMessageID: mailbox.LastCodeMessageID,
		IncludeServed: cacheOnly || peekOnly,
		MarkAsServed:  !cacheOnly && !peekOnly,
	}
	if result, found, lookupErr := s.mailbox.CachedCodeWithQuery(mailbox.ID, query); lookupErr != nil {
		writeServiceError(c, lookupErr)
		return
	} else if found {
		writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": result})
		return
	}
	if cacheOnly {
		writeError(c, http.StatusOK, "no_code", "本地缓存中暂无验证码")
		return
	}
	if waitMS == 0 {
		writeError(c, http.StatusOK, "no_code", "暂未收到验证码")
		return
	}

	deadline := time.NewTimer(time.Duration(waitMS) * time.Millisecond)
	defer deadline.Stop()
	poll := time.NewTicker(200 * time.Millisecond)
	defer poll.Stop()
	syncDone := make(chan error, 1)
	minInterval := time.Duration(s.cfg.PublicSyncMinIntervalMS) * time.Millisecond
	if minInterval < 0 {
		minInterval = 0
	}
	if mailbox.LastSyncAt.IsZero() || time.Since(mailbox.LastSyncAt) >= minInterval {
		go func() {
			syncCtx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 120*time.Second)
			defer cancel()
			_, syncErr := s.mailbox.SyncMessages(syncCtx, mailbox.ID)
			syncDone <- syncErr
		}()
	} else {
		syncDone <- nil
	}
	var syncErr error
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case <-deadline.C:
			if syncErr != nil && !allowStale {
				writeError(c, http.StatusBadGateway, "mail_sync_failed", "同步验证码邮件失败，请检查 IMAP 或 iCloud 登录态后重试")
				return
			}
			if syncErr != nil && allowStale {
				if result, found, lookupErr := s.staleCachedCode(mailbox.ID, query); lookupErr != nil {
					writeServiceError(c, lookupErr)
					return
				} else if found {
					writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": staleCodeData(result)})
					return
				}
			}
			writeError(c, http.StatusOK, "no_code", "暂未收到验证码")
			return
		case syncErr = <-syncDone:
			syncDone = nil
			if result, found, lookupErr := s.mailbox.CachedCodeWithQuery(mailbox.ID, query); lookupErr != nil {
				writeServiceError(c, lookupErr)
				return
			} else if found {
				writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": result})
				return
			}
			if syncErr != nil && !allowStale {
				writeError(c, http.StatusBadGateway, "mail_sync_failed", "同步验证码邮件失败，请检查 IMAP 或 iCloud 登录态后重试")
				return
			}
			if syncErr != nil && allowStale {
				if result, found, lookupErr := s.staleCachedCode(mailbox.ID, query); lookupErr != nil {
					writeServiceError(c, lookupErr)
					return
				} else if found {
					writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": staleCodeData(result)})
					return
				}
			}
		case <-poll.C:
			if result, found, lookupErr := s.mailbox.CachedCodeWithQuery(mailbox.ID, query); lookupErr != nil {
				writeServiceError(c, lookupErr)
				return
			} else if found {
				writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": result})
				return
			}
		}
	}
}

// handlePublicCodePageStatus 复刻自原 net/http 实现。
func (s *Server) handlePublicCodePageStatus(c *gin.Context) {
	settings := s.store.Settings()
	writeJSON(c, http.StatusOK, map[string]any{
		"success": true,
		"data": map[string]any{
			"enabled": settings.EnablePublicCodePage,
			"route":   "/email-code",
		},
	})
}

// handlePublicCodePageLookup 复刻自原 net/http 实现。
// 原版用 r.Clone(context.WithValue(publicCodePageContextKey, true))+SetPathValue("email")
// 中转调 handlePublicMailboxCode；gin 版等效为：设置 public-code-page 标记、
// 把 email 注入 c.Params（等效 SetPathValue），再调用同一个 handler 实现。
func (s *Server) handlePublicCodePageLookup(c *gin.Context) {
	email := strings.ToLower(strings.TrimSpace(c.Request.URL.Query().Get("email")))
	if email == "" || !strings.Contains(email, "@") {
		writeError(c, http.StatusBadRequest, "invalid_email", "请输入完整的邮箱地址")
		return
	}
	c.Set(publicCodePageContextKey, true)
	// 等效原 SetPathValue("email", email)：追加/覆盖 :email 参数供 servePublicMailboxCode 读取。
	overridden := false
	for i := range c.Params {
		if c.Params[i].Key == "email" {
			c.Params[i].Value = email
			overridden = true
		}
	}
	if !overridden {
		c.Params = append(c.Params, gin.Param{Key: "email", Value: email})
	}
	s.servePublicMailboxCode(c, true)
}

// publicCodePageMailbox 复刻自原 net/http 实现。
func (s *Server) publicCodePageMailbox(c *gin.Context) (domain.Mailbox, bool) {
	if !s.store.Settings().EnablePublicCodePage {
		writeError(c, http.StatusForbidden, "public_code_page_disabled", "公共验证码页面尚未开启")
		return domain.Mailbox{}, false
	}
	email := strings.ToLower(strings.TrimSpace(c.Request.URL.Query().Get("email")))
	if email == "" || !strings.Contains(email, "@") {
		writeError(c, http.StatusBadRequest, "invalid_email", "请输入完整的邮箱地址")
		return domain.Mailbox{}, false
	}
	mailbox, ok := s.store.FindMailboxByEmail(email)
	if !ok {
		writeError(c, http.StatusNotFound, "mailbox_not_found", "邮箱不存在")
		return domain.Mailbox{}, false
	}
	if !mailbox.APIActive || mailbox.Status == domain.StatusDisabled {
		writeError(c, http.StatusForbidden, "api_disabled", "邮箱 API 已停用")
		return domain.Mailbox{}, false
	}
	if !mailbox.ICloudActive {
		writeError(c, http.StatusForbidden, "icloud_inactive", "邮箱在 iCloud 中已停用")
		return domain.Mailbox{}, false
	}
	return mailbox, true
}

// handlePublicCodePageMessages 复刻自原 net/http 实现。
func (s *Server) handlePublicCodePageMessages(c *gin.Context) {
	mailbox, ok := s.publicCodePageMailbox(c)
	if !ok {
		return
	}
	var syncErr error
	if parseBool(c.Request.URL.Query().Get("sync")) {
		minInterval := time.Duration(s.cfg.PublicSyncMinIntervalMS) * time.Millisecond
		if minInterval < 0 {
			minInterval = 0
		}
		if mailbox.LastSyncAt.IsZero() || time.Since(mailbox.LastSyncAt) >= minInterval {
			syncCtx, cancel := context.WithTimeout(c.Request.Context(), 120*time.Second)
			_, syncErr = s.mailbox.SyncMessages(syncCtx, mailbox.ID)
			cancel()
		}
	}
	items := s.store.MessagesForMailbox(mailbox.ID)
	total := len(items)
	limit := 50
	if raw := strings.TrimSpace(c.Request.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if limit > 100 {
		limit = 100
	}
	if len(items) > limit {
		items = items[:limit]
	}
	summaries := make([]map[string]any, 0, len(items))
	for _, message := range items {
		summaries = append(summaries, publicMessageSummary(message))
	}
	refreshed, _ := s.store.FindMailboxByID(mailbox.ID)
	data := map[string]any{"email": mailbox.Email, "items": summaries, "total": total}
	if !refreshed.LastSyncAt.IsZero() {
		data["last_sync_at"] = refreshed.LastSyncAt
	}
	if syncErr != nil {
		data["sync_error"] = "同步邮件失败，当前显示本地已保存的邮件"
	}
	writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": data})
}

// handlePublicCodePageMessage 复刻自原 net/http 实现。原 {messageID} → :messageID。
func (s *Server) handlePublicCodePageMessage(c *gin.Context) {
	mailbox, ok := s.publicCodePageMailbox(c)
	if !ok {
		return
	}
	message, err := s.mailbox.MessageContent(c.Request.Context(), mailbox.ID, c.Param("messageID"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"message": publicMessageContent(message),
	}})
}

// publicMessageSummary 复刻自原 net/http 实现。
func publicMessageSummary(message domain.Message) map[string]any {
	return map[string]any{
		"id":           message.ID,
		"source":       message.Source,
		"subject":      message.Subject,
		"from":         message.From,
		"content_type": message.ContentType,
		"has_html":     strings.TrimSpace(message.HTMLBody) != "",
		"received_at":  message.ReceivedAt,
	}
}

// publicMessageContent 复刻自原 net/http 实现。
func publicMessageContent(message domain.Message) map[string]any {
	return map[string]any{
		"id":           message.ID,
		"source":       message.Source,
		"subject":      message.Subject,
		"from":         message.From,
		"body":         message.Body,
		"html_body":    message.HTMLBody,
		"content_type": message.ContentType,
		"received_at":  message.ReceivedAt,
	}
}

// staleCachedCode 复刻自原 net/http 实现。
func (s *Server) staleCachedCode(mailboxID string, query mailboxservice.CodeQuery) (mailboxservice.CodeResult, bool, error) {
	query.IncludeServed = false
	query.MarkAsServed = false
	return s.mailbox.CachedCodeWithQuery(mailboxID, query)
}

// staleCodeData 复刻自原 net/http 实现。
func staleCodeData(result mailboxservice.CodeResult) map[string]any {
	return map[string]any{
		"email": result.Email, "code": result.Code, "subject": result.Subject, "from": result.From,
		"received_at": result.ReceivedAt, "message_id": result.MessageID, "stale_cache": true,
		"sync_error": "同步验证码邮件失败，当前验证码来自本地缓存",
	}
}

// handleSchedulerStatus 复刻自原 net/http 实现。
func (s *Server) handleSchedulerStatus(c *gin.Context) {
	writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"scheduler": s.scheduler.Snapshot(),
		"defaults":  scheduler.DefaultConfig(s.store),
	}})
}

// handleSchedulerStart 复刻自原 net/http 实现。
func (s *Server) handleSchedulerStart(c *gin.Context) {
	var cfg scheduler.Config
	if err := decodeJSON(c, &cfg); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	state, err := s.scheduler.Start(s.runtimeCtx, cfg)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": map[string]any{"scheduler": state}})
}

// handleSchedulerStop 复刻自原 net/http 实现。
func (s *Server) handleSchedulerStop(c *gin.Context) {
	writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": map[string]any{"scheduler": s.scheduler.Stop("已手动停止定时创建")}})
}

// handleSchedulerClearLogs 复刻自原 net/http 实现。
func (s *Server) handleSchedulerClearLogs(c *gin.Context) {
	writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": map[string]any{"scheduler": s.scheduler.ClearEvents()}})
}

// handleExportRuntime 复刻自原 net/http 实现。
func (s *Server) handleExportRuntime(c *gin.Context) {
	state := s.store.Snapshot()
	payload := struct {
		SchemaVersion  int                    `json:"schema_version"`
		ExportedAt     time.Time              `json:"exported_at"`
		AppleAccounts  []domain.AppleAccount  `json:"apple_accounts"`
		Mailboxes      []domain.Mailbox       `json:"mailboxes"`
		MailboxLeases  []domain.MailboxLease  `json:"mailbox_leases"`
		ICloudSessions []domain.ICloudSession `json:"icloud_sessions"`
		CreateSettings domain.CreateSettings  `json:"create_settings"`
		Settings       domain.Settings        `json:"settings"`
		MessageCount   int                    `json:"message_count"`
		Messages       []domain.Message       `json:"messages,omitempty"`
	}{
		SchemaVersion: state.SchemaVersion, ExportedAt: time.Now(), AppleAccounts: state.AppleAccounts,
		Mailboxes: state.Mailboxes, MailboxLeases: state.MailboxLeases, ICloudSessions: state.ICloudSessions, CreateSettings: state.CreateSettings,
		Settings: state.Settings, MessageCount: len(state.Messages),
	}
	if parseBool(c.Request.URL.Query().Get("include_messages")) {
		payload.Messages = state.Messages
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		writeError(c, http.StatusInternalServerError, "export_failed", err.Error())
		return
	}
	writeDownload(c, "application/json; charset=utf-8", "icloud-privacy-mail-state-"+time.Now().Format("20060102-150405")+".json", append(data, '\n'))
}

// handleExportMailboxAPIs 复刻自原 net/http 实现。
func (s *Server) handleExportMailboxAPIs(c *gin.Context) {
	s.writeMailboxExport(c, true)
}

// handleExportMailboxEmails 复刻自原 net/http 实现。
func (s *Server) handleExportMailboxEmails(c *gin.Context) {
	s.writeMailboxExport(c, false)
}

// writeMailboxExport 复刻自原 net/http 实现。
func (s *Server) writeMailboxExport(c *gin.Context, includeAPI bool) {
	format := strings.ToLower(strings.TrimSpace(c.Request.URL.Query().Get("format")))
	if format == "" {
		format = "txt"
	}
	accountID := strings.TrimSpace(c.Request.URL.Query().Get("account_id"))
	rows := make([][]string, 0)
	for _, mailbox := range s.store.AllMailboxes() {
		if accountID != "" && mailbox.AccountID != accountID {
			continue
		}
		row := []string{mailbox.Email}
		if includeAPI {
			row = append(row, s.mailboxAPIURL(c, mailbox))
		}
		rows = append(rows, row)
	}
	var body strings.Builder
	ext := "txt"
	contentType := "text/plain; charset=utf-8"
	switch format {
	case "txt":
		separator := "\n"
		if includeAPI {
			separator = "----"
		}
		for _, row := range rows {
			body.WriteString(strings.Join(row, separator))
			body.WriteByte('\n')
		}
	case "csv", "tsv":
		ext = format
		contentType = "text/csv; charset=utf-8"
		writer := csv.NewWriter(&body)
		if format == "tsv" {
			writer.Comma = '\t'
			contentType = "text/tab-separated-values; charset=utf-8"
		}
		for _, row := range rows {
			_ = writer.Write(row)
		}
		writer.Flush()
	case "jsonl":
		ext = "jsonl"
		contentType = "application/x-ndjson; charset=utf-8"
		for _, row := range rows {
			record := map[string]string{"email": row[0]}
			if includeAPI {
				record["api"] = row[1]
			}
			data, _ := json.Marshal(record)
			body.Write(data)
			body.WriteByte('\n')
		}
	default:
		writeError(c, http.StatusBadRequest, "invalid_export_format", "导出格式只支持 txt、csv、tsv、jsonl")
		return
	}
	prefix := "icloud-mailbox-emails"
	if includeAPI {
		prefix = "icloud-mailbox-apis"
	}
	writeDownload(c, contentType, prefix+"-"+time.Now().Format("20060102-150405")+"."+ext, []byte(body.String()))
}

// writeDownload 复刻自原 net/http 实现。
func writeDownload(c *gin.Context, contentType, filename string, body []byte) {
	c.Writer.Header().Set("Content-Type", contentType)
	c.Writer.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Writer.WriteHeader(http.StatusOK)
	_, _ = c.Writer.Write(body)
}

// publicMailbox 复刻自原 net/http 实现。
func (s *Server) publicMailbox(c *gin.Context, mailbox domain.Mailbox, includeAPI bool) map[string]any {
	out := map[string]any{
		"id": mailbox.ID, "account_id": mailbox.AccountID, "label": mailbox.Label, "email": mailbox.Email,
		"api_active": mailbox.APIActive, "icloud_active": mailbox.ICloudActive, "status": mailbox.Status,
		"note": mailbox.Note, "active_lease_id": mailbox.ActiveLeaseID, "receive_count": mailbox.ReceiveCount, "last_sync_at": mailbox.LastSyncAt,
	}
	if includeAPI {
		out["api_url"] = s.mailboxAPIURL(c, mailbox)
		out["api_token_mask"] = maskSecret(mailbox.APIToken)
	}
	return out
}

// mailboxAPIURL 复刻自原 net/http 实现（baseURL 推导改用 c.Request）。
func (s *Server) mailboxAPIURL(c *gin.Context, mailbox domain.Mailbox) string {
	baseURL := strings.TrimRight(strings.TrimSpace(s.cfg.PublicBaseURL), "/")
	if baseURL == "" {
		scheme := "http"
		if c.Request.TLS != nil || strings.EqualFold(c.Request.Header.Get("X-Forwarded-Proto"), "https") {
			scheme = "https"
		}
		baseURL = scheme + "://" + c.Request.Host
	}
	// 迁移注意:模块挂载在 /api/icloud,公共取码路由实际为 /api/icloud/v1/...,
	// 这里必须带 icloud 前缀,否则导出的 api_url 全部 404。
	return fmt.Sprintf("%s/api/icloud/v1/mailboxes/%s/code?key=%s", baseURL, url.PathEscape(mailbox.Email), url.QueryEscape(mailbox.APIToken))
}

// authorizedGlobalAPI 复刻自原 net/http 实现。
func (s *Server) authorizedGlobalAPI(c *gin.Context) bool {
	want := s.globalAPIKey()
	if want == "" {
		return false
	}
	return anySecretEqual(want, c.Request.URL.Query().Get("key"), c.GetHeader("X-арi_keys"), strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer "))
}

// authorizedMailboxAPI 复刻自原 net/http 实现。
func (s *Server) authorizedMailboxAPI(c *gin.Context, mailbox domain.Mailbox) bool {
	candidates := []string{c.Request.URL.Query().Get("key"), c.GetHeader("X-арi_keys"), strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")}
	if anySecretEqual(mailbox.APIToken, candidates...) {
		return true
	}
	globalKey := s.globalAPIKey()
	return globalKey != "" && anySecretEqual(globalKey, candidates...)
}

// globalAPIKey 复刻自原 net/http 实现。
func (s *Server) globalAPIKey() string {
	if value := strings.TrimSpace(s.store.Settings().PublicAPIKey); value != "" {
		return value
	}
	return strings.TrimSpace(s.cfg.APIKey)
}

// anySecretEqual 复刻自原 net/http 实现。
func anySecretEqual(want string, candidates ...string) bool {
	want = strings.TrimSpace(want)
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if want != "" && len(candidate) == len(want) && subtle.ConstantTimeCompare([]byte(candidate), []byte(want)) == 1 {
			return true
		}
	}
	return false
}

// maskSecret 复刻自原 net/http 实现。
func maskSecret(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 8 {
		return strings.Repeat("*", len(value))
	}
	return value[:4] + strings.Repeat("*", len(value)-8) + value[len(value)-4:]
}
