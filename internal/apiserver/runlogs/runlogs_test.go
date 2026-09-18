// runlogs_test.go 运行日志 HTTP 接口测试（REST list/get + SSE stream）。
//
// 不联网：gin 测试模式 + httptest。验证：
//
//	① list 返回摘要（新→旧）；② get 返回历史（404 兜底）；③ SSE 流（history 回放 +
//	   增量 log + 终态 done + 正确 SSE 帧格式 + 响应头）。
package runlogs

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-go/internal/service/signup"
)

func newTestRouter(hub *signup.RunLogHub) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	NewHandler(hub).Register(r.Group("/api/run-logs"))
	return r
}

func seedRun(hub *signup.RunLogHub, runID string, terminal bool) {
	lg := hub.Logger(runID)
	lg.Emit(signup.LogInfo, "run_created", "创建", "", nil)
	lg.Emit(signup.LogInfo, "proxy_acquired", "租代理", "", nil)
	if terminal {
		lg.Emit(signup.LogSuccess, "run_completed", "完成", "", nil)
	}
}

// TestListRuns 验证摘要列表（新→旧）。
func TestListRuns(t *testing.T) {
	hub := signup.NewRunLogHub()
	seedRun(hub, "run-a", true)
	seedRun(hub, "run-b", false)
	r := newTestRouter(hub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/run-logs/runs", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var out []runSummaryJSON
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("应 2 个摘要, got %d", len(out))
	}
	// 新→旧：run-b 在前。
	if out[0].RunID != "run-b" {
		t.Fatalf("应新→旧, got[0]=%s", out[0].RunID)
	}
	// run-a 是终态。
	for _, s := range out {
		if s.RunID == "run-a" && !s.Terminal {
			t.Fatal("run-a 应 terminal=true")
		}
	}
}

// TestGetRun 验证单 run 历史（全量 + 时间序）。
func TestGetRun(t *testing.T) {
	hub := signup.NewRunLogHub()
	seedRun(hub, "run-x", true)
	r := newTestRouter(hub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/run-logs/runs/run-x", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	var out runLogFileJSON
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Entries) != 3 {
		t.Fatalf("应 3 条历史, got %d", len(out.Entries))
	}
	if out.Entries[0].Event != "run_created" || out.Entries[2].Event != "run_completed" {
		t.Fatalf("顺序错误: %s..%s", out.Entries[0].Event, out.Entries[2].Event)
	}
}

// TestGetRunNotFound 验证不存在 run → 404。
func TestGetRunNotFound(t *testing.T) {
	r := newTestRouter(signup.NewRunLogHub())
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/run-logs/runs/nope", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("应 404, got %d", w.Code)
	}
}

// TestStreamRunHeadersAndHistory 验证 SSE：响应头 + history 帧。
func TestStreamRunHeadersAndHistory(t *testing.T) {
	hub := signup.NewRunLogHub()
	seedRun(hub, "run-s", true) // 已终态 → history + done 即结束
	r := newTestRouter(hub)

	// 用真实 server + 手动读流（httptest.NewRecorder 不适合流式）。
	ts := httptest.NewServer(r)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/run-logs/runs/run-s/stream", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求: %v", err)
	}
	defer resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	if ct != "text/event-stream" {
		t.Fatalf("Content-Type 应 text/event-stream, got %s", ct)
	}

	// 读 SSE 帧：应见 history 事件 + done 事件。
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	var events []string
	deadline := time.Now().Add(3 * time.Second)
	for sc.Scan() && time.Now().Before(deadline) {
		line := sc.Text()
		if strings.HasPrefix(line, "event: ") {
			events = append(events, strings.TrimPrefix(line, "event: "))
		}
		if line == "event: done" {
			break
		}
	}
	if len(events) < 2 || events[0] != "history" {
		t.Fatalf("应先 history 事件, got %v", events)
	}
	if events[len(events)-1] != "done" {
		t.Fatalf("终态应发 done, got %v", events)
	}
}

// TestStreamRunLive 验证 SSE 实时增量：连接后新日志实时到达。
func TestStreamRunLive(t *testing.T) {
	hub := signup.NewRunLogHub()
	seedRun(hub, "run-live", false) // 未终态 → 挂实时
	r := newTestRouter(hub)
	ts := httptest.NewServer(r)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/run-logs/runs/run-live/stream", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求: %v", err)
	}
	defer resp.Body.Close()

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	gotLive := make(chan string, 1)
	go func() {
		var lastEvent string
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "event: ") {
				lastEvent = strings.TrimPrefix(line, "event: ")
			}
			if strings.HasPrefix(line, "data: ") && lastEvent == "log" {
				gotLive <- strings.TrimPrefix(line, "data: ")
				return
			}
		}
	}()

	// 连接建立后发一条新日志（稍等确保订阅已挂上）。
	time.Sleep(150 * time.Millisecond)
	hub.Logger("run-live").Emit(signup.LogInfo, "dial_succeeded", "拨号成功", "a@x.com", nil)

	select {
	case data := <-gotLive:
		var e signup.RunLogEntry
		if err := json.Unmarshal([]byte(data), &e); err != nil {
			t.Fatalf("log 帧 data 非 JSON: %v", err)
		}
		if e.Event != "dial_succeeded" || e.Email != "a@x.com" {
			t.Fatalf("实时帧内容错误: %+v", e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("实时日志未在超时内到达")
	}
}

// TestStreamRunNilHub 验证未装配 hub → 503。
func TestStreamRunNilHub(t *testing.T) {
	r := newTestRouter(nil)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/run-logs/runs/x/stream", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("应 503, got %d", w.Code)
	}
}
