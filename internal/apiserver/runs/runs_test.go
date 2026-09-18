// runs_test.go 批量注册触发点（POST /api/runs）的 mock 测试（不联网）。
//
// 核心验证【A 方案】：每次发起批量注册时，handler 实时读 ExecutionSettings
// （并发数/同出口IP上限/单号超时）并注入 BatchParams + 拨号器——证明配置栏改动即时生效。
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

// mockSettings 返回固定 ExecutionSettings（验证它被实时读取并注入）。
type mockSettings struct {
	got model.ExecutionSettings
}

func (m mockSettings) Get(ctx context.Context) (model.ExecutionSettings, error) {
	return m.got, nil
}

// mockRunner 捕获收到的 BatchParams（验证 settings 注入是否正确）。
type mockRunner struct {
	gotParams signup.BatchParams
	calls     int
}

func (m *mockRunner) RunBatch(ctx context.Context, params signup.BatchParams) (*signup.BatchResult, error) {
	m.calls++
	m.gotParams = params
	return &signup.BatchResult{OK: params.Count}, nil
}

// setupRouter 起一个不依赖 server.go 的最小 gin 路由（只挂 runs handler）。
func setupRouter(h *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h.Register(r.Group("/api/runs"))
	return r
}

// postJSON 发 POST /api/runs 并解析响应。
func postJSON(t *testing.T, r *gin.Engine, body string) (int, createRunResponse) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/runs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var resp createRunResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return w.Code, resp
}

// TestCreateRunInjectsSettings 验证 A 方案：settings 的并发/超时/出口IP上限被实时注入 BatchParams。
func TestCreateRunInjectsSettings(t *testing.T) {
	settings := mockSettings{got: model.ExecutionSettings{
		Concurrency:               5,
		TaskTimeoutSeconds:        180,
		MaxRegistrationsPerExitIP: 3,
	}}
	runner := &mockRunner{}
	// newDialer 记录收到的 exitIPCap（验证 settings.MaxRegistrationsPerExitIP 传入）。
	var gotCap int
	h := NewHandler(settings, func(cap int) *signup.SessionDialer {
		gotCap = cap
		return nil // 测试不需要真拨号器
	}, runner)
	r := setupRouter(h)

	code, resp := postJSON(t, r, `{"count": 2, "country": "VN", "emailSource": "mailcode"}`)
	if code != http.StatusOK {
		t.Fatalf("应 200, got %d", code)
	}
	if runner.calls != 1 {
		t.Fatalf("RunBatch 应调 1 次, got %d", runner.calls)
	}
	// 验证 settings 三个字段被注入 BatchParams。
	p := runner.gotParams
	if p.Concurrency != 5 {
		t.Fatalf("Concurrency 应注入 5, got %d", p.Concurrency)
	}
	if p.TaskTimeoutSeconds != 180 {
		t.Fatalf("TaskTimeoutSeconds 应注入 180, got %d", p.TaskTimeoutSeconds)
	}
	if gotCap != 3 {
		t.Fatalf("newDialer 应收 exitIPCap=3（settings.MaxRegistrationsPerExitIP）, got %d", gotCap)
	}
	// 验证响应回显 applied（证明实时注入）。
	if resp.Applied.Concurrency != 5 || resp.Applied.TaskTimeoutSeconds != 180 || resp.Applied.MaxRegistrationsPerExit != 3 {
		t.Fatalf("applied 回显错误: %+v", resp.Applied)
	}
	if resp.OK != 2 || resp.Total != 2 {
		t.Fatalf("结果汇总错误: %+v", resp)
	}
}

// TestCreateRunValidation 验证请求体校验：count 缺失/非法 → 400。
func TestCreateRunValidation(t *testing.T) {
	runner := &mockRunner{}
	h := NewHandler(mockSettings{}, nil, runner)
	r := setupRouter(h)

	// 缺 count。
	if code, _ := postJSON(t, r, `{"country": "VN"}`); code != http.StatusBadRequest {
		t.Fatalf("缺 count 应 400, got %d", code)
	}
	// count=0（违反 min=1）。
	if code, _ := postJSON(t, r, `{"count": 0}`); code != http.StatusBadRequest {
		t.Fatalf("count=0 应 400, got %d", code)
	}
	if runner.calls != 0 {
		t.Fatalf("校验失败不应触发 RunBatch, got %d 次", runner.calls)
	}
}

// TestCreateRunUnavailable 验证注册引擎未装配（runner=nil）→ 503。
func TestCreateRunUnavailable(t *testing.T) {
	h := NewHandler(mockSettings{}, nil, nil)
	r := setupRouter(h)
	if code, _ := postJSON(t, r, `{"count": 1}`); code != http.StatusServiceUnavailable {
		t.Fatalf("引擎未装配应 503, got %d", code)
	}
}

// TestCreateRunAutoRunID 验证缺省自动生成 runId。
func TestCreateRunAutoRunID(t *testing.T) {
	runner := &mockRunner{}
	h := NewHandler(mockSettings{got: model.DefaultExecutionSettings()}, nil, runner)
	r := setupRouter(h)
	code, resp := postJSON(t, r, `{"count": 1}`)
	if code != http.StatusOK {
		t.Fatalf("应 200, got %d", code)
	}
	if resp.RunID == "" || runner.gotParams.RunID == "" {
		t.Fatal("缺省应自动生成 runId")
	}
}

// TestCreateRunRequestPassthrough 验证请求体参数（country/group/emailSource）透传到 RunParams。
func TestCreateRunRequestPassthrough(t *testing.T) {
	runner := &mockRunner{}
	h := NewHandler(mockSettings{got: model.DefaultExecutionSettings()}, nil, runner)
	r := setupRouter(h)
	postJSON(t, r, `{"count": 1, "country": "US", "group": "g1", "emailSource": "jsoncode", "otpTimeout": 90}`)
	p := runner.gotParams
	if p.Country != "US" || p.Group != "g1" || p.EmailSource != "jsoncode" || p.OTPTimeout != 90 {
		t.Fatalf("请求参数未透传: %+v", p.RunParams)
	}
}
