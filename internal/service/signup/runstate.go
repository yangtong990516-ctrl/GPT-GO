// runstate.go 注册批次的运行态（RunState）：内存实时维护 + 可选 mongo 持久化。
//
// 对齐 codex-auto resource_models.RunState（requested/pending/processed/succeeded/failed
// + 成功率），但按用户决策采用「内存为主、结束后可选落库」的混合模式：
//
//   - 注册进行中：进度实时维护在内存（RunTracker），runs API 可随时查询当前批次
//     进度（pending/processed/successRate 等）——零 DB 开销、零延迟。
//   - 批次结束后：用户可【手动选择】把该批次 RunState 一条 mongo insert 持久化
//     （runs collection）——只是「内存结构 → BSON 一次 insert」，功能与落库兼得。
//
// 并发安全：RunBatch 多 worker 并发回调 Progress，RunTracker 全部读写持锁。
package signup

import (
	"sync"
	"time"
)

// RunKind 标记批次类型（对齐 RunKind，目前仅 protocol 真实注册）。
type RunKind = string

const (
	RunKindProtocol RunKind = "protocol" // 纯协议注册
	RunKindMock     RunKind = "mock"     // mock 批次（占位，对齐 codex）
)

// RunStatus 批次状态（对齐 RunStatus）。
type RunStatus = string

const (
	RunStatusRunning   RunStatus = "running"
	RunStatusSucceeded RunStatus = "succeeded" // 全部成功
	RunStatusPartial   RunStatus = "partial_success"
	RunStatusFailed    RunStatus = "failed"
	RunStatusCancelled RunStatus = "cancelled"
)

// RunState 是一个注册批次的运行态快照（对齐 resource_models.RunState）。
type RunState struct {
	RunID     string    `json:"runId"`
	Kind      RunKind   `json:"kind"`
	Status    RunStatus `json:"status"`
	Requested int       `json:"requested"` // 目标数量（= Count，目标）
	Pending   int       `json:"pending"`   // 待处理（已派发未完成 + 未派发）
	Processed int       `json:"processed"` // 已处理（succeeded+failed+cancelled）
	Succeeded int       `json:"succeeded"` // 成功（OK）
	Failed    int       `json:"failed"`    // 失败
	Cancelled int       `json:"cancelled"` // 取消
	// SuccessRate 成功率（succeeded/processed*100，processed=0 时 0；对齐 codex 取 1 位小数）。
	SuccessRate float64 `json:"successRate"`

	RegistrationCountry    string     `json:"registrationCountry,omitempty"`
	RegistrationProxyGroup string     `json:"registrationProxyGroup,omitempty"`
	EmailSource            string     `json:"emailSource,omitempty"`
	WorkerCount            int        `json:"workerCount"` // 并发 worker 数（= Concurrency）
	StartedAt              time.Time  `json:"startedAt"`
	UpdatedAt              time.Time  `json:"updatedAt"`
	FinishedAt             *time.Time `json:"finishedAt,omitempty"`
	CancelRequested        bool       `json:"cancelRequested"`
}

// RunTracker 实时跟踪一个批次的进度（内存态、线程安全）。
//
// 生命周期：NewRunTracker（批次开始）→ 每账号完成调 Track（processed/succeeded/failed
// +1，pending -1）→ 批次结束调 Finish（定 status/successRate/finishedAt）。
// Snapshot 随时可取当前一致快照（runs API 查询用）。
type RunTracker struct {
	mu sync.Mutex
	st RunState
}

// NewRunTracker 创建一个批次跟踪器。
//
//	requested: 目标数量（Count）；concurrency: worker 数；country/group/emailSource 记入快照。
func NewRunTracker(runID string, kind RunKind, requested, concurrency int, country, group, emailSource string) *RunTracker {
	now := time.Now().UTC()
	return &RunTracker{st: RunState{
		RunID:                  runID,
		Kind:                   kind,
		Status:                 RunStatusRunning,
		Requested:              requested,
		Pending:                requested, // 初始全部待处理
		Processed:              0,
		RegistrationCountry:    country,
		RegistrationProxyGroup: group,
		EmailSource:            emailSource,
		WorkerCount:            concurrency,
		StartedAt:              now,
		UpdatedAt:              now,
	}}
}

// Track 记录一个账号的完成结果（对齐 BatchItemResult 三分支）。
// succeeded: OK；failed: 非 OK 非取消；cancelled: 取消。
func (t *RunTracker) Track(ok, cancelled bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.st.Pending > 0 {
		t.st.Pending--
	}
	t.st.Processed++
	switch {
	case cancelled:
		t.st.Cancelled++
	case ok:
		t.st.Succeeded++
	default:
		t.st.Failed++
	}
	t.st.UpdatedAt = time.Now().UTC()
}

// Finish 标记批次结束，定终态 status / successRate / finishedAt。
// cancelled 为 true 表示被主动取消（对齐 cancel_event）。
func (t *RunTracker) Finish(cancelled bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now().UTC()
	t.st.FinishedAt = &now
	t.st.UpdatedAt = now
	t.st.CancelRequested = cancelled
	// 终态：全取消→cancelled；全成功→succeeded；有成功有失败→partial；全失败→failed。
	switch {
	case cancelled:
		t.st.Status = RunStatusCancelled
	case t.st.Succeeded == t.st.Requested && t.st.Requested > 0:
		t.st.Status = RunStatusSucceeded
	case t.st.Succeeded > 0:
		t.st.Status = RunStatusPartial
	default:
		t.st.Status = RunStatusFailed
	}
	t.st.SuccessRate = successRate(t.st.Succeeded, t.st.Processed)
	t.st.Pending = 0
}

// Snapshot 取当前一致快照（返回拷贝，外部改动不影响内部）。
func (t *RunTracker) Snapshot() RunState {
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.st
	// 运行中也算实时成功率（processed>0 时）。
	if st.FinishedAt == nil {
		st.SuccessRate = successRate(st.Succeeded, st.Processed)
	}
	return st
}

// successRate 计算成功率（succeeded/processed*100，1 位小数；processed=0 → 0）。
func successRate(succeeded, processed int) float64 {
	if processed <= 0 {
		return 0
	}
	return round1(float64(succeeded) * 100 / float64(processed))
}

// round1 保留 1 位小数（对齐 codex round(...,1)）。
func round1(f float64) float64 {
	return float64(int(f*10+0.5)) / 10
}
