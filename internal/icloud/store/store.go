// Package store 基于 MongoDB 实现 iCloud 隐私邮箱的数据访问层。
// 整体语义复刻自原 sqlite 实现（iCloud-Privacy-Mail-v2/internal/store），
// 仅将存储引擎由 SQLite 单库文件切换为 MongoDB 集合。
package store

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"gpt-go/internal/icloud/config"
	"gpt-go/internal/icloud/domain"
)

const (
	// databaseSchemaVersion 复刻自原 sqlite 实现（schema v5）。
	databaseSchemaVersion = 5
	defaultChangeLogLimit = 5000
	secretPrefix          = "enc:v1:"
	// mongoOpTimeout 单次 MongoDB 操作的默认超时。
	mongoOpTimeout = 30 * time.Second
)

// entityCollections 复刻自原 sqlite 实现的 entityTables。
var entityCollections = []string{
	"admins",
	"web_sessions",
	"apple_accounts",
	"mailboxes",
	"mailbox_leases",
	"messages",
	"events",
	"settings",
	"create_settings",
	"icloud_sessions",
}

// Store 是 MongoDB 版数据访问层，字段布局复刻自原 sqlite 实现。
type Store struct {
	mu             sync.RWMutex
	database       string
	client         *mongo.Client
	db             *mongo.Database
	codec          *secretCodec
	changes        *changeHub
	changeLogLimit int
}

// Open 建立 MongoDB 连接并完成初始化（系统文档、计数器、索引）。
// 对应原 sqlite 实现的 Open(path)；changeLogLimit 取自 cfg.DatabaseChangeLogLimit。
func Open(uri, database string, cfg config.Config) (*Store, error) {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017"
	}
	database = strings.TrimSpace(database)
	if database == "" {
		database = "icloud_mail"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		return nil, err
	}
	if err := client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("连接 MongoDB 失败：%w", err)
	}
	s, err := OpenWithClient(client, database)
	if err != nil {
		_ = client.Disconnect(context.Background())
		return nil, err
	}
	if cfg.DatabaseChangeLogLimit > 0 {
		s.SetChangeLogLimit(cfg.DatabaseChangeLogLimit)
	}
	return s, nil
}

// OpenWithClient 复用调用方已有的 mongo.Client 构建 Store。
func OpenWithClient(client *mongo.Client, database string) (*Store, error) {
	if client == nil {
		return nil, errors.New("mongo client 不能为空")
	}
	database = strings.TrimSpace(database)
	if database == "" {
		return nil, errors.New("数据库名不能为空")
	}
	s := &Store{
		database:       database,
		client:         client,
		db:             client.Database(database),
		changes:        newChangeHub(),
		changeLogLimit: defaultChangeLogLimit,
	}
	if err := s.openDatabase(); err != nil {
		return nil, err
	}
	if err := s.initializeDatabase(); err != nil {
		return nil, err
	}
	return s, nil
}

// Path 返回连接占位串，复刻自原 sqlite 实现的 Path()。
func (s *Store) Path() string {
	return "mongodb://" + s.database
}

func (s *Store) col(name string) *mongo.Collection {
	return s.db.Collection(name)
}

// ctx 派生带默认超时的操作上下文。
func (s *Store) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), mongoOpTimeout)
}

// openDatabase 复刻自原 sqlite 实现：加载加密密钥并确保历史明文敏感字段加密。
func (s *Store) openDatabase() error {
	codec, err := s.loadSecretCodec()
	if err != nil {
		return err
	}
	s.codec = codec
	if err := s.ensureIndexes(); err != nil {
		return err
	}
	return s.encryptSensitiveRows()
}

// initializeDatabase 复刻自原 sqlite 实现：写入 metadata 并确保 settings/create_settings 系统文档存在。
func (s *Store) initializeDatabase() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, cancel := s.ctx()
	defer cancel()
	now := time.Now()
	meta := s.col("metadata")
	// created_at 仅在首次初始化时写入。
	if _, err := meta.UpdateOne(ctx, bson.M{"_id": "created_at", "value": ""},
		bson.M{"$set": bson.M{"value": now.Format(time.RFC3339Nano)}}); err != nil {
		return err
	}
	if _, err := meta.UpdateOne(ctx, bson.M{"_id": "schema_version"},
		bson.M{"$set": bson.M{"value": strconv.Itoa(databaseSchemaVersion)}}, options.Update().SetUpsert(true)); err != nil {
		return err
	}
	// 计数器文档（next_id / change_seq），复刻原 metadata 计数语义。
	counters := s.col("counters")
	for _, id := range []string{"next_id", "change_seq"} {
		if _, err := counters.UpdateOne(ctx, bson.M{"_id": id},
			bson.M{"$setOnInsert": bson.M{"seq": int64(0)}}, options.Update().SetUpsert(true)); err != nil {
			return err
		}
	}
	settings := domain.DefaultSettings()
	if found, err := s.readEntity("settings", "system", &settings); err != nil {
		return err
	} else if !found {
		settings = domain.DefaultSettings()
	}
	settings.AppleAccountModuleReady = true
	if _, _, err := s.upsertEntity("settings", "settings", "system", settings); err != nil {
		return err
	}
	var createSettings domain.CreateSettings
	if found, err := s.readEntity("create_settings", "system", &createSettings); err != nil {
		return err
	} else if !found {
		createSettings = domain.DefaultCreateSettings()
	}
	normalizeCreateSettings(&createSettings)
	if _, _, err := s.upsertEntity("create_settings", "create-settings", "system", createSettings); err != nil {
		return err
	}
	// 复刻原 commitTx 的 updated_at 元数据维护。
	if _, err := meta.UpdateOne(ctx, bson.M{"_id": "updated_at"},
		bson.M{"$set": bson.M{"value": now.Format(time.RFC3339Nano)}}, options.Update().SetUpsert(true)); err != nil {
		return err
	}
	return nil
}

// normalizeCreateSettings 复刻自原 sqlite 实现，逐行保持一致。
func normalizeCreateSettings(settings *domain.CreateSettings) {
	defaults := domain.DefaultCreateSettings()
	if settings.Mode != "once" && settings.Mode != "scheduled" {
		settings.Mode = defaults.Mode
	}
	if strings.TrimSpace(settings.Label) == "" {
		settings.Label = defaults.Label
	}
	if strings.TrimSpace(settings.CreateChannel) == "" {
		settings.CreateChannel = defaults.CreateChannel
	}
	if strings.TrimSpace(settings.SchedulerCreateChannel) == "" {
		settings.SchedulerCreateChannel = defaults.SchedulerCreateChannel
	}
	if strings.TrimSpace(settings.AppleAccountTwoFactorMethod) == "" {
		settings.AppleAccountTwoFactorMethod = defaults.AppleAccountTwoFactorMethod
	}
	if strings.TrimSpace(settings.ICloudWebTwoFactorMethod) == "" {
		settings.ICloudWebTwoFactorMethod = defaults.ICloudWebTwoFactorMethod
	}
	if settings.SchedulerIntervalMinMinutes <= 0 {
		settings.SchedulerIntervalMinMinutes = defaults.SchedulerIntervalMinMinutes
	}
	if settings.SchedulerIntervalMaxMinutes <= 0 {
		settings.SchedulerIntervalMaxMinutes = settings.SchedulerIntervalMinMinutes
	}
	if settings.SchedulerIntervalMinMinutes > settings.SchedulerIntervalMaxMinutes {
		settings.SchedulerIntervalMinMinutes, settings.SchedulerIntervalMaxMinutes = settings.SchedulerIntervalMaxMinutes, settings.SchedulerIntervalMinMinutes
	}
	if settings.SchedulerAccountIntervalMinSeconds <= 0 {
		settings.SchedulerAccountIntervalMinSeconds = defaults.SchedulerAccountIntervalMinSeconds
	}
	if settings.SchedulerAccountIntervalMaxSeconds <= 0 {
		settings.SchedulerAccountIntervalMaxSeconds = settings.SchedulerAccountIntervalMinSeconds
	}
	if settings.SchedulerAccountIntervalMinSeconds > settings.SchedulerAccountIntervalMaxSeconds {
		settings.SchedulerAccountIntervalMinSeconds, settings.SchedulerAccountIntervalMaxSeconds = settings.SchedulerAccountIntervalMaxSeconds, settings.SchedulerAccountIntervalMinSeconds
	}
}

// ensureIndexes 按迁移要求创建索引；重复创建时忽略错误。
func (s *Store) ensureIndexes() error {
	ctx, cancel := s.ctx()
	defer cancel()
	type indexSpec struct {
		collection string
		model      mongo.IndexModel
	}
	specs := []indexSpec{
		{"mailboxes", mongo.IndexModel{Keys: bson.D{{Key: "status", Value: 1}, {Key: "account_id", Value: 1}, {Key: "created_at", Value: -1}}}},
		{"mailboxes", mongo.IndexModel{Keys: bson.D{{Key: "email", Value: 1}}, Options: options.Index().SetUnique(true).SetName("idx_mailboxes_email")}},
		{"messages", mongo.IndexModel{Keys: bson.D{{Key: "mailbox_id", Value: 1}, {Key: "received_at", Value: -1}}}},
		// M3 修复:消息查重唯一性兜底(原 schema 的 partial unique index)。
		// 仅对 remote_id/canonical_id 非空字符串的文档建唯一索引,支撑 findDuplicateMessage 查询。
		{"messages", mongo.IndexModel{Keys: bson.D{{Key: "mailbox_id", Value: 1}, {Key: "remote_id", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("idx_messages_remote").
				SetPartialFilterExpression(bson.M{"remote_id": bson.M{"$type": "string", "$ne": ""}})}},
		{"messages", mongo.IndexModel{Keys: bson.D{{Key: "mailbox_id", Value: 1}, {Key: "canonical_id", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("idx_messages_canonical").
				SetPartialFilterExpression(bson.M{"canonical_id": bson.M{"$type": "string", "$ne": ""}})}},
		{"mailbox_leases", mongo.IndexModel{Keys: bson.D{{Key: "state", Value: 1}, {Key: "expires_at", Value: 1}}}},
		{"mailbox_leases", mongo.IndexModel{Keys: bson.D{{Key: "project", Value: 1}, {Key: "request_id", Value: 1}}}},
		{"events", mongo.IndexModel{Keys: bson.D{{Key: "created_at", Value: -1}}}},
		{"icloud_sessions", mongo.IndexModel{Keys: bson.D{{Key: "dsid", Value: 1}}}},
		{"icloud_sessions", mongo.IndexModel{Keys: bson.D{{Key: "apple_id", Value: 1}}}},
	}
	for _, spec := range specs {
		// 索引已存在（同名同键或同名冲突）时忽略错误，复刻原 CREATE INDEX IF NOT EXISTS 语义。
		_, _ = s.col(spec.collection).Indexes().CreateOne(ctx, spec.model)
	}
	return nil
}

// ---------------------------------------------------------------------------
// 加密：secretCodec / protectJSON / unprotectJSON / transformSecrets
// 全部复刻自原 sqlite 实现，密钥改由 MongoDB metadata 集合托管。
// ---------------------------------------------------------------------------

type secretCodec struct {
	aead cipher.AEAD
}

// loadSecretCodec 从 metadata.secret_key 读取（或生成并保存）32 字节密钥。
// 复刻自原 sqlite 实现的 loadSecretCodec(path)，存储介质由 <db>.key 文件改为集合文档。
func (s *Store) loadSecretCodec() (*secretCodec, error) {
	ctx, cancel := s.ctx()
	defer cancel()
	meta := s.col("metadata")
	var doc struct {
		Value string `bson:"value"`
	}
	err := meta.FindOne(ctx, bson.M{"_id": "secret_key"}).Decode(&doc)
	var key []byte
	if errors.Is(err, mongo.ErrNoDocuments) {
		key = make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, key); err != nil {
			return nil, fmt.Errorf("生成数据库加密密钥失败：%w", err)
		}
		if _, err := meta.UpdateOne(ctx, bson.M{"_id": "secret_key"},
			bson.M{"$set": bson.M{"value": base64.RawURLEncoding.EncodeToString(key)}},
			options.Update().SetUpsert(true)); err != nil {
			return nil, fmt.Errorf("保存数据库加密密钥失败：%w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("读取数据库加密密钥失败：%w", err)
	} else {
		key, err = base64.RawURLEncoding.DecodeString(strings.TrimSpace(doc.Value))
		if err != nil {
			return nil, fmt.Errorf("读取数据库加密密钥失败：%w", err)
		}
	}
	if len(key) != 32 {
		return nil, errors.New("数据库加密密钥长度不正确")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &secretCodec{aead: aead}, nil
}

// encrypt 复刻自原 sqlite 实现。
func (c *secretCodec) encrypt(value string) (string, error) {
	if value == "" || strings.HasPrefix(value, secretPrefix) {
		return value, nil
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := c.aead.Seal(nonce, nonce, []byte(value), nil)
	return secretPrefix + base64.RawURLEncoding.EncodeToString(sealed), nil
}

// decrypt 复刻自原 sqlite 实现。
func (c *secretCodec) decrypt(value string) (string, error) {
	if !strings.HasPrefix(value, secretPrefix) {
		return value, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, secretPrefix))
	if err != nil || len(data) < c.aead.NonceSize() {
		return "", errors.New("数据库敏感字段密文损坏")
	}
	nonce := data[:c.aead.NonceSize()]
	plain, err := c.aead.Open(nil, nonce, data[c.aead.NonceSize():], nil)
	if err != nil {
		return "", errors.New("数据库敏感字段解密失败")
	}
	return string(plain), nil
}

// encryptSensitiveRows 复刻自原 sqlite 实现：启动时把历史明文敏感字段改写为密文。
func (s *Store) encryptSensitiveRows() error {
	ctx, cancel := s.ctx()
	defer cancel()
	for _, collection := range []string{"apple_accounts", "mailboxes", "settings", "icloud_sessions"} {
		cursor, err := s.col(collection).Find(ctx, bson.M{})
		if err != nil {
			return err
		}
		var docs []bson.M
		if err := cursor.All(ctx, &docs); err != nil {
			return err
		}
		for _, doc := range docs {
			id, _ := doc["_id"].(string)
			if id == "" {
				continue
			}
			delete(doc, "_id")
			plain, err := json.Marshal(doc)
			if err != nil {
				return err
			}
			protected, err := s.protectJSON(collection, plain)
			if err != nil {
				return err
			}
			if bytes.Equal(plain, protected) {
				continue
			}
			var protectedDoc bson.M
			if err := json.Unmarshal(protected, &protectedDoc); err != nil {
				return err
			}
			protectedDoc["updated_at"] = time.Now().Format(time.RFC3339Nano)
			if _, err := s.col(collection).ReplaceOne(ctx, bson.M{"_id": id}, protectedDoc); err != nil {
				return err
			}
		}
	}
	return nil
}

// protectJSON 复刻自原 sqlite 实现（table 参数对应集合名）。
func (s *Store) protectJSON(collection string, plain []byte) ([]byte, error) {
	var value any
	if err := json.Unmarshal(plain, &value); err != nil {
		return nil, err
	}
	if err := s.transformSecrets(collection, value, true); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

// unprotectJSON 复刻自原 sqlite 实现（table 参数对应集合名）。
func (s *Store) unprotectJSON(collection string, protected []byte) ([]byte, error) {
	var value any
	if err := json.Unmarshal(protected, &value); err != nil {
		return nil, err
	}
	if err := s.transformSecrets(collection, value, false); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

// transformSecrets 复刻自原 sqlite 实现：按集合递归加解密敏感字段。
func (s *Store) transformSecrets(collection string, value any, encrypt bool) error {
	keys := map[string]bool{}
	switch collection {
	case "apple_accounts":
		keys["password"] = true
	case "mailboxes":
		keys["api_token"] = true
	case "settings":
		keys["public_api_key"] = true
		keys["server_chan_send_key"] = true
	case "icloud_sessions":
		for _, key := range []string{"value", "api_keys", "data_access_token", "imap_app_password"} {
			keys[key] = true
		}
	}
	if len(keys) == 0 {
		return nil
	}
	var walk func(any) error
	walk = func(current any) error {
		switch typed := current.(type) {
		case map[string]any:
			for key, child := range typed {
				if keys[key] {
					if text, ok := child.(string); ok && text != "" {
						var transformed string
						var err error
						if encrypt {
							transformed, err = s.codec.encrypt(text)
						} else {
							transformed, err = s.codec.decrypt(text)
						}
						if err != nil {
							return err
						}
						typed[key] = transformed
					}
				}
				if err := walk(typed[key]); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range typed {
				if err := walk(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(value)
}

// ---------------------------------------------------------------------------
// 文档读写：json 作为中间表示（domain 结构体只有 json tag）。
// ---------------------------------------------------------------------------

// marshalEntity 复刻自原 sqlite 实现：返回明文与加密后的 JSON。
func (s *Store) marshalEntity(collection string, value any) ([]byte, []byte, error) {
	plain, err := json.Marshal(value)
	if err != nil {
		return nil, nil, err
	}
	protected, err := s.protectJSON(collection, plain)
	return plain, protected, err
}

// decodeEntity 复刻自原 sqlite 实现。
func (s *Store) decodeEntity(collection string, data []byte, target any) error {
	plain, err := s.unprotectJSON(collection, data)
	if err != nil {
		return err
	}
	return json.Unmarshal(plain, target)
}

// encodeDoc 把受保护的 JSON 转成可写入 MongoDB 的 bson.M（含 _id）。
func encodeDoc(id string, protected []byte) (bson.M, error) {
	var doc bson.M
	if err := json.Unmarshal(protected, &doc); err != nil {
		return nil, err
	}
	doc["_id"] = id
	return doc, nil
}

// docToJSON 把 MongoDB 读出的文档还原为受保护的 JSON（去掉 _id）。
func docToJSON(doc bson.M) ([]byte, error) {
	delete(doc, "_id")
	return json.Marshal(doc)
}

// readEntity 复刻自原 sqlite 实现：按主键读取并解密。
func (s *Store) readEntity(collection, id string, target any) (bool, error) {
	ctx, cancel := s.ctx()
	defer cancel()
	var doc bson.M
	err := s.col(collection).FindOne(ctx, bson.M{"_id": strings.TrimSpace(id)}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	data, err := docToJSON(doc)
	if err != nil {
		return false, err
	}
	return true, s.decodeEntity(collection, data, target)
}

// upsertEntity 复刻自原 sqlite 实现的 upsertEntityTx：
// 读旧值比对（明文 bytes.Equal），无变化则跳过；有变化写入并产出 Change。
func (s *Store) upsertEntity(collection, resource, id string, value any) (Change, bool, error) {
	plain, protected, err := s.marshalEntity(collection, value)
	if err != nil {
		return Change{}, false, err
	}
	ctx, cancel := s.ctx()
	defer cancel()
	var existingDoc bson.M
	err = s.col(collection).FindOne(ctx, bson.M{"_id": id}).Decode(&existingDoc)
	operation := "updated"
	if errors.Is(err, mongo.ErrNoDocuments) {
		operation = "created"
	} else if err != nil {
		return Change{}, false, err
	} else {
		existingData, err := docToJSON(existingDoc)
		if err != nil {
			return Change{}, false, err
		}
		existingPlain, err := s.unprotectJSON(collection, existingData)
		if err != nil {
			return Change{}, false, err
		}
		// M1 修复:existingPlain 源自 docToJSON(bson.M)(字母序),而 plain 源自
		// struct json.Marshal(字段声明序),bytes.Equal 因键序不同恒为 false,
		// 导致「无变化则跳过」永不命中。这里把 plain 归一化(map 往返)再比对。
		if jsonBytesEqual(existingPlain, plain) {
			return Change{}, false, nil
		}
	}
	now := time.Now()
	doc, err := encodeDoc(id, protected)
	if err != nil {
		return Change{}, false, err
	}
	if _, err := s.col(collection).ReplaceOne(ctx, bson.M{"_id": id}, doc, options.Replace().SetUpsert(true)); err != nil {
		return Change{}, false, err
	}
	return makeEntityChange(collection, resource, id, operation, plain, now), true, nil
}

// jsonBytesEqual 语义级比对两段 JSON:先尝试字节相等(快路径),否则各自
// Unmarshal 到 any 再重新 Marshal(键序归一化)后比对。用于 upsert 幂等判断。
func jsonBytesEqual(a, b []byte) bool {
	if bytes.Equal(a, b) {
		return true
	}
	var av, bv any
	if json.Unmarshal(a, &av) != nil || json.Unmarshal(b, &bv) != nil {
		return false
	}
	am, err1 := json.Marshal(av)
	bm, err2 := json.Marshal(bv)
	if err1 != nil || err2 != nil {
		return false
	}
	return bytes.Equal(am, bm)
}

// deleteEntity 复刻自原 sqlite 实现的 deleteEntityTx。
func (s *Store) deleteEntity(collection, resource, id string) (Change, bool, error) {
	ctx, cancel := s.ctx()
	defer cancel()
	result, err := s.col(collection).DeleteOne(ctx, bson.M{"_id": strings.TrimSpace(id)})
	if err != nil {
		return Change{}, false, err
	}
	if result.DeletedCount == 0 {
		return Change{}, false, nil
	}
	now := time.Now()
	return makeEntityChange(collection, resource, id, "deleted", nil, now), true, nil
}

// makeEntityChange 复刻自原 sqlite 实现：对 icloud_sessions/web_sessions/admins 不嵌 data。
func makeEntityChange(collection, resource, id, operation string, plain []byte, now time.Time) Change {
	payload := map[string]any{"id": id, "operation": operation}
	if len(plain) > 0 && collection != "icloud_sessions" && collection != "web_sessions" && collection != "admins" {
		var data any
		if json.Unmarshal(plain, &data) == nil {
			redactSecrets(data)
			payload["data"] = data
		}
	}
	payloadData, _ := json.Marshal(payload)
	return Change{
		Type: resource + "." + operation, Resource: resource, ResourceID: id,
		Operation: operation, Payload: payloadData, CreatedAt: now,
	}
}

// redactSecrets 复刻自原 sqlite 实现：递归删除敏感 key。
func redactSecrets(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			switch key {
			case "api_token", "public_api_key", "server_chan_send_key", "password", "password_hash", "value", "api_keys", "data_access_token", "imap_app_password":
				delete(typed, key)
			default:
				redactSecrets(child)
			}
		}
	case []any:
		for _, child := range typed {
			redactSecrets(child)
		}
	}
}

// nextID 复刻自原 sqlite 实现的 nextIDTx：
// 用 counters 集合原子自增，格式 prefix_000001。
func (s *Store) nextID(prefix string) (string, error) {
	seq, err := s.nextCounter("next_id")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s_%06d", prefix, seq), nil
}

// nextCounter 原子自增 counters 集合并返回自增后的值。
func (s *Store) nextCounter(id string) (int64, error) {
	ctx, cancel := s.ctx()
	defer cancel()
	opts := options.FindOneAndUpdate().
		SetUpsert(true).
		SetReturnDocument(options.After)
	var doc struct {
		Seq int64 `bson:"seq"`
	}
	if err := s.col("counters").FindOneAndUpdate(ctx, bson.M{"_id": id},
		bson.M{"$inc": bson.M{"seq": int64(1)}}, opts).Decode(&doc); err != nil {
		return 0, err
	}
	return doc.Seq, nil
}

// currentCounter 读取计数器当前值（无则返回 0），对应原 metadata.next_id 快照语义。
func (s *Store) currentCounter(id string) int64 {
	ctx, cancel := s.ctx()
	defer cancel()
	var doc struct {
		Seq int64 `bson:"seq"`
	}
	if err := s.col("counters").FindOne(ctx, bson.M{"_id": id}).Decode(&doc); err != nil {
		return 0
	}
	return doc.Seq
}

// ---------------------------------------------------------------------------
// Change / changeHub / change_log：复刻自原 sqlite 实现。
// ---------------------------------------------------------------------------

// Change 描述一次已经提交的数据变更，供 SSE 客户端增量刷新。
type Change struct {
	Sequence   int64           `json:"sequence"`
	Type       string          `json:"type"`
	Resource   string          `json:"resource"`
	ResourceID string          `json:"resource_id,omitempty"`
	Operation  string          `json:"operation"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
}

// DatabaseStatus 是系统设置页展示的数据库运行状态。
type DatabaseStatus struct {
	Path              string `json:"path"`
	SchemaVersion     int    `json:"schema_version"`
	JournalMode       string `json:"journal_mode"`
	DatabaseBytes     int64  `json:"database_bytes"`
	WALBytes          int64  `json:"wal_bytes"`
	ChangeLogCount    int64  `json:"change_log_count"`
	LatestSequence    int64  `json:"latest_sequence"`
	EncryptedFields   bool   `json:"encrypted_fields"`
	LastMaintenanceAt string `json:"last_maintenance_at,omitempty"`
	LastMaintenance   string `json:"last_maintenance,omitempty"`
}

type changeHub struct {
	mu          sync.RWMutex
	nextID      uint64
	subscribers map[uint64]chan Change
}

func newChangeHub() *changeHub {
	return &changeHub{subscribers: make(map[uint64]chan Change)}
}

// subscribe 复刻自原 sqlite 实现。
func (h *changeHub) subscribe(buffer int) (<-chan Change, func()) {
	if buffer <= 0 {
		buffer = 64
	}
	h.mu.Lock()
	h.nextID++
	id := h.nextID
	channel := make(chan Change, buffer)
	h.subscribers[id] = channel
	h.mu.Unlock()
	return channel, func() {
		h.mu.Lock()
		if current, ok := h.subscribers[id]; ok {
			delete(h.subscribers, id)
			close(current)
		}
		h.mu.Unlock()
	}
}

// publish 复刻自原 sqlite 实现：非阻塞扇出。
func (h *changeHub) publish(changes []Change) {
	if len(changes) == 0 {
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, change := range changes {
		for _, channel := range h.subscribers {
			select {
			case channel <- change:
			default:
			}
		}
	}
}

// commitChanges 复刻自原 sqlite 实现的 commitTx：
// 过滤空变更 → 逐条插入 change_log（sequence 由 counters.change_seq 提供）→
// 截断超限历史 → 更新 metadata.updated_at → 广播。
func (s *Store) commitChanges(changes []Change) error {
	validChanges := changes[:0]
	for _, change := range changes {
		if strings.TrimSpace(change.Resource) != "" {
			validChanges = append(validChanges, change)
		}
	}
	changes = validChanges
	ctx, cancel := s.ctx()
	defer cancel()
	for index := range changes {
		sequence, err := s.insertChange(changes[index])
		if err != nil {
			return err
		}
		changes[index].Sequence = sequence
	}
	// 注意：commitChanges 在持有 s.mu.Lock() 的写方法内被调用，sync.RWMutex
	// 不可重入，这里直接读 changeLogLimit 字段（调用方已持锁，读安全），
	// 切勿再次 s.mu.RLock()，否则自死锁。
	limit := s.changeLogLimit
	if limit <= 0 {
		limit = defaultChangeLogLimit
	}
	// 截断逻辑复刻自原 sqlite：删除 sequence <= max(sequence) - limit 的记录。
	latest, err := s.LatestChangeSequence()
	if err != nil {
		return err
	}
	if latest > int64(limit) {
		if _, err := s.col("change_log").DeleteMany(ctx, bson.M{"sequence": bson.M{"$lte": latest - int64(limit)}}); err != nil {
			return err
		}
	}
	if _, err := s.col("metadata").UpdateOne(ctx, bson.M{"_id": "updated_at"},
		bson.M{"$set": bson.M{"value": time.Now().Format(time.RFC3339Nano)}}, options.Update().SetUpsert(true)); err != nil {
		return err
	}
	s.changes.publish(changes)
	return nil
}

// insertChange 复刻自原 sqlite 实现：sequence 由 counters.change_seq 原子分配。
func (s *Store) insertChange(change Change) (int64, error) {
	sequence, err := s.nextCounter("change_seq")
	if err != nil {
		return 0, err
	}
	ctx, cancel := s.ctx()
	defer cancel()
	_, err = s.col("change_log").InsertOne(ctx, bson.M{
		"_id":         sequence,
		"sequence":    sequence,
		"event_type":  change.Type,
		"resource":    change.Resource,
		"resource_id": change.ResourceID,
		"operation":   change.Operation,
		"payload_json": func() string {
			if len(change.Payload) == 0 {
				return ""
			}
			return string(change.Payload)
		}(),
		"created_at": change.CreatedAt.Format(time.RFC3339Nano),
	})
	if err != nil {
		return 0, err
	}
	return sequence, nil
}

// SubscribeChanges 订阅当前进程中已经提交的实时变更。
func (s *Store) SubscribeChanges(buffer int) (<-chan Change, func()) {
	return s.changes.subscribe(buffer)
}

// ChangesAfter 返回指定序号之后的持久化变更，用于 SSE 断线续传。
func (s *Store) ChangesAfter(sequence int64, limit int) ([]Change, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	ctx, cancel := s.ctx()
	defer cancel()
	cursor, err := s.col("change_log").Find(ctx,
		bson.M{"sequence": bson.M{"$gt": sequence}},
		options.Find().SetSort(bson.D{{Key: "sequence", Value: 1}}).SetLimit(int64(limit)))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	changes := make([]Change, 0)
	for cursor.Next(ctx) {
		var doc bson.M
		if err := cursor.Decode(&doc); err != nil {
			return nil, err
		}
		change := Change{
			Sequence:   int64Value(doc["sequence"]),
			Type:       stringValue(doc["event_type"]),
			Resource:   stringValue(doc["resource"]),
			ResourceID: stringValue(doc["resource_id"]),
			Operation:  stringValue(doc["operation"]),
			Payload:    json.RawMessage(stringValue(doc["payload_json"])),
		}
		change.CreatedAt, _ = time.Parse(time.RFC3339Nano, stringValue(doc["created_at"]))
		changes = append(changes, change)
	}
	return changes, cursor.Err()
}

// LatestChangeSequence 复刻自原 sqlite 实现。
func (s *Store) LatestChangeSequence() (int64, error) {
	ctx, cancel := s.ctx()
	defer cancel()
	var doc bson.M
	err := s.col("change_log").FindOne(ctx, bson.M{},
		options.FindOne().SetSort(bson.D{{Key: "sequence", Value: -1}})).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return int64Value(doc["sequence"]), nil
}

// SetChangeLogLimit 复刻自原 sqlite 实现(下限语义:小于 1000 时回落到默认 5000)。
func (s *Store) SetChangeLogLimit(limit int) {
	if limit < 1000 {
		limit = defaultChangeLogLimit
	}
	s.mu.Lock()
	s.changeLogLimit = limit
	s.mu.Unlock()
}

// Close 断开 MongoDB 连接，复刻自原 sqlite 实现的 Close。
func (s *Store) Close(ctx context.Context) error {
	if s == nil || s.client == nil {
		return nil
	}
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
	}
	return s.client.Disconnect(ctx)
}

// ---------------------------------------------------------------------------
// 通用查询辅助
// ---------------------------------------------------------------------------

// findDoc 按过滤条件读取单个文档并解密到 target。
func (s *Store) findDoc(collection string, filter bson.M, opts *options.FindOneOptions, target any) (bool, error) {
	ctx, cancel := s.ctx()
	defer cancel()
	findOpts := options.FindOne()
	if opts != nil {
		findOpts = opts
	}
	var doc bson.M
	err := s.col(collection).FindOne(ctx, filter, findOpts).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	data, err := docToJSON(doc)
	if err != nil {
		return false, err
	}
	return true, s.decodeEntity(collection, data, target)
}

// findDocs 按过滤条件读取文档列表并逐条解密，appendFn 返回 false 跳过该条。
func (s *Store) findDocs(collection string, filter bson.M, opts *options.FindOptions, appendFn func(data []byte) error) error {
	ctx, cancel := s.ctx()
	defer cancel()
	findOpts := options.Find()
	if opts != nil {
		findOpts = opts
	}
	cursor, err := s.col(collection).Find(ctx, filter, findOpts)
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)
	for cursor.Next(ctx) {
		var doc bson.M
		if err := cursor.Decode(&doc); err != nil {
			return err
		}
		data, err := docToJSON(doc)
		if err != nil {
			return err
		}
		if err := appendFn(data); err != nil {
			return err
		}
	}
	return cursor.Err()
}

// loadEntities 复刻自原 sqlite 实现：读取整个集合并解密到切片。
func (s *Store) loadEntities(collection string, sort bson.D, target any) error {
	items := make([]json.RawMessage, 0)
	err := s.findDocs(collection, bson.M{}, options.Find().SetSort(sort), func(data []byte) error {
		plain, err := s.unprotectJSON(collection, data)
		if err != nil {
			return err
		}
		items = append(items, plain)
		return nil
	})
	if err != nil {
		return err
	}
	data, err := json.Marshal(items)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

// decodeInto 把受保护的 JSON 解密并绑定到实体，复刻 decodeEntity 的便捷封装。
func (s *Store) decodeInto(collection string, data []byte, target any) error {
	return s.decodeEntity(collection, data, target)
}

// firstAdmin 读取首位管理员，复刻原 sqlite 多处 `SELECT data_json FROM admins LIMIT 1` 的语义。
func (s *Store) firstAdmin() (domain.Admin, bool) {
	var admin domain.Admin
	found, err := s.findDoc("admins", bson.M{}, options.FindOne().SetSort(bson.D{{Key: "created_at", Value: 1}}), &admin)
	return admin, found && err == nil
}

// appendEvent 复刻自原 sqlite 实现的 appendEventTx：生成 evt_ 前缀 ID、写入并裁剪到 500 条。
func (s *Store) appendEvent(level, category, message string) (Change, error) {
	id, err := s.nextID("evt")
	if err != nil {
		return Change{}, err
	}
	event := domain.Event{ID: id, Level: level, Category: category, Message: message, CreatedAt: time.Now()}
	change, _, err := s.upsertEntity("events", "event", id, event)
	if err != nil {
		return Change{}, err
	}
	if err := s.pruneEvents(500); err != nil {
		return Change{}, err
	}
	return change, nil
}

// pruneEvents 复刻原 sqlite `DELETE FROM events ... OFFSET 500` 的裁剪语义。
func (s *Store) pruneEvents(keep int) error {
	ctx, cancel := s.ctx()
	defer cancel()
	cursor, err := s.col("events").Find(ctx, bson.M{},
		options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}, {Key: "_id", Value: -1}}).
			SetProjection(bson.M{"_id": 1}).SetSkip(int64(keep)))
	if err != nil {
		return err
	}
	var ids []string
	for cursor.Next(ctx) {
		var doc struct {
			ID string `bson:"_id"`
		}
		if err := cursor.Decode(&doc); err != nil {
			_ = cursor.Close(ctx)
			return err
		}
		ids = append(ids, doc.ID)
	}
	_ = cursor.Close(ctx)
	if len(ids) == 0 {
		return nil
	}
	_, err = s.col("events").DeleteMany(ctx, bson.M{"_id": bson.M{"$in": ids}})
	return err
}

// countDocs 统计集合文档数。
func (s *Store) countDocs(collection string, filter bson.M) int64 {
	ctx, cancel := s.ctx()
	defer cancel()
	count, err := s.col(collection).CountDocuments(ctx, filter)
	if err != nil {
		return 0
	}
	return count
}

// metaValue 读取 metadata 文档的 value 字段。
func (s *Store) metaValue(key string) string {
	ctx, cancel := s.ctx()
	defer cancel()
	var doc struct {
		Value string `bson:"value"`
	}
	if err := s.col("metadata").FindOne(ctx, bson.M{"_id": key}).Decode(&doc); err != nil {
		return ""
	}
	return doc.Value
}

// constantTimeEqual 复刻自原 sqlite 实现。
func constantTimeEqual(candidate, expected string) bool {
	if candidate == "" || expected == "" || len(candidate) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(expected)) == 1
}

// stringValue 从 bson.M 值安全取字符串。
func stringValue(v any) string {
	switch typed := v.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		return ""
	}
}

// int64Value 从 bson.M 值安全取 int64。
func int64Value(v any) int64 {
	switch typed := v.(type) {
	case int64:
		return typed
	case int32:
		return int64(typed)
	case int:
		return int64(typed)
	case float64:
		return int64(typed)
	default:
		return 0
	}
}

// ---------------------------------------------------------------------------
// admin / session：复刻自原 sqlite 实现 store.go。
// ---------------------------------------------------------------------------

// SetupAdmin 复刻自原 sqlite 实现。
func (s *Store) SetupAdmin(username, passwordHash string) (domain.Admin, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.countDocs("admins", bson.M{}) > 0 {
		return domain.Admin{}, errors.New("管理员已经初始化")
	}
	id, err := s.nextID("admin")
	if err != nil {
		return domain.Admin{}, err
	}
	now := time.Now()
	admin := domain.Admin{ID: id, Username: strings.ToLower(strings.TrimSpace(username)), PasswordHash: passwordHash, CreatedAt: now, UpdatedAt: now}
	change, _, err := s.upsertEntity("admins", "admin", admin.ID, admin)
	if err != nil {
		return domain.Admin{}, err
	}
	eventChange, err := s.appendEvent("info", "auth", "已完成单管理员初始化")
	if err != nil {
		return domain.Admin{}, err
	}
	return admin, s.commitChanges([]Change{change, eventChange})
}

// Admin 复刻自原 sqlite 实现。
func (s *Store) Admin() (domain.Admin, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.firstAdmin()
}

// MarkLogin 复刻自原 sqlite 实现。
func (s *Store) MarkLogin() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	admin, found := s.firstAdmin()
	if !found {
		return errors.New("管理员尚未初始化")
	}
	now := time.Now()
	admin.LastLoginAt, admin.UpdatedAt = now, now
	adminChange, _, err := s.upsertEntity("admins", "admin", admin.ID, admin)
	if err != nil {
		return err
	}
	eventChange, err := s.appendEvent("info", "auth", "管理员已登录")
	if err != nil {
		return err
	}
	return s.commitChanges([]Change{adminChange, eventChange})
}

// UpdateAdminPassword 更新管理员密码哈希并使全部旧会话失效(需重新登录)。
func (s *Store) UpdateAdminPassword(passwordHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	admin, found := s.firstAdmin()
	if !found {
		return errors.New("管理员尚未初始化")
	}
	now := time.Now()
	admin.PasswordHash, admin.UpdatedAt = passwordHash, now
	adminChange, _, err := s.upsertEntity("admins", "admin", admin.ID, admin)
	if err != nil {
		return err
	}
	// 删除所有会话,强制重新登录。
	dCtx, dCancel := s.ctx()
	defer dCancel()
	if _, err := s.col("web_sessions").DeleteMany(dCtx, bson.M{}); err != nil {
		return err
	}
	eventChange, err := s.appendEvent("info", "auth", "管理员已修改密码,所有会话已失效")
	if err != nil {
		return err
	}
	return s.commitChanges([]Change{adminChange, eventChange})
}

// SaveSession 复刻自原 sqlite 实现：先清理过期会话，再写入新会话。
func (s *Store) SaveSession(tokenHash string, expiresAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	ctx, cancel := s.ctx()
	defer cancel()
	if _, err := s.col("web_sessions").DeleteMany(ctx, bson.M{"expires_at": bson.M{"$lte": now.Format(time.RFC3339Nano)}}); err != nil {
		return err
	}
	session := domain.WebSession{TokenHash: tokenHash, CreatedAt: now, LastSeenAt: now, ExpiresAt: expiresAt}
	change, _, err := s.upsertEntity("web_sessions", "web-session", tokenHash, session)
	if err != nil {
		return err
	}
	return s.commitChanges([]Change{change})
}

// ValidateSession 复刻自原 sqlite 实现。
func (s *Store) ValidateSession(tokenHash string) (domain.Admin, bool) {
	tokenHash = strings.TrimSpace(tokenHash)
	if tokenHash == "" {
		return domain.Admin{}, false
	}
	// 读路径走读锁(认证热路径,每次 API 请求都过),避免全局写锁冻结控制台;
	// 仅 LastSeenAt 回写(每 10 分钟一次)才短暂取写锁,且先释放读锁再取写锁(防升级死锁)。
	s.mu.RLock()
	var session domain.WebSession
	found, err := s.readEntity("web_sessions", tokenHash, &session)
	if err != nil || !found || !session.ExpiresAt.After(time.Now()) {
		s.mu.RUnlock()
		return domain.Admin{}, false
	}
	admin, foundAdmin := s.firstAdmin()
	if !foundAdmin || !constantTimeEqual(session.TokenHash, tokenHash) {
		s.mu.RUnlock()
		return domain.Admin{}, false
	}
	needTouch := time.Since(session.LastSeenAt) >= 10*time.Minute
	s.mu.RUnlock()

	// 最近访问时间最多每十分钟持久化一次,并且不作为前端业务变更广播。
	if needTouch {
		s.mu.Lock()
		session.LastSeenAt = time.Now()
		if _, changed, saveErr := s.upsertEntity("web_sessions", "web-session", tokenHash, session); saveErr == nil && changed {
			// 复刻原语义：仅持久化，不广播。
			_ = s.touchMetadataUpdatedAt()
		}
		s.mu.Unlock()
	}
	return admin, true
}

// touchMetadataUpdatedAt 复刻原 commitTx 中 updated_at 元数据维护（无变更广播场景）。
func (s *Store) touchMetadataUpdatedAt() error {
	ctx, cancel := s.ctx()
	defer cancel()
	_, err := s.col("metadata").UpdateOne(ctx, bson.M{"_id": "updated_at"},
		bson.M{"$set": bson.M{"value": time.Now().Format(time.RFC3339Nano)}}, options.Update().SetUpsert(true))
	return err
}

// DeleteSession 复刻自原 sqlite 实现。
func (s *Store) DeleteSession(tokenHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	change, changed, err := s.deleteEntity("web_sessions", "web-session", tokenHash)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	return s.commitChanges([]Change{change})
}
