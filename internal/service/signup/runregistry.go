// runregistry.go 进程级 RunTracker 注册表：管理「当前进行中 + 最近完成」的批次进度。
//
// 用途：
//   - runs 触发批量注册时 Start() 建 tracker，注入 RunBatch 的 Progress 实时更新；
//   - runs 查询 API 用 Get()/List() 取当前/历史批次的 RunState 快照；
//   - 批次结束后 tracker 保留在内存（recent 环形，上限 recentCap），供查询与可选落库；
//     重启即清空（内存态，持久化走 mongo insert，见 RunStore 接口）。
//
// 并发安全：map 读写持锁；tracker 自身也线程安全（见 runstate.go）。
package signup

import (
	"sync"
)

// recentCap 是内存中保留的最近完成批次数上限（防无限增长）。
const recentCap = 64

// RunRegistry 是批次进度的进程内注册表。
type RunRegistry struct {
	mu      sync.RWMutex
	byRunID map[string]*RunTracker
	order   []string // 完成/创建顺序（旧→新），用于裁剪 recent
}

// NewRunRegistry 创建空注册表。
func NewRunRegistry() *RunRegistry {
	return &RunRegistry{byRunID: map[string]*RunTracker{}}
}

// Start 为一个新批次建 tracker 并登记（若 runID 已存在返回旧的——幂等，复触发不丢进度）。
func (r *RunRegistry) Start(runID string, kind RunKind, requested, concurrency int, country, group, emailSource string) *RunTracker {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t, ok := r.byRunID[runID]; ok {
		return t
	}
	t := NewRunTracker(runID, kind, requested, concurrency, country, group, emailSource)
	r.byRunID[runID] = t
	r.order = append(r.order, runID)
	r.trimLocked()
	return t
}

// Get 按 runID 取 tracker（不存在返回 nil）。
func (r *RunRegistry) Get(runID string) *RunTracker {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byRunID[runID]
}

// Snapshot 按 runID 取一致快照；不存在返回 ok=false。
func (r *RunRegistry) Snapshot(runID string) (RunState, bool) {
	t := r.Get(runID)
	if t == nil {
		return RunState{}, false
	}
	return t.Snapshot(), true
}

// List 返回所有批次的快照（新的在前，对齐「最近优先」展示）。
func (r *RunRegistry) List() []RunState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]RunState, 0, len(r.order))
	for i := len(r.order) - 1; i >= 0; i-- {
		if t, ok := r.byRunID[r.order[i]]; ok {
			out = append(out, t.Snapshot())
		}
	}
	return out
}

// Current 返回当前仍在 running 的批次快照（新的在前）。
func (r *RunRegistry) Current() []RunState {
	all := r.List()
	out := make([]RunState, 0, len(all))
	for _, st := range all {
		if st.Status == RunStatusRunning {
			out = append(out, st)
		}
	}
	return out
}

// trimLocked 裁剪：当登记数超过 recentCap 时，移除最旧的【已完成】批次（进行中的不删）。
// 调用方须已持写锁。
func (r *RunRegistry) trimLocked() {
	if len(r.order) <= recentCap {
		return
	}
	kept := r.order[:0]
	removed := 0
	need := len(r.order) - recentCap
	for _, id := range r.order {
		t := r.byRunID[id]
		// 仅删已完成且仍需裁剪的；进行中的一律保留。
		if removed < need && t != nil && t.Snapshot().Status != RunStatusRunning {
			delete(r.byRunID, id)
			removed++
			continue
		}
		kept = append(kept, id)
	}
	r.order = kept
}
