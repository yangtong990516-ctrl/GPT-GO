package signup

// Config 是注册流程的最小配置，对齐 Python protocol_signup/_runtime/config.py 的 Config。
//
// 仅保留注册阶段必需的字段；支付相关（card/billing/stripe/captcha）已剔除，
// 与 Python 源保持一致（"剥离自原 config.py，去掉支付相关字段"）。
type Config struct {
	// Proxy 是出口代理 URL，例：socks5://user:pass@host:port 或 socks5://127.0.0.1:18899。
	// 留空走系统直连。
	Proxy string
}
