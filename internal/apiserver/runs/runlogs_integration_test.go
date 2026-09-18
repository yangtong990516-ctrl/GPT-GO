// runlogs_integration_test.go 「注册 → 运行日志」端到端联动测试：
// POST /api/runs 触发批量注册（mock runner 模拟 service 写日志）→ runlogs REST
// 能读到完整日志链（run_created → 账号事件 → run_completed）。
//
// 验证 runs handler 正确把 RunLogger 注入 BatchParams（Log + Step + Progress），
// 且批次终态写 run_completed。
package runs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-go/internal/apiserver/runlogs"
	"gpt-go/internal/model"
	"gpt-go/internal/service/signup"
)

// loggingRunner 模拟真实 service：用注入的 Log + Step + Progress 写一轮日志。
type loggingRunner struct{}

func (loggingRunner) RunBatch(ctx context.Context, params signup.BatchParams) (*signup.BatchResult, error) {
	log := params.RunParams.Log
	// 模拟 service 的账号级节点（对齐真实 Run 的事件序列）。
	if params.Step != nil {
		params.Step("步骤1/5 租用代理 ...")
	}
	log.Emit(signup.LogInfo, "proxy_acquired", "租用代理成功", "", map[string]any{"country": "US"})
	log.Emit(signup.LogInfo, "email_reserved", "预留邮箱成功", "a@x.com", nil)
	log.Emit(signup.LogSuccess, "account_succeeded", "注册成功", "a@x.com", map[string]any{"accountId": "acc1"})
	// 模拟一个失败账号（走 Progress）。
	if params.Progress != nil {
		params.Progress(signup.BatchItemResult{Index: 0, OK: true, Email: "a@x.com"})
		params.Progress(signup.BatchItemResult{Index: 1, OK: false, Code: "protocol_failed", Error: "otp 超时", Email: "b@x.com"})
	}
	return &signup.BatchResult{OK: 1, Failed: 1}, nil
}

// TestRunProducesReadableLogs 验证：触发注册后，runlogs 能读到完整日志链。
func TestRunProducesReadableLogs(t *testing.T) {
	hub := signup.NewRunLogHub()
	h := NewHandler(mockSettings{got: model.ExecutionSettings{
		Concurrency: 2, TaskTimeoutSeconds: 60, MaxRegistrationsPerExitIP: 1,
	}}, nil, loggingRunner{}).WithLogHub(hub)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	h.Register(r.Group("/api/runs"))
	runlogs.NewHandler(hub).Register(r.Group("/api/run-logs"))

	// 1) 触发注册。
	req := httptest.NewRequest(http.MethodPost, "/api/runs", strings.NewReader(`{"count": 2}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("触发应 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp createRunResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	runID := resp.RunID
	if runID == "" {
		t.Fatal("应返回 runId")
	}

	// 2) runlogs 读历史：应含完整事件链。
	req2 := httptest.NewRequest(http.MethodGet, "/api/run-logs/runs/"+runID, nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("读日志应 200, got %d", w2.Code)
	}
	var file struct {
		Terminal bool                 `json:"terminal"`
		Entries  []signup.RunLogEntry `json:"entries"`
	}
	if err := json.Unmarshal(w2.Body.Bytes(), &file); err != nil {
		t.Fatalf("解析日志: %v body=%s", err, w2.Body.String())
	}

	// 收集事件名（按序）。
	events := make([]string, 0, len(file.Entries))
	for _, e := range file.Entries {
		events = append(events, e.Event)
	}
	// 聚合行为(新):里程碑事件直通,高频过程事件(租代理/预留邮箱/步骤/成功)被
	// 聚合器合并为 progress 汇总(防大批次刷屏)。故:
	//   - 必达的里程碑:run_created / account_failed / run_completed 直通可见;
	//   - 过程事件不再逐条独立出现,而是聚成 progress(details 含各事件计数)。
	milestones := []string{"run_created", "account_failed", "run_completed"}
	for _, we := range milestones {
		if !contains(events, we) {
			t.Fatalf("缺少里程碑事件 %s, got %v", we, events)
		}
	}
	// 终态：run_completed 是最后一个，terminal=true。
	if events[len(events)-1] != "run_completed" {
		t.Fatalf("最后事件应 run_completed, got %s", events[len(events)-1])
	}
	if !file.Terminal {
		t.Fatal("run_completed 后 terminal 应 true")
	}
	// 聚合:应有一条 progress(终态前 agg.Close 刷出累计的过程事件),且其 details
	// 记录被合并的事件计数(如 proxy_acquired / protocol_step / email_reserved)。
	foundProgress := false
	for _, e := range file.Entries {
		if e.Event != "progress" {
			continue
		}
		foundProgress = true
		if e.Details == nil || len(e.Details) == 0 {
			t.Fatal("progress 事件应带聚合计数 details")
		}
	}
	if !foundProgress {
		t.Fatalf("应有聚合 progress 事件(过程事件合并), got %v", events)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// TestDuplicateRunIDGetsSuffix 验证 M2 修复:显式传相同 runId 触发两次,第二次应
// 自动追加随机后缀(两批次日志不合并到同一 buffer)。
func TestDuplicateRunIDGetsSuffix(t *testing.T) {
	hub := signup.NewRunLogHub()
	h := NewHandler(mockSettings{got: model.ExecutionSettings{
		Concurrency: 2, TaskTimeoutSeconds: 60, MaxRegistrationsPerExitIP: 1,
	}}, nil, loggingRunner{}).WithLogHub(hub)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	h.Register(r.Group("/api/runs"))

	// 第一次:显式 runId=dup-test。
	body := `{"count": 1, "runId": "dup-test"}`
	req1 := httptest.NewRequest(http.MethodPost, "/api/runs", strings.NewReader(body))
	req1.Header.Set("Content-Type", "application/json")
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)
	var resp1 createRunResponse
	_ = json.Unmarshal(w1.Body.Bytes(), &resp1)
	if resp1.RunID != "dup-test" {
		t.Fatalf("第一次应用原 runId, got %q", resp1.RunID)
	}

	// 第二次:同样 runId=dup-test → 应加后缀(不等于第一次)。
	req2 := httptest.NewRequest(http.MethodPost, "/api/runs", strings.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	var resp2 createRunResponse
	_ = json.Unmarshal(w2.Body.Bytes(), &resp2)
	if resp2.RunID == "dup-test" {
		t.Errorf("重复 runId 应加随机后缀, got %q(与第一次相同)", resp2.RunID)
	}
	if resp2.RunID == resp1.RunID {
		t.Errorf("两次批次 runId 不应相同(日志会合并), got %q", resp2.RunID)
	}
	// 两个 run 都应有独立日志 buffer。
	if _, ok := hub.Summary(resp1.RunID); !ok {
		t.Errorf("批次1 %q 应有独立日志 buffer", resp1.RunID)
	}
	if _, ok := hub.Summary(resp2.RunID); !ok {
		t.Errorf("批次2 %q 应有独立日志 buffer", resp2.RunID)
	}
}
