# 主站 Store Mongo 化 — 实施契约(子代理共用)

## 目标
把主站 in-memory mock store 迁移为 MongoDB 持久化,接口签名零改动,上层(service/apiserver)无感。

## 已有基建(直接复用,不要重写)
- `internal/store/mongo_base.go` → `storeBase`(内嵌即可):
  - `NewStoreBase(client *mongo.Client, database string) *storeBase`
  - `b.coll(name) *mongo.Collection`、`b.ctx(ctx)(ctx,cancel)`(带 30s 超时)
  - `b.ensureIndex(ctx, collName, keys, opts) error`(幂等)
  - `b.loadSingleton(ctx, coll, id, out)(found bool, err error)`、`b.saveSingleton(ctx, coll, id, doc) error`
  - `isNoDocuments(err) bool`
- 范本:`internal/store/mongo_mailcode.go`(单文档配置 store,已实现)。
- mongo client 连接:`internal/store/mongo/client.go` 的 `Connect(ctx, uri, manager)`(NewServer 会传入已连接的 client)。

## 统一规则
1. **接口签名一字不改**——新类型实现 `store.go`/`account.go`/`proxy.go` 里已有的 interface,编译期断言 `var _ XxxStore = (*mongoXxxStore)(nil)`。
2. **语义严格对照内存 Mock 实现**(同文件的 MockXxxStore),尤其是:
   - 原子占位/租约:用 `FindOneAndUpdate` 带条件(filter 含前置状态),返回更新后文档(`options.FindOneAndUpdate().SetReturnDocument(options.After)`),条件不满足=未抢到。
   - Upsert:按业务唯一键(如 emailNormalized / host+port+user+pass)建唯一索引,已存在返回 false(用 `mongo.IsDuplicateKeyError` 或先 FindOne)。
   - 时间:与 Mock 一致(RFC3339 / time.Time)。
   - Count(pred func)/复杂过滤无法下推时,按 query 过滤后拉全量在内存跑 pred(数据量小,可接受,注释说明)。
3. **bson tag 已就绪**:EmailDocument(store.go)、AccountDocument(account.go)、model.ProxyDocument(model/proxy.go)都已有 bson tag,直接用。remail/sentinel 的 config document 若缺 bson tag 请补齐(camelCase,与 json tag 一致;ID→`_id`)。
4. **集合名**:emails / accounts / proxies / mailcode_settings / remail_settings / sentinel_settings。
5. **索引**:在构造函数或 init 里用 ensureIndex 建业务所需索引(唯一键 + 常用过滤字段)。
6. **导出构造函数**:`func newMongoXxxStore(base *storeBase) XxxStore`(小写,由 NewServer 统一装配;不要自己连 mongo)。
7. 不要改 Mock 实现、不要改 NewServer(我统一接)、不要改 service 层。
8. 每个文件顶部加注释说明集合、唯一键、索引。
9. 完成后 `cd /Users/iceman/Documents/workspace/GPT-GO && GOTOOLCHAIN=auto go build ./internal/store/ && GOTOOLCHAIN=auto go vet ./internal/store/` 必须通过。

## 各 store 接口位置
- EmailStore:`internal/store/store.go`(EmailQuery/EmailDocument 同文件;Mock 在 mock_email.go)
- AccountStore:`internal/store/account.go`(AccountDocument/查询/PlanResultUpdate 同文件;Mock 在同文件)
- ProxyStore:`internal/store/proxy.go`(用 model.ProxyDocument;Mock 在同文件)
- RemailStore:`internal/store/remail.go`(单文档配置)
- SentinelStore:`internal/store/sentinel.go`(单文档配置)
