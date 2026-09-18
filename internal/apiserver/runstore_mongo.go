// runstore.go 注册批次 RunState 的 mongo 持久化（runs collection）。
//
// 对齐 codex-auto run_store.py 的 MongoRunStore（runs collection），但按用户决策
// 只实现【批次结束后的一条 insert】——内存态 RunState → BSON insert，不做运行中
// 的增量写（运行中进度在内存，见 signup.RunRegistry）。
//
// 这是项目首个真实 mongo 写入点（此前 store 全 mock）：自带懒连接（首次 insert
// 才连），连接失败返回明确错误（不崩服务）；连接建立后复用。
package apiserver

import (
	"context"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"gpt-go/internal/service/signup"
)

// runsCollection 是批次落库的集合名（对齐 codex runs）。
const runsCollection = "runs"

// RunStore 把批次 RunState 落库到 mongo runs collection。
//
// 实现 apiserver/runs 的 RunStore 接口（InsertRun）。懒连接：New 时不连，
// 首次 InsertRun 才建立连接并 ping；之后复用。Close 释放连接。
type RunStore struct {
	uri      string
	database string

	mu      sync.Mutex
	client  *mongo.Client
	connErr error
}

// NewRunStore 创建批次 mongo 存储（uri/database 来自 config.mongodb）。
// 不立即连接（懒连接，首次 InsertRun 才连）。
func NewRunStore(uri, database string) *RunStore {
	return &RunStore{uri: uri, database: database}
}

// connect 懒建立连接并 ping（幂等；失败后缓存错误，避免每次重试卡住）。
func (s *RunStore) connect(ctx context.Context) (*mongo.Collection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		return s.client.Database(s.database).Collection(runsCollection), nil
	}
	if s.connErr != nil {
		return nil, s.connErr
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	client, err := mongo.Connect(cctx, options.Client().ApplyURI(s.uri))
	if err != nil {
		s.connErr = err
		return nil, err
	}
	if err := client.Ping(cctx, nil); err != nil {
		_ = client.Disconnect(ctx)
		s.connErr = err
		return nil, err
	}
	s.client = client
	return s.client.Database(s.database).Collection(runsCollection), nil
}

// InsertRun 把批次终态 RunState 落库（runs collection 一条 insert）。
//
// 文档结构对齐 codex RunState（runId/kind/status/requested/pending/processed/
// succeeded/failed/cancelled/successRate/startedAt/finishedAt 等）。重复保存同一
// runId 由上层控制（saveRun 已挡「进行中」与「不存在」）；这里直接 insert。
func (s *RunStore) InsertRun(ctx context.Context, st signup.RunState) error {
	coll, err := s.connect(ctx)
	if err != nil {
		return err
	}
	doc := runStateToBSON(st)
	insCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := coll.InsertOne(insCtx, doc); err != nil {
		return err
	}
	return nil
}

// Close 释放 mongo 连接（服务关停时调）。
func (s *RunStore) Close(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		_ = s.client.Disconnect(ctx)
		s.client = nil
	}
}

// runStateToBSON 把 RunState 转 mongo 文档（字段名对齐 codex RunState 的 BSON key）。
func runStateToBSON(st signup.RunState) map[string]any {
	doc := map[string]any{
		"runId":                  st.RunID,
		"kind":                   st.Kind,
		"status":                 st.Status,
		"requested":              st.Requested,
		"pending":                st.Pending,
		"processed":              st.Processed,
		"succeeded":              st.Succeeded,
		"failed":                 st.Failed,
		"cancelled":              st.Cancelled,
		"successRate":            st.SuccessRate,
		"workerCount":            st.WorkerCount,
		"startedAt":              st.StartedAt,
		"updatedAt":              st.UpdatedAt,
		"cancelRequested":        st.CancelRequested,
		"registrationCountry":    st.RegistrationCountry,
		"registrationProxyGroup": st.RegistrationProxyGroup,
		"emailSource":            st.EmailSource,
	}
	if st.FinishedAt != nil {
		doc["finishedAt"] = *st.FinishedAt
	}
	return doc
}
