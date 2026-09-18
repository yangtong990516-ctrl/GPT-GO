// Package signup 实现纯协议 ChatGPT 注册（protocol_signup 的 Go 迁移）。
//
// 对齐 Python 源 app/backend/protocol_signup/，采用：
//   - httpcloak（替代 curl_cffi / tls-client，TLS 指纹 + 浏览器预设，见 core 包）
//   - v8go（真 V8，替代 Node/QuickJS，跑 Sentinel sdk.js 解 PoW）
//
// 包结构（按职责分组）：
//   - service.go     run_protocol_registration 编排（5 步：租代理→预留邮箱→拨号→authflow→落库）
//   - batch.go       run_protocol_batch 批量入口（并发信号量 + 取消）
//   - dialer.go      会话型住宅代理「现场拨号」（通道→新出口 IP；L4 出口 IP 并发去重）
//   - resource_adapter.go  邮箱池/账号池资源适配（ReserveEmails→注册→成功落账号池）
//   - proxy_adapter.go     proxy.Service → ProxyStore 桥接
//   - core/          地基：Identity 三路自洽(TLS↔UA↔SO) + Session(httpcloak) + GeoIP 时区
//   - authflow/      AuthFlow 注册状态机（10 步 + Codex RT）
//   - sentinel/      Sentinel PoW 求解（v8go，-tags v8 构建）
//   - otp/           邮箱验证码取件（防串号 issuedAfter）
//   - totp/          TOTP 二步验证
package signup
