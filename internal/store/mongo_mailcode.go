package store

import (
	"context"
)

// mongoMailcodeStore 是 MailcodeStore 的 Mongo 实现(单文档配置)。
// 通过 storeBase 复用连接/单文档读写 helper;接口与 Mock 完全一致,上层零改动。
type mongoMailcodeStore struct {
	base *storeBase
}

// newMongoMailcodeStore 以共享基座构造 Mongo 版 mailcode store。
func newMongoMailcodeStore(base *storeBase) MailcodeStore {
	return &mongoMailcodeStore{base: base}
}

const mailcodeSingletonID = "mailcode_config"

// Load 读取固定 _id 的配置文档(不存在返回零值)。
func (m *mongoMailcodeStore) Load(ctx context.Context) (MailcodeConfigDocument, error) {
	var doc MailcodeConfigDocument
	_, err := m.base.loadSingleton(ctx, "mailcode_settings", mailcodeSingletonID, &doc)
	if err != nil {
		return MailcodeConfigDocument{}, err
	}
	return doc, nil
}

// Save upsert 固定 _id 的配置文档。
func (m *mongoMailcodeStore) Save(ctx context.Context, d MailcodeConfigDocument) error {
	return m.base.saveSingleton(ctx, "mailcode_settings", mailcodeSingletonID, d)
}
