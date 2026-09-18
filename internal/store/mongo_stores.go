package store

import (
	"context"
	"errors"
	"time"
)

// MongoStores 是全部业务 store 的 Mongo 实现集合(供 apiserver 装配)。
type MongoStores struct {
	Email    EmailStore
	Mailcode MailcodeStore
	Remail   RemailStore
	Account  AccountStore
	Sentinel SentinelStore
	Proxy    ProxyStore
}

// BuildMongoStores 用共享基座装配全部 Mongo 版 store 并建索引。
// 各 newMongoXxxStore 由对应 mongo_*.go 提供;任何必需的 store 未实现则返回错误(整体回退内存)。
func BuildMongoStores(base *storeBase) (MongoStores, error) {
	return BuildMongoStoresContext(context.Background(), base)
}

// BuildMongoStoresContext 同 BuildMongoStores,带 ctx(建索引超时)。
// 策略:已实现 Mongo 版的 store(email/mailcode/proxy)用 Mongo 持久化;
// 尚未实现的(account/remail/sentinel)回退内存 Mock,不阻断整体装配(渐进迁移)。
func BuildMongoStoresContext(ctx context.Context, base *storeBase) (MongoStores, error) {
	if base == nil {
		return MongoStores{}, errors.New("store base 不能为空")
	}
	out := MongoStores{}

	// mailcode(单文档配置,已实现 Mongo)。
	out.Mailcode = newMongoMailcodeStore(base)

	// email(已实现 Mongo)。
	emailStore, err := buildMongoEmailStore(ctx, base)
	if err != nil {
		return MongoStores{}, err
	}
	out.Email = emailStore

	// proxy(已实现 Mongo)。
	proxyStore, err := buildMongoProxyStore(ctx, base)
	if err != nil {
		return MongoStores{}, err
	}
	out.Proxy = proxyStore

	// account/remail/sentinel:Mongo 实现尚未落地,回退内存 Mock(渐进迁移,后续补)。
	if out.Account, err = buildMongoAccountStore(ctx, base); err != nil {
		out.Account = NewMockAccountStore()
	}
	if out.Remail, err = buildMongoRemailStore(ctx, base); err != nil {
		out.Remail = NewMockRemailStore()
	}
	if out.Sentinel, err = buildMongoSentinelStore(ctx, base); err != nil {
		out.Sentinel = NewMockSentinelStore()
	}

	_ = time.Now // 保留:索引建腔用 ctx 超时
	return out, nil
}
