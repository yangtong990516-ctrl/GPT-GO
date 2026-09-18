// helper.go 主站各业务 store 共享的 Mongo 轻量 helper。
// 设计:主站 Document 是强类型 struct(带 bson tag),直接用 mongo-driver 原生
// 编解码,无需 icloud 那套 json 中间表示。这里只收敛「单文档配置读写」「通用
// 超时」「错误归一」等横切逻辑,具体 CRUD 由各 store 自己的 Mongo 实现完成。
package mongo

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// OpTimeout 是单次 Mongo 操作的默认超时(与 icloud store 的 mongoOpTimeout 对齐)。
const OpTimeout = 30 * time.Second

// Ctx 给单次操作套上默认超时(若 ctx 已无 deadline)。
func Ctx(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, OpTimeout)
}

// IsNoDocuments 判断是否为「查无文档」。
func IsNoDocuments(err error) bool {
	return errors.Is(err, mongo.ErrNoDocuments)
}

// Store 是各业务 store 的基座,持有 db 句柄。
// 各业务 store 内嵌它并获得 Coll(name)/DB() 能力。
type Store struct {
	client *mongo.Client
	db     *mongo.Database
}

// NewStore 以已连接的 client + 库名构造基座。
func NewStore(client *mongo.Client, database string) *Store {
	return &Store{client: client, db: client.Database(database)}
}

// DB 返回底层 database(建索引等高级用法)。
func (s *Store) DB() *mongo.Database { return s.db }

// Client 返回底层 client(事务等高级用法)。
func (s *Store) Client() *mongo.Client { return s.client }

// Coll 返回指定集合。
func (s *Store) Coll(name string) *mongo.Collection { return s.db.Collection(name) }

// EnsureIndex 在集合上建索引(幂等;keys 形如 bson.D{{Key:"emailNormalized",Value:1}})。
func (s *Store) EnsureIndex(ctx context.Context, coll string, keys interface{}, opts *options.IndexOptions) error {
	cctx, cancel := Ctx(ctx)
	defer cancel()
	model := mongo.IndexModel{Keys: keys, Options: opts}
	_, err := s.Coll(coll).Indexes().CreateOne(cctx, model)
	return err
}

// LoadSingleton 读取「单文档配置」集合里固定 _id 的文档到 out(不存在返回 found=false)。
func (s *Store) LoadSingleton(ctx context.Context, coll, id string, out interface{}) (found bool, err error) {
	cctx, cancel := Ctx(ctx)
	defer cancel()
	err = s.Coll(coll).FindOne(cctx, map[string]interface{}{"_id": id}).Decode(out)
	if IsNoDocuments(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// SaveSingleton upsert「单文档配置」(固定 _id)。
func (s *Store) SaveSingleton(ctx context.Context, coll, id string, doc interface{}) error {
	cctx, cancel := Ctx(ctx)
	defer cancel()
	_, err := s.Coll(coll).ReplaceOne(
		cctx,
		map[string]interface{}{"_id": id},
		doc,
		options.Replace().SetUpsert(true),
	)
	return err
}
