package apiserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"gpt-go/internal/config"
)

// TestPaymentCheckRoutesRegistered 冒烟：/api/payment-check/* 路由组已注册。
func TestPaymentCheckRoutesRegistered(t *testing.T) {
	s := NewServer(&config.Config{}, nil, nil)
	h := s.Handler()

	// GET /api/payment-check/status（无批次时应返回 running:false）。
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/payment-check/status", nil)
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status 应 200, got %d body=%s", w.Code, w.Body.String())
	}

	// GET /api/payment-check/routes-config。
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/payment-check/routes-config", nil)
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("routes-config 应 200, got %d", w.Code)
	}

	// POST /api/payment-check/run 空 ids 应 400（InvalidBody）。
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/payment-check/run", nil)
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest && w.Code != 422 {
		t.Fatalf("run 空 body 应 400, got %d", w.Code)
	}
}
