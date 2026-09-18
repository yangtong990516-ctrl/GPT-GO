package store

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"

	"gpt-go/internal/model"
)

// mongoSentinelStore 是 SentinelStore 的 Mongo 实现。
// 集合:
//   - sentinel_settings       单文档(_id="main"),运行开关配置。
//   - sentinel_version_checks 检测历史,每条一个文档,保留最新 sentinelKeepChecks(50) 条。
type mongoSentinelStore struct {
	base *storeBase
}

// 编译期断言:mongoSentinelStore 必须实现 SentinelStore。
var _ SentinelStore = (*mongoSentinelStore)(nil)

const sentinelConfigSingletonID = "main"

// newMongoSentinelStore 以共享基座构造 Mongo 版 sentinel store,并建索引。
func newMongoSentinelStore(ctx context.Context, base *storeBase) SentinelStore {
	m := &mongoSentinelStore{base: base}
	// checkedAt 索引供「按时间取最新/裁最旧」使用(幂等,失败不阻断启动)。
	_ = base.ensureIndex(ctx, "sentinel_version_checks", bson.D{{Key: "checked_at", Value: -1}}, nil)
	return m
}

// LoadConfig 读取固定 _id="main" 的配置;未保存返回 ok=false(对齐 Mock)。
func (m *mongoSentinelStore) LoadConfig(ctx context.Context) (model.SentinelConfig, bool, error) {
	var cfg model.SentinelConfig
	found, err := m.base.loadSingleton(ctx, "sentinel_settings", sentinelConfigSingletonID, &cfg)
	if err != nil {
		return model.SentinelConfig{}, false, err
	}
	return cfg, found, nil
}

// SaveConfig upsert 固定 _id="main" 的配置文档。
func (m *mongoSentinelStore) SaveConfig(ctx context.Context, cfg model.SentinelConfig) error {
	return m.base.saveSingleton(ctx, "sentinel_settings", sentinelConfigSingletonID, cfg)
}

// AppendCheck 追加一条检测结果,并把历史裁剪到最新 sentinelKeepChecks 条(最旧先删)。
func (m *mongoSentinelStore) AppendCheck(ctx context.Context, r model.SentinelCheckResult) error {
	cctx, cancel := m.base.ctx(ctx)
	defer cancel()
	coll := m.base.coll("sentinel_version_checks")
	if _, err := coll.InsertOne(cctx, r); err != nil {
		return err
	}
	// 裁剪:超过 keepChecks 时,删除最旧的(按 checkedAt 升序的第 N 条之前的全部)。
	count, err := coll.CountDocuments(cctx, bson.D{})
	if err != nil || count <= sentinelKeepChecks {
		return err
	}
	// 找出第 sentinelKeepChecks 新的那条的 checkedAt 作为下限(保留 >= 它)。
	skip := count - int64(sentinelKeepChecks)
	var pivot model.SentinelCheckResult
	err = coll.FindOne(cctx, bson.D{}, options.FindOne().SetSort(bson.D{{Key: "checked_at", Value: -1}}).SetSkip(skip-1)).Decode(&pivot)
	if err != nil {
		return err
	}
	_, err = coll.DeleteMany(cctx, bson.M{"checked_at": bson.M{"$lt": pivot.CheckedAt}})
	return err
}

// LatestCheck 返回 checkedAt 最新的一条,无记录返回 (nil,nil)(对齐 Mock)。
func (m *mongoSentinelStore) LatestCheck(ctx context.Context) (*model.SentinelCheckResult, error) {
	cctx, cancel := m.base.ctx(ctx)
	defer cancel()
	var out model.SentinelCheckResult
	err := m.base.coll("sentinel_version_checks").
		FindOne(cctx, bson.D{}, options.FindOne().SetSort(bson.D{{Key: "checked_at", Value: -1}})).
		Decode(&out)
	if err != nil {
		if isNoDocuments(err) {
			return nil, nil
		}
		return nil, err
	}
	return &out, nil
}
