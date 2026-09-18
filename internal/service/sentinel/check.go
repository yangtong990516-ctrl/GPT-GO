// Sentinel detection primitives: proxy resolution and remote SDK probing.
package sentinel

import (
	"context"
	"fmt"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gpt-go/internal/model"
	"gpt-go/internal/service/signup/core"
)

// ProxyProvider is the minimal contract sentinel needs from the proxy pool.
// It mirrors proxy_store.all_eligible_proxy_candidates: returns available
// proxies (country-ascending), or an empty slice when none.
//
// TODO(T-104): wire a real implementation once the proxies module is migrated.
type ProxyProvider interface {
	AllEligibleProxyCandidates(ctx context.Context) ([]model.ProxyLease, error)
}

// mockProxyProvider returns no candidates, so resolveProxy falls back to the
// configured fixed proxy (or direct connection). Replaced by the real proxy
// pool when T-104 lands.
type mockProxyProvider struct{}

func (mockProxyProvider) AllEligibleProxyCandidates(context.Context) ([]model.ProxyLease, error) {
	return nil, nil
}

// frameVersionRe 从 frame.html 提取最新版本号：URL query 里 sv=VERSION
// （对齐 codex frame.html?sv=20260810913b；版本号为小写十六进制串）。
var frameVersionRe = regexp.MustCompile(`[?&]sv=([0-9a-z]+)`)

// probeSession 为一次探测建一个指纹自洽的 core.Session（httpcloak，对齐
// curl_cffi 指纹）。sdk.js / frame.html 是公开 CDN，无需账号身份——用一个
// 随机浏览器身份即可（对齐 codex session.get 的指纹头）。
func probeSession(proxy string, timeoutSeconds float64) (*core.Session, error) {
	if timeoutSeconds <= 0 {
		timeoutSeconds = 15
	}
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	id := core.NewIdentity(rng, "US", "America/New_York", "")
	return core.NewSession(id, proxy, time.Duration(timeoutSeconds*float64(time.Second)))
}

// realCheckSDKStatus probes the bundled sdk.js for validity via the given proxy,
// returning metadata (status_code / reachable / etag / last_modified /
// content_length / content_type). Mirrors sentinel_quickjs.check_sentinel_sdk_status.
//
// 真实网络探测：core.Session(httpcloak) GET sdk.js，对齐 codex 的 accept/referer 头。
func realCheckSDKStatus(proxy string, timeoutSeconds float64) map[string]any {
	out := map[string]any{
		"status_code":    nil,
		"reachable":      false,
		"etag":           nil,
		"last_modified":  nil,
		"content_length": nil,
		"content_type":   nil,
		"error":          nil,
	}
	sess, err := probeSession(proxy, timeoutSeconds)
	if err != nil {
		out["error"] = "构建探测会话失败: " + err.Error()
		return out
	}
	defer sess.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds*float64(time.Second)))
	defer cancel()
	resp, err := sess.Get(ctx, model.SentinelSDKURL(), "https://auth.AI Platform.com/")
	if err != nil {
		out["error"] = "请求 sdk.js 失败: " + err.Error()
		return out
	}
	out["reachable"] = true
	out["status_code"] = resp.StatusCode
	if v := resp.GetHeader("etag"); v != "" {
		out["etag"] = v
	}
	if v := resp.GetHeader("last-modified"); v != "" {
		out["last_modified"] = v
	}
	if v := resp.GetHeader("content-type"); v != "" {
		out["content_type"] = v
	}
	if v := resp.GetHeader("content-length"); v != "" {
		if n, cerr := strconv.Atoi(strings.TrimSpace(v)); cerr == nil {
			out["content_length"] = n
		}
	}
	return out
}

// realFetchLatestVersion parses the online latest version from frame.html, mirroring
// sentinel_quickjs.fetch_latest_sentinel_version：GET frame.html，正则提取 sv= 版本。
func realFetchLatestVersion(proxy string, timeoutSeconds float64) map[string]any {
	out := map[string]any{
		"discovered_version": nil,
		"configured_version": model.SentinelVersion,
		"reachable":          false,
		"status_code":        nil,
		"method":             "frame.html-sv-parse",
		"error":              nil,
	}
	sess, err := probeSession(proxy, timeoutSeconds)
	if err != nil {
		out["error"] = "构建探测会话失败: " + err.Error()
		return out
	}
	defer sess.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds*float64(time.Second)))
	defer cancel()
	resp, err := sess.Get(ctx, model.SentinelFrameURL(), "https://auth.AI Platform.com/")
	if err != nil {
		out["error"] = "请求 frame.html 失败: " + err.Error()
		return out
	}
	out["reachable"] = true
	out["status_code"] = resp.StatusCode
	if resp.StatusCode != 200 {
		out["error"] = fmt.Sprintf("frame.html 返回 HTTP %d", resp.StatusCode)
		return out
	}
	body := resp.Bytes()
	if m := frameVersionRe.FindSubmatch(body); len(m) == 2 {
		out["discovered_version"] = string(m[1])
	} else {
		out["error"] = "frame.html 未解析到 sv 版本号"
	}
	return out
}
