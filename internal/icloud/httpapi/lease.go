package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-go/internal/icloud/domain"
	"gpt-go/internal/icloud/store"
)

// mailboxLeaseActionRequest 复刻自原 net/http 实现。
type mailboxLeaseActionRequest struct {
	Project        string `json:"project"`
	Note           string `json:"note"`
	Reason         string `json:"reason"`
	TTLSeconds     int    `json:"ttl_seconds"`
	ReconcileBound bool   `json:"reconcile_bound"`
}

// handlePublicMailboxLease 复刻自原 net/http 实现。原 {lease_id} → :lease_id。
func (s *Server) handlePublicMailboxLease(c *gin.Context) {
	if !s.requirePublicMailboxLeaseAPI(c) {
		return
	}
	lease, ok := s.store.FindMailboxLease(c.Param("lease_id"))
	if !ok {
		writeMailboxLeaseError(c, store.ErrLeaseNotFound)
		return
	}
	project := mailboxLeaseProject(c.Request.URL.Query().Get("project"), c.GetHeader("X-Project"))
	if project == "" {
		writeMailboxLeaseError(c, store.ErrLeaseProjectRequired)
		return
	}
	if !strings.EqualFold(lease.Project, project) {
		writeMailboxLeaseError(c, store.ErrLeaseProjectMismatch)
		return
	}
	data := map[string]any{"lease": s.publicMailboxLease(lease)}
	if mailbox, found := s.store.FindMailboxByID(lease.MailboxID); found {
		data["mailbox"] = s.publicMailbox(c, mailbox, true)
	}
	writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": data})
}

// handlePublicMailboxLeaseCommit 复刻自原 net/http 实现。
func (s *Server) handlePublicMailboxLeaseCommit(c *gin.Context) {
	s.handlePublicMailboxLeaseAction(c, "commit", c.Param("lease_id"))
}

// handlePublicMailboxLeaseRelease 复刻自原 net/http 实现。
func (s *Server) handlePublicMailboxLeaseRelease(c *gin.Context) {
	s.handlePublicMailboxLeaseAction(c, "release", c.Param("lease_id"))
}

// handlePublicMailboxLeaseRenew 复刻自原 net/http 实现。
func (s *Server) handlePublicMailboxLeaseRenew(c *gin.Context) {
	s.handlePublicMailboxLeaseAction(c, "renew", c.Param("lease_id"))
}

// handlePublicMailboxLeaseNote 复刻自原 net/http 实现。
func (s *Server) handlePublicMailboxLeaseNote(c *gin.Context) {
	s.handlePublicMailboxLeaseAction(c, "note", c.Param("lease_id"))
}

// handlePublicMailboxLeaseCommitCompat 复刻自原 net/http 实现。
func (s *Server) handlePublicMailboxLeaseCommitCompat(c *gin.Context) {
	s.handlePublicMailboxLeaseActionCompat(c, "commit")
}

// handlePublicMailboxLeaseReleaseCompat 复刻自原 net/http 实现。
func (s *Server) handlePublicMailboxLeaseReleaseCompat(c *gin.Context) {
	s.handlePublicMailboxLeaseActionCompat(c, "release")
}

// handlePublicMailboxLeaseRenewCompat 复刻自原 net/http 实现。
func (s *Server) handlePublicMailboxLeaseRenewCompat(c *gin.Context) {
	s.handlePublicMailboxLeaseActionCompat(c, "renew")
}

// handlePublicMailboxLeaseActionCompat 复刻自原 net/http 实现。
func (s *Server) handlePublicMailboxLeaseActionCompat(c *gin.Context, action string) {
	if !s.requirePublicMailboxLeaseAPI(c) {
		return
	}
	request, ok := decodeMailboxLeaseAction(c)
	if !ok {
		return
	}
	// 原代码用 url.PathUnescape(r.PathValue("email"))；gin 的 c.Param 已解码，直接使用。
	email := c.Param("email")
	project := mailboxLeaseProject(request.Project, c.GetHeader("X-Project"))
	lease, found := s.store.LatestMailboxLeaseByEmailProject(email, project)
	if !found {
		if project == "" {
			writeMailboxLeaseError(c, store.ErrLeaseProjectRequired)
		} else {
			writeMailboxLeaseError(c, store.ErrLeaseNotFound)
		}
		return
	}
	s.applyPublicMailboxLeaseAction(c, action, lease.ID, project, request)
}

// handlePublicMailboxLeaseAction 复刻自原 net/http 实现。
func (s *Server) handlePublicMailboxLeaseAction(c *gin.Context, action, leaseID string) {
	if !s.requirePublicMailboxLeaseAPI(c) {
		return
	}
	request, ok := decodeMailboxLeaseAction(c)
	if !ok {
		return
	}
	project := mailboxLeaseProject(request.Project, c.GetHeader("X-Project"))
	s.applyPublicMailboxLeaseAction(c, action, leaseID, project, request)
}

// applyPublicMailboxLeaseAction 复刻自原 net/http 实现。
func (s *Server) applyPublicMailboxLeaseAction(c *gin.Context, action, leaseID, project string, request mailboxLeaseActionRequest) {
	note := strings.TrimSpace(request.Note)
	if note == "" {
		note = strings.TrimSpace(request.Reason)
	}
	now := time.Now()
	var (
		mailbox    domain.Mailbox
		lease      domain.MailboxLease
		idempotent bool
		err        error
	)
	switch action {
	case "commit":
		if request.ReconcileBound {
			mailbox, lease, idempotent, err = s.store.ReconcileUsedMailboxLease(leaseID, project, note, now)
		} else {
			mailbox, lease, idempotent, err = s.store.CommitMailboxLease(leaseID, project, note, now)
		}
	case "release":
		mailbox, lease, idempotent, err = s.store.ReleaseMailboxLease(leaseID, project, note, now)
	case "renew":
		mailbox, lease, err = s.store.RenewMailboxLease(leaseID, project, note, s.mailboxLeaseTTL(request.TTLSeconds), now)
	case "note":
		mailbox, lease, err = s.store.SetMailboxLeaseNote(leaseID, project, request.Note, now)
	default:
		writeError(c, http.StatusBadRequest, "invalid_lease_action", "邮箱租约动作不正确")
		return
	}
	if err != nil {
		writeMailboxLeaseError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"mailbox":    s.publicMailbox(c, mailbox, true),
		"lease":      s.publicMailboxLease(lease),
		"idempotent": idempotent,
		"reconciled": request.ReconcileBound,
	}})
}

// decodeMailboxLeaseAction 复刻自原 net/http 实现。
func decodeMailboxLeaseAction(c *gin.Context) (mailboxLeaseActionRequest, bool) {
	request := mailboxLeaseActionRequest{}
	if c.Request.ContentLength == 0 {
		return request, true
	}
	if err := decodeJSON(c, &request); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_json", err.Error())
		return mailboxLeaseActionRequest{}, false
	}
	return request, true
}

// requirePublicMailboxLeaseAPI 复刻自原 net/http 实现。
func (s *Server) requirePublicMailboxLeaseAPI(c *gin.Context) bool {
	if !s.store.Settings().EnablePublicMailboxAPI {
		writeError(c, http.StatusForbidden, "public_api_disabled", "公共取号 API 尚未开启")
		return false
	}
	if !s.authorizedGlobalAPI(c) {
		writeError(c, http.StatusUnauthorized, "global_api_key_required", "邮箱租约操作需要提交全局 АРI keys")
		return false
	}
	return true
}

// mailboxLeaseTTL 复刻自原 net/http 实现。
func (s *Server) mailboxLeaseTTL(requestedSeconds int) time.Duration {
	seconds := requestedSeconds
	if seconds <= 0 {
		seconds = s.cfg.PublicMailboxLeaseTTLMinutes * 60
	}
	if seconds < 60 {
		seconds = 60
	}
	maxSeconds := s.cfg.PublicMailboxLeaseMaxTTLMinutes * 60
	if maxSeconds <= 0 {
		maxSeconds = 7 * 24 * 60 * 60
	}
	if seconds > maxSeconds {
		seconds = maxSeconds
	}
	return time.Duration(seconds) * time.Second
}

// mailboxLeaseProject 复刻自原 net/http 实现。
func mailboxLeaseProject(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return strings.ToLower(value)
		}
	}
	return ""
}

// publicMailboxLease 复刻自原 net/http 实现。
func (s *Server) publicMailboxLease(lease domain.MailboxLease) map[string]any {
	return map[string]any{
		"id":           lease.ID,
		"mailbox_id":   lease.MailboxID,
		"email":        lease.Email,
		"project":      lease.Project,
		"purpose":      lease.Purpose,
		"request_id":   lease.RequestID,
		"state":        lease.State,
		"note":         lease.Note,
		"expires_at":   formatTime(lease.ExpiresAt),
		"created_at":   formatTime(lease.CreatedAt),
		"updated_at":   formatTime(lease.UpdatedAt),
		"committed_at": formatTime(lease.CommittedAt),
		"released_at":  formatTime(lease.ReleasedAt),
		"expired_at":   formatTime(lease.ExpiredAt),
	}
}

// writeMailboxLeaseError 复刻自原 net/http 实现。
func writeMailboxLeaseError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, store.ErrLeaseProjectRequired):
		writeError(c, http.StatusBadRequest, "lease_project_required", err.Error())
	case errors.Is(err, store.ErrLeaseNotFound):
		writeError(c, http.StatusNotFound, "lease_not_found", err.Error())
	case errors.Is(err, store.ErrLeaseProjectMismatch):
		writeError(c, http.StatusForbidden, "lease_project_mismatch", err.Error())
	case errors.Is(err, store.ErrLeaseRequestConflict):
		writeError(c, http.StatusConflict, "lease_request_conflict", err.Error())
	case errors.Is(err, store.ErrLeaseCommitted):
		writeError(c, http.StatusConflict, "lease_committed", err.Error())
	case errors.Is(err, store.ErrLeaseReleased):
		writeError(c, http.StatusConflict, "lease_released", err.Error())
	case errors.Is(err, store.ErrLeaseExpired):
		writeError(c, http.StatusConflict, "lease_expired", err.Error())
	case errors.Is(err, store.ErrLeaseBindingConflict):
		writeError(c, http.StatusConflict, "lease_binding_conflict", err.Error())
	case errors.Is(err, store.ErrLeaseReconcileState):
		writeError(c, http.StatusConflict, "lease_reconcile_state", err.Error())
	case errors.Is(err, store.ErrNoAvailableMailbox):
		writeError(c, http.StatusOK, "no_available_mailbox", err.Error())
	default:
		writeError(c, http.StatusInternalServerError, "lease_operation_failed", err.Error())
	}
}
