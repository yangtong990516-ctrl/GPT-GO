package apiserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gpt-go/internal/config"
)

// TestPaymentCheckReviewSmoke review 后回归：items 空批次不 panic、结构化线路校验生效。
func TestPaymentCheckReviewSmoke(t *testing.T) {
	s := NewServer(&config.Config{}, nil, nil)
	h := s.Handler()

	// 1) GET /items 在任何批次前不应 panic（原 BatchItems nil deref bug）。
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/payment-check/items", nil)
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("items 应 200, got %d body=%s", w.Code, w.Body.String())
	}

	// 2) POST /run 结构化线路缺代理 → 400（resolveRoutes 校验生效）。
	body := `{"ids":["a1"],"routes":[{"country":"VN","currency":"VND","locale":"vi-VN","proxies":[]}]}`
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/payment-check/run", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("空代理线路应 400, got %d body=%s", w.Code, w.Body.String())
	}

	// 3) POST /run 非法国家码 → 400（ParseRoutesText 校验生效）。
	body = `{"ids":["a1"],"routes":[{"country":"XX123","currency":"VND","locale":"vi-VN","proxies":["http://h:1"]}]}`
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/payment-check/run", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("非法国家码应 400, got %d body=%s", w.Code, w.Body.String())
	}
}
