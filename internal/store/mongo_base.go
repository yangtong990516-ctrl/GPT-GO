package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// storeBase 是主站各业务 Mongo store 的共享基座,自包含(只用 mongo-driver,
// 不引 internal/store/mongo 包——后者因 runstore.go 引 signup,会造成 store→mongo→signup→store 循环)。
// 各 MongoXxxStore 内嵌它,用最少代码获得连接/集合/单文档读写/索引能力。
type storeBase struct {
	client *mongo.Client
	db     *mongo.Database
}

// mongoOpTimeout 单次 Mongo 操作默认超时(与 icloud store 对齐)。
const mongoOpTimeout = 30 * time.Second

// NewStoreBase 以已连接的 client + 库名构造共享基座。
func NewStoreBase(client *mongo.Client, database string) *storeBase {
	return &storeBase{client: client, db: client.Database(database)}
}

// StoreBase 是 storeBase 的导出别名,供 apiserver 等包外装配层持有/传递
// (具体 store 构造仍在 store 包内完成,见 BuildMongoStores)。
type StoreBase = storeBase

// ctx 给单次操作套默认超时(若 ctx 已无 deadline)。
func (b *storeBase) ctx(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, mongoOpTimeout)
}

// coll 返回集合。
func (b *storeBase) coll(name string) *mongo.Collection { return b.db.Collection(name) }

// database 返回 database(建索引)。
func (b *storeBase) database() *mongo.Database { return b.db }

// ensureIndex 建索引(幂等)。
func (b *storeBase) ensureIndex(ctx context.Context, collName string, keys interface{}, opts *options.IndexOptions) error {
	cctx, cancel := b.ctx(ctx)
	defer cancel()
	_, err := b.coll(collName).Indexes().CreateOne(cctx, mongo.IndexModel{Keys: keys, Options: opts})
	return err
}

// isNoDocuments 判断是否「查无文档」。
func isNoDocuments(err error) bool { return errors.Is(err, mongo.ErrNoDocuments) }

// loadSingleton 读固定 _id 的单文档配置到 out(不存在返回 found=false)。
func (b *storeBase) loadSingleton(ctx context.Context, collName, id string, out interface{}) (bool, error) {
	cctx, cancel := b.ctx(ctx)
	defer cancel()
	err := b.coll(collName).FindOne(cctx, map[string]interface{}{"_id": id}).Decode(out)
	if isNoDocuments(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// saveSingleton upsert 固定 _id 的单文档配置。
func (b *storeBase) saveSingleton(ctx context.Context, collName, id string, doc interface{}) error {
	cctx, cancel := b.ctx(ctx)
	defer cancel()
	_, err := b.coll(collName).ReplaceOne(
		cctx,
		map[string]interface{}{"_id": id},
		doc,
		options.Replace().SetUpsert(true),
	)
	return err
}

// 共享常量/工具(accountsColl、regexEscape)由 mongo_email.go 等已落地文件定义,
// 此处不再重复(避免 redeclared)。后续 mongo_account.go 落地时若需 accountsColl,
// 直接复用 mongo_email.go 中的定义即可。

// ─────────────────────────────────────────────────────────────────────────────
// counters:自增 ID(参照 internal/icloud/store 的 nextCounter,用 counters 集合
// FindOneAndUpdate($inc) 原子生成)。
// ─────────────────────────────────────────────────────────────────────────────

// countersColl 是计数器集合名。
const countersColl = "counters"

// nextCounter 原子自增 counters 集合里 _id=key 的 seq 并返回自增后的值(upsert)。
func (b *storeBase) nextCounter(ctx context.Context, key string) (int64, error) {
	cctx, cancel := b.ctx(ctx)
	defer cancel()
	opts := options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)
	var doc struct {
		Seq int64 `bson:"seq"`
	}
	err := b.coll(countersColl).FindOneAndUpdate(cctx,
		bson.M{"_id": key}, bson.M{"$inc": bson.M{"seq": int64(1)}}, opts).Decode(&doc)
	if err != nil {
		return 0, err
	}
	return doc.Seq, nil
}

// nextID 生成 prefix_NNNNNN 形式的自增 ID(参照 icloud nextID)。
func (b *storeBase) nextID(ctx context.Context, prefix string) (string, error) {
	seq, err := b.nextCounter(ctx, prefix)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s_%06d", prefix, seq), nil
}

// ─────────────────────────────────────────────────────────────────────────────
// secretCodec:账号敏感字段(AccessToken/RefreshToken/ChatgptPassword/TotpSecret)
// AES-GCM 落库加密,密文带 enc:v1: 前缀(参照 internal/icloud/store secretCodec)。
// 密钥 32 字节,存 metadata 集合 _id=secret_key(不存在则生成);解密失败(密钥
// 轮换/丢失)时保留原密文读出,不阻断业务(对齐「能读就不崩」的降级语义)。
// ─────────────────────────────────────────────────────────────────────────────

// secretPrefix 是密文前缀(与 icloud 对齐,跨模块可识别)。
const secretPrefix = "enc:v1:"

// metadataColl 是元数据集合名(密钥托管)。
const metadataColl = "metadata"

type secretCodec struct {
	aead cipher.AEAD
}

// loadSecretCodec 从 metadata.secret_key 读取(或生成并保存)32 字节密钥。
// 与 icloud 同库时复用同一把密钥(_id 相同,upsert 幂等)。
func (b *storeBase) loadSecretCodec(ctx context.Context) (*secretCodec, error) {
	cctx, cancel := b.ctx(ctx)
	defer cancel()
	meta := b.coll(metadataColl)
	var doc struct {
		Value string `bson:"value"`
	}
	err := meta.FindOne(cctx, bson.M{"_id": "secret_key"}).Decode(&doc)
	var key []byte
	if errors.Is(err, mongo.ErrNoDocuments) {
		key = make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, key); err != nil {
			return nil, fmt.Errorf("生成数据库加密密钥失败: %w", err)
		}
		if _, err := meta.UpdateOne(cctx, bson.M{"_id": "secret_key"},
			bson.M{"$set": bson.M{"value": base64.RawURLEncoding.EncodeToString(key)}},
			options.Update().SetUpsert(true)); err != nil {
			return nil, fmt.Errorf("保存数据库加密密钥失败: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("读取数据库加密密钥失败: %w", err)
	} else {
		key, err = base64.RawURLEncoding.DecodeString(strings.TrimSpace(doc.Value))
		if err != nil {
			return nil, fmt.Errorf("读取数据库加密密钥失败: %w", err)
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

// encrypt 加密非空明文(已是密文则原样返回)。
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

// decrypt 解密 enc:v1: 密文;非密文原样返回;解密失败原样保留(降级不阻断)。
func (c *secretCodec) decrypt(value string) string {
	if !strings.HasPrefix(value, secretPrefix) {
		return value
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, secretPrefix))
	if err != nil || len(data) < c.aead.NonceSize() {
		return value
	}
	nonce := data[:c.aead.NonceSize()]
	plain, err := c.aead.Open(nil, nonce, data[c.aead.NonceSize():], nil)
	if err != nil {
		return value
	}
	return string(plain)
}

// encryptStringPtr 加密字符串指针(空指针/空串原样)。
func (c *secretCodec) encryptStringPtr(p *string) (*string, error) {
	if p == nil || *p == "" {
		return p, nil
	}
	v, err := c.encrypt(*p)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// decryptStringPtr 解密字符串指针(空指针原样)。
func (c *secretCodec) decryptStringPtr(p *string) *string {
	if p == nil {
		return nil
	}
	v := c.decrypt(*p)
	return &v
}
