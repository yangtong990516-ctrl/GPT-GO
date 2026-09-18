package apiserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gpt-go/internal/config"
	"gpt-go/internal/store/mongo"
)

func newProxyServer() *Server {
	return NewServer(config.Default(), mongo.NewManager("autoregister"), nil)
}

// TestProxyImportAndList mirrors POST /api/proxies/import + GET /api/proxies.
func TestProxyImportAndList(t *testing.T) {
	s := newProxyServer()

	raw := `sp.ipipbright.net:1000:bru36433_area-VN_life-5_session-U0nM1dHxwW:bzqbku
prem.country.iprocket.io:9595:com44840385-res-VN-Lsid-967708287-TTL-1800:Sy3p57oVo24q1tY`

	code, body := doJSONBody(t, s, http.MethodPost, "/api/proxies/import", `{"rawText": `+jsonStr(raw)+`}`)
	if code != http.StatusOK {
		t.Fatalf("import status = %d, want 200 (body=%v)", code, body)
	}
	if body["imported"] != float64(2) {
		t.Errorf("imported = %v, want 2", body["imported"])
	}
	if body["errorCount"] != float64(0) {
		t.Errorf("errorCount = %v, want 0", body["errorCount"])
	}

	code, body = doJSONBody(t, s, http.MethodGet, "/api/proxies?page=1&pageSize=20", "")
	if code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", code)
	}
	if body["total"] != float64(2) {
		t.Errorf("total = %v, want 2", body["total"])
	}
	items, ok := body["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("items = %v, want 2 items", body["items"])
	}
	// 校验第一条 iprocket（socks5h）或 ipipbright（http）的 scheme 与 country。
	for _, it := range items {
		rec := it.(map[string]any)
		host := rec["host"].(string)
		switch host {
		case "prem.country.iprocket.io":
			if rec["scheme"] != "socks5h" {
				t.Errorf("iprocket scheme = %v, want socks5h", rec["scheme"])
			}
			if rec["country"] != "VN" {
				t.Errorf("iprocket country = %v, want VN", rec["country"])
			}
		case "sp.ipipbright.net":
			if rec["scheme"] != "http" {
				t.Errorf("ipipbright scheme = %v, want http", rec["scheme"])
			}
			if rec["country"] != "VN" {
				t.Errorf("ipipbright country = %v, want VN", rec["country"])
			}
		default:
			t.Errorf("unexpected host %v", host)
		}
	}
}

// doJSONArray issues a request and decodes the response as a JSON array.
func doJSONArray(t *testing.T, s *Server, method, path, body string) (int, []any) {
	t.Helper()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	var out []any
	if len(rec.Body.Bytes()) > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
	}
	return rec.Code, out
}

// TestProxyCountriesAndGroups mirrors GET /api/proxies/countries + /groups.
func TestProxyCountriesAndGroups(t *testing.T) {
	s := newProxyServer()
	raw := `sp.ipipbright.net:1000:bru36433_area-VN_life-5_session-U0nM1dHxwW:bzqbku`
	doJSONBody(t, s, http.MethodPost, "/api/proxies/import", `{"rawText": `+jsonStr(raw)+`}`)

	code, arr := doJSONArray(t, s, http.MethodGet, "/api/proxies/countries", "")
	if code != http.StatusOK {
		t.Fatalf("countries status = %d", code)
	}
	if len(arr) != 1 {
		t.Fatalf("countries len = %d, want 1", len(arr))
	}
	first := arr[0].(map[string]any)
	if first["country"] != "VN" || first["total"] != float64(1) {
		t.Errorf("country summary = %v", first)
	}

	code, garr := doJSONArray(t, s, http.MethodGet, "/api/proxies/groups", "")
	if code != http.StatusOK {
		t.Fatalf("groups status = %d", code)
	}
	if len(garr) != 1 {
		t.Fatalf("groups len = %d, want 1", len(garr))
	}
	g := garr[0].(map[string]any)
	if g["country"] != "VN" || g["group"] != "默认组" {
		t.Errorf("group summary = %v", g)
	}
}

// TestProxyDelete mirrors DELETE /api/proxies/{id}.
func TestProxyDelete(t *testing.T) {
	s := newProxyServer()
	raw := `sp.ipipbright.net:1000:bru36433_area-VN_life-5_session-U0nM1dHxwW:bzqbku`
	doJSONBody(t, s, http.MethodPost, "/api/proxies/import", `{"rawText": `+jsonStr(raw)+`}`)

	_, body := doJSONBody(t, s, http.MethodGet, "/api/proxies?page=1&pageSize=20", "")
	items := body["items"].([]any)
	id := items[0].(map[string]any)["id"].(string)

	code, delBody := doJSONBody(t, s, http.MethodDelete, "/api/proxies/"+id, "")
	if code != http.StatusOK {
		t.Fatalf("delete status = %d", code)
	}
	if delBody["deleted"] != float64(1) {
		t.Errorf("deleted = %v, want 1", delBody["deleted"])
	}
}

// TestProxyTestStub mirrors POST /api/proxies/test (stub prober → all failed).
func TestProxyTestStub(t *testing.T) {
	s := newProxyServer()
	code, body := doJSONBody(t, s, http.MethodPost, "/api/proxies/test", `{"timeoutSeconds": 8}`)
	if code != http.StatusOK {
		t.Fatalf("test status = %d", code)
	}
	if body["tested"] != nil && body["available"] != nil {
		// 空池：tested=0, available=0。
		if body["available"] != float64(0) {
			t.Errorf("available = %v, want 0", body["available"])
		}
	}
}

func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
