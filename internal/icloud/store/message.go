package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"gpt-go/internal/icloud/domain"
)

// MailboxSyncMessage 是一次同步中准备写入的标准邮件。复刻自原 sqlite 实现。
type MailboxSyncMessage struct {
	RemoteID    string
	RemoteIDs   []string
	CanonicalID string
	Source      string
	Subject     string
	From        string
	Body        string
	HTMLBody    string
	ContentType string
	ReceivedAt  time.Time
}

// MailboxSyncUpdate 描述一个邮箱在本轮同步后的游标和新增邮件。复刻自原 sqlite 实现。
type MailboxSyncUpdate struct {
	MailboxID string
	LastUID   string
	SyncedAt  time.Time
	Messages  []MailboxSyncMessage
}

// MessageContentUpdate 复刻自原 sqlite 实现。
type MessageContentUpdate struct {
	MailboxID   string
	MessageID   string
	Body        string
	HTMLBody    string
	ContentType string
}

// MessagesForMailbox 复刻自原 sqlite 实现：按 received_at 倒序。
func (s *Store) MessagesForMailbox(mailboxID string) []domain.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []domain.Message{}
	appendFn := func(data []byte) error {
		var message domain.Message
		if err := s.decodeInto("messages", data, &message); err != nil {
			return err
		}
		out = append(out, message)
		return nil
	}
	_ = s.findDocs("messages", bson.M{"mailbox_id": strings.TrimSpace(mailboxID)},
		options.Find().SetSort(bson.D{{Key: "received_at", Value: -1}}), appendFn)
	return out
}

// FindMessageForMailbox 复刻自原 sqlite 实现。
func (s *Store) FindMessageForMailbox(mailboxID, messageID string) (domain.Message, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var message domain.Message
	found, err := s.readEntity("messages", strings.TrimSpace(messageID), &message)
	return message, found && err == nil && message.MailboxID == strings.TrimSpace(mailboxID)
}

// MessagesMissingContent 复刻自原 sqlite 实现：source=imap 且 content_type 为空。
func (s *Store) MessagesMissingContent(limit int) []domain.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	findOpts := options.Find().SetSort(bson.D{{Key: "received_at", Value: -1}})
	if limit > 0 {
		findOpts.SetLimit(int64(limit))
	}
	filter := bson.M{
		"source":       "imap",
		"content_type": bson.M{"$in": bson.A{"", nil}},
	}
	out := []domain.Message{}
	appendFn := func(data []byte) error {
		var message domain.Message
		if err := s.decodeInto("messages", data, &message); err != nil {
			return err
		}
		// 复刻原 lower(source)='imap' AND COALESCE(content_type,'')='' 语义。
		if strings.ToLower(strings.TrimSpace(message.Source)) != "imap" || strings.TrimSpace(message.ContentType) != "" {
			return nil
		}
		out = append(out, message)
		return nil
	}
	_ = s.findDocs("messages", filter, findOpts, appendFn)
	return out
}

// UpsertMessage 复刻自原 sqlite 实现。
func (s *Store) UpsertMessage(mailboxID, remoteID, source, subject, from, body string, receivedAt time.Time) (domain.Message, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	message, added, mailbox, changes, err := s.upsertMessageDetailed(mailboxID, MailboxSyncMessage{
		RemoteID: remoteID, RemoteIDs: []string{remoteID}, Source: source, Subject: subject, From: from,
		Body: body, HTMLBody: "", ContentType: "text/plain", ReceivedAt: receivedAt,
	})
	if err != nil {
		return domain.Message{}, false, err
	}
	if !added {
		if len(changes) == 0 {
			return message, false, nil
		}
		return message, false, s.commitChanges(changes)
	}
	mailbox.LastSyncAt, mailbox.UpdatedAt = time.Now(), time.Now()
	mailboxChange, _, err := s.upsertEntity("mailboxes", "mailbox", mailbox.ID, mailbox)
	if err != nil {
		return domain.Message{}, false, err
	}
	changes = append(changes, mailboxChange)
	return message, true, s.commitChanges(changes)
}

// upsertMessageDetailed 复刻自原 sqlite 实现的 upsertMessageDetailedTx：
// canonical_id / remote_id / remote_ids 三路查重，命中则合并，未命中则新建并累加收件数。
func (s *Store) upsertMessageDetailed(mailboxID string, incoming MailboxSyncMessage) (domain.Message, bool, domain.Mailbox, []Change, error) {
	var mailbox domain.Mailbox
	if found, err := s.readEntity("mailboxes", mailboxID, &mailbox); err != nil || !found {
		if err != nil {
			return domain.Message{}, false, mailbox, nil, err
		}
		return domain.Message{}, false, mailbox, nil, errors.New("邮箱不存在")
	}
	remoteID := strings.TrimSpace(incoming.RemoteID)
	canonicalID := strings.TrimSpace(incoming.CanonicalID)
	remoteIDs := uniqueNonEmptyStrings(append(append([]string(nil), incoming.RemoteIDs...), remoteID))
	if remoteID != "" || canonicalID != "" {
		existing, found, err := s.findDuplicateMessage(mailboxID, remoteID, canonicalID)
		if err != nil {
			return domain.Message{}, false, mailbox, nil, err
		}
		if found {
			changed := false
			if existing.CanonicalID == "" && canonicalID != "" {
				existing.CanonicalID = canonicalID
				changed = true
			}
			mergedRemoteIDs := uniqueNonEmptyStrings(append(append([]string(nil), existing.RemoteIDs...), remoteIDs...))
			if !equalStrings(existing.RemoteIDs, mergedRemoteIDs) {
				existing.RemoteIDs = mergedRemoteIDs
				changed = true
			}
			if strings.TrimSpace(existing.ContentType) == "" && strings.TrimSpace(incoming.ContentType) != "" {
				existing.ContentType = strings.TrimSpace(incoming.ContentType)
				changed = true
			}
			if strings.TrimSpace(incoming.HTMLBody) != "" && existing.HTMLBody != incoming.HTMLBody {
				existing.HTMLBody = incoming.HTMLBody
				existing.ContentType = "text/html"
				changed = true
			}
			if strings.TrimSpace(incoming.Body) != "" && (strings.TrimSpace(existing.Body) == "" || changed) && existing.Body != incoming.Body {
				existing.Body = incoming.Body
				changed = true
			}
			if !changed {
				return existing, false, mailbox, nil, nil
			}
			change, _, err := s.upsertEntity("messages", "message", existing.ID, existing)
			if err != nil {
				return domain.Message{}, false, mailbox, nil, err
			}
			return existing, false, mailbox, []Change{change}, nil
		}
	}
	id, err := s.nextID("msg")
	if err != nil {
		return domain.Message{}, false, mailbox, nil, err
	}
	now := time.Now()
	if incoming.ReceivedAt.IsZero() {
		incoming.ReceivedAt = now
	}
	contentType := strings.TrimSpace(incoming.ContentType)
	if contentType == "" {
		contentType = "text/plain"
	}
	message := domain.Message{
		ID: id, OwnerID: mailbox.OwnerID, MailboxID: mailboxID, RemoteID: remoteID, RemoteIDs: remoteIDs, CanonicalID: canonicalID,
		Source: strings.TrimSpace(incoming.Source), Subject: strings.TrimSpace(incoming.Subject), From: strings.TrimSpace(incoming.From),
		Body: incoming.Body, HTMLBody: incoming.HTMLBody, ContentType: contentType, ReceivedAt: incoming.ReceivedAt, CreatedAt: now,
	}
	change, _, err := s.upsertEntity("messages", "message", message.ID, message)
	if err != nil {
		return domain.Message{}, false, mailbox, nil, err
	}
	mailbox.ReceiveCount++
	return message, true, mailbox, []Change{change}, nil
}

// findDuplicateMessage 复刻原 sqlite 三路查重：canonical_id → remote_id → remote_ids 数组包含。
func (s *Store) findDuplicateMessage(mailboxID, remoteID, canonicalID string) (domain.Message, bool, error) {
	ctx, cancel := s.ctx()
	defer cancel()
	findOne := func(filter bson.M) (domain.Message, bool, error) {
		var doc bson.M
		err := s.col("messages").FindOne(ctx, filter).Decode(&doc)
		if err != nil {
			return domain.Message{}, false, err
		}
		data, err := docToJSON(doc)
		if err != nil {
			return domain.Message{}, false, err
		}
		var message domain.Message
		if err := s.decodeInto("messages", data, &message); err != nil {
			return domain.Message{}, false, err
		}
		return message, true, nil
	}
	if canonicalID != "" {
		message, found, err := findOne(bson.M{"mailbox_id": mailboxID, "canonical_id": canonicalID})
		if err == nil {
			return message, found, nil
		}
		if !errors.Is(err, mongo.ErrNoDocuments) {
			return domain.Message{}, false, err
		}
	}
	if remoteID != "" {
		message, found, err := findOne(bson.M{"mailbox_id": mailboxID, "remote_id": remoteID})
		if err == nil {
			return message, found, nil
		}
		if !errors.Is(err, mongo.ErrNoDocuments) {
			return domain.Message{}, false, err
		}
		// remote_ids 数组包含查询，复刻原 json_each 语义。
		message, found, err = findOne(bson.M{"mailbox_id": mailboxID, "remote_ids": remoteID})
		if err == nil {
			return message, found, nil
		}
		if !errors.Is(err, mongo.ErrNoDocuments) {
			return domain.Message{}, false, err
		}
	}
	return domain.Message{}, false, nil
}

// ApplyMailboxSyncBatch 复刻自原 sqlite 实现：整组同步结果一次提交，只发布一条批量变更。
func (s *Store) ApplyMailboxSyncBatch(updates []MailboxSyncUpdate) (int, error) {
	return s.applyMailboxSyncBatch(updates, "", nil)
}

// ApplyMailboxSyncBatchWithRuntimeState 复刻自原 sqlite 实现。
func (s *Store) ApplyMailboxSyncBatchWithRuntimeState(updates []MailboxSyncUpdate, stateID string, state any) (int, error) {
	return s.applyMailboxSyncBatch(updates, strings.TrimSpace(stateID), state)
}

// applyMailboxSyncBatch 复刻自原 sqlite 实现：含 heartbeat 节流与批量 Change。
func (s *Store) applyMailboxSyncBatch(updates []MailboxSyncUpdate, stateID string, state any) (int, error) {
	if len(updates) == 0 {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	created := 0
	changedMailboxes := make([]domain.Mailbox, 0, len(updates))
	changedMessages := make([]domain.Message, 0)
	updatedMessages := 0
	for _, update := range updates {
		var mailbox domain.Mailbox
		if found, err := s.readEntity("mailboxes", strings.TrimSpace(update.MailboxID), &mailbox); err != nil || !found {
			if err != nil {
				return created, err
			}
			return created, errors.New("邮箱不存在")
		}
		mailboxChanged := false
		addedForMailbox := 0
		for _, incoming := range update.Messages {
			message, added, _, messageChanges, err := s.upsertMessageDetailed(mailbox.ID, incoming)
			if err != nil {
				return created, err
			}
			if added {
				created++
				addedForMailbox++
				mailboxChanged = true
				changedMessages = append(changedMessages, message)
			} else if len(messageChanges) > 0 {
				updatedMessages++
				changedMessages = append(changedMessages, message)
			}
		}
		mailbox.ReceiveCount += addedForMailbox
		syncedAt := update.SyncedAt
		if syncedAt.IsZero() {
			syncedAt = time.Now()
		}
		lastUID := strings.TrimSpace(update.LastUID)
		uidChanged := lastUID != "" && lastUID != mailbox.LastSyncUID
		heartbeatDue := mailbox.LastSyncAt.IsZero() || syncedAt.Sub(mailbox.LastSyncAt) >= mailboxSyncHeartbeatInterval
		if mailboxChanged || uidChanged || heartbeatDue {
			mailbox.LastSyncAt = syncedAt
			if lastUID != "" {
				mailbox.LastSyncUID = lastUID
			}
			mailbox.UpdatedAt = syncedAt
			if _, changed, err := s.upsertEntity("mailboxes", "mailbox", mailbox.ID, mailbox); err != nil {
				return created, err
			} else if changed {
				changedMailboxes = append(changedMailboxes, sanitizeMailbox(mailbox))
			}
		}
	}
	stateChanged := false
	if stateID != "" && state != nil {
		if _, changed, err := s.upsertEntity("runtime_states", "mail-sync", stateID, state); err != nil {
			return created, err
		} else {
			stateChanged = changed
		}
	}
	if created == 0 && updatedMessages == 0 && len(changedMailboxes) == 0 && !stateChanged {
		return 0, nil
	}
	payload, _ := json.Marshal(map[string]any{"operation": "batch-updated", "created_message_count": created, "updated_message_count": updatedMessages, "items": changedMailboxes, "messages": changedMessages})
	change := Change{Type: "mailbox.batch-updated", Resource: "mailbox", ResourceID: "batch", Operation: "batch-updated", Payload: payload, CreatedAt: time.Now()}
	return created, s.commitChanges([]Change{change})
}

// uniqueNonEmptyStrings 复刻自原 sqlite 实现。
func uniqueNonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

// equalStrings 复刻自原 sqlite 实现。
func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// sanitizeMailbox 复刻自原 sqlite 实现。
func sanitizeMailbox(mailbox domain.Mailbox) domain.Mailbox { mailbox.APIToken = ""; return mailbox }

// ApplyMessageContentUpdates 批量补全邮件正文，只发布一条不含正文的实时变更。
// 复刻自原 sqlite 实现。
func (s *Store) ApplyMessageContentUpdates(updates []MessageContentUpdate) (int, error) {
	if len(updates) == 0 {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	updated := 0
	mailboxIDs := map[string]bool{}
	for _, update := range updates {
		var message domain.Message
		found, err := s.readEntity("messages", strings.TrimSpace(update.MessageID), &message)
		if err != nil {
			return updated, err
		}
		if !found || message.MailboxID != strings.TrimSpace(update.MailboxID) {
			continue
		}
		if strings.TrimSpace(update.Body) != "" {
			message.Body = update.Body
		}
		if strings.TrimSpace(update.HTMLBody) != "" {
			message.HTMLBody = update.HTMLBody
			message.ContentType = "text/html"
		} else if strings.TrimSpace(update.ContentType) != "" {
			message.ContentType = strings.TrimSpace(update.ContentType)
		}
		if _, changed, err := s.upsertEntity("messages", "message", message.ID, message); err != nil {
			return updated, err
		} else if changed {
			updated++
			mailboxIDs[message.MailboxID] = true
		}
	}
	if updated == 0 {
		return 0, nil
	}
	ids := make([]string, 0, len(mailboxIDs))
	for id := range mailboxIDs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	payload, _ := json.Marshal(map[string]any{"operation": "content-backfilled", "updated_message_count": updated, "mailbox_ids": ids})
	change := Change{Type: "message.content-backfilled", Resource: "message", ResourceID: "batch", Operation: "content-backfilled", Payload: payload, CreatedAt: time.Now()}
	return updated, s.commitChanges([]Change{change})
}

// UpdateMessageContent 补全本地邮件正文，不改变邮箱收件数量。复刻自原 sqlite 实现。
func (s *Store) UpdateMessageContent(mailboxID, messageID, body, htmlBody, contentType string) (domain.Message, error) {
	if _, err := s.ApplyMessageContentUpdates([]MessageContentUpdate{{MailboxID: mailboxID, MessageID: messageID, Body: body, HTMLBody: htmlBody, ContentType: contentType}}); err != nil {
		return domain.Message{}, err
	}
	message, ok := s.FindMessageForMailbox(mailboxID, messageID)
	if !ok {
		return domain.Message{}, errors.New("邮件不存在")
	}
	return message, nil
}

// DeleteMailboxMessages 复刻自原 sqlite 实现：清空单个邮箱邮件并重置收件元数据。
func (s *Store) DeleteMailboxMessages(mailboxID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var mailbox domain.Mailbox
	if found, err := s.readEntity("mailboxes", mailboxID, &mailbox); err != nil || !found {
		if err != nil {
			return 0, err
		}
		return 0, errors.New("邮箱不存在")
	}
	ctx, cancel := s.ctx()
	defer cancel()
	result, err := s.col("messages").DeleteMany(ctx, bson.M{"mailbox_id": mailboxID})
	if err != nil {
		return 0, err
	}
	count := int(result.DeletedCount)
	metadataChanged := mailbox.ReceiveCount != 0 || mailbox.LastCodeMessageID != "" || !mailbox.LastCodeAt.IsZero()
	if count == 0 && !metadataChanged {
		return 0, nil
	}
	mailbox.ReceiveCount, mailbox.LastCodeMessageID, mailbox.LastCodeAt, mailbox.UpdatedAt = 0, "", time.Time{}, time.Now()
	mailboxChange, _, err := s.upsertEntity("mailboxes", "mailbox", mailbox.ID, mailbox)
	if err != nil {
		return 0, err
	}
	eventChange, err := s.appendEvent("info", "mailbox", fmt.Sprintf("已清空本地邮件 %s：%d 封", mailbox.Email, count))
	if err != nil {
		return 0, err
	}
	messageChange := makeEntityChange("messages", "message", mailboxID, "batch-deleted", nil, time.Now())
	return count, s.commitChanges([]Change{mailboxChange, messageChange, eventChange})
}

// DeleteAccountMessages 复刻自原 sqlite 实现：清空整个 Apple 账号的本地邮件并重置收件统计。
func (s *Store) DeleteAccountMessages(accountID string) (int, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return 0, errors.New("Apple 账号标识不能为空")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	mailboxes := make([]domain.Mailbox, 0)
	appendFn := func(data []byte) error {
		var mailbox domain.Mailbox
		if err := s.decodeInto("mailboxes", data, &mailbox); err != nil {
			return err
		}
		mailboxes = append(mailboxes, mailbox)
		return nil
	}
	if err := s.findDocs("mailboxes", bson.M{"account_id": accountID}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}), appendFn); err != nil {
		return 0, err
	}
	mailboxIDs := make([]string, 0, len(mailboxes))
	for _, mailbox := range mailboxes {
		mailboxIDs = append(mailboxIDs, mailbox.ID)
	}
	ctx, cancel := s.ctx()
	defer cancel()
	count := 0
	if len(mailboxIDs) > 0 {
		result, err := s.col("messages").DeleteMany(ctx, bson.M{"mailbox_id": bson.M{"$in": mailboxIDs}})
		if err != nil {
			return 0, err
		}
		count = int(result.DeletedCount)
	}
	changes := make([]Change, 0, len(mailboxes)+2)
	now := time.Now()
	for _, mailbox := range mailboxes {
		if mailbox.ReceiveCount == 0 && mailbox.LastCodeMessageID == "" && mailbox.LastCodeAt.IsZero() {
			continue
		}
		mailbox.ReceiveCount = 0
		mailbox.LastCodeMessageID = ""
		mailbox.LastCodeAt = time.Time{}
		mailbox.UpdatedAt = now
		change, _, err := s.upsertEntity("mailboxes", "mailbox", mailbox.ID, mailbox)
		if err != nil {
			return 0, err
		}
		changes = append(changes, change)
	}
	if count == 0 && len(changes) == 0 {
		return 0, nil
	}
	payload, _ := json.Marshal(map[string]any{"account_id": accountID, "deleted": count})
	changes = append(changes, Change{Type: "message.batch-deleted", Resource: "message", ResourceID: accountID, Operation: "batch-deleted", Payload: payload, CreatedAt: now})
	eventChange, err := s.appendEvent("info", "mailbox", fmt.Sprintf("已清空 Apple 账号本地邮件：%d 封", count))
	if err != nil {
		return 0, err
	}
	changes = append(changes, eventChange)
	return count, s.commitChanges(changes)
}

// DeleteMailboxMessagesByRemoteIDs 复刻自原 sqlite 实现：按 remote_id / remote_ids 数组匹配删除。
func (s *Store) DeleteMailboxMessagesByRemoteIDs(mailboxID string, remoteIDs []string) (int, error) {
	targets := make([]string, 0, len(remoteIDs))
	for _, id := range remoteIDs {
		if id = strings.TrimSpace(id); id != "" {
			targets = append(targets, id)
		}
	}
	if len(targets) == 0 {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var mailbox domain.Mailbox
	if found, err := s.readEntity("mailboxes", mailboxID, &mailbox); err != nil || !found {
		if err != nil {
			return 0, err
		}
		return 0, errors.New("邮箱不存在")
	}
	ctx, cancel := s.ctx()
	defer cancel()
	removed := 0
	for _, remoteID := range targets {
		result, err := s.col("messages").DeleteMany(ctx, bson.M{
			"mailbox_id": mailboxID,
			"$or": bson.A{
				bson.M{"remote_id": remoteID},
				bson.M{"remote_ids": remoteID},
			},
		})
		if err != nil {
			return 0, err
		}
		removed += int(result.DeletedCount)
	}
	if removed == 0 {
		return 0, nil
	}
	mailbox.ReceiveCount = s.recountMailboxMessages(mailboxID)
	mailbox.UpdatedAt = time.Now()
	mailboxChange, _, err := s.upsertEntity("mailboxes", "mailbox", mailbox.ID, mailbox)
	if err != nil {
		return 0, err
	}
	eventChange, err := s.appendEvent("info", "mailbox", fmt.Sprintf("已清理本地邮件缓存 %s：%d 封", mailbox.Email, removed))
	if err != nil {
		return 0, err
	}
	messageChange := makeEntityChange("messages", "message", mailboxID, "batch-deleted", nil, time.Now())
	return removed, s.commitChanges([]Change{mailboxChange, messageChange, eventChange})
}

// PruneMessagesBefore 清理保留期之外的邮件，并修正邮箱收件数。复刻自原 sqlite 实现。
func (s *Store) PruneMessagesBefore(before time.Time) (int, error) {
	if before.IsZero() {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// 复刻原 SELECT DISTINCT mailbox_id：先找出受影响邮箱。
	ctx, cancel := s.ctx()
	defer cancel()
	mailboxIDValues, err := s.col("messages").Distinct(ctx, "mailbox_id", bson.M{"received_at": bson.M{"$lt": before.Format(time.RFC3339Nano)}})
	if err != nil {
		return 0, err
	}
	ids := make([]string, 0, len(mailboxIDValues))
	for _, value := range mailboxIDValues {
		if id, ok := value.(string); ok && id != "" {
			ids = append(ids, id)
		}
	}
	result, err := s.col("messages").DeleteMany(ctx, bson.M{"received_at": bson.M{"$lt": before.Format(time.RFC3339Nano)}})
	if err != nil {
		return 0, err
	}
	count := int(result.DeletedCount)
	if count == 0 {
		return 0, nil
	}
	for _, id := range ids {
		var mailbox domain.Mailbox
		if found, _ := s.readEntity("mailboxes", id, &mailbox); !found {
			continue
		}
		mailbox.ReceiveCount = s.recountMailboxMessages(id)
		mailbox.UpdatedAt = time.Now()
		_, _, _ = s.upsertEntity("mailboxes", "mailbox", id, mailbox)
	}
	payload, _ := json.Marshal(map[string]any{"operation": "retention-pruned", "deleted": count})
	change := Change{Type: "message.retention-pruned", Resource: "message", ResourceID: "retention", Operation: "retention-pruned", Payload: payload, CreatedAt: time.Now()}
	return count, s.commitChanges([]Change{change})
}
