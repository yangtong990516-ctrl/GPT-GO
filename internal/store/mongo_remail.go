package store

import (
	"context"
)

// mongoRemailStore 是 RemailStore 的 Mongo 实现(单文档配置)。
// 集合:remail_settings;固定 _id:remail_config(与 mock 的内存单文档语义一致)。
type mongoRemailStore struct {
	base *storeBase
}

// 编译期断言:mongoRemailStore 必须实现 RemailStore。
var _ RemailStore = (*mongoRemailStore)(nil)

// newMongoRemailStore 以共享基座构造 Mongo 版 remail store。
func newMongoRemailStore(base *storeBase) RemailStore {
	return &mongoRemailStore{base: base}
}

const remailSingletonID = "remail_config"

// Load 读取固定 _id 的配置文档(不存在返回零值,对齐 Mock)。
func (m *mongoRemailStore) Load(ctx context.Context) (RemailConfigDocument, error) {
	var doc RemailConfigDocument
	_, err := m.base.loadSingleton(ctx, "remail_settings", remailSingletonID, &doc)
	if err != nil {
		return RemailConfigDocument{}, err
	}
	return doc, nil
}

// Save upsert 固定 _id 的配置文档。
func (m *mongoRemailStore) Save(ctx context.Context, d RemailConfigDocument) error {
	return m.base.saveSingleton(ctx, "remail_settings", remailSingletonID, d)
}
