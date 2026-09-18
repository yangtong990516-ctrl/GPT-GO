package apiserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gpt-go/internal/config"
)

// TestRebindRoutesRegistered 冒烟：/api/rebind/* 路由组已注册。
func TestRebindRoutesRegistered(t *testing.T) {
	s := NewServer(&config.Config{}, nil, nil)
	h := s.Handler()

	// GET /api/rebind/status（无批次时应返回 running:false）。
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/rebind/status", nil)
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status 应 200, got %d body=%s", w.Code, w.Body.String())
	}

	// GET /api/rebind/items（空批次不应 panic）。
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/rebind/items", nil)
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("items 应 200, got %d body=%s", w.Code, w.Body.String())
	}

	// POST /api/rebind/run 空 ids 应 400/422。
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/rebind/run", strings.NewReader(`{"ids":[]}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest && w.Code != 422 {
		t.Fatalf("空 ids 应 400/422, got %d body=%s", w.Code, w.Body.String())
	}

	// POST /api/rebind/cancel 无批次应 ok:false。
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/rebind/cancel", nil)
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("cancel 应 200, got %d", w.Code)
	}
}
