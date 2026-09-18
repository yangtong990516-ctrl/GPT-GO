package core

import (
	"context"
	"encoding/json"
	"math/rand"
	"os"
	"testing"
	"time"
)

// TestLiveFingerprintSmoke 是【联网冒烟测试】，默认跳过。
//
// 作用：直连 tls.peet.ws 回显指纹，验证 httpcloak 传输层在真实网线上成立
// （protocol=H2、JA4/peetprint 回显、服务器看到的 UA 与 Identity 一致）。
// 这是 TLS ↔ UA 一致性在「网线上」的最终自证（单测只证构造逻辑）。
//
// 启用方式（避免常规 go test 触发外网依赖）：
//
//	CORE_LIVE=1 GOTOOLCHAIN=auto go test -run TestLiveFingerprintSmoke -v ./internal/service/signup/core/
//
// 可选：CORE_LIVE_PROXY="socks5://u:p@host:port" 指定代理出口验证代理链路。
func TestLiveFingerprintSmoke(t *testing.T) {
	if os.Getenv("CORE_LIVE") != "1" {
		t.Skip("联网冒烟测试默认跳过；设 CORE_LIVE=1 启用")
	}

	id := NewIdentity(rand.New(rand.NewSource(1)), "US", "America/New_York", "")
	s, err := NewSession(id, os.Getenv("CORE_LIVE_PROXY"), 25*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	resp, err := s.Get(context.Background(), "https://tls.peet.ws/api/all", "https://tls.peet.ws/")
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("状态码=%d, body=%s", resp.StatusCode, resp.Text()[:200])
	}

	var data map[string]any
	if err := json.Unmarshal(resp.Bytes(), &data); err != nil {
		t.Fatalf("解析回显失败: %v", err)
	}

	t.Logf("family=%s preset=%s", id.Family, id.PresetName)
	t.Logf("protocol=%v http_version=%v", resp.Protocol, data["http_version"])
	if tlsx, ok := data["tls"].(map[string]any); ok {
		t.Logf("ja4=%v", tlsx["ja4"])
		t.Logf("peetprint=%v", tlsx["peetprint_hash"])
	}
	if ua, ok := data["user_agent"].(string); ok {
		t.Logf("server 看到的 UA=%s", ua)
		if ua != id.UserAgent {
			t.Fatalf("服务器看到的 UA 与 Identity 不一致:\n  server=%s\n  identity=%s", ua, id.UserAgent)
		}
	}
}
