package store

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"

	"gpt-go/internal/icloud/domain"
)

// DeleteAppleAccountResult 复刻自原 sqlite 实现。
type DeleteAppleAccountResult struct {
	AccountID      string `json:"account_id"`
	AppleID        string `json:"apple_id"`
	Mailboxes      int    `json:"mailboxes"`
	Messages       int    `json:"messages"`
	ICloudSessions int    `json:"icloud_sessions"`
}

// FindAppleAccount 复刻自原 sqlite 实现。
func (s *Store) FindAppleAccount(id string) (domain.AppleAccount, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var account domain.AppleAccount
	found, err := s.readEntity("apple_accounts", strings.TrimSpace(id), &account)
	if found && err == nil {
		var session domain.ICloudSession
		if sessionFound, sessionErr := s.readEntity("icloud_sessions", account.ID, &session); sessionErr == nil && sessionFound {
			account.ICloudStatus = iCloudStatusFromSession(session)
		}
	}
	return account, found && err == nil
}

// ICloudSessionByAccountID 复刻自原 sqlite 实现。
func (s *Store) ICloudSessionByAccountID(accountID string) (domain.ICloudSession, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var session domain.ICloudSession
	found, err := s.readEntity("icloud_sessions", strings.TrimSpace(accountID), &session)
	return cloneICloudSession(session), found && err == nil
}

// SaveICloudSession 复刻自原 sqlite 实现。
func (s *Store) SaveICloudSession(session domain.ICloudSession) (domain.ICloudSession, error) {
	return s.saveICloudSession(session, "", "info", "已更新 Apple 登录态", "已保存 Apple 登录态")
}

// SaveICloudSessionWithPassword 在保存登录态时同步更新加密的 Apple ID 密码。
func (s *Store) SaveICloudSessionWithPassword(session domain.ICloudSession, password string) (domain.ICloudSession, error) {
	return s.saveICloudSession(session, password, "info", "已更新 Apple 登录态", "已保存 Apple 登录态")
}

// SaveICloudSessionWithEvent 复刻自原 sqlite 实现。
func (s *Store) SaveICloudSessionWithEvent(session domain.ICloudSession, level, message string) (domain.ICloudSession, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		message = "已更新 Apple 登录态"
	}
	return s.saveICloudSession(session, "", level, message, message)
}

// saveICloudSession 复刻自原 sqlite 实现的完整逻辑：
// 账号查找/创建 → OwnerID 取自 admin → 合并已有登录态 → 双向 upsert → 事件记录。
func (s *Store) saveICloudSession(session domain.ICloudSession, password, level, updateMessage, createMessage string) (domain.ICloudSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	level = strings.TrimSpace(level)
	if level == "" {
		level = "info"
	}
	accountID := strings.TrimSpace(session.AccountID)
	var account domain.AppleAccount
	accountCreated := false
	var err error
	if accountID == "" {
		accountID, err = s.findAppleAccountIDBySession(session)
		if err != nil {
			return domain.ICloudSession{}, err
		}
	}
	if accountID != "" {
		if found, err := s.readEntity("apple_accounts", accountID, &account); err != nil || !found {
			if err != nil {
				return domain.ICloudSession{}, err
			}
			return domain.ICloudSession{}, errors.New("Apple 账号不存在")
		}
	} else {
		accountID, err = s.nextID("acc")
		if err != nil {
			return domain.ICloudSession{}, err
		}
		now := time.Now()
		account = domain.AppleAccount{ID: accountID, Label: firstNonEmpty(session.AppleID, "Apple 账号"), AppleID: strings.TrimSpace(session.AppleID), Status: domain.StatusActive, ICloudStatus: iCloudStatusFromSession(session), Note: session.Note, CreatedAt: now, UpdatedAt: now}
		session.AccountID = accountID
		accountCreated = true
	}
	if session.SavedAt.IsZero() {
		session.SavedAt = time.Now()
	}
	if session.OwnerID == "" {
		// 复刻原语义：OwnerID 取自首位管理员。
		if admin, found := s.firstAdmin(); found {
			session.OwnerID = admin.ID
		}
	}
	var existing domain.ICloudSession
	exists, err := s.readEntity("icloud_sessions", accountID, &existing)
	if err != nil {
		return domain.ICloudSession{}, err
	}
	if exists {
		session = mergeICloudSession(existing, session)
	}
	session.AccountID = accountID
	account.OwnerID = firstNonEmpty(account.OwnerID, session.OwnerID)
	account.AppleID = firstNonEmpty(session.AppleID, account.AppleID)
	if strings.TrimSpace(password) != "" {
		account.Password = password
	}
	if account.Label == "" {
		account.Label = firstNonEmpty(account.AppleID, "Apple 账号")
	}
	account.Status = domain.StatusActive
	account.ICloudStatus = iCloudStatusFromSession(session)
	account.Note = firstNonEmpty(session.Note, account.Note)
	account.UpdatedAt = time.Now()
	changes := []Change{}
	accountChange, accountChanged, err := s.upsertEntity("apple_accounts", "apple-account", account.ID, account)
	if err != nil {
		return domain.ICloudSession{}, err
	}
	if accountChanged {
		changes = append(changes, accountChange)
	}
	sessionChange, sessionChanged, err := s.upsertEntity("icloud_sessions", "apple-session", accountID, session)
	if err != nil {
		return domain.ICloudSession{}, err
	}
	if sessionChanged {
		changes = append(changes, sessionChange)
	}
	if !accountChanged && !sessionChanged {
		return cloneICloudSession(session), nil
	}
	message := updateMessage
	if accountCreated || !exists {
		message = createMessage
	}
	eventChange, err := s.appendEvent(level, "apple", message)
	if err != nil {
		return domain.ICloudSession{}, err
	}
	changes = append(changes, eventChange)
	return cloneICloudSession(session), s.commitChanges(changes)
}

// findAppleAccountIDBySession 复刻自原 sqlite 实现的 findAppleAccountIDBySessionTx：
// 先按 DSID 匹配 icloud_sessions，再按 apple_id 匹配 apple_accounts，最后按 apple_id 匹配 icloud_sessions。
func (s *Store) findAppleAccountIDBySession(session domain.ICloudSession) (string, error) {
	ctx, cancel := s.ctx()
	defer cancel()
	dsid := strings.TrimSpace(session.DSID)
	if dsid != "" {
		// 复刻原排序：COALESCE(saved_at, updated_at) ASC, id ASC。
		cursor, err := s.col("icloud_sessions").Find(ctx, bson.M{"dsid": dsid},
			options.Find().SetProjection(bson.M{"_id": 1, "saved_at": 1}))
		if err != nil {
			return "", err
		}
		var docs []bson.M
		if err := cursor.All(ctx, &docs); err != nil {
			return "", err
		}
		if len(docs) > 0 {
			sortByTimeThenID(docs, "saved_at")
			return strings.TrimSpace(stringValue(docs[0]["_id"])), nil
		}
	}

	appleID := strings.TrimSpace(session.AppleID)
	if appleID == "" {
		return "", nil
	}
	// apple_id 大小写不敏感匹配：与 SQL lower() 语义一致，改用读回后内存比较，避免正则注入与排序复杂度。
	matchByAppleID := func(collection, timeField string) (string, error) {
		cursor, err := s.col(collection).Find(ctx, bson.M{},
			options.Find().SetProjection(bson.M{"_id": 1, "apple_id": 1, timeField: 1}))
		if err != nil {
			return "", err
		}
		var docs []bson.M
		if err := cursor.All(ctx, &docs); err != nil {
			return "", err
		}
		matched := docs[:0]
		for _, doc := range docs {
			if strings.EqualFold(strings.TrimSpace(stringValue(doc["apple_id"])), appleID) {
				matched = append(matched, doc)
			}
		}
		if len(matched) == 0 {
			return "", nil
		}
		sortByTimeThenID(matched, timeField)
		return strings.TrimSpace(stringValue(matched[0]["_id"])), nil
	}
	if accountID, err := matchByAppleID("apple_accounts", "created_at"); err != nil {
		return "", err
	} else if accountID != "" {
		return accountID, nil
	}
	return matchByAppleID("icloud_sessions", "saved_at")
}

// sortByTimeThenID 复刻原 SQL ORDER BY COALESCE(timeField, updated_at), id 的排序语义。
func sortByTimeThenID(docs []bson.M, timeField string) {
	keyOf := func(doc bson.M) (string, string) {
		ts := stringValue(doc[timeField])
		if ts == "" {
			ts = stringValue(doc["updated_at"])
		}
		return ts, stringValue(doc["_id"])
	}
	for i := 1; i < len(docs); i++ {
		for j := i; j > 0; j-- {
			t1, id1 := keyOf(docs[j-1])
			t2, id2 := keyOf(docs[j])
			if t1 > t2 || (t1 == t2 && id1 > id2) {
				docs[j-1], docs[j] = docs[j], docs[j-1]
			} else {
				break
			}
		}
	}
}

// DeleteAppleAccount 删除数据库中的 Apple 账号、登录态、关联邮箱、租约和邮件。
// 复刻自原 sqlite 实现。
func (s *Store) DeleteAppleAccount(id string) (DeleteAppleAccountResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id = strings.TrimSpace(id)
	var account domain.AppleAccount
	if found, err := s.readEntity("apple_accounts", id, &account); err != nil || !found {
		if err != nil {
			return DeleteAppleAccountResult{}, err
		}
		return DeleteAppleAccountResult{}, errors.New("Apple 账号不存在")
	}
	ctx, cancel := s.ctx()
	defer cancel()
	result := DeleteAppleAccountResult{AccountID: account.ID, AppleID: account.AppleID}
	// 先取该账号全部邮箱 ID，用于级联删除邮件与租约。
	mailboxIDs := make([]string, 0)
	cursor, err := s.col("mailboxes").Find(ctx, bson.M{"account_id": id}, options.Find().SetProjection(bson.M{"_id": 1}))
	if err != nil {
		return DeleteAppleAccountResult{}, err
	}
	for cursor.Next(ctx) {
		var doc struct {
			ID string `bson:"_id"`
		}
		if err := cursor.Decode(&doc); err != nil {
			_ = cursor.Close(ctx)
			return DeleteAppleAccountResult{}, err
		}
		mailboxIDs = append(mailboxIDs, doc.ID)
	}
	_ = cursor.Close(ctx)
	result.Mailboxes = len(mailboxIDs)
	if len(mailboxIDs) > 0 {
		msgFilter := bson.M{"mailbox_id": bson.M{"$in": mailboxIDs}}
		result.Messages = int(s.countDocs("messages", msgFilter))
		if _, err := s.col("messages").DeleteMany(ctx, msgFilter); err != nil {
			return DeleteAppleAccountResult{}, err
		}
		if _, err := s.col("mailbox_leases").DeleteMany(ctx, bson.M{"mailbox_id": bson.M{"$in": mailboxIDs}}); err != nil {
			return DeleteAppleAccountResult{}, err
		}
		if _, err := s.col("mailboxes").DeleteMany(ctx, bson.M{"_id": bson.M{"$in": mailboxIDs}}); err != nil {
			return DeleteAppleAccountResult{}, err
		}
	}
	result.ICloudSessions = int(s.countDocs("icloud_sessions", bson.M{"_id": id}))
	_, _ = s.col("icloud_sessions").DeleteOne(ctx, bson.M{"_id": id})
	change, _, err := s.deleteEntity("apple_accounts", "apple-account", id)
	if err != nil {
		return DeleteAppleAccountResult{}, err
	}
	eventChange, err := s.appendEvent("warning", "apple", "已删除 Apple 账号及其全部本地关联数据 "+firstNonEmpty(account.AppleID, account.ID))
	if err != nil {
		return DeleteAppleAccountResult{}, err
	}
	return result, s.commitChanges([]Change{change, eventChange})
}

// UpdateICloudSession 复刻自原 sqlite 实现。
func (s *Store) UpdateICloudSession(accountID string, update func(*domain.ICloudSession) error) (domain.ICloudSession, error) {
	accountID = strings.TrimSpace(accountID)
	session, ok := s.ICloudSessionByAccountID(accountID)
	if !ok {
		return domain.ICloudSession{}, errors.New("Apple 账号登录态不存在")
	}
	if err := update(&session); err != nil {
		return domain.ICloudSession{}, err
	}
	return s.SaveICloudSession(session)
}

// iCloudStatusFromSession 复刻自原 sqlite 实现，逐行保持一致。
func iCloudStatusFromSession(session domain.ICloudSession) string {
	successes := 0
	failures := 0
	for _, state := range session.LoginStates {
		if state.LastCheckedAt.IsZero() {
			continue
		}
		if state.LastCheckOK {
			successes++
		} else {
			failures++
		}
	}
	if successes > 0 {
		if failures > 0 {
			return domain.ICloudStatusPartial
		}
		return domain.ICloudStatusActive
	}
	if failures > 0 {
		return domain.ICloudStatusFailed
	}
	if !session.LastCheckedAt.IsZero() {
		if session.LastCheckOK {
			return domain.ICloudStatusActive
		}
		return domain.ICloudStatusFailed
	}
	if session.IsICloudPlus && session.CanCreateHME {
		return domain.ICloudStatusActive
	}
	if !session.IsICloudPlus {
		return domain.ICloudStatusNoICloudPlus
	}
	return domain.ICloudStatusActive
}

// sameICloudSessionIdentity 复刻自原 sqlite 实现。
func sameICloudSessionIdentity(left, right domain.ICloudSession) bool {
	if left.AccountID != "" && right.AccountID != "" {
		return left.AccountID == right.AccountID
	}
	if left.DSID != "" && right.DSID != "" {
		return left.DSID == right.DSID
	}
	return strings.EqualFold(strings.TrimSpace(left.AppleID), strings.TrimSpace(right.AppleID))
}

// mergeICloudSession 复刻自原 sqlite 实现，逐行保持一致。
func mergeICloudSession(existing, incoming domain.ICloudSession) domain.ICloudSession {
	out := cloneICloudSession(incoming)
	out.OwnerID = firstNonEmpty(incoming.OwnerID, existing.OwnerID)
	out.AccountID = firstNonEmpty(incoming.AccountID, existing.AccountID)
	if out.SavedAt.IsZero() {
		out.SavedAt = existing.SavedAt
	}
	out.AppleID = firstNonEmpty(incoming.AppleID, existing.AppleID)
	out.DSID = firstNonEmpty(incoming.DSID, existing.DSID)
	out.ClientID = firstNonEmpty(incoming.ClientID, existing.ClientID)
	out.ClientBuildNumber = firstNonEmpty(incoming.ClientBuildNumber, existing.ClientBuildNumber)
	out.MasteringNumber = firstNonEmpty(incoming.MasteringNumber, existing.MasteringNumber)
	out.PremiumMailBaseURL = firstNonEmpty(incoming.PremiumMailBaseURL, existing.PremiumMailBaseURL)
	out.MailGatewayBaseURL = firstNonEmpty(incoming.MailGatewayBaseURL, existing.MailGatewayBaseURL)
	out.MailBaseURL = firstNonEmpty(incoming.MailBaseURL, existing.MailBaseURL)
	out.Host = firstNonEmpty(incoming.Host, existing.Host)
	out.IsICloudPlus = incoming.IsICloudPlus || existing.IsICloudPlus
	out.CanCreateHME = incoming.CanCreateHME || existing.CanCreateHME
	if len(out.Cookies) == 0 {
		out.Cookies = append([]domain.SessionCookie(nil), existing.Cookies...)
	}
	out.LoginStates = mergeLoginStates(existing.LoginStates, incoming.LoginStates)
	out.Note = firstNonEmpty(incoming.Note, existing.Note)
	if out.LastCheckedAt.IsZero() {
		out.LastCheckedAt = existing.LastCheckedAt
		out.LastCheckOK = existing.LastCheckOK
		out.LastStatusMessage = existing.LastStatusMessage
	}
	return out
}

// mergeLoginStates 复刻自原 sqlite 实现。
func mergeLoginStates(existing, incoming []domain.LoginState) []domain.LoginState {
	out := cloneLoginStates(existing)
	for _, state := range incoming {
		replaced := false
		for i := range out {
			if out[i].Kind == state.Kind {
				out[i] = cloneLoginState(state)
				replaced = true
				break
			}
		}
		if !replaced {
			out = append(out, cloneLoginState(state))
		}
	}
	return out
}

// cloneICloudSession 复刻自原 sqlite 实现。
func cloneICloudSession(session domain.ICloudSession) domain.ICloudSession {
	data, _ := json.Marshal(session)
	var out domain.ICloudSession
	_ = json.Unmarshal(data, &out)
	return out
}

// cloneLoginStates 复刻自原 sqlite 实现。
func cloneLoginStates(states []domain.LoginState) []domain.LoginState {
	out := make([]domain.LoginState, 0, len(states))
	for _, state := range states {
		out = append(out, cloneLoginState(state))
	}
	return out
}

// cloneLoginState 复刻自原 sqlite 实现。
func cloneLoginState(state domain.LoginState) domain.LoginState {
	state.Cookies = append([]domain.SessionCookie(nil), state.Cookies...)
	return state
}

// firstNonEmpty 复刻自原 sqlite 实现。
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// AppleAccounts 复刻自原 sqlite 实现：去掉 password 字段并按 icloud_sessions 填充 ICloudStatus。
func (s *Store) AppleAccounts() []domain.AppleAccount {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []domain.AppleAccount
	_ = s.loadEntities("apple_accounts", bson.D{{Key: "created_at", Value: -1}}, &out)
	for index := range out {
		out[index].Password = ""
	}
	var sessions []domain.ICloudSession
	if err := s.loadEntities("icloud_sessions", nil, &sessions); err == nil {
		byAccount := make(map[string]domain.ICloudSession, len(sessions))
		for _, session := range sessions {
			byAccount[session.AccountID] = session
		}
		for index := range out {
			if session, ok := byAccount[out[index].ID]; ok {
				out[index].ICloudStatus = iCloudStatusFromSession(session)
			}
		}
	}
	return out
}

// ICloudSessions 复刻自原 sqlite 实现。
func (s *Store) ICloudSessions() []domain.ICloudSession {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var sessions []domain.ICloudSession
	_ = s.loadEntities("icloud_sessions", bson.D{{Key: "saved_at", Value: -1}}, &sessions)
	return sessions
}
