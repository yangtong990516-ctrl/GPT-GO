package store

import (
	"context"
	"sort"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"gpt-go/internal/model"
)

// mongoProxyStore 是 ProxyStore 的 Mongo 实现。
// 通过 storeBase 复用连接/集合/索引/自增ID helper;接口语义与 MockProxyStore 完全一致,
// 上层 service 零改动。
type mongoProxyStore struct {
	base *storeBase
}

// 编译期接口断言。
var _ ProxyStore = (*mongoProxyStore)(nil)

// newMongoProxyStore 以共享基座构造 Mongo 版 proxy store,并确保索引(幂等)。
// 由 mongo_factories.go 的 buildMongoProxyStore 直接调用(不再走函数变量注入)。
func newMongoProxyStore(base *storeBase) ProxyStore {
	s := &mongoProxyStore{base: base}
	_ = s.ensureIndexes(context.Background())
	return s
}

const proxyColl = "proxies"

func (m *mongoProxyStore) ensureIndexes(ctx context.Context) error {
	if err := m.base.ensureIndex(ctx, proxyColl, bson.D{
		{Key: "host", Value: 1}, {Key: "port", Value: 1}, {Key: "username", Value: 1}, {Key: "password", Value: 1},
	}, options.Index().SetUnique(true).SetName("uniq_identity")); err != nil {
		_ = m.base.ensureIndex(ctx, proxyColl, bson.D{{Key: "host", Value: 1}, {Key: "port", Value: 1}}, nil)
	}
	_ = m.base.ensureIndex(ctx, proxyColl, bson.D{{Key: "status", Value: 1}}, nil)
	_ = m.base.ensureIndex(ctx, proxyColl, bson.D{{Key: "country", Value: 1}}, nil)
	_ = m.base.ensureIndex(ctx, proxyColl, bson.D{{Key: "group", Value: 1}}, nil)
	_ = m.base.ensureIndex(ctx, proxyColl, bson.D{{Key: "leaseUntil", Value: 1}}, nil)
	_ = m.base.ensureIndex(ctx, proxyColl, bson.D{{Key: "createdAt", Value: -1}}, nil)
	return nil
}

func (m *mongoProxyStore) c(ctx context.Context) (context.Context, context.CancelFunc) {
	return m.base.ctx(ctx)
}

func (m *mongoProxyStore) collection() *mongo.Collection { return m.base.coll(proxyColl) }

func (m *mongoProxyStore) List(ctx context.Context, q ProxyListQuery) ([]model.ProxyDocument, int, error) {
	cctx, cancel := m.c(ctx)
	defer cancel()
	filter := bson.M{}
	if q.Country != "" {
		cc := model.NormalizeCountryCode(q.Country)
		filter["$or"] = []bson.M{{"country": cc}, {"country": bson.M{"$in": []string{"", "ZZ"}}}}
	}
	cursor, err := m.collection().Find(cctx, filter, options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}}))
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = cursor.Close(cctx) }()
	var all []model.ProxyDocument
	for cursor.Next(cctx) {
		var d model.ProxyDocument
		if err := cursor.Decode(&d); err != nil {
			return nil, 0, err
		}
		if q.Country != "" && !proxyDocMatchesCountry(&d, q.Country) {
			continue
		}
		if q.Query != "" {
			qq := strings.ToLower(q.Query)
			if !strings.Contains(strings.ToLower(d.Host), qq) && !strings.Contains(strings.ToLower(d.Username), qq) {
				continue
			}
		}
		all = append(all, d)
	}
	if err := cursor.Err(); err != nil {
		return nil, 0, err
	}
	total := len(all)
	start := (q.Page - 1) * q.Size
	if start < 0 {
		start = 0
	}
	if start > total {
		start = total
	}
	end := start + q.Size
	if end > total {
		end = total
	}
	return all[start:end], total, nil
}

func (m *mongoProxyStore) Upsert(ctx context.Context, d model.ProxyDocument) (bool, error) {
	cctx, cancel := m.c(ctx)
	defer cancel()
	filter := bson.M{"host": d.Host, "port": d.Port, "username": d.Username, "password": d.Password}
	var existing model.ProxyDocument
	err := m.collection().FindOne(cctx, filter).Decode(&existing)
	if err == nil {
		update := bson.M{}
		if d.Scheme != "" {
			update["scheme"] = d.Scheme
		}
		if d.Country != "" && d.Country != "ZZ" {
			update["country"] = d.Country
		}
		if d.Group != "" {
			update["group"] = d.Group
		}
		if d.TimezoneID != "" {
			update["timezoneId"] = d.TimezoneID
		}
		if d.TimezoneOffsetSec != nil {
			update["timezoneOffsetSec"] = d.TimezoneOffsetSec
		}
		if len(update) > 0 {
			_, _ = m.collection().UpdateOne(cctx, bson.M{"_id": existing.ID}, bson.M{"$set": update})
		}
		return false, nil
	}
	if !isNoDocuments(err) {
		return false, err
	}
	id, err := m.base.nextID(cctx, "proxy")
	if err != nil {
		id = "proxy_" + time.Now().UTC().Format("20060102150405")
	}
	d.ID = id
	if d.CreatedAt == "" {
		d.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if d.RandomScore == 0 {
		d.RandomScore = time.Now().UnixNano()
	}
	if d.ActiveLeaseOwners == nil {
		d.ActiveLeaseOwners = []string{}
	}
	_, err = m.collection().InsertOne(cctx, d)
	if err != nil {
		return false, err
	}
	return true, nil
}

func (m *mongoProxyStore) Update(ctx context.Context, id string, enabled *bool, country, group *string) (*model.ProxyDocument, error) {
	cctx, cancel := m.c(ctx)
	defer cancel()
	update := bson.M{}
	if enabled != nil {
		update["enabled"] = *enabled
	}
	if country != nil {
		update["country"] = model.NormalizeCountryCode(*country)
	}
	if group != nil {
		update["group"] = model.NormalizeProxyGroup(*group)
	}
	if len(update) == 0 {
		return nil, ErrNotFound
	}
	var result model.ProxyDocument
	opts := options.FindOneAndUpdate().SetReturnDocument(options.After)
	err := m.collection().FindOneAndUpdate(cctx, bson.M{"_id": id}, bson.M{"$set": update}, opts).Decode(&result)
	if err != nil {
		if isNoDocuments(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &result, nil
}

func (m *mongoProxyStore) SetStatus(ctx context.Context, id, status string) (*model.ProxyDocument, error) {
	cctx, cancel := m.c(ctx)
	defer cancel()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	set := bson.M{"status": status, "statusUpdatedAt": now}
	unset := bson.M{}
	if status == model.ProxyStatusAvailable {
		unset["usedAt"] = ""
	}
	update := bson.M{"$set": set}
	if len(unset) > 0 {
		update["$unset"] = unset
	}
	var result model.ProxyDocument
	opts := options.FindOneAndUpdate().SetReturnDocument(options.After)
	err := m.collection().FindOneAndUpdate(cctx, bson.M{"_id": id}, update, opts).Decode(&result)
	if err != nil {
		if isNoDocuments(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &result, nil
}

func (m *mongoProxyStore) DeleteOne(ctx context.Context, id string) (int, error) {
	cctx, cancel := m.c(ctx)
	defer cancel()
	res, err := m.collection().DeleteOne(cctx, bson.M{"_id": id})
	if err != nil {
		return 0, err
	}
	return int(res.DeletedCount), nil
}

func (m *mongoProxyStore) DeleteMany(ctx context.Context, ids []string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	cctx, cancel := m.c(ctx)
	defer cancel()
	res, err := m.collection().DeleteMany(cctx, bson.M{"_id": bson.M{"$in": ids}})
	if err != nil {
		return 0, err
	}
	return int(res.DeletedCount), nil
}

func (m *mongoProxyStore) Clear(ctx context.Context) (int, error) {
	cctx, cancel := m.c(ctx)
	defer cancel()
	res, err := m.collection().DeleteMany(cctx, bson.M{})
	if err != nil {
		return 0, err
	}
	return int(res.DeletedCount), nil
}

func (m *mongoProxyStore) CountrySummaries(ctx context.Context) ([]model.ProxyCountrySummary, error) {
	cctx, cancel := m.c(ctx)
	defer cancel()
	cursor, err := m.collection().Find(cctx, bson.M{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = cursor.Close(cctx) }()
	var docs []model.ProxyDocument
	if err := cursor.All(cctx, &docs); err != nil {
		return nil, err
	}
	counts := map[string][2]int{}
	for _, d := range docs {
		cc := effectiveCountry(&d)
		v := counts[cc]
		v[0]++
		if d.Enabled {
			v[1]++
		}
		counts[cc] = v
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]model.ProxyCountrySummary, 0, len(keys))
	for _, k := range keys {
		out = append(out, model.ProxyCountrySummary{Country: k, Total: counts[k][0], Enabled: counts[k][1]})
	}
	return out, nil
}

func (m *mongoProxyStore) GroupSummaries(ctx context.Context) ([]model.ProxyGroupSummary, error) {
	cctx, cancel := m.c(ctx)
	defer cancel()
	cursor, err := m.collection().Find(cctx, bson.M{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = cursor.Close(cctx) }()
	var docs []model.ProxyDocument
	if err := cursor.All(cctx, &docs); err != nil {
		return nil, err
	}
	type key struct{ c, g string }
	counts := map[key]*model.ProxyGroupSummary{}
	schemes := map[key]map[string]bool{}
	for _, d := range docs {
		k := key{effectiveCountry(&d), model.NormalizeProxyGroup(d.Group)}
		s := counts[k]
		if s == nil {
			s = &model.ProxyGroupSummary{Country: k.c, Group: k.g}
			counts[k] = s
			schemes[k] = map[string]bool{}
		}
		s.Total++
		if d.Enabled {
			s.Enabled++
		}
		switch d.Status {
		case model.ProxyStatusAvailable:
			s.Available++
		case model.ProxyStatusUsed:
			s.Used++
		case model.ProxyStatusQuarantined:
			s.Quarantined++
		}
		schemes[k][model.NormalizeProxyScheme(d.Scheme)] = true
	}
	keys := make([]key, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].c != keys[j].c {
			return keys[i].c < keys[j].c
		}
		return keys[i].g < keys[j].g
	})
	out := make([]model.ProxyGroupSummary, 0, len(keys))
	for _, k := range keys {
		s := counts[k]
		s.Schemes = sortedKeys(schemes[k])
		out = append(out, *s)
	}
	return out, nil
}

func (m *mongoProxyStore) UpdateGroup(ctx context.Context, country, group string, newCountry, newGroup *string, enabled *bool) (int, int, error) {
	cctx, cancel := m.c(ctx)
	defer cancel()
	cursor, err := m.collection().Find(cctx, bson.M{"group": model.NormalizeProxyGroup(group)})
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = cursor.Close(cctx) }()
	var docs []model.ProxyDocument
	if err := cursor.All(cctx, &docs); err != nil {
		return 0, 0, err
	}
	matched, modified := 0, 0
	for _, d := range docs {
		if !proxyDocMatchesCountry(&d, country) {
			continue
		}
		matched++
		update := bson.M{}
		if newCountry != nil {
			update["country"] = model.NormalizeCountryCode(*newCountry)
		}
		if newGroup != nil {
			update["group"] = model.NormalizeProxyGroup(*newGroup)
		}
		if enabled != nil {
			update["enabled"] = *enabled
		}
		if len(update) > 0 {
			if _, err := m.collection().UpdateOne(cctx, bson.M{"_id": d.ID}, bson.M{"$set": update}); err == nil {
				modified++
			}
		}
	}
	return matched, modified, nil
}

func (m *mongoProxyStore) DeleteGroup(ctx context.Context, country, group string) (int, error) {
	cctx, cancel := m.c(ctx)
	defer cancel()
	cursor, err := m.collection().Find(cctx, bson.M{"group": model.NormalizeProxyGroup(group)})
	if err != nil {
		return 0, err
	}
	defer func() { _ = cursor.Close(cctx) }()
	var docs []model.ProxyDocument
	if err := cursor.All(cctx, &docs); err != nil {
		return 0, err
	}
	var ids []string
	for _, d := range docs {
		if proxyDocMatchesCountry(&d, country) {
			ids = append(ids, d.ID)
		}
	}
	if len(ids) == 0 {
		return 0, nil
	}
	res, err := m.collection().DeleteMany(cctx, bson.M{"_id": bson.M{"$in": ids}})
	if err != nil {
		return 0, err
	}
	return int(res.DeletedCount), nil
}

func (m *mongoProxyStore) RestoreUsed(ctx context.Context, country, group *string) (int, error) {
	cctx, cancel := m.c(ctx)
	defer cancel()
	cursor, err := m.collection().Find(cctx, bson.M{"status": model.ProxyStatusUsed})
	if err != nil {
		return 0, err
	}
	defer func() { _ = cursor.Close(cctx) }()
	var docs []model.ProxyDocument
	if err := cursor.All(cctx, &docs); err != nil {
		return 0, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	n := 0
	for _, d := range docs {
		if country != nil && *country != "" && !proxyDocMatchesCountry(&d, *country) {
			continue
		}
		if group != nil && *group != "" && model.NormalizeProxyGroup(d.Group) != model.NormalizeProxyGroup(*group) {
			continue
		}
		_, err := m.collection().UpdateOne(cctx, bson.M{"_id": d.ID},
			bson.M{"$set": bson.M{"status": model.ProxyStatusAvailable, "usedAt": nil, "statusUpdatedAt": now}})
		if err == nil {
			n++
		}
	}
	return n, nil
}

func (m *mongoProxyStore) AllEligibleProxyCandidates(ctx context.Context, country string) ([]model.ProxyLease, error) {
	cctx, cancel := m.c(ctx)
	defer cancel()
	cursor, err := m.collection().Find(cctx, bson.M{"enabled": true, "status": model.ProxyStatusAvailable})
	if err != nil {
		return nil, err
	}
	defer func() { _ = cursor.Close(cctx) }()
	var docs []model.ProxyDocument
	if err := cursor.All(cctx, &docs); err != nil {
		return nil, err
	}
	var matched []model.ProxyDocument
	for _, d := range docs {
		if country != "" && !proxyDocMatchesCountry(&d, country) {
			continue
		}
		matched = append(matched, d)
	}
	sort.SliceStable(matched, func(i, j int) bool {
		ci, cj := effectiveCountry(&matched[i]), effectiveCountry(&matched[j])
		if ci != cj {
			return ci < cj
		}
		if matched[i].CreatedAt != matched[j].CreatedAt {
			return matched[i].CreatedAt < matched[j].CreatedAt
		}
		return matched[i].ID < matched[j].ID
	})
	out := make([]model.ProxyLease, 0, len(matched))
	for _, d := range matched {
		out = append(out, toLease(&d))
	}
	return out, nil
}

func (m *mongoProxyStore) CountEligible(ctx context.Context, country, group string) (int, error) {
	if group == model.LocalProxyGroup {
		return 10000, nil
	}
	cctx, cancel := m.c(ctx)
	defer cancel()
	cursor, err := m.collection().Find(cctx, bson.M{"enabled": true, "status": model.ProxyStatusAvailable})
	if err != nil {
		return 0, err
	}
	defer func() { _ = cursor.Close(cctx) }()
	var docs []model.ProxyDocument
	if err := cursor.All(cctx, &docs); err != nil {
		return 0, err
	}
	n := 0
	for _, d := range docs {
		if country != "" && !proxyDocMatchesCountry(&d, country) {
			continue
		}
		if group != "" && model.NormalizeProxyGroup(d.Group) != model.NormalizeProxyGroup(group) {
			continue
		}
		n++
	}
	return n, nil
}

func (m *mongoProxyStore) AcquireProxy(ctx context.Context, owner string, excluded map[string]bool, leaseSeconds int, country, group string) (*model.ProxyLease, error) {
	if group == model.LocalProxyGroup {
		return &model.ProxyLease{
			ID:      model.LocalProxyIDPrefix + owner,
			Host:    "127.0.0.1",
			Port:    7890,
			Country: strings.ToUpper(country),
			Group:   model.LocalProxyGroup,
			Scheme:  model.ProxySchemeHTTP,
		}, nil
	}
	cctx, cancel := m.c(ctx)
	defer cancel()
	// 并发竞争重试:多个 worker 会同时选中同一条 best,只有一个 FindOneAndUpdate
	// 成功,其余 isNoDocuments → 不能返回 nil(那会被当「无可用通道」判死),
	// 要重查重选下一条。最多重试 8 次(对齐代理池规模,足够错开竞争)。
	for attempt := 0; attempt < 8; attempt++ {
		lease, found, err := m.tryAcquireOnce(cctx, owner, excluded, leaseSeconds, country, group)
		if err != nil {
			return nil, err
		}
		if found {
			return lease, nil // 含 lease==nil(真无可用) —— 见 tryAcquireOnce 语义
		}
		// found=false = best 被并发抢走,重试选下一条
	}
	return nil, nil
}

// tryAcquireOnce 选一条最优代理并原子占租。
// 返回 (lease, found, err):
//   - found=true  + lease 非空 = 抢到;
//   - found=true  + lease==nil = 真无可用通道(池空/全被占且过了重试);
//   - found=false = best 被并发抢走(isNoDocuments),调用方应重试。
func (m *mongoProxyStore) tryAcquireOnce(cctx context.Context, owner string, excluded map[string]bool, leaseSeconds int, country, group string) (*model.ProxyLease, bool, error) {
	now := time.Now().UTC()
	leaseUntil := now.Add(time.Duration(leaseSeconds) * time.Second).Format(time.RFC3339Nano)
	cursor, err := m.collection().Find(cctx, bson.M{"enabled": true, "status": model.ProxyStatusAvailable})
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = cursor.Close(cctx) }()
	var docs []model.ProxyDocument
	if err := cursor.All(cctx, &docs); err != nil {
		return nil, false, err
	}
	var best *model.ProxyDocument
	for i := range docs {
		d := &docs[i]
		if excluded != nil && excluded[d.ID] {
			continue
		}
		if country != "" && !proxyDocMatchesCountry(d, country) {
			continue
		}
		if group != "" && model.NormalizeProxyGroup(d.Group) != model.NormalizeProxyGroup(group) {
			continue
		}
		if d.LeaseOwner != "" && d.LeaseUntil != nil && parseTime(*d.LeaseUntil).After(now) {
			continue
		}
		if best == nil {
			best = d
			continue
		}
		if d.ActiveLeaseCount < best.ActiveLeaseCount {
			best = d
		} else if d.ActiveLeaseCount == best.ActiveLeaseCount {
			if d.RandomScore > best.RandomScore {
				best = d
			} else if d.RandomScore == best.RandomScore && d.ID < best.ID {
				best = d
			}
		}
	}
	if best == nil {
		return nil, true, nil // 池里真没有可用通道
	}
	occupancyFilter := bson.M{
		"_id": best.ID,
		"$or": []bson.M{
			{"leaseOwner": ""},
			{"leaseUntil": bson.M{"$lt": now.Format(time.RFC3339Nano)}},
			{"leaseUntil": nil},
		},
	}
	update := bson.M{
		"$set": bson.M{
			"leaseOwner":     owner,
			"leaseUntil":     leaseUntil,
			"lastSelectedAt": now.Format(time.RFC3339Nano),
		},
		"$addToSet": bson.M{"activeLeaseOwners": owner},
		"$inc":      bson.M{"activeLeaseCount": 1},
	}
	var result model.ProxyDocument
	opts := options.FindOneAndUpdate().SetReturnDocument(options.After)
	err = m.collection().FindOneAndUpdate(cctx, occupancyFilter, update, opts).Decode(&result)
	if err != nil {
		if isNoDocuments(err) {
			return nil, false, nil // best 被并发抢走,调用方重试
		}
		return nil, false, err
	}
	lease := toLease(&result)
	return &lease, true, nil
}

func (m *mongoProxyStore) AcquireProxyByID(ctx context.Context, proxyID, owner string, leaseSeconds int) (*model.ProxyLease, error) {
	cctx, cancel := m.c(ctx)
	defer cancel()
	now := time.Now().UTC()
	filter := bson.M{"_id": proxyID, "enabled": true, "status": model.ProxyStatusAvailable}
	update := bson.M{
		"$set":      bson.M{"lastSelectedAt": now.Format(time.RFC3339Nano)},
		"$addToSet": bson.M{"activeLeaseOwners": owner},
		"$inc":      bson.M{"activeLeaseCount": 1},
	}
	var result model.ProxyDocument
	opts := options.FindOneAndUpdate().SetReturnDocument(options.After)
	err := m.collection().FindOneAndUpdate(cctx, filter, update, opts).Decode(&result)
	if err != nil {
		if isNoDocuments(err) {
			return nil, nil
		}
		return nil, err
	}
	lease := toLease(&result)
	return &lease, nil
}

func (m *mongoProxyStore) ReleaseProxy(ctx context.Context, proxyID, owner string) error {
	if strings.HasPrefix(proxyID, model.LocalProxyIDPrefix) {
		return nil
	}
	cctx, cancel := m.c(ctx)
	defer cancel()
	_, err := m.collection().UpdateOne(cctx,
		bson.M{"_id": proxyID, "activeLeaseOwners": owner},
		bson.M{
			"$pull": bson.M{"activeLeaseOwners": owner},
			"$inc":  bson.M{"activeLeaseCount": -1},
			"$set":  bson.M{"leaseOwner": "", "leaseUntil": nil},
		})
	return err
}

func (m *mongoProxyStore) ConsumeProxy(ctx context.Context, proxyID, owner string) error {
	if strings.HasPrefix(proxyID, model.LocalProxyIDPrefix) {
		return nil
	}
	cctx, cancel := m.c(ctx)
	defer cancel()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := m.collection().UpdateOne(cctx,
		bson.M{"_id": proxyID, "activeLeaseOwners": owner},
		bson.M{
			"$set":  bson.M{"status": model.ProxyStatusUsed, "usedAt": now, "leaseOwner": "", "leaseUntil": nil},
			"$pull": bson.M{"activeLeaseOwners": owner},
			"$inc":  bson.M{"activeLeaseCount": -1},
		})
	return err
}

func (m *mongoProxyStore) HeartbeatProxy(ctx context.Context, proxyID, owner string) (bool, error) {
	if strings.HasPrefix(proxyID, model.LocalProxyIDPrefix) {
		return true, nil
	}
	cctx, cancel := m.c(ctx)
	defer cancel()
	count, err := m.collection().CountDocuments(cctx, bson.M{"_id": proxyID, "activeLeaseOwners": owner})
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (m *mongoProxyStore) ReleaseProxyOwner(ctx context.Context, owner string) (int, error) {
	cctx, cancel := m.c(ctx)
	defer cancel()
	cursor, err := m.collection().Find(cctx, bson.M{
		"$or": []bson.M{{"leaseOwner": owner}, {"activeLeaseOwners": owner}},
	})
	if err != nil {
		return 0, err
	}
	defer func() { _ = cursor.Close(cctx) }()
	var docs []model.ProxyDocument
	if err := cursor.All(cctx, &docs); err != nil {
		return 0, err
	}
	n := 0
	for _, d := range docs {
		update := bson.M{
			"$pull": bson.M{"activeLeaseOwners": owner},
			"$set":  bson.M{"leaseOwner": "", "leaseUntil": nil},
		}
		if contains(d.ActiveLeaseOwners, owner) {
			update["$inc"] = bson.M{"activeLeaseCount": -1}
		}
		if _, err := m.collection().UpdateOne(cctx, bson.M{"_id": d.ID}, update); err == nil {
			n++
		}
	}
	return n, nil
}

func (m *mongoProxyStore) ProxyDocumentsForTest(ctx context.Context, country, group string, limit *int) ([]model.ProxyDocument, error) {
	cctx, cancel := m.c(ctx)
	defer cancel()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	filter := bson.M{
		"$or": []bson.M{
			{"activeLeaseCount": 0},
			{"activeLeaseCount": bson.M{"$exists": false}},
		},
		"leaseUntil": bson.M{"$not": bson.M{"$gt": now}},
	}
	cursor, err := m.collection().Find(cctx, filter)
	if err != nil {
		return nil, err
	}
	defer func() { _ = cursor.Close(cctx) }()
	var docs []model.ProxyDocument
	if err := cursor.All(cctx, &docs); err != nil {
		return nil, err
	}
	var matched []model.ProxyDocument
	for _, d := range docs {
		if country != "" && !proxyDocMatchesCountry(&d, country) {
			continue
		}
		if group != "" && model.NormalizeProxyGroup(d.Group) != model.NormalizeProxyGroup(group) {
			continue
		}
		matched = append(matched, d)
	}
	sort.SliceStable(matched, func(i, j int) bool {
		return lastCheckedLess(matched[i].LastCheckedAt, matched[j].LastCheckedAt)
	})
	if limit != nil && *limit > 0 && len(matched) > *limit {
		matched = matched[:*limit]
	}
	return matched, nil
}

func (m *mongoProxyStore) RecordProxyTest(ctx context.Context, id string, available bool, latencyMs *int, country, timezoneID string, timezoneOffsetSec *int) error {
	cctx, cancel := m.c(ctx)
	defer cancel()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if available {
		update := bson.M{
			"lastCheckedAt":       now,
			"latencyMs":           latencyMs,
			"status":              model.ProxyStatusAvailable,
			"consecutiveFailures": 0,
		}
		if country != "" {
			update["country"] = model.NormalizeCountryCode(country)
		}
		if timezoneID != "" {
			update["timezoneId"] = timezoneID
		}
		if timezoneOffsetSec != nil {
			update["timezoneOffsetSec"] = timezoneOffsetSec
		}
		_, err := m.collection().UpdateOne(cctx, bson.M{"_id": id}, bson.M{"$set": update})
		return err
	}
	var doc model.ProxyDocument
	err := m.collection().FindOne(cctx, bson.M{"_id": id}).Decode(&doc)
	if err != nil {
		if isNoDocuments(err) {
			return nil
		}
		return err
	}
	doc.ConsecutiveFailures++
	var newStatus string
	if doc.ConsecutiveFailures >= 3 {
		newStatus = model.ProxyStatusQuarantined
	} else if doc.Status == model.ProxyStatusAvailable {
		newStatus = model.ProxyStatusAvailable
	} else {
		newStatus = model.ProxyStatusUnknown
	}
	_, err = m.collection().UpdateOne(cctx, bson.M{"_id": id},
		bson.M{"$set": bson.M{
			"lastCheckedAt":       now,
			"latencyMs":           nil,
			"consecutiveFailures": doc.ConsecutiveFailures,
			"status":              newStatus,
		}})
	return err
}

func (m *mongoProxyStore) Count(ctx context.Context, pred func(model.ProxyDocument) bool) (int, error) {
	cctx, cancel := m.c(ctx)
	defer cancel()
	if pred == nil {
		count, err := m.collection().CountDocuments(cctx, bson.M{})
		return int(count), err
	}
	cursor, err := m.collection().Find(cctx, bson.M{})
	if err != nil {
		return 0, err
	}
	defer func() { _ = cursor.Close(cctx) }()
	var docs []model.ProxyDocument
	if err := cursor.All(cctx, &docs); err != nil {
		return 0, err
	}
	n := 0
	for _, d := range docs {
		if pred(d) {
			n++
		}
	}
	return n, nil
}
