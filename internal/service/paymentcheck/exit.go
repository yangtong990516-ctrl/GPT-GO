// exit.go 实现线路出口校验（对齐支付类型检测实现说明 §6.1）。
//
// 每条线路开始前，经该线路选中的代理请求 chatgpt.com/cdn-cgi/trace，
// 实测出口 IP + 出口国家（GeoIP 校准），并做 sliver 纯度分级。
//
// 强制要求（红线）：
//   - 能够检测到出口国家 → 否则 proxy_unavailable；
//   - 出口国家 == 线路配置国家 → 否则 proxy_country_mismatch，该线路不建单。
package paymentcheck

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// traceURL 是出口检测地址（文档 §6.1，默认 cloudflare trace 经 chatgpt 边缘）。
const traceURL = "https://chatgpt.com/cdn-cgi/trace"

var (
	traceIPRe     = regexp.MustCompile(`(?m)^ip=([^\s]+)\s*$`)
	traceLocRe    = regexp.MustCompile(`(?m)^loc=([A-Za-z]{2})\s*$`)
	traceSliverRe = regexp.MustCompile(`(?m)^sliver=([^\s]+)\s*$`)
)

// dirtyMarks 是纯度分级的脏标记（与 proxy/probe.go 的 gradePurity 同源）。
var dirtyMarks = []string{
	"datacenter", "cloud", "hosting", "vpn", "proxy",
	"tier2", "tier3", "tier4", "tier5", "tunnel",
}

// ExitInfo 是出口实测结果。
type ExitInfo struct {
	IP      string // 出口 IP（代理 IP，可明文展示）
	Country string // 出口国家（2 位大写）
	Purity  string // clean / dirty
}

// ExitProber 抽象出口探测（测试可注入替身）。
type ExitProber interface {
	ProbeExit(ctx context.Context, proxyURL string, timeoutSeconds float64) (*ExitInfo, error)
}

// CDNExitProber 是真实现：经代理 GET cdn-cgi/trace 解析 ip/loc/sliver。
type CDNExitProber struct{}

// NewCDNExitProber 返回默认出口探测器。
func NewCDNExitProber() *CDNExitProber { return &CDNExitProber{} }

// ProbeExit 经 proxyURL 探测出口（国家 + IP + 纯度）。
func (CDNExitProber) ProbeExit(ctx context.Context, proxyURL string, timeoutSeconds float64) (*ExitInfo, error) {
	if timeoutSeconds <= 0 {
		timeoutSeconds = 8
	}
	var transport *http.Transport
	if strings.TrimSpace(proxyURL) != "" {
		parsed, err := url.Parse(proxyURL)
		if err != nil {
			return nil, err
		}
		transport = &http.Transport{Proxy: http.ProxyURL(parsed)}
	} else {
		transport = &http.Transport{}
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   time.Duration(timeoutSeconds) * time.Second,
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, traceURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "curl/8.0")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	text := string(body)
	info := &ExitInfo{
		IP:      matchTraceGroup(traceIPRe, text),
		Country: strings.ToUpper(matchTraceGroup(traceLocRe, text)),
		Purity:  gradeSliverPurity(matchTraceGroup(traceSliverRe, text)),
	}
	if !isTwoLetterUpper(info.Country) {
		return nil, errExitCountryUnknown
	}
	return info, nil
}

// errExitCountryUnknown 表示出口国家无法识别（对齐 proxy_unavailable）。
var errExitCountryUnknown = &exitError{kind: "proxy_unavailable", msg: "出口国家无法识别"}

// exitError 是出口探测错误（kind 对齐线路状态）。
type exitError struct {
	kind string
	msg  string
}

func (e *exitError) Error() string { return e.msg }

// matchTraceGroup 提取 trace 文本字段。
func matchTraceGroup(re *regexp.Regexp, s string) string {
	m := re.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// gradeSliverPurity 按 sliver 分级纯度（none/tier1→clean；机房/云/代理/tier2-5→dirty）。
func gradeSliverPurity(sliver string) string {
	low := strings.ToLower(sliver)
	if low == "" {
		return "clean" // sliver 缺失按干净处理（上游重试语义）
	}
	for _, mark := range dirtyMarks {
		if strings.Contains(low, mark) {
			return "dirty"
		}
	}
	return "clean"
}
