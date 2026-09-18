package store

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"

	"gpt-go/internal/icloud/domain"
)

const mailboxSyncHeartbeatInterval = time.Minute

// AllMailboxes 复刻自原 sqlite 实现。
func (s *Store) AllMailboxes() []domain.Mailbox {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []domain.Mailbox
	_ = s.loadEntities("mailboxes", bson.D{{Key: "created_at", Value: -1}}, &out)
	return out
}

// Mailboxes 复刻自原 sqlite 实现：分页 + query/status/accountID 过滤，APIToken 置空返回。
func (s *Store) Mailboxes(query, status, accountID string, page, pageSize int) domain.MailboxPage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	query = strings.ToLower(strings.TrimSpace(query))
	status = strings.ToLower(strings.TrimSpace(status))
	accountID = strings.TrimSpace(accountID)
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 7
	}
	if pageSize > 200 {
		pageSize = 200
	}
	// 过滤条件复刻原 SQL where 子句语义；query 走读回后内存过滤（等价于原 lower(...) LIKE）。
	filter := bson.M{}
	if accountID != "" {
		filter["account_id"] = accountID
	}
	if status != "" {
		// 原 SQL 为 lower(status) = ?；status 均为小写枚举，直接等值匹配。
		filter["status"] = status
	}
	matches := func(mailbox domain.Mailbox) bool {
		if query == "" {
			return true
		}
		haystack := strings.ToLower(mailbox.Email + " " + mailbox.Label + " " + mailbox.Note)
		return strings.Contains(haystack, query)
	}
	matched := make([]domain.Mailbox, 0)
	appendFn := func(data []byte) error {
		var mailbox domain.Mailbox
		if err := s.decodeInto("mailboxes", data, &mailbox); err != nil {
			return err
		}
		if matches(mailbox) {
			matched = append(matched, mailbox)
		}
		return nil
	}
	_ = s.findDocs("mailboxes", filter, options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}), appendFn)
	total := len(matched)
	totalPages := (total + pageSize - 1) / pageSize
	if totalPages == 0 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}
	start := (page - 1) * pageSize
	end := start + pageSize
	if start > total {
		start = total
	}
	if end > total {
		end = total
	}
	items := make([]domain.Mailbox, 0, pageSize)
	for _, mailbox := range matched[start:end] {
		mailbox.APIToken = ""
		items = append(items, mailbox)
	}
	return domain.MailboxPage{Items: items, Page: page, PageSize: pageSize, Total: total, TotalPages: totalPages}
}

// FindMailboxByID 复刻自原 sqlite 实现。
func (s *Store) FindMailboxByID(id string) (domain.Mailbox, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var mailbox domain.Mailbox
	found, err := s.readEntity("mailboxes", strings.TrimSpace(id), &mailbox)
	return mailbox, found && err == nil
}

// FindMailboxByEmail 复刻自原 sqlite 实现：lower(email) 匹配。
func (s *Store) FindMailboxByEmail(email string) (domain.Mailbox, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.findMailboxByEmail(email)
}

// findMailboxByEmail 复刻原 `WHERE lower(email) = ? LIMIT 1` 语义。
func (s *Store) findMailboxByEmail(email string) (domain.Mailbox, bool) {
	target := strings.ToLower(strings.TrimSpace(email))
	var mailbox domain.Mailbox
	found := false
	_ = s.findDocs("mailboxes", bson.M{}, options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}}), func(data []byte) error {
		if found {
			return errScanDone
		}
		var candidate domain.Mailbox
		if err := s.decodeInto("mailboxes", data, &candidate); err != nil {
			return err
		}
		if strings.ToLower(candidate.Email) == target {
			mailbox = candidate
			found = true
			return errScanDone
		}
		return nil
	})
	return mailbox, found
}

// errScanDone 用于在 findDocs 回调中提前终止扫描（对应原 SQL LIMIT 1）。
var errScanDone = errors.New("scan done")

// finishFindDocs 吞掉 errScanDone，其余错误透传。
func finishFindDocs(err error) error {
	if errors.Is(err, errScanDone) {
		return nil
	}
	return err
}

// UpsertMailboxFromRemote 复刻自原 sqlite 实现：
// 已存在则合并远端身份；不存在则生成 randomAPIToken(24) + OwnerID 取自 admin + 状态机。
func (s *Store) UpsertMailboxFromRemote(accountID string, remote domain.RemoteMailbox, defaultNote string) (domain.Mailbox, bool, error) {
	email := strings.ToLower(strings.TrimSpace(remote.Email))
	if email == "" {
		return domain.Mailbox{}, false, errors.New("Apple 返回的隐私邮箱地址为空")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	mailbox, created := s.findMailboxByEmail(email)
	created = !created
	now := time.Now()
	if !created {
		mailbox.AccountID = firstNonEmpty(accountID, mailbox.AccountID)
		mailbox.AnonymousID = firstNonEmpty(remote.AnonymousID, mailbox.AnonymousID)
		mailbox.RemoteOrigin = firstNonEmpty(remote.Origin, mailbox.RemoteOrigin)
		mailbox.ForwardToEmail = firstNonEmpty(strings.ToLower(strings.TrimSpace(remote.ForwardToEmail)), mailbox.ForwardToEmail)
		if strings.TrimSpace(remote.Label) != "" {
			mailbox.Label = strings.TrimSpace(remote.Label)
		}
		mailbox.ICloudActive = remote.IsActive
		if strings.TrimSpace(mailbox.Note) == "" {
			mailbox.Note = firstNonEmpty(remote.Note, defaultNote)
		}
		mailbox.UpdatedAt = now
		change, changed, err := s.upsertEntity("mailboxes", "mailbox", mailbox.ID, mailbox)
		if err != nil {
			return domain.Mailbox{}, false, err
		}
		if !changed {
			return mailbox, false, nil
		}
		return mailbox, false, s.commitChanges([]Change{change})
	}
	id, err := s.nextID("mbx")
	if err != nil {
		return domain.Mailbox{}, false, err
	}
	token, err := randomAPIToken(24)
	if err != nil {
		return domain.Mailbox{}, false, err
	}
	ownerID := ""
	if admin, found := s.firstAdmin(); found {
		ownerID = admin.ID
	}
	status := domain.StatusAvailable
	if !remote.IsActive {
		status = domain.StatusDisabled
	}
	mailbox = domain.Mailbox{
		ID: id, OwnerID: ownerID, AccountID: strings.TrimSpace(accountID), AnonymousID: strings.TrimSpace(remote.AnonymousID),
		RemoteOrigin: strings.TrimSpace(remote.Origin), Label: firstNonEmpty(remote.Label, "隐私邮箱 "+now.Format("0102-150405")),
		Email: email, ForwardToEmail: strings.ToLower(strings.TrimSpace(remote.ForwardToEmail)), APIToken: token, APIActive: true, ICloudActive: remote.IsActive, Status: status,
		Note: firstNonEmpty(remote.Note, defaultNote), CreatedAt: now, UpdatedAt: now,
	}
	mailboxChange, _, err := s.upsertEntity("mailboxes", "mailbox", mailbox.ID, mailbox)
	if err != nil {
		return domain.Mailbox{}, false, err
	}
	eventChange, err := s.appendEvent("info", "mailbox", "已导入隐私邮箱 "+mailbox.Email)
	if err != nil {
		return domain.Mailbox{}, false, err
	}
	return mailbox, true, s.commitChanges([]Change{mailboxChange, eventChange})
}

// SetMailboxRemoteIdentity 复刻自原 sqlite 实现。
func (s *Store) SetMailboxRemoteIdentity(id, anonymousID, origin string) (domain.Mailbox, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var mailbox domain.Mailbox
	if found, err := s.readEntity("mailboxes", id, &mailbox); err != nil || !found {
		if err != nil {
			return domain.Mailbox{}, err
		}
		return domain.Mailbox{}, errors.New("邮箱不存在")
	}
	anonymousID, origin = strings.TrimSpace(anonymousID), strings.TrimSpace(origin)
	if mailbox.AnonymousID == anonymousID && mailbox.RemoteOrigin == origin {
		return mailbox, nil
	}
	mailbox.AnonymousID, mailbox.RemoteOrigin, mailbox.UpdatedAt = anonymousID, origin, time.Now()
	change, _, err := s.upsertEntity("mailboxes", "mailbox", mailbox.ID, mailbox)
	if err != nil {
		return domain.Mailbox{}, err
	}
	return mailbox, s.commitChanges([]Change{change})
}

// SetMailboxStatus 复刻自原 sqlite 实现：含租约联动状态机。
func (s *Store) SetMailboxStatus(id string, apiActive, icloudActive *bool, status string, note *string) (domain.Mailbox, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var mailbox domain.Mailbox
	if found, err := s.readEntity("mailboxes", id, &mailbox); err != nil || !found {
		if err != nil {
			return domain.Mailbox{}, err
		}
		return domain.Mailbox{}, errors.New("邮箱不存在")
	}
	now := time.Now()
	changes := []Change{}
	if apiActive != nil {
		mailbox.APIActive = *apiActive
	}
	if icloudActive != nil {
		mailbox.ICloudActive = *icloudActive
	}
	desiredStatus := strings.TrimSpace(status)
	if desiredStatus != "" {
		if desiredStatus == domain.StatusReserved && mailbox.ActiveLeaseID == "" {
			return domain.Mailbox{}, errors.New("邮箱没有有效租约，不能手动标记为已预留")
		}
		if mailbox.ActiveLeaseID != "" && desiredStatus != domain.StatusReserved {
			var lease domain.MailboxLease
			if found, _ := s.readEntity("mailbox_leases", mailbox.ActiveLeaseID, &lease); found && lease.State == domain.MailboxLeaseClaimed {
				lease.UpdatedAt = now
				if desiredStatus == domain.StatusUsed {
					lease.State, lease.CommittedAt = domain.MailboxLeaseCommitted, now
				} else {
					lease.State, lease.ReleasedAt = domain.MailboxLeaseReleased, now
				}
				if change, changed, err := s.upsertEntity("mailbox_leases", "mailbox-lease", lease.ID, lease); err != nil {
					return domain.Mailbox{}, err
				} else if changed {
					changes = append(changes, change)
				}
			}
			mailbox.ActiveLeaseID = ""
		}
		mailbox.Status = desiredStatus
	}
	if note != nil {
		mailbox.Note = strings.TrimSpace(*note)
		if mailbox.ActiveLeaseID != "" {
			var lease domain.MailboxLease
			if found, _ := s.readEntity("mailbox_leases", mailbox.ActiveLeaseID, &lease); found && lease.State == domain.MailboxLeaseClaimed {
				lease.Note, lease.UpdatedAt = mailbox.Note, now
				if change, changed, err := s.upsertEntity("mailbox_leases", "mailbox-lease", lease.ID, lease); err != nil {
					return domain.Mailbox{}, err
				} else if changed {
					changes = append(changes, change)
				}
			}
		}
	}
	mailbox.UpdatedAt = now
	mailboxChange, changed, err := s.upsertEntity("mailboxes", "mailbox", mailbox.ID, mailbox)
	if err != nil {
		return domain.Mailbox{}, err
	}
	if !changed && len(changes) == 0 {
		return mailbox, nil
	}
	if changed {
		changes = append(changes, mailboxChange)
	}
	eventChange, err := s.appendEvent("info", "mailbox", "已更新邮箱状态 "+mailbox.Email)
	if err != nil {
		return domain.Mailbox{}, err
	}
	changes = append(changes, eventChange)
	return mailbox, s.commitChanges(changes)
}

// DeleteMailbox 复刻自原 sqlite 实现：级联删除 messages/leases。
func (s *Store) DeleteMailbox(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var mailbox domain.Mailbox
	if found, err := s.readEntity("mailboxes", id, &mailbox); err != nil || !found {
		if err != nil {
			return err
		}
		return errors.New("邮箱不存在")
	}
	ctx, cancel := s.ctx()
	defer cancel()
	if _, err := s.col("messages").DeleteMany(ctx, bson.M{"mailbox_id": id}); err != nil {
		return err
	}
	if _, err := s.col("mailbox_leases").DeleteMany(ctx, bson.M{"mailbox_id": id}); err != nil {
		return err
	}
	change, _, err := s.deleteEntity("mailboxes", "mailbox", id)
	if err != nil {
		return err
	}
	eventChange, err := s.appendEvent("warning", "mailbox", "已删除本地邮箱 "+mailbox.Email)
	if err != nil {
		return err
	}
	return s.commitChanges([]Change{change, eventChange})
}

// SetMailboxSyncCursor 复刻自原 sqlite 实现。
func (s *Store) SetMailboxSyncCursor(id string, syncedAt time.Time, lastUID string) (domain.Mailbox, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var mailbox domain.Mailbox
	if found, err := s.readEntity("mailboxes", id, &mailbox); err != nil || !found {
		if err != nil {
			return domain.Mailbox{}, err
		}
		return domain.Mailbox{}, errors.New("邮箱不存在")
	}
	if syncedAt.IsZero() {
		syncedAt = time.Now()
	}
	lastUID = strings.TrimSpace(lastUID)
	if lastUID == mailbox.LastSyncUID && !mailbox.LastSyncAt.IsZero() && syncedAt.Sub(mailbox.LastSyncAt) < mailboxSyncHeartbeatInterval {
		return mailbox, nil
	}
	mailbox.LastSyncAt = syncedAt
	if lastUID != "" {
		mailbox.LastSyncUID = lastUID
	}
	mailbox.UpdatedAt = syncedAt
	change, _, err := s.upsertEntity("mailboxes", "mailbox", mailbox.ID, mailbox)
	if err != nil {
		return domain.Mailbox{}, err
	}
	return mailbox, s.commitChanges([]Change{change})
}

// SetMailboxLastCode 复刻自原 sqlite 实现。
func (s *Store) SetMailboxLastCode(id, messageID string, servedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var mailbox domain.Mailbox
	if found, err := s.readEntity("mailboxes", id, &mailbox); err != nil || !found {
		if err != nil {
			return err
		}
		return errors.New("邮箱不存在")
	}
	mailbox.LastCodeMessageID, mailbox.LastCodeAt, mailbox.UpdatedAt = strings.TrimSpace(messageID), servedAt, time.Now()
	change, _, err := s.upsertEntity("mailboxes", "mailbox", mailbox.ID, mailbox)
	if err != nil {
		return err
	}
	return s.commitChanges([]Change{change})
}

// NextMailboxLabel 复刻自原 sqlite 实现：扫描 prefix_N 形式标签取最大序号。
func (s *Store) NextMailboxLabel(prefix string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if strings.TrimSpace(prefix) == "" {
		prefix = domain.DefaultCreateSettings().Label
	}
	maxNumber := 0
	marker := prefix + "_"
	appendFn := func(data []byte) error {
		var doc struct {
			Label string `json:"label"`
		}
		if err := s.decodeInto("mailboxes", data, &doc); err != nil {
			return err
		}
		label := doc.Label
		if !strings.HasPrefix(strings.ToLower(label), strings.ToLower(marker)) {
			return nil
		}
		number, err := strconv.Atoi(label[len(marker):])
		if err == nil && number > maxNumber {
			maxNumber = number
		}
		return nil
	}
	_ = s.findDocs("mailboxes", bson.M{"label": bson.M{"$regex": "^" + regexEscape(marker), "$options": "i"}}, nil, appendFn)
	if maxNumber == 0 {
		// 与原版一致：无匹配时从 _1 开始。
		return prefix + "_1"
	}
	return prefix + "_" + strconv.Itoa(maxNumber+1)
}

// regexEscape 转义正则元字符，保证标签前缀按字面匹配。
func regexEscape(value string) string {
	var builder strings.Builder
	for _, r := range value {
		if strings.ContainsRune(`\.+*?()|[]{}^$`, r) {
			builder.WriteByte('\\')
		}
		builder.WriteRune(r)
	}
	return builder.String()
}

// randomAPIToken 复刻自原 sqlite 实现。
func randomAPIToken(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

// 供 message.go 使用的邮箱收件数重算，复刻原 `SELECT COUNT(*) ... WHERE mailbox_id = ?`。
func (s *Store) recountMailboxMessages(mailboxID string) int {
	return int(s.countDocs("messages", bson.M{"mailbox_id": mailboxID}))
}
