package store

import (
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"

	"gpt-go/internal/icloud/domain"
)

// Dashboard 复刻自原 sqlite 实现：各计数 + 最近 30 条事件。
func (s *Store) Dashboard() domain.Dashboard {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := domain.Dashboard{}
	out.AppleAccountCount = int(s.countDocs("apple_accounts", bson.M{}))
	out.ActiveAccountCount = int(s.countDocs("apple_accounts", bson.M{"$or": bson.A{
		bson.M{"status": domain.StatusActive},
		bson.M{"icloud_status": domain.ICloudStatusActive},
	}}))
	out.MailboxCount = int(s.countDocs("mailboxes", bson.M{}))
	out.AvailableCount = int(s.countDocs("mailboxes", bson.M{
		"api_active":    true,
		"icloud_active": true,
		"status":        domain.StatusAvailable,
	}))
	out.MessageCount = int(s.countDocs("messages", bson.M{}))
	appendFn := func(data []byte) error {
		var event domain.Event
		if err := s.decodeInto("events", data, &event); err != nil {
			return err
		}
		out.Events = append(out.Events, event)
		return nil
	}
	_ = s.findDocs("events", bson.M{},
		options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetLimit(30), appendFn)
	return out
}

// Settings 复刻自原 sqlite 实现。
func (s *Store) Settings() domain.Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	settings := domain.DefaultSettings()
	_, _ = s.readEntity("settings", "system", &settings)
	return settings
}

// SaveSettings 复刻自原 sqlite 实现。
func (s *Store) SaveSettings(settings domain.Settings) (domain.Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	settings.PublicAPIKey = strings.TrimSpace(settings.PublicAPIKey)
	settings.ServerChanSendKey = strings.TrimSpace(settings.ServerChanSendKey)
	settings.AppleAccountModuleReady = true
	change, _, err := s.upsertEntity("settings", "settings", "system", settings)
	if err != nil {
		return domain.Settings{}, err
	}
	eventChange, err := s.appendEvent("info", "settings", "已保存系统设置")
	if err != nil {
		return domain.Settings{}, err
	}
	return settings, s.commitChanges([]Change{change, eventChange})
}

// CreateSettings 复刻自原 sqlite 实现。
func (s *Store) CreateSettings() domain.CreateSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var settings domain.CreateSettings
	if found, _ := s.readEntity("create_settings", "system", &settings); !found {
		settings = domain.DefaultCreateSettings()
	}
	normalizeCreateSettings(&settings)
	settings.AccountIDs = append([]string(nil), settings.AccountIDs...)
	return settings
}

// SaveCreateSettings 复刻自原 sqlite 实现。
func (s *Store) SaveCreateSettings(settings domain.CreateSettings) (domain.CreateSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	normalizeCreateSettings(&settings)
	settings.UpdatedAt = time.Now()
	change, _, err := s.upsertEntity("create_settings", "create-settings", "system", settings)
	if err != nil {
		return domain.CreateSettings{}, err
	}
	eventChange, err := s.appendEvent("info", "settings", "已保存邮箱创建设置")
	if err != nil {
		return domain.CreateSettings{}, err
	}
	return settings, s.commitChanges([]Change{change, eventChange})
}

// Snapshot 从 MongoDB 组装导出快照，不保留运行期全量状态缓存。
// 复刻自原 sqlite 实现。
func (s *Store) Snapshot() domain.State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state := domain.State{SchemaVersion: domain.SchemaVersion, Settings: domain.DefaultSettings(), CreateSettings: domain.DefaultCreateSettings()}
	byCreatedAsc := bson.D{{Key: "created_at", Value: 1}}
	_ = s.loadEntities("apple_accounts", byCreatedAsc, &state.AppleAccounts)
	_ = s.loadEntities("mailboxes", byCreatedAsc, &state.Mailboxes)
	_ = s.loadEntities("mailbox_leases", byCreatedAsc, &state.MailboxLeases)
	_ = s.loadEntities("messages", bson.D{{Key: "received_at", Value: 1}}, &state.Messages)
	_ = s.loadEntities("events", byCreatedAsc, &state.Events)
	_ = s.loadEntities("web_sessions", byCreatedAsc, &state.Sessions)
	_ = s.loadEntities("icloud_sessions", bson.D{{Key: "saved_at", Value: 1}}, &state.ICloudSessions)
	_, _ = s.readEntity("settings", "system", &state.Settings)
	_, _ = s.readEntity("create_settings", "system", &state.CreateSettings)
	var admins []domain.Admin
	_ = s.loadEntities("admins", byCreatedAsc, &admins)
	if len(admins) > 0 {
		state.Admin = &admins[0]
	}
	// NextID 复刻原 metadata.next_id 快照语义：导出“下一个将分配的序号”。
	state.NextID = int(s.currentCounter("next_id")) + 1
	state.CreatedAt, _ = time.Parse(time.RFC3339Nano, s.metaValue("created_at"))
	state.UpdatedAt, _ = time.Parse(time.RFC3339Nano, s.metaValue("updated_at"))
	return state
}

// ClearEvents 复刻自原 sqlite 实现。
func (s *Store) ClearEvents() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, cancel := s.ctx()
	defer cancel()
	if _, err := s.col("events").DeleteMany(ctx, bson.M{}); err != nil {
		return err
	}
	change := makeEntityChange("events", "event", "all", "deleted", nil, time.Now())
	return s.commitChanges([]Change{change})
}

// RecordEvent 复刻自原 sqlite 实现：裁剪到 500 条由 appendEvent 保证。
func (s *Store) RecordEvent(level, category, message string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		return nil
	}
	if level = strings.TrimSpace(level); level == "" {
		level = "info"
	}
	if category = strings.TrimSpace(category); category == "" {
		category = "system"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	change, err := s.appendEvent(level, category, message)
	if err != nil {
		return err
	}
	return s.commitChanges([]Change{change})
}
