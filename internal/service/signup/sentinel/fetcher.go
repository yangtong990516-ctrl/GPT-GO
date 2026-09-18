package sentinel

import (
	"context"
)

// ChallengeFetcher 抽象 /sentinel/req 网络请求（对齐 Python _fetch_sentinel_challenge）。
//
// 生产实现是 authflow 包的 coreChallengeFetcher（见 authflow/wire.go）：用注册同一
// core.Session 发 challenge，保证 challenge 请求与主线 TLS 会话/指纹/cookie 同源——
// 这是 TLS↔UA↔SO 三路自洽的延伸（sdk.js 画像是 chrome-148，challenge 请求就不能是
// 另一个 TLS 指纹）。测试注入 mock。
//
// 历史：旧实现 DefaultChallengeFetcher（走 httpclient/tls-client，另起短会话）已删除——
// 它与主线会话不同源，存在对齐裂缝，且无真实调用方。
type ChallengeFetcher func(ctx context.Context, env EnvPayload, flow Flow, requestP string) (*Challenge, error)
