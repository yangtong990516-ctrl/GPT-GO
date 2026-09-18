package store

import (
	"context"
)

// 各 buildMongoXxxStore 工厂:调用对应 mongo_*.go 的 newMongoXxxStore(索引已在各自构造里确保)。
// 全部 store 均已实现 Mongo 版;装配层(wireStores)在连接失败时整体回退内存 Mock。

func buildMongoEmailStore(ctx context.Context, base *storeBase) (EmailStore, error) {
	return newMongoEmailStore(base), nil
}

func buildMongoRemailStore(ctx context.Context, base *storeBase) (RemailStore, error) {
	return newMongoRemailStore(base), nil
}

func buildMongoAccountStore(ctx context.Context, base *storeBase) (AccountStore, error) {
	return newMongoAccountStore(base)
}

func buildMongoSentinelStore(ctx context.Context, base *storeBase) (SentinelStore, error) {
	return newMongoSentinelStore(ctx, base), nil
}

func buildMongoProxyStore(ctx context.Context, base *storeBase) (ProxyStore, error) {
	return newMongoProxyStore(base), nil
}
