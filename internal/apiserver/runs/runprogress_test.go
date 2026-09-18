// runprogress_test.go runs 批次进度查询与手动落库的 HTTP 测试（不联网、不依赖真 mongo）。
//
// 验证：触发批次时建内存 tracker（Progress 实时更新）→ GET 查询进度（含
// successRate/pending/processed）→ 结束后 POST /:runId/save 手动落库（mock RunStore）。
package runs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-go/internal/model"
	"gpt-go/internal/service/signup"
)

// progressRunner 模拟 RunBatch：逐账号触发 Progress 回调（3 成功 1 失败）。
type progressRunner struct{}

func (m *progressRunner) RunBatch(ctx context.Context, params signup.BatchParams) (*signup.BatchResult, error) {
	res := &signup.BatchResult{}
	for i := 0; i < params.Count; i++ {
		ok := i != params.Count-1 // 最后一个失败
		item := signup.BatchItemResult{Index: i, OK: ok}
		if ok {
			res.OK++
		} else {
			res.Failed++
		}
		if params.Progress != nil {
			params.Progress(item)
		}
	}
	return res, nil
}

// mockRunStore 捕获落库的 RunState（验证 save 手动落库）。
type mockRunStore struct {
	saved []signup.RunState
	err   error
}

func (m *mockRunStore) InsertRun(ctx context.Context, st signup.RunState) error {
	if m.err != nil {
		return m.err
	}
	m.saved = append(m.saved, st)
	return nil
}

// newProgressHandler 构造带 registry + mockRunStore 的 Handler。
func newProgressHandler(rs RunStore) (*Handler, *signup.RunRegistry) {
	reg := signup.NewRunRegistry()
	h := NewHandler(mockSettings{got: model.DefaultExecutionSettings()}, nil, &progressRunner{}).
		WithRegistry(reg)
	if rs != nil {
		h = h.WithRunStore(rs)
	}
	return h, reg
}

// doJSON 发请求并返回状态码 + body。
func doJSON(t *testing.T, r *gin.Engine, method, path string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

// TestRunProgressTracked 验证：触发批次后进度被实时跟踪，GET /:runId 返回正确 RunState。
func TestRunProgressTracked(t *testing.T) {
	h, _ := newProgressHandler(nil)
	r := setupRouter(h)

	// 触发 4 个的批次。
	code, resp := postJSON(t, r, `{"count": 4, "runId": "run-p1", "emailSource": "remail"}`)
	if code != http.StatusOK {
		t.Fatalf("触发应 200, got %d", code)
	}
	if resp.RunID != "run-p1" {
		t.Fatalf("runId 应 run-p1, got %s", resp.RunID)
	}

	// 查询进度。
	code, body := doJSON(t, r, http.MethodGet, "/api/runs/run-p1")
	if code != http.StatusOK {
		t.Fatalf("查询应 200, got %d (%v)", code, body)
	}
	if body["requested"].(float64) != 4 {
		t.Fatalf("requested 应 4, got %v", body["requested"])
	}
	if body["processed"].(float64) != 4 {
		t.Fatalf("processed 应 4, got %v", body["processed"])
	}
	if body["succeeded"].(float64) != 3 || body["failed"].(float64) != 1 {
		t.Fatalf("succeeded/failed 应 3/1, got %v/%v", body["succeeded"], body["failed"])
	}
	if body["successRate"].(float64) != 75.0 {
		t.Fatalf("successRate 应 75.0, got %v", body["successRate"])
	}
	if body["status"].(string) != signup.RunStatusPartial {
		t.Fatalf("status 应 partial_success, got %v", body["status"])
	}
}

// TestListAndCurrentRuns 验证：GET /api/runs 列表 + /current 仅 running。
func TestListAndCurrentRuns(t *testing.T) {
	h, reg := newProgressHandler(nil)
	r := setupRouter(h)
	// 触发一个完成批次。
	postJSON(t, r, `{"count": 2, "runId": "run-done", "emailSource": "remail"}`)
	// 手动塞一个 running 批次。
	reg.Start("run-live", signup.RunKindProtocol, 3, 1, "", "", "")

	code, body := doJSON(t, r, http.MethodGet, "/api/runs")
	if code != http.StatusOK {
		t.Fatalf("list 应 200, got %d", code)
	}
	runs := body["runs"].([]any)
	if len(runs) != 2 {
		t.Fatalf("应 2 个批次, got %d", len(runs))
	}
	// current 只含 running。
	_, cbody := doJSON(t, r, http.MethodGet, "/api/runs/current")
	cruns := cbody["runs"].([]any)
	if len(cruns) != 1 {
		t.Fatalf("current 应 1 个 running, got %d", len(cruns))
	}
	if cruns[0].(map[string]any)["runId"].(string) != "run-live" {
		t.Fatalf("current 应是 run-live, got %v", cruns[0])
	}
}

// TestSaveRunPersistsToMongo 验证：结束后 POST /:runId/save 把终态落库（mock mongo）。
func TestSaveRunPersistsToMongo(t *testing.T) {
	rs := &mockRunStore{}
	h, _ := newProgressHandler(rs)
	r := setupRouter(h)

	postJSON(t, r, `{"count": 4, "runId": "run-save", "emailSource": "remail"}`)

	code, body := doJSON(t, r, http.MethodPost, "/api/runs/run-save/save")
	if code != http.StatusOK {
		t.Fatalf("save 应 200, got %d (%v)", code, body)
	}
	if body["saved"].(bool) != true {
		t.Fatalf("saved 应 true, got %v", body)
	}
	if len(rs.saved) != 1 {
		t.Fatalf("应落库 1 条, got %d", len(rs.saved))
	}
	st := rs.saved[0]
	if st.RunID != "run-save" || st.Status != signup.RunStatusPartial || st.Succeeded != 3 {
		t.Fatalf("落库 RunState 错误: %+v", st)
	}
}

// TestSaveRunNotFound 验证：不存在的批次 save → 404。
func TestSaveRunNotFound(t *testing.T) {
	rs := &mockRunStore{}
	h, _ := newProgressHandler(rs)
	r := setupRouter(h)
	code, body := doJSON(t, r, http.MethodPost, "/api/runs/nonexistent/save")
	if code != http.StatusNotFound {
		t.Fatalf("不存在批次 save 应 404, got %d", code)
	}
	detail := body["detail"].(map[string]any)
	if detail["code"].(string) != "run_not_found" {
		t.Fatalf("code 应 run_not_found, got %v", detail)
	}
}

// TestSaveRunUnconfigured 验证：未配置 runStore 时 save → 503。
func TestSaveRunUnconfigured(t *testing.T) {
	h, _ := newProgressHandler(nil) // 不注入 runStore
	r := setupRouter(h)
	postJSON(t, r, `{"count": 1, "runId": "run-ns", "emailSource": "remail"}`)
	code, body := doJSON(t, r, http.MethodPost, "/api/runs/run-ns/save")
	if code != http.StatusServiceUnavailable {
		t.Fatalf("未配置 runStore 应 503, got %d", code)
	}
	detail := body["detail"].(map[string]any)
	if detail["code"].(string) != "run_store_unconfigured" {
		t.Fatalf("code 应 run_store_unconfigured, got %v", detail)
	}
}

// TestGetRunNotFound 验证：查询不存在的批次 → 404。
func TestGetRunNotFound(t *testing.T) {
	h, _ := newProgressHandler(nil)
	r := setupRouter(h)
	code, body := doJSON(t, r, http.MethodGet, "/api/runs/ghost")
	if code != http.StatusNotFound {
		t.Fatalf("查询不存在批次应 404, got %d", code)
	}
	detail := body["detail"].(map[string]any)
	if !strings.Contains(detail["code"].(string), "run_not_found") {
		t.Fatalf("code 应含 run_not_found, got %v", detail)
	}
}
