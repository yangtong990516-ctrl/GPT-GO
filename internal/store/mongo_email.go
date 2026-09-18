// mongo_email.go 邮箱池(EmailStore)的 MongoDB 持久化实现(替代 MockEmailStore)。
//
// 集合:emails
// 唯一键:emailNormalized(Upsert 幂等:已存在返回 false)
// 索引:emailNormalized(唯一)、status、rebindReservedBy、sourceType、randomScore
//
// 语义严格对照 MockEmailStore(mock_email.go):
//   - List:importedAt desc 排序 + 分页;注册邮箱(emailNormalized ∈ accounts)排除在内存做。
//   - Upsert:InsertOne 撞唯一索引 → false(对齐 Mock「已存在返回 false」)。
//   - SetStatus:更新 status/statusReason/statusUpdatedAt/errorCode 并清 rebindReservedBy/reservedAt;
//     找不到返回 ErrNotFound,返回更新后文档。
//   - Reserve*/Consume*/Release*:FindOneAndUpdate 带前置条件 + ReturnDocument=After,
//     条件不满足(已被占位/非归属)返回 nil,nil 或 false(与 Mock 一致)。
//   - Count(pred):pred 是内存谓词无法下推,拉全量内存跑(邮箱池数据量小,对齐 Mock)。
package store

import (
	"context"
	"sort"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// emailsColl 是邮箱池集合名;accountsColl 是账号集合名(RegisteredEmails 取 distinct)。
// 二者为包内共享常量(供后续 mongo_account.go 复用)。
const (
	emailsColl   = "emails"
	accountsColl = "accounts"
)

// regexEscape 转义 regex 元字符(emailNormalized 子串匹配字面量,对齐 Mock strings.Contains)。
func regexEscape(s string) string {
	var b []rune
	for _, r := range s {
		switch r {
		case '.', '+', '*', '?', '(', ')', '[', ']', '{', '}', '^', '$', '|', '\\':
			b = append(b, '\\', r)
		default:
			b = append(b, r)
		}
	}
	return string(b)
}

// mongoEmailStore 是 EmailStore 的 Mongo 实现,内嵌共享基座。
type mongoEmailStore struct {
	base *storeBase
}

// 编译期接口断言。
var _ EmailStore = (*mongoEmailStore)(nil)

// newMongoEmailStore 以共享基座构造 Mongo 版邮箱 store,并确保索引(幂等)。
func newMongoEmailStore(base *storeBase) EmailStore {
	ctx := context.Background()
	// emailNormalized 唯一(Upsert 幂等);常用过滤/占位字段建索引。
	_ = base.ensureIndex(ctx, emailsColl, bson.D{{Key: "emailNormalized", Value: 1}}, options.Index().SetUnique(true))
	_ = base.ensureIndex(ctx, emailsColl, bson.D{{Key: "status", Value: 1}}, nil)
	_ = base.ensureIndex(ctx, emailsColl, bson.D{{Key: "rebindReservedBy", Value: 1}}, nil)
	_ = base.ensureIndex(ctx, emailsColl, bson.D{{Key: "sourceType", Value: 1}}, nil)
	_ = base.ensureIndex(ctx, emailsColl, bson.D{{Key: "randomScore", Value: 1}}, nil)
	return &mongoEmailStore{base: base}
}

// coll 返回邮箱集合。
func (s *mongoEmailStore) coll() *mongo.Collection { return s.base.coll(emailsColl) }

// ── List(过滤 + importedAt desc + 分页)─────────────────────────────────────

func (s *mongoEmailStore) List(ctx context.Context, q EmailQuery) ([]EmailDocument, int, error) {
	cctx, cancel := s.base.ctx(ctx)
	defer cancel()

	filter := emailFilter(q)
	// 拉全量过滤结果(importedAt desc),注册邮箱排除与分页在内存做——
	// 排除集 RegisteredEmails 来自 accounts 集合,Mongo 侧无 join,对齐 Mock 内存语义。
	opts := options.Find().SetSort(bson.D{{Key: "importedAt", Value: -1}})
	cur, err := s.coll().Find(cctx, filter, opts)
	if err != nil {
		return nil, 0, err
	}
	defer cur.Close(cctx)
	var all []EmailDocument
	if err := cur.All(cctx, &all); err != nil {
		return nil, 0, err
	}

	excluded, err := s.registeredSet(ctx)
	if err != nil {
		return nil, 0, err
	}
	docs := make([]EmailDocument, 0, len(all))
	for _, d := range all {
		if excluded[d.EmailNormalized] {
			continue
		}
		docs = append(docs, d)
	}
	total := len(docs)

	start := (q.Page - 1) * q.Size
	if start < 0 {
		start = 0
	}
	if start > len(docs) {
		start = len(docs)
	}
	end := start + q.Size
	if end > len(docs) {
		end = len(docs)
	}
	out := make([]EmailDocument, 0, end-start)
	out = append(out, docs[start:end]...)
	return out, total, nil
}

// emailFilter 把 EmailQuery 下推为 Mongo filter(对齐 Mock filtered 的前三项)。
func emailFilter(q EmailQuery) bson.M {
	filter := bson.M{}
	if q.Status != "" {
		filter["status"] = q.Status
	}
	switch q.Source {
	case "mailcom_alias", "mailcode", "remail":
		filter["sourceType"] = q.Source
	case "standard":
		// {$or:[{sourceType:{$exists:false}},{sourceType:{$nin:["mailcom_alias","mailcode"]}}]}
		filter["$or"] = bson.A{
			bson.M{"sourceType": bson.M{"$exists": false}},
			bson.M{"sourceType": bson.M{"$nin": bson.A{"mailcom_alias", "mailcode"}}},
		}
	default: // "all" or "" => no filter
	}
	if q.Query != "" {
		// emailNormalized 子串匹配(对齐 Mock strings.Contains;转小写由调用方保证)。
		filter["emailNormalized"] = bson.M{"$regex": regexEscape(q.Query)}
	}
	return filter
}

// ── SetStatus(清占位字段)────────────────────────────────────────────────────

func (s *mongoEmailStore) SetStatus(ctx context.Context, id, status, statusReason string, statusUpdatedAt time.Time, errorCode string) (*EmailDocument, error) {
	cctx, cancel := s.base.ctx(ctx)
	defer cancel()
	set := bson.M{
		"status":          status,
		"statusReason":    statusReason, // Mock:空串也覆盖
		"statusUpdatedAt": statusUpdatedAt,
	}
	if errorCode != "" {
		set["errorCode"] = errorCode
	}
	update := bson.M{
		"$set":   set,
		"$unset": bson.M{"rebindReservedBy": "", "reservedAt": ""},
	}
	opts := options.FindOneAndUpdate().SetReturnDocument(options.After)
	var d EmailDocument
	err := s.coll().FindOneAndUpdate(cctx, bson.M{"_id": id}, update, opts).Decode(&d)
	if isNoDocuments(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// ── ResetFailed(failed → available)──────────────────────────────────────────

func (s *mongoEmailStore) ResetFailed(ctx context.Context, ids []string) (int, error) {
	cctx, cancel := s.base.ctx(ctx)
	defer cancel()
	filter := bson.M{"status": "failed"}
	if len(ids) > 0 {
		filter["_id"] = bson.M{"$in": ids}
	}
	update := bson.M{
		"$set":   bson.M{"status": "available", "statusReason": ""},
		"$unset": bson.M{"statusUpdatedAt": "", "rebindReservedBy": "", "reservedAt": ""},
	}
	res, err := s.coll().UpdateMany(cctx, filter, update)
	if err != nil {
		return 0, err
	}
	return int(res.ModifiedCount), nil
}

// ── Delete(按 id 批量删)─────────────────────────────────────────────────────

func (s *mongoEmailStore) Delete(ctx context.Context, ids []string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	cctx, cancel := s.base.ctx(ctx)
	defer cancel()
	res, err := s.coll().DeleteMany(cctx, bson.M{"_id": bson.M{"$in": ids}})
	if err != nil {
		return 0, err
	}
	return int(res.DeletedCount), nil
}

// ── Upsert(emailNormalized 唯一幂等)─────────────────────────────────────────

func (s *mongoEmailStore) Upsert(ctx context.Context, d EmailDocument) (bool, error) {
	cctx, cancel := s.base.ctx(ctx)
	defer cancel()
	_, err := s.coll().InsertOne(cctx, d)
	if err != nil {
		// emailNormalized 唯一索引冲突 → 返回 false(对齐 Mock「已存在返回 false」)。
		if mongo.IsDuplicateKeyError(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// ── ListForExport(available,排除已注册,importedAt desc)──────────────────────

func (s *mongoEmailStore) ListForExport(ctx context.Context, ids []string) ([]EmailDocument, error) {
	cctx, cancel := s.base.ctx(ctx)
	defer cancel()
	filter := bson.M{"status": "available"}
	if len(ids) > 0 {
		filter["_id"] = bson.M{"$in": ids}
	}
	opts := options.Find().SetSort(bson.D{{Key: "importedAt", Value: -1}})
	cur, err := s.coll().Find(cctx, filter, opts)
	if err != nil {
		return nil, err
	}
	defer cur.Close(cctx)
	var all []EmailDocument
	if err := cur.All(cctx, &all); err != nil {
		return nil, err
	}
	excluded, err := s.registeredSet(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]EmailDocument, 0, len(all))
	for _, d := range all {
		if excluded[d.EmailNormalized] {
			continue
		}
		out = append(out, d)
	}
	return out, nil
}

// ── RegisteredEmails(accounts 集合 distinct emailNormalized)─────────────────

func (s *mongoEmailStore) RegisteredEmails(ctx context.Context) ([]string, error) {
	cctx, cancel := s.base.ctx(ctx)
	defer cancel()
	vals, err := s.base.coll(accountsColl).Distinct(cctx, "emailNormalized", bson.M{})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		if str, ok := v.(string); ok && str != "" {
			out = append(out, str)
		}
	}
	sort.Strings(out) // 对齐 Mock:排序保证确定性
	return out, nil
}

// registeredSet 返回已注册 emailNormalized 集合(供排除过滤)。
func (s *mongoEmailStore) registeredSet(ctx context.Context) (map[string]bool, error) {
	emails, err := s.RegisteredEmails(ctx)
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(emails))
	for _, e := range emails {
		set[e] = true
	}
	return set, nil
}

// ── Count(内存谓词)──────────────────────────────────────────────────────────

func (s *mongoEmailStore) Count(ctx context.Context, pred func(EmailDocument) bool) (int, error) {
	cctx, cancel := s.base.ctx(ctx)
	defer cancel()
	// pred 是内存谓词,无法下推 Mongo;拉全量内存过滤(邮箱池数据量小,对齐 Mock 语义)。
	cur, err := s.coll().Find(cctx, bson.M{})
	if err != nil {
		return 0, err
	}
	defer cur.Close(cctx)
	n := 0
	for cur.Next(cctx) {
		var d EmailDocument
		if err := cur.Decode(&d); err != nil {
			return 0, err
		}
		if pred == nil || pred(d) {
			n++
		}
	}
	return n, cur.Err()
}

// ── 换绑(rebind):原子占位/消费/释放 ─────────────────────────────────────────

// ReserveForRebind 原子预留一个 available 邮箱:filter 含 status==available 前置,
// 抢到返回更新后文档;无可用邮箱返回 nil,nil(对齐 Mock)。
func (s *mongoEmailStore) ReserveForRebind(ctx context.Context, runID string) (*EmailDocument, error) {
	cctx, cancel := s.base.ctx(ctx)
	defer cancel()
	now := time.Now().UTC()
	filter := bson.M{"status": "available"}
	update := bson.M{"$set": bson.M{
		"status":           "reserved",
		"rebindReservedBy": runID,
		"reservedAt":       now,
	}}
	// 排序保证多并发下取到确定性的一条(importedAt desc,对齐 List 顺序)。
	opts := options.FindOneAndUpdate().
		SetSort(bson.D{{Key: "importedAt", Value: -1}}).
		SetReturnDocument(options.After)
	var d EmailDocument
	err := s.coll().FindOneAndUpdate(cctx, filter, update, opts).Decode(&d)
	if isNoDocuments(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// ReserveForRebindByID 按 ID 预留指定邮箱:仅当 status==available 时置 reserved;
// 已被预留/消耗返回 nil,nil(对齐 Mock)。
func (s *mongoEmailStore) ReserveForRebindByID(ctx context.Context, emailID, runID string) (*EmailDocument, error) {
	cctx, cancel := s.base.ctx(ctx)
	defer cancel()
	now := time.Now().UTC()
	filter := bson.M{"_id": emailID, "status": "available"}
	update := bson.M{"$set": bson.M{
		"status":           "reserved",
		"rebindReservedBy": runID,
		"reservedAt":       now,
	}}
	opts := options.FindOneAndUpdate().SetReturnDocument(options.After)
	var d EmailDocument
	err := s.coll().FindOneAndUpdate(cctx, filter, update, opts).Decode(&d)
	if isNoDocuments(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// ConsumeRebindEmail 换绑成功后消费预留:仅当 rebindReservedBy==runID 时置
// status=used + usagePurpose=rebind 并清预留字段;返回是否成功消费。
func (s *mongoEmailStore) ConsumeRebindEmail(ctx context.Context, emailID, runID string) (bool, error) {
	cctx, cancel := s.base.ctx(ctx)
	defer cancel()
	filter := bson.M{"_id": emailID, "rebindReservedBy": runID}
	update := bson.M{
		"$set":   bson.M{"status": "used", "usagePurpose": "rebind"},
		"$unset": bson.M{"rebindReservedBy": "", "reservedAt": ""},
	}
	res, err := s.coll().UpdateOne(cctx, filter, update)
	if err != nil {
		return false, err
	}
	return res.ModifiedCount > 0, nil
}

// ReleaseRebindReservation 任务失败/取消时释放预留(status 回 available);
// 仅当 rebindReservedBy==runID 时生效,返回是否释放。
func (s *mongoEmailStore) ReleaseRebindReservation(ctx context.Context, emailID, runID string) (bool, error) {
	cctx, cancel := s.base.ctx(ctx)
	defer cancel()
	filter := bson.M{"_id": emailID, "rebindReservedBy": runID}
	update := bson.M{
		"$set":   bson.M{"status": "available"},
		"$unset": bson.M{"rebindReservedBy": "", "reservedAt": ""},
	}
	res, err := s.coll().UpdateOne(cctx, filter, update)
	if err != nil {
		return false, err
	}
	return res.ModifiedCount > 0, nil
}
