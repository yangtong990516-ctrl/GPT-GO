// mongo_account.go 账号池(AccountStore)的 MongoDB 持久化实现(替代 MockAccountStore)。
//
// 集合:accounts
// 唯一键:emailNormalized(Create 幂等:已存在返回 false,对齐 Mock)
//
// 语义严格对照 MockAccountStore(account.go):
//   - List:matchAccount 谓词下推为 Mongo filter + createdAt desc + 内存分页。
//   - Claim*:FindOneAndUpdate 带前置条件(accessToken 非空 + 未 running/卡死),
//     原子认领,返回更新后文档(含解密后的 AccessToken)。
//   - Store*/Mark*:UpdateOne $set/$unset,只写接口语义要求的字段;
//     找不到返回 &AccountNotFoundError{ID}(对齐 Mock)。
//   - Count(pred):内存谓词拉全量过滤(对齐 Mock;账号池量级可接受)。
//
// 敏感字段落库加密(参照 internal/icloud/store secretCodec,AES-GCM,enc:v1: 前缀):
//   - accessToken / refreshToken / chatgptPassword / totpSecret
//   写入路径(Create/MarkRebindSuccess)加密;读取路径(Get/Claim*/List/Count)解密。
//   历史明文启动时一次性迁移为密文(对齐 icloud encryptSensitiveRows)。
package store

import (
	"context"
	"strings"
	"time"

	"gpt-go/internal/model"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// mongoAccountStore 是 AccountStore 的 Mongo 实现,内嵌共享基座 + 加密 codec。
type mongoAccountStore struct {
	base  *storeBase
	codec *secretCodec
}

// 编译期接口断言。
var _ AccountStore = (*mongoAccountStore)(nil)

// newMongoAccountStore 以共享基座构造账号 Mongo store:加载加密 codec,
// 迁移历史明文敏感字段,并确保索引(幂等)。
func newMongoAccountStore(base *storeBase) (AccountStore, error) {
	ctx := context.Background()
	codec, err := base.loadSecretCodec(ctx)
	if err != nil {
		return nil, err
	}
	s := &mongoAccountStore{base: base, codec: codec}
	// emailNormalized 唯一(Create 幂等服务端兜底);常用过滤字段建索引。
	_ = base.ensureIndex(ctx, accountsColl, bson.D{{Key: "emailNormalized", Value: 1}}, options.Index().SetUnique(true))
	_ = base.ensureIndex(ctx, accountsColl, bson.D{{Key: "createdAt", Value: -1}}, nil)
	_ = base.ensureIndex(ctx, accountsColl, bson.D{{Key: "aliveStatus", Value: 1}}, nil)
	_ = base.ensureIndex(ctx, accountsColl, bson.D{{Key: "planCheckStatus", Value: 1}}, nil)
	_ = base.ensureIndex(ctx, accountsColl, bson.D{{Key: "paymentStatus", Value: 1}}, nil)
	_ = base.ensureIndex(ctx, accountsColl, bson.D{{Key: "rebindStatus", Value: 1}}, nil)
	// 历史明文敏感字段一次性加密迁移(幂等;enc:v1: 前缀跳过)。
	if err := s.encryptSensitiveRows(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// coll 返回账号集合。
func (s *mongoAccountStore) coll() *mongo.Collection { return s.base.coll(accountsColl) }

// ── 加解密 ─────────────────────────────────────────────────────────────────

// encryptDoc 加密文档敏感字段(写入前)。
func (s *mongoAccountStore) encryptDoc(d *AccountDocument) error {
	var err error
	if d.AccessToken, err = s.codec.encrypt(d.AccessToken); err != nil {
		return err
	}
	if d.ChatgptPassword, err = s.codec.encrypt(d.ChatgptPassword); err != nil {
		return err
	}
	if d.TotpSecret, err = s.codec.encrypt(d.TotpSecret); err != nil {
		return err
	}
	if d.RefreshToken, err = s.codec.encryptStringPtr(d.RefreshToken); err != nil {
		return err
	}
	return nil
}

// decryptDoc 解密文档敏感字段(读取后,原地修改)。
func (s *mongoAccountStore) decryptDoc(d *AccountDocument) {
	d.AccessToken = s.codec.decrypt(d.AccessToken)
	d.ChatgptPassword = s.codec.decrypt(d.ChatgptPassword)
	d.TotpSecret = s.codec.decrypt(d.TotpSecret)
	d.RefreshToken = s.codec.decryptStringPtr(d.RefreshToken)
}

// encryptSensitiveRows 启动时把历史明文敏感字段改写为密文(幂等)。
func (s *mongoAccountStore) encryptSensitiveRows(ctx context.Context) error {
	cctx, cancel := s.base.ctx(ctx)
	defer cancel()
	// 只扫「任一敏感字段非 enc:v1: 前缀」的文档,避免全表重写。
	filter := bson.M{"$or": bson.A{
		bson.M{"accessToken": bson.M{"$type": "string", "$not": bson.M{"$regex": "^enc:v1:"}, "$ne": ""}},
		bson.M{"chatgptPassword": bson.M{"$type": "string", "$not": bson.M{"$regex": "^enc:v1:"}, "$ne": ""}},
		bson.M{"totpSecret": bson.M{"$type": "string", "$not": bson.M{"$regex": "^enc:v1:"}, "$ne": ""}},
		bson.M{"refreshToken": bson.M{"$type": "string", "$not": bson.M{"$regex": "^enc:v1:"}, "$ne": ""}},
	}}
	cur, err := s.coll().Find(cctx, filter)
	if err != nil {
		return err
	}
	defer cur.Close(cctx)
	for cur.Next(cctx) {
		var d AccountDocument
		if err := cur.Decode(&d); err != nil {
			return err
		}
		// 记录加密前的敏感字段,若加密后有变化才回写(struct 含 slice 不能直接 ==)。
		before := [4]string{d.AccessToken, d.ChatgptPassword, d.TotpSecret, derefStr(d.RefreshToken)}
		if err := s.encryptDoc(&d); err != nil {
			return err
		}
		after := [4]string{d.AccessToken, d.ChatgptPassword, d.TotpSecret, derefStr(d.RefreshToken)}
		if after == before {
			continue
		}
		if _, err := s.coll().ReplaceOne(cctx, bson.M{"_id": d.ID}, d); err != nil {
			return err
		}
	}
	return cur.Err()
}

// ── List(过滤 + createdAt desc + 分页)──────────────────────────────────────

// accountFilter 把 AccountQuery 下推为 Mongo filter(对齐 matchAccount)。
func accountFilter(q AccountQuery) bson.M {
	filter := bson.M{}
	if q.Query != "" {
		// 对齐 Mock strings.Contains(emailNormalized, query):转义子串匹配。
		filter["emailNormalized"] = bson.M{"$regex": regexEscape(q.Query)}
	}
	if q.Country != "" {
		if q.Country == "ZZ" {
			// unmatched:registrationCountry 不存在或 null。
			filter["$or"] = bson.A{
				bson.M{"registrationCountry": bson.M{"$exists": false}},
				bson.M{"registrationCountry": nil},
			}
		} else {
			filter["registrationCountry"] = q.Country
		}
	}
	switch q.Promotion {
	case "untried_plus":
		// accountType=free && promotionEligible=true
		filter["accountType"] = "free"
		filter["promotionEligible"] = true
	case "ineligible":
		filter["$or"] = bson.A{
			bson.M{"promotionEligible": false},
			bson.M{"promotionEligible": bson.M{"$exists": false}},
			bson.M{"promotionEligible": nil},
		}
		// 注意:Mock 是「promotionEligible==nil 或 false」→ 用上面 $or;
		// 但 ineligible 语义要求显式 false,Mock 实现为「nil 或 !true」即非 true。
		// 对齐 Mock:d.PromotionEligible == nil || !*d.PromotionEligible → promotionEligible != true。
		filter["promotionEligible"] = bson.M{"$ne": true}
		delete(filter, "$or")
	case "unchecked":
		filter["$or"] = bson.A{
			bson.M{"promotionEligible": bson.M{"$exists": false}},
			bson.M{"promotionEligible": nil},
		}
	}
	if q.Alive != "" {
		switch q.Alive {
		case "alive", "dead", "unknown":
			filter["aliveStatus"] = q.Alive
		case "unchecked":
			filter["$or"] = bson.A{
				bson.M{"aliveStatus": bson.M{"$exists": false}},
				bson.M{"aliveStatus": nil},
			}
		}
	}
	if q.Payment != "" {
		filter["paymentMethods"] = q.Payment
	}
	if q.ZeroPayment != "" {
		filter["paymentZeroMethods"] = q.ZeroPayment
	}
	if q.PaymentStatus != "" {
		if q.PaymentStatus == "unchecked" {
			filter["$or"] = bson.A{
				bson.M{"paymentStatus": bson.M{"$exists": false}},
				bson.M{"paymentStatus": nil},
			}
		} else {
			filter["paymentStatus"] = q.PaymentStatus
		}
	}
	return filter
}

// List 过滤 + createdAt desc + 分页(返回的文档已解密)。
func (s *mongoAccountStore) List(ctx context.Context, q AccountQuery, page, pageSize int) ([]AccountDocument, int, error) {
	cctx, cancel := s.base.ctx(ctx)
	defer cancel()
	filter := accountFilter(q)
	// 拉全量过滤结果(createdAt desc),分页在内存做——对齐 Mock(数据量可接受,
	// 且 PaymentMethods 包含匹配等谓词在 Mongo 侧已下推,内存只做分页)。
	cur, err := s.coll().Find(cctx, filter, options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}}))
	if err != nil {
		return nil, 0, err
	}
	defer cur.Close(cctx)
	var all []AccountDocument
	if err := cur.All(cctx, &all); err != nil {
		return nil, 0, err
	}
	total := len(all)
	start := (page - 1) * pageSize
	if start < 0 {
		start = 0
	}
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	out := make([]AccountDocument, 0, end-start)
	for i := start; i < end; i++ {
		d := all[i]
		s.decryptDoc(&d)
		out = append(out, d)
	}
	return out, total, nil
}

// Create 插入账号(加密敏感字段);emailNormalized 已存在返回 false(唯一索引兜底)。
func (s *mongoAccountStore) Create(ctx context.Context, d AccountDocument) (bool, error) {
	if d.ID == "" {
		id, err := s.base.nextID(ctx, "ac")
		if err != nil {
			return false, err
		}
		d.ID = id
	}
	doc := d
	if err := s.encryptDoc(&doc); err != nil {
		return false, err
	}
	cctx, cancel := s.base.ctx(ctx)
	defer cancel()
	_, err := s.coll().InsertOne(cctx, doc)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Delete 按 id 批量删除,返回删除数。
func (s *mongoAccountStore) Delete(ctx context.Context, ids []string) (int, error) {
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

// Count 返回匹配 pred 的账号数(内存谓词,拉全量过滤,对齐 Mock;已解密)。
func (s *mongoAccountStore) Count(ctx context.Context, pred func(AccountDocument) bool) (int, error) {
	cctx, cancel := s.base.ctx(ctx)
	defer cancel()
	cur, err := s.coll().Find(cctx, bson.M{})
	if err != nil {
		return 0, err
	}
	defer cur.Close(cctx)
	n := 0
	for cur.Next(cctx) {
		var d AccountDocument
		if err := cur.Decode(&d); err != nil {
			return 0, err
		}
		s.decryptDoc(&d)
		if pred == nil || pred(d) {
			n++
		}
	}
	return n, cur.Err()
}

// Get 按 id 取账号(不存在返回 nil,nil;已解密)。
func (s *mongoAccountStore) Get(ctx context.Context, id string) (*AccountDocument, error) {
	cctx, cancel := s.base.ctx(ctx)
	defer cancel()
	var d AccountDocument
	err := s.coll().FindOne(cctx, bson.M{"_id": id}).Decode(&d)
	if isNoDocuments(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s.decryptDoc(&d)
	return &d, nil
}

// ── 认领(Claim*)────────────────────────────────────────────────────────────

// staleCutoff 计算卡死阈值(staleMin<=0 默认 5,对齐 Mock)。
func staleCutoff(staleMin int) time.Time {
	if staleMin <= 0 {
		staleMin = 5
	}
	return time.Now().UTC().Add(-time.Duration(staleMin) * time.Minute)
}

// claimFilter 组装认领前置条件:accessToken 非空 + (status!=running 或 startedAt 已卡死)。
// statusField/startedField 形如 planCheckStatus/planCheckStartedAt。
func claimFilter(id, statusField, startedField string, staleMin int) bson.M {
	cutoff := staleCutoff(staleMin)
	return bson.M{
		"_id": id,
		// accessToken 非空(可能是密文 enc:v1:...,只需非空)。
		"accessToken": bson.M{"$exists": true, "$ne": ""},
		"$or": bson.A{
			bson.M{statusField: bson.M{"$ne": "running"}},
			bson.M{startedField: bson.M{"$exists": false}},
			bson.M{startedField: nil},
			bson.M{startedField: bson.M{"$lte": cutoff}},
		},
	}
}

// claim 原子认领:置 status=running + startedAt=now + 清 error/httpStatus,
// 返回更新后文档(已解密);条件不满足返回 nil,nil。
func (s *mongoAccountStore) claim(ctx context.Context, id, statusField, startedField, errField, httpField string, staleMin int) (*AccountDocument, error) {
	cctx, cancel := s.base.ctx(ctx)
	defer cancel()
	now := time.Now().UTC()
	running := "running"
	update := bson.M{
		"$set":   bson.M{statusField: running, startedField: now},
		"$unset": bson.M{errField: "", httpField: ""},
	}
	opts := options.FindOneAndUpdate().SetReturnDocument(options.After)
	var d AccountDocument
	err := s.coll().FindOneAndUpdate(cctx, claimFilter(id, statusField, startedField, staleMin), update, opts).Decode(&d)
	if isNoDocuments(err) {
		return nil, nil // 不存在/无 token/已被认领
	}
	if err != nil {
		return nil, err
	}
	s.decryptDoc(&d)
	return &d, nil
}

// ClaimPlanCheck 原子认领套餐检查。
func (s *mongoAccountStore) ClaimPlanCheck(ctx context.Context, id string, staleMin int) (*AccountDocument, error) {
	return s.claim(ctx, id, "planCheckStatus", "planCheckStartedAt", "planCheckErrorCode", "planCheckHttpStatus", staleMin)
}

// ClaimAliveCheck 原子认领验活。
func (s *mongoAccountStore) ClaimAliveCheck(ctx context.Context, id string, staleMin int) (*AccountDocument, error) {
	return s.claim(ctx, id, "aliveStatus", "aliveCheckStartedAt", "aliveErrorCode", "aliveHttpStatus", staleMin)
}

// ClaimPaymentCheck 原子认领支付检测(对齐 Mock:清 running 前置 + 置 running)。
func (s *mongoAccountStore) ClaimPaymentCheck(ctx context.Context, id string, staleMin int) (*AccountDocument, error) {
	cctx, cancel := s.base.ctx(ctx)
	defer cancel()
	now := time.Now().UTC()
	filter := claimFilter(id, "paymentStatus", "paymentCheckStartedAt", staleMin)
	update := bson.M{"$set": bson.M{"paymentStatus": "running", "paymentCheckStartedAt": now}}
	opts := options.FindOneAndUpdate().SetReturnDocument(options.After)
	var d AccountDocument
	err := s.coll().FindOneAndUpdate(cctx, filter, update, opts).Decode(&d)
	if isNoDocuments(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s.decryptDoc(&d)
	return &d, nil
}

// ClaimRebind 原子认领换绑(对齐 Mock:running 未卡死拒绝;ReboundAt 复用为认领时间戳)。
func (s *mongoAccountStore) ClaimRebind(ctx context.Context, id string, staleMin int) (*AccountDocument, error) {
	cctx, cancel := s.base.ctx(ctx)
	defer cancel()
	now := time.Now().UTC()
	cutoff := staleCutoff(staleMin)
	filter := bson.M{
		"_id": id,
		"$or": bson.A{
			bson.M{"rebindStatus": bson.M{"$ne": "running"}},
			// running 但 reboundAt 已卡死(或不存在)→ 可重新认领。
			bson.M{"reboundAt": bson.M{"$exists": false}},
			bson.M{"reboundAt": nil},
			bson.M{"reboundAt": bson.M{"$lte": cutoff}},
		},
	}
	update := bson.M{"$set": bson.M{"rebindStatus": "running", "reboundAt": now}}
	opts := options.FindOneAndUpdate().SetReturnDocument(options.After)
	var d AccountDocument
	err := s.coll().FindOneAndUpdate(cctx, filter, update, opts).Decode(&d)
	if isNoDocuments(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s.decryptDoc(&d)
	return &d, nil
}

// ── 落库(Store*/Mark*)──────────────────────────────────────────────────────

// updateByID 执行 $set/$unset 更新;找不到返回 AccountNotFoundError(对齐 Mock)。
func (s *mongoAccountStore) updateByID(ctx context.Context, id string, update bson.M) error {
	cctx, cancel := s.base.ctx(ctx)
	defer cancel()
	res, err := s.coll().UpdateOne(cctx, bson.M{"_id": id}, update)
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return &AccountNotFoundError{ID: id}
	}
	return nil
}

// planResultSet 组装套餐成功落库的 $set(对齐 StorePlanResult/StoreCombinedResult 的套餐组)。
func planResultSet(r PlanResultUpdate) bson.M {
	set := bson.M{
		"planCheckStatus":       "success",
		"planCheckedAt":         r.CheckedAt,
		"planCheckHttpStatus":   r.HTTPStatus,
		"planAccountId":         r.AccountID,
		"subscriptionPlan":      r.SubscriptionPlan,
		"hasActiveSubscription": r.HasActiveSubscription,
		"planExpiresAt":         r.ExpiresAt,
		"planRenewsAt":          r.RenewsAt,
		"promotionEligible":     r.PromotionEligible,
		"promotionCampaignId":   r.PromotionCampaignID,
	}
	// 仅归一化为 free/plus 才写 accountType(对齐 codex normalized_plan)。
	if r.AccountType == "free" || r.AccountType == "plus" {
		set["accountType"] = r.AccountType
	}
	return set
}

// StorePlanResult 套餐检查成功落库。
func (s *mongoAccountStore) StorePlanResult(ctx context.Context, id string, r PlanResultUpdate) error {
	set := planResultSet(r)
	return s.updateByID(ctx, id, bson.M{
		"$set":   set,
		"$unset": bson.M{"planCheckErrorCode": ""},
	})
}

// StorePlanFailure 套餐检查失败落库;tokenInvalid 时置 accessTokenConfigured=false。
func (s *mongoAccountStore) StorePlanFailure(ctx context.Context, id, errCode string, httpStatus *int, tokenInvalid bool) error {
	now := time.Now().UTC()
	set := bson.M{
		"planCheckStatus":     "failed",
		"planCheckedAt":       now,
		"planCheckErrorCode":  errCode,
		"planCheckHttpStatus": httpStatus,
	}
	if tokenInvalid {
		set["accessTokenConfigured"] = false
	}
	return s.updateByID(ctx, id, bson.M{"$set": set})
}

// StoreAliveResult 验活落库(alive/dead);alive 时清 errorCode。
func (s *mongoAccountStore) StoreAliveResult(ctx context.Context, id string, alive bool, httpStatus *int) error {
	now := time.Now().UTC()
	st := "dead"
	if alive {
		st = "alive"
	}
	update := bson.M{"$set": bson.M{
		"aliveStatus":     st,
		"aliveCheckedAt":  now,
		"aliveHttpStatus": httpStatus,
	}}
	if alive {
		update["$unset"] = bson.M{"aliveErrorCode": ""}
	}
	return s.updateByID(ctx, id, update)
}

// StoreTotpStatus 2FA 状态检测落库(totpStatus + mfaFlagEnabled + totpCheckedAt)。
func (s *mongoAccountStore) StoreTotpStatus(ctx context.Context, id string, totpStatus string, mfaEnabled bool) error {
	now := time.Now().UTC()
	set := bson.M{"mfaFlagEnabled": mfaEnabled, "totpCheckedAt": now}
	if totpStatus == "" {
		set["totpStatus"] = nil
	} else {
		set["totpStatus"] = totpStatus
	}
	return s.updateByID(ctx, id, bson.M{"$set": set})
}

// StoreTotp 补 2FA 成功落库(对齐 Mock:totpSecret 加密 + totpStatus=enabled +
// mfaFlagEnabled=true + accessToken 组字段;totpSecret 空返回参数错误)。
func (s *mongoAccountStore) StoreTotp(ctx context.Context, id string, u TotpUpdate) error {
	secret := strings.TrimSpace(u.TotpSecret)
	if secret == "" {
		return &AccountInvalidUpdateError{ID: id, Reason: "totpSecret 不能为空"}
	}
	encSecret, err := s.codec.encrypt(secret)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	set := bson.M{
		"totpSecret":     encSecret,
		"totpStatus":     "enabled",
		"mfaFlagEnabled": true,
		"totpCheckedAt":  now,
	}
	// enroll 时的 recent_auth AT 一并刷新 token 字段(对齐 codex store_account_totp)。
	if tok := strings.TrimSpace(u.AccessToken); tok != "" {
		encTok, err := s.codec.encrypt(tok)
		if err != nil {
			return err
		}
		set["accessToken"] = encTok
		set["accessTokenConfigured"] = true
		set["accessTokenUpdatedAt"] = now
		if u.AccessTokenExpiresAt != nil {
			set["accessTokenExpiresAt"] = *u.AccessTokenExpiresAt
		}
	}
	// 补密码分支:新生效密码加密落库(对齐 codex 无密码分支 chatgptPassword + passwordConfiguredAt)。
	if pwd := strings.TrimSpace(u.ChatgptPassword); pwd != "" {
		encPwd, err := s.codec.encrypt(pwd)
		if err != nil {
			return err
		}
		set["chatgptPassword"] = encPwd
		set["passwordConfiguredAt"] = now
	}
	return s.updateByID(ctx, id, bson.M{"$set": set})
}

// StorePassword 仅写密码 + AT(Mongo,不碰已有 totp 字段)。
func (s *mongoAccountStore) StorePassword(ctx context.Context, id string, u TotpUpdate) error {
	pwd := strings.TrimSpace(u.ChatgptPassword)
	if pwd == "" {
		return &AccountInvalidUpdateError{ID: id, Reason: "chatgptPassword 不能为空"}
	}
	encPwd, err := s.codec.encrypt(pwd)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	set := bson.M{
		"chatgptPassword":      encPwd,
		"passwordConfiguredAt": now,
	}
	if tok := strings.TrimSpace(u.AccessToken); tok != "" {
		encTok, err := s.codec.encrypt(tok)
		if err != nil {
			return err
		}
		set["accessToken"] = encTok
		set["accessTokenConfigured"] = true
		set["accessTokenUpdatedAt"] = now
		if u.AccessTokenExpiresAt != nil {
			set["accessTokenExpiresAt"] = *u.AccessTokenExpiresAt
		}
	}
	return s.updateByID(ctx, id, bson.M{"$set": set})
}

// StoreAliveFailure 验活异常落库(aliveStatus=unknown + errorCode)。
func (s *mongoAccountStore) StoreAliveFailure(ctx context.Context, id, errCode string, httpStatus *int) error {
	now := time.Now().UTC()
	return s.updateByID(ctx, id, bson.M{"$set": bson.M{
		"aliveStatus":     "unknown",
		"aliveCheckedAt":  now,
		"aliveErrorCode":  errCode,
		"aliveHttpStatus": httpStatus,
	}})
}

// StoreCombinedResult 验活+套餐成功一次写两组。
func (s *mongoAccountStore) StoreCombinedResult(ctx context.Context, id string, r PlanResultUpdate) error {
	set := planResultSet(r)
	set["aliveStatus"] = "alive"
	set["aliveCheckedAt"] = r.CheckedAt
	set["aliveHttpStatus"] = r.HTTPStatus
	return s.updateByID(ctx, id, bson.M{
		"$set":   set,
		"$unset": bson.M{"aliveErrorCode": "", "planCheckErrorCode": ""},
	})
}

// StoreCombinedFailure 验活+套餐失败一次写两组;tokenInvalid 置 accessTokenConfigured=false。
func (s *mongoAccountStore) StoreCombinedFailure(ctx context.Context, id, errCode string, httpStatus *int, dead, tokenInvalid bool) error {
	now := time.Now().UTC()
	aliveSt := "unknown"
	if dead {
		aliveSt = "dead"
	}
	set := bson.M{
		"aliveStatus":         aliveSt,
		"aliveCheckedAt":      now,
		"aliveErrorCode":      errCode,
		"aliveHttpStatus":     httpStatus,
		"planCheckStatus":     "failed",
		"planCheckedAt":       now,
		"planCheckErrorCode":  errCode,
		"planCheckHttpStatus": httpStatus,
	}
	if tokenInvalid {
		set["accessTokenConfigured"] = false
	}
	return s.updateByID(ctx, id, bson.M{"$set": set})
}

// StorePaymentRouteResult 单条线路结果落库(按 country 键覆盖/新增,对齐 Mock map 语义)。
func (s *mongoAccountStore) StorePaymentRouteResult(ctx context.Context, id string, r model.PaymentRouteResult) error {
	return s.updateByID(ctx, id, bson.M{"$set": bson.M{"paymentRoutes." + r.Country: r}})
}

// StorePaymentSummary 支付检测聚合落库;tokenInvalid 联动置死(对齐 Mock)。
func (s *mongoAccountStore) StorePaymentSummary(ctx context.Context, id string, sum PaymentSummaryUpdate) error {
	set := bson.M{
		"paymentStatus":       sum.Status,
		"paymentMethods":      sum.Methods,
		"paymentZeroMethods":  sum.ZeroMethods,
		"paymentCheckedAt":    sum.CheckedAt,
		"paymentCheckStartedAt": nil,
	}
	if sum.TokenInvalid {
		set["accessTokenConfigured"] = false
		set["aliveStatus"] = "dead"
		set["aliveCheckedAt"] = time.Now().UTC()
	}
	return s.updateByID(ctx, id, bson.M{"$set": set})
}

// ── 换绑落库 ────────────────────────────────────────────────────────────────

// rebindChangedSet 组装 MarkRebindEmailChanged 的 $set(对齐 Mock)。
func rebindChangedSet(u RebindEmailChangedUpdate) bson.M {
	norm := normalizeEmail(u.NewEmail)
	set := bson.M{
		"email":          norm,
		"emailNormalized": norm,
		"emailAccessUrl": trimSpace(u.NewAccessURL),
		"rebindStatus":   "email_changed_token_pending",
		"previousEmail":  normalizeEmail(u.PreviousEmail),
		"reboundEmail":   norm,
		"reboundAt":      u.ReboundAt,
	}
	if u.ProxyCountry != "" {
		set["rebindProxyCountry"] = u.ProxyCountry
	}
	return set
}

// MarkRebindEmailChanged 远端换绑不可逆时立即落库(清 rebindError)。
func (s *mongoAccountStore) MarkRebindEmailChanged(ctx context.Context, id string, u RebindEmailChangedUpdate) error {
	return s.updateByID(ctx, id, bson.M{
		"$set":   rebindChangedSet(u),
		"$unset": bson.M{"rebindError": ""},
	})
}

// MarkRebindSuccess 新邮箱重登确认 + 新 AT 落库(AT 加密)。
func (s *mongoAccountStore) MarkRebindSuccess(ctx context.Context, id string, u RebindSuccessUpdate) error {
	set := rebindChangedSet(u.RebindEmailChangedUpdate)
	set["rebindStatus"] = "success"
	// AT 加密落库。
	encToken, err := s.codec.encrypt(u.AccessToken)
	if err != nil {
		return err
	}
	set["accessToken"] = encToken
	set["accessTokenConfigured"] = u.AccessToken != ""
	set["accessTokenExpiresAt"] = u.AccessTokenExpiresAt
	set["accessTokenUpdatedAt"] = time.Now().UTC()
	return s.updateByID(ctx, id, bson.M{
		"$set":   set,
		"$unset": bson.M{"rebindError": ""},
	})
}

// MarkRebindFailure 换绑失败落库(保留原邮箱)。
func (s *mongoAccountStore) MarkRebindFailure(ctx context.Context, id, errCode string) error {
	return s.updateByID(ctx, id, bson.M{"$set": bson.M{
		"rebindStatus": "failed",
		"rebindError":  errCode,
	}})
}

// ── 小工具 ─────────────────────────────────────────────────────────────────

// normalizeEmail 对齐 Mock 的 strings.ToLower(strings.TrimSpace(...))。
func normalizeEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// trimSpace 对齐 Mock 的 strings.TrimSpace。
func trimSpace(s string) string { return strings.TrimSpace(s) }

// derefStr 安全解引用 *string(nil → "")。
func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

