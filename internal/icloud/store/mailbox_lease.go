package store

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"gpt-go/internal/icloud/domain"
)

// 哨兵错误全部复刻自原 sqlite 实现。
var (
	ErrNoAvailableMailbox   = errors.New("没有可用隐私邮箱")
	ErrLeaseProjectRequired = errors.New("project 不能为空")
	ErrLeaseNotFound        = errors.New("邮箱租约不存在")
	ErrLeaseProjectMismatch = errors.New("邮箱租约不属于当前项目")
	ErrLeaseRequestConflict = errors.New("request_id 已被其他领取请求使用")
	ErrLeaseCommitted       = errors.New("邮箱租约已经提交")
	ErrLeaseReleased        = errors.New("邮箱租约已经释放")
	ErrLeaseExpired         = errors.New("邮箱租约已经过期")
	ErrLeaseBindingConflict = errors.New("邮箱与租约绑定关系不一致")
	ErrLeaseReconcileState  = errors.New("只有已释放或已过期的邮箱租约可以执行已绑定纠偏")
)

// ClaimMailboxLease 复刻自原 sqlite 实现：原子执行 available -> reserved。
// MongoDB 版通过 FindOneAndUpdate(filter{status:available, active_lease_id:""}) 实现原子占用，
// 复刻原单事务内 SELECT ... LIMIT 1 再更新的竞争语义。
func (s *Store) ClaimMailboxLease(project, purpose, requestID, note string, ttl time.Duration, now time.Time) (domain.Mailbox, domain.MailboxLease, bool, error) {
	project = normalizeLeaseProject(project)
	if project == "" {
		return domain.Mailbox{}, domain.MailboxLease{}, false, ErrLeaseProjectRequired
	}
	purpose, requestID, note = strings.TrimSpace(purpose), strings.TrimSpace(requestID), strings.TrimSpace(note)
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	if now.IsZero() {
		now = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	expiryChanges, _, err := s.expireMailboxLeases(now)
	if err != nil {
		return domain.Mailbox{}, domain.MailboxLease{}, false, err
	}
	if requestID != "" {
		// request_id 幂等：复刻原按 project+request_id 查最新租约的逻辑。
		var lease domain.MailboxLease
		found, err := s.findDoc("mailbox_leases",
			bson.M{"project": project, "request_id": requestID},
			options.FindOne().SetSort(bson.D{{Key: "created_at", Value: -1}}), &lease)
		if err != nil {
			return domain.Mailbox{}, domain.MailboxLease{}, false, err
		}
		if found {
			if lease.Purpose != purpose {
				return domain.Mailbox{}, lease, false, ErrLeaseRequestConflict
			}
			var mailbox domain.Mailbox
			mailboxFound, err := s.readEntity("mailboxes", lease.MailboxID, &mailbox)
			if err != nil || !mailboxFound {
				return domain.Mailbox{}, lease, false, ErrLeaseBindingConflict
			}
			if len(expiryChanges) > 0 {
				if err := s.commitChanges(expiryChanges); err != nil {
					return domain.Mailbox{}, lease, false, err
				}
			}
			return mailbox, lease, false, nil
		}
	}
	// 预生成租约 ID，用于一次性原子占用邮箱。
	leaseID, err := s.nextID("lease")
	if err != nil {
		return domain.Mailbox{}, domain.MailboxLease{}, false, err
	}
	marker := fmt.Sprintf("__claiming__%s", leaseID)
	claimFilter := bson.M{
		"api_active":      true,
		"icloud_active":   true,
		"status":          domain.StatusAvailable,
		"active_lease_id": bson.M{"$in": bson.A{"", nil}},
	}
	ctx, cancel := s.ctx()
	defer cancel()
	claimOpts := options.FindOneAndUpdate().
		SetSort(bson.D{{Key: "created_at", Value: 1}}).
		SetReturnDocument(options.After).
		SetProjection(bson.M{"_id": 1})
	var claimedDoc bson.M
	err = s.col("mailboxes").FindOneAndUpdate(ctx, claimFilter,
		bson.M{"$set": bson.M{"active_lease_id": marker, "updated_at": now.Format(time.RFC3339Nano)}},
		claimOpts).Decode(&claimedDoc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		if len(expiryChanges) > 0 {
			_ = s.commitChanges(expiryChanges)
		}
		return domain.Mailbox{}, domain.MailboxLease{}, false, ErrNoAvailableMailbox
	}
	if err != nil {
		return domain.Mailbox{}, domain.MailboxLease{}, false, err
	}
	// 已原子占位。占位成功的邮箱 _id。
	claimedID := stringValue(claimedDoc["_id"])
	// releaseMarker 在占位成功后的任何失败路径上补偿回写 active_lease_id="",
	// 防止邮箱因残留 __claiming__ marker 被 claimFilter 永久跳过(C1 修复)。
	releaseMarker := func() {
		rCtx, rCancel := s.ctx()
		defer rCancel()
		_, _ = s.col("mailboxes").UpdateOne(rCtx,
			bson.M{"_id": claimedID, "active_lease_id": marker},
			bson.M{"$set": bson.M{"active_lease_id": ""}})
	}
	// 已原子占位，读回完整邮箱实体继续走原状态机。
	var mailbox domain.Mailbox
	if found, err := s.readEntity("mailboxes", claimedID, &mailbox); err != nil || !found {
		releaseMarker()
		if err != nil {
			return domain.Mailbox{}, domain.MailboxLease{}, false, err
		}
		return domain.Mailbox{}, domain.MailboxLease{}, false, errors.New("邮箱不存在")
	}
	lease := domain.MailboxLease{ID: leaseID, MailboxID: mailbox.ID, Email: strings.ToLower(strings.TrimSpace(mailbox.Email)), Project: project, Purpose: purpose, RequestID: requestID, State: domain.MailboxLeaseClaimed, Note: note, ExpiresAt: now.Add(ttl), CreatedAt: now, UpdatedAt: now}
	mailbox.Status, mailbox.ActiveLeaseID, mailbox.UpdatedAt = domain.StatusReserved, lease.ID, now
	if note != "" {
		mailbox.Note = note
	}
	leaseChange, _, err := s.upsertEntity("mailbox_leases", "mailbox-lease", lease.ID, lease)
	if err != nil {
		releaseMarker()
		return domain.Mailbox{}, domain.MailboxLease{}, false, err
	}
	mailboxChange, _, err := s.upsertEntity("mailboxes", "mailbox", mailbox.ID, mailbox)
	if err != nil {
		releaseMarker()
		return domain.Mailbox{}, domain.MailboxLease{}, false, err
	}
	eventChange, err := s.appendEvent("info", "mailbox_lease", fmt.Sprintf("项目 %s 已预留邮箱 %s，租约 %s", project, mailbox.Email, lease.ID))
	if err != nil {
		releaseMarker()
		return domain.Mailbox{}, domain.MailboxLease{}, false, err
	}
	changes := append(expiryChanges, leaseChange, mailboxChange, eventChange)
	return mailbox, lease, true, s.commitChanges(changes)
}

// CommitMailboxLease 复刻自原 sqlite 实现，状态机语义逐行一致。
func (s *Store) CommitMailboxLease(leaseID, project, note string, now time.Time) (domain.Mailbox, domain.MailboxLease, bool, error) {
	if now.IsZero() {
		now = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lease, err := s.authorizedLease(leaseID, project)
	if err != nil {
		return domain.Mailbox{}, domain.MailboxLease{}, false, err
	}
	var mailbox domain.Mailbox
	mailboxFound, _ := s.readEntity("mailboxes", lease.MailboxID, &mailbox)
	if lease.State == domain.MailboxLeaseCommitted {
		if !mailboxFound {
			return domain.Mailbox{}, lease, true, ErrLeaseBindingConflict
		}
		return mailbox, lease, true, nil
	}
	if lease.State == domain.MailboxLeaseReleased {
		return domain.Mailbox{}, lease, false, ErrLeaseReleased
	}
	if lease.State == domain.MailboxLeaseExpired || !lease.ExpiresAt.After(now) {
		changes, _, _ := s.expireMailboxLeases(now)
		if len(changes) > 0 {
			_ = s.commitChanges(changes)
		}
		return domain.Mailbox{}, lease, false, ErrLeaseExpired
	}
	if !mailboxFound || mailbox.ActiveLeaseID != lease.ID || mailbox.Status != domain.StatusReserved {
		return domain.Mailbox{}, lease, false, ErrLeaseBindingConflict
	}
	note = strings.TrimSpace(note)
	lease.State, lease.CommittedAt, lease.UpdatedAt = domain.MailboxLeaseCommitted, now, now
	mailbox.Status, mailbox.ActiveLeaseID, mailbox.UpdatedAt = domain.StatusUsed, "", now
	if note != "" {
		lease.Note, mailbox.Note = note, note
	}
	return s.finishLeaseMutation(mailbox, lease, "info", fmt.Sprintf("项目 %s 已提交邮箱租约 %s，邮箱 %s 标记为已使用", lease.Project, lease.ID, mailbox.Email), false)
}

// ReconcileUsedMailboxLease 修复“上游账号已绑定，但租约此前被误释放”的历史状态。
// 复刻自原 sqlite 实现，逐行保持一致。
func (s *Store) ReconcileUsedMailboxLease(leaseID, project, note string, now time.Time) (domain.Mailbox, domain.MailboxLease, bool, error) {
	if now.IsZero() {
		now = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lease, err := s.authorizedLease(leaseID, project)
	if err != nil {
		return domain.Mailbox{}, domain.MailboxLease{}, false, err
	}
	var mailbox domain.Mailbox
	mailboxFound, _ := s.readEntity("mailboxes", lease.MailboxID, &mailbox)
	if !mailboxFound {
		return domain.Mailbox{}, lease, false, ErrLeaseBindingConflict
	}
	if lease.State == domain.MailboxLeaseCommitted || (mailbox.Status == domain.StatusUsed && mailbox.ActiveLeaseID == "") {
		return mailbox, lease, true, nil
	}
	// 复刻原最新租约校验：ORDER BY created_at DESC, id DESC LIMIT 1。
	var latest domain.MailboxLease
	found, err := s.findDoc("mailbox_leases", bson.M{"mailbox_id": lease.MailboxID},
		options.FindOne().SetSort(bson.D{{Key: "created_at", Value: -1}, {Key: "_id", Value: -1}}), &latest)
	if err != nil || !found || latest.ID != lease.ID {
		return domain.Mailbox{}, lease, false, ErrLeaseBindingConflict
	}
	if lease.State != domain.MailboxLeaseReleased && lease.State != domain.MailboxLeaseExpired {
		return domain.Mailbox{}, lease, false, ErrLeaseReconcileState
	}
	if mailbox.Status != domain.StatusAvailable || mailbox.ActiveLeaseID != "" {
		return domain.Mailbox{}, lease, false, ErrLeaseBindingConflict
	}
	note = strings.TrimSpace(note)
	mailbox.Status, mailbox.ActiveLeaseID, mailbox.UpdatedAt = domain.StatusUsed, "", now
	lease.UpdatedAt = now
	if note != "" {
		lease.Note, mailbox.Note = note, note
	}
	return s.finishLeaseMutation(
		mailbox,
		lease,
		"warning",
		fmt.Sprintf("项目 %s 已纠偏邮箱租约 %s，邮箱 %s 标记为已使用", lease.Project, lease.ID, mailbox.Email),
		false,
	)
}

// ReleaseMailboxLease 复刻自原 sqlite 实现，状态机语义逐行一致。
func (s *Store) ReleaseMailboxLease(leaseID, project, note string, now time.Time) (domain.Mailbox, domain.MailboxLease, bool, error) {
	if now.IsZero() {
		now = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lease, err := s.authorizedLease(leaseID, project)
	if err != nil {
		return domain.Mailbox{}, domain.MailboxLease{}, false, err
	}
	var mailbox domain.Mailbox
	mailboxFound, _ := s.readEntity("mailboxes", lease.MailboxID, &mailbox)
	if lease.State == domain.MailboxLeaseCommitted {
		return domain.Mailbox{}, lease, false, ErrLeaseCommitted
	}
	if lease.State == domain.MailboxLeaseReleased || lease.State == domain.MailboxLeaseExpired {
		if !mailboxFound {
			return domain.Mailbox{}, lease, true, ErrLeaseBindingConflict
		}
		return mailbox, lease, true, nil
	}
	if !lease.ExpiresAt.After(now) {
		changes, _, _ := s.expireMailboxLeases(now)
		if len(changes) > 0 {
			_ = s.commitChanges(changes)
		}
		if !mailboxFound {
			return domain.Mailbox{}, lease, true, ErrLeaseBindingConflict
		}
		mailbox.Status, mailbox.ActiveLeaseID = domain.StatusAvailable, ""
		lease.State = domain.MailboxLeaseExpired
		return mailbox, lease, true, nil
	}
	if !mailboxFound || mailbox.ActiveLeaseID != lease.ID || mailbox.Status != domain.StatusReserved {
		return domain.Mailbox{}, lease, false, ErrLeaseBindingConflict
	}
	note = strings.TrimSpace(note)
	lease.State, lease.ReleasedAt, lease.UpdatedAt = domain.MailboxLeaseReleased, now, now
	mailbox.Status, mailbox.ActiveLeaseID, mailbox.UpdatedAt = domain.StatusAvailable, "", now
	if note != "" {
		lease.Note, mailbox.Note = note, note
	}
	return s.finishLeaseMutation(mailbox, lease, "info", fmt.Sprintf("项目 %s 已释放邮箱租约 %s，邮箱 %s 恢复可用", lease.Project, lease.ID, mailbox.Email), false)
}

// RenewMailboxLease 复刻自原 sqlite 实现。
func (s *Store) RenewMailboxLease(leaseID, project, note string, ttl time.Duration, now time.Time) (domain.Mailbox, domain.MailboxLease, error) {
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	if now.IsZero() {
		now = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lease, err := s.authorizedLease(leaseID, project)
	if err != nil {
		return domain.Mailbox{}, domain.MailboxLease{}, err
	}
	if lease.State == domain.MailboxLeaseCommitted {
		return domain.Mailbox{}, lease, ErrLeaseCommitted
	}
	if lease.State == domain.MailboxLeaseReleased {
		return domain.Mailbox{}, lease, ErrLeaseReleased
	}
	if lease.State == domain.MailboxLeaseExpired || !lease.ExpiresAt.After(now) {
		changes, _, _ := s.expireMailboxLeases(now)
		if len(changes) > 0 {
			_ = s.commitChanges(changes)
		}
		return domain.Mailbox{}, lease, ErrLeaseExpired
	}
	var mailbox domain.Mailbox
	found, _ := s.readEntity("mailboxes", lease.MailboxID, &mailbox)
	if !found || mailbox.ActiveLeaseID != lease.ID || mailbox.Status != domain.StatusReserved {
		return domain.Mailbox{}, lease, ErrLeaseBindingConflict
	}
	note = strings.TrimSpace(note)
	lease.ExpiresAt, lease.UpdatedAt, mailbox.UpdatedAt = now.Add(ttl), now, now
	if note != "" {
		lease.Note, mailbox.Note = note, note
	}
	mailbox, lease, _, err = s.finishLeaseMutation(mailbox, lease, "info", fmt.Sprintf("项目 %s 已续期邮箱租约 %s，新到期时间 %s", lease.Project, lease.ID, lease.ExpiresAt.Format(time.RFC3339)), false)
	return mailbox, lease, err
}

// SetMailboxLeaseNote 复刻自原 sqlite 实现。
func (s *Store) SetMailboxLeaseNote(leaseID, project, note string, now time.Time) (domain.Mailbox, domain.MailboxLease, error) {
	if now.IsZero() {
		now = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lease, err := s.authorizedLease(leaseID, project)
	if err != nil {
		return domain.Mailbox{}, domain.MailboxLease{}, err
	}
	var mailbox domain.Mailbox
	found, _ := s.readEntity("mailboxes", lease.MailboxID, &mailbox)
	if !found {
		return domain.Mailbox{}, lease, ErrLeaseBindingConflict
	}
	lease.Note, lease.UpdatedAt = strings.TrimSpace(note), now
	if mailbox.ActiveLeaseID == lease.ID || (mailbox.ActiveLeaseID == "" && mailbox.Status == domain.StatusUsed && lease.State == domain.MailboxLeaseCommitted) {
		mailbox.Note, mailbox.UpdatedAt = lease.Note, now
	}
	mailbox, lease, _, err = s.finishLeaseMutation(mailbox, lease, "info", fmt.Sprintf("项目 %s 已更新邮箱租约 %s 的备注", lease.Project, lease.ID), false)
	return mailbox, lease, err
}

// finishLeaseMutation 复刻自原 sqlite 实现：租约 + 邮箱 upsert + 事件，一次提交。
func (s *Store) finishLeaseMutation(mailbox domain.Mailbox, lease domain.MailboxLease, level, message string, idempotent bool) (domain.Mailbox, domain.MailboxLease, bool, error) {
	leaseChange, _, err := s.upsertEntity("mailbox_leases", "mailbox-lease", lease.ID, lease)
	if err != nil {
		return domain.Mailbox{}, domain.MailboxLease{}, false, err
	}
	mailboxChange, _, err := s.upsertEntity("mailboxes", "mailbox", mailbox.ID, mailbox)
	if err != nil {
		return domain.Mailbox{}, domain.MailboxLease{}, false, err
	}
	eventChange, err := s.appendEvent(level, "mailbox_lease", message)
	if err != nil {
		return domain.Mailbox{}, domain.MailboxLease{}, false, err
	}
	return mailbox, lease, idempotent, s.commitChanges([]Change{leaseChange, mailboxChange, eventChange})
}

// FindMailboxLease 复刻自原 sqlite 实现。
func (s *Store) FindMailboxLease(leaseID string) (domain.MailboxLease, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var lease domain.MailboxLease
	found, err := s.readEntity("mailbox_leases", strings.TrimSpace(leaseID), &lease)
	return lease, found && err == nil
}

// LatestMailboxLeaseByEmailProject 复刻自原 sqlite 实现：lower(email) + project 匹配取最新。
func (s *Store) LatestMailboxLeaseByEmailProject(email, project string) (domain.MailboxLease, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	target := strings.ToLower(strings.TrimSpace(email))
	normalizedProject := normalizeLeaseProject(project)
	var lease domain.MailboxLease
	found := false
	appendFn := func(data []byte) error {
		if found {
			return errScanDone
		}
		var candidate domain.MailboxLease
		if err := s.decodeInto("mailbox_leases", data, &candidate); err != nil {
			return err
		}
		if strings.ToLower(candidate.Email) == target && candidate.Project == normalizedProject {
			lease = candidate
			found = true
			return errScanDone
		}
		return nil
	}
	_ = finishFindDocs(s.findDocs("mailbox_leases",
		bson.M{"project": normalizedProject},
		options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}), appendFn))
	return lease, found
}

// MailboxLeases 复刻自原 sqlite 实现。
func (s *Store) MailboxLeases() []domain.MailboxLease {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []domain.MailboxLease
	_ = s.loadEntities("mailbox_leases", bson.D{{Key: "created_at", Value: -1}}, &out)
	return out
}

// ExpireMailboxLeases 复刻自原 sqlite 实现。
func (s *Store) ExpireMailboxLeases(now time.Time) (int, error) {
	if now.IsZero() {
		now = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	changes, count, err := s.expireMailboxLeases(now)
	if err != nil {
		return 0, err
	}
	if count == 0 {
		return 0, nil
	}
	return count, s.commitChanges(changes)
}

// expireMailboxLeases 复刻自原 sqlite 实现的 expireMailboxLeasesTx：
// 找出 claimed 且已到期的租约，置为 expired 并联动回收邮箱。
func (s *Store) expireMailboxLeases(now time.Time) ([]Change, int, error) {
	leases := []domain.MailboxLease{}
	appendFn := func(data []byte) error {
		var lease domain.MailboxLease
		if err := s.decodeInto("mailbox_leases", data, &lease); err != nil {
			return err
		}
		if lease.State == domain.MailboxLeaseClaimed && !lease.ExpiresAt.After(now) {
			leases = append(leases, lease)
		}
		return nil
	}
	if err := s.findDocs("mailbox_leases", bson.M{"state": domain.MailboxLeaseClaimed}, nil, appendFn); err != nil {
		return nil, 0, err
	}
	changes := []Change{}
	for _, lease := range leases {
		lease.State, lease.ExpiredAt, lease.UpdatedAt = domain.MailboxLeaseExpired, now, now
		if change, changed, err := s.upsertEntity("mailbox_leases", "mailbox-lease", lease.ID, lease); err != nil {
			return nil, 0, err
		} else if changed {
			changes = append(changes, change)
		}
		var mailbox domain.Mailbox
		if found, _ := s.readEntity("mailboxes", lease.MailboxID, &mailbox); found && mailbox.ActiveLeaseID == lease.ID {
			mailbox.ActiveLeaseID = ""
			if mailbox.Status == domain.StatusReserved {
				mailbox.Status = domain.StatusAvailable
			}
			mailbox.UpdatedAt = now
			if change, changed, err := s.upsertEntity("mailboxes", "mailbox", mailbox.ID, mailbox); err != nil {
				return nil, 0, err
			} else if changed {
				changes = append(changes, change)
			}
		}
		eventChange, err := s.appendEvent("warning", "mailbox_lease", fmt.Sprintf("邮箱租约 %s 已过期，邮箱 %s 已自动回收", lease.ID, lease.Email))
		if err != nil {
			return nil, 0, err
		}
		changes = append(changes, eventChange)
	}
	return changes, len(leases), nil
}

// authorizedLease 复刻自原 sqlite 实现的 authorizedLeaseTx。
func (s *Store) authorizedLease(leaseID, project string) (domain.MailboxLease, error) {
	project = normalizeLeaseProject(project)
	if project == "" {
		return domain.MailboxLease{}, ErrLeaseProjectRequired
	}
	var lease domain.MailboxLease
	found, err := s.readEntity("mailbox_leases", strings.TrimSpace(leaseID), &lease)
	if err != nil {
		return domain.MailboxLease{}, err
	}
	if !found {
		return domain.MailboxLease{}, ErrLeaseNotFound
	}
	if lease.Project != project {
		return domain.MailboxLease{}, ErrLeaseProjectMismatch
	}
	return lease, nil
}

// normalizeLeaseProject 复刻自原 sqlite 实现。
func normalizeLeaseProject(project string) string { return strings.ToLower(strings.TrimSpace(project)) }
