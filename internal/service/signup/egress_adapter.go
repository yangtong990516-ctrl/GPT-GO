// egress_adapter.go 把 proxy 包的出口探测适配成拨号器的 EgressProber。
// 单独成文件是为了隔离 import（signup → proxy），保持 dialer.go 不含第三方依赖。
package signup

import (
	"context"

	"gpt-go/internal/service/proxy"
)

// CDNProber 是 EgressProber 的真实实现：经 proxyURL 走 chatgpt.com/cdn-cgi/trace
// 实测出口（国家 + IP + 时区）。TimeoutSeconds 是单次探测超时（默认 8s，
// 对齐 codex-auto _probe_trace 的默认）。
type CDNProber struct {
	TimeoutSeconds float64
}

// NewCDNProber 创建出口探测器（接 proxy 包真实 trace 探测）。
func NewCDNProber() *CDNProber { return &CDNProber{TimeoutSeconds: 8} }

// ProbeEgress 实现 EgressProber：经 proxyURL 实测出口。
// 国家是【实测值】（每次拨号当次有效，不锁死）。
func (p *CDNProber) ProbeEgress(ctx context.Context, proxyURL string) (country, egressIP, timezoneID string, err error) {
	to := p.TimeoutSeconds
	if to <= 0 {
		to = 8
	}
	return proxy.ProbeEgressOn(ctx, proxyURL, to)
}
