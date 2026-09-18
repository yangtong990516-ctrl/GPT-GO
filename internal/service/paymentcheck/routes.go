// routes.go 实现「国家|币种|语言|代理」区域线路配置的解析、分组与校验
// （对齐支付类型检测实现说明 §3 区域线路与代理池）。
//
// 设计要点：
//   - 持久化格式为文本行 `国家|币种|语言|代理`，相同 国家+币种+语言 的多行合并
//     成一个代理池，每次检测从池中随机选一个代理；
//   - DIRECT 或空代理视为禁用线路，绝不回退到机器直连（红线）；
//   - 最多 12 组区域线路；完全没有有效线路时拒绝执行检测；
//   - 兼容旧部署：MOMO_PROXY_POOL / PAYMENT_VN_PROXY_POOL 迁移为 VN|VND|vi-VN|代理。
package paymentcheck

import (
	"errors"
	"fmt"
	"strings"

	"gpt-go/internal/util"
)

// MaxRoutes 是区域线路组数上限（对齐文档 §3.1）。
const MaxRoutes = 12

// Route 是一组区域线路（国家+币种+语言相同的一个代理池）。
type Route struct {
	Country  string   // 2 位大写国家码（VN）
	Currency string   // 3 位大写币种（VND）
	Locale   string   // 语言（vi-VN）
	Proxies  []string // 代理池（已归一化为标准 URL）
}

// Key 返回分组键（国家|币种|小写语言）。
func (r Route) Key() string {
	return r.Country + "|" + r.Currency + "|" + strings.ToLower(r.Locale)
}

// ParseRoutesText 解析多行「国家|币种|语言|代理」配置文本，按分组键合并代理池。
// 空行与 # 注释行忽略。格式错误的行返回 error（指出行号）。
func ParseRoutesText(text string) ([]Route, error) {
	var routes []Route
	indexes := map[string]int{}
	for lineno, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "|", 4)
		if len(parts) != 4 {
			return nil, fmt.Errorf("第 %d 行格式错误（应为 国家|币种|语言|代理）", lineno+1)
		}
		country := strings.TrimSpace(parts[0])
		currency := strings.ToUpper(strings.TrimSpace(parts[1]))
		locale := strings.TrimSpace(parts[2])
		proxyRaw := strings.TrimSpace(parts[3])
		// 国家码必须本身即为两个大写字母（对齐文档 §3.1，不做大小写宽容）。
		if !isTwoLetterUpper(country) {
			return nil, fmt.Errorf("第 %d 行国家码错误（必须两位大写字母）: %q", lineno+1, parts[0])
		}
		if len(currency) != 3 {
			return nil, fmt.Errorf("第 %d 行币种错误（必须三位大写字母）: %q", lineno+1, parts[1])
		}
		if locale == "" {
			return nil, fmt.Errorf("第 %d 行语言不能为空", lineno+1)
		}
		proxy, disabled := normalizeProxy(proxyRaw)
		key := country + "|" + currency + "|" + strings.ToLower(locale)
		idx, exists := indexes[key]
		if !exists {
			if len(routes) >= MaxRoutes {
				return nil, errors.New("支付检测线路最多支持 12 组")
			}
			idx = len(routes)
			indexes[key] = idx
			routes = append(routes, Route{Country: country, Currency: currency, Locale: locale})
		}
		if disabled {
			continue // DIRECT/空代理：线路保留但代理池不加条目
		}
		routes[idx].Proxies = appendUnique(routes[idx].Proxies, proxy)
	}
	return routes, nil
}

// EnabledRoutes 过滤出有可用代理的线路（DIRECT 禁用线路被剔除）。
// 完全没有有效线路时返回 false（调用方应拒绝执行检测）。
func EnabledRoutes(routes []Route) []Route {
	out := make([]Route, 0, len(routes))
	for _, r := range routes {
		if len(r.Proxies) > 0 {
			out = append(out, r)
		}
	}
	return out
}

// DefaultRoutesText 返回环境变量 PAYMENT_PROXY_ROUTES 的配置文本；
// 兼容回退：MOMO_PROXY_POOL / PAYMENT_VN_PROXY_POOL 迁移为 VN|VND|vi-VN|代理。
func DefaultRoutesText() string {
	if v := strings.TrimSpace(util.EnvOrDefault("PAYMENT_PROXY_ROUTES", "")); v != "" {
		return strings.ReplaceAll(v, `\n`, "\n")
	}
	var lines []string
	for _, envKey := range []string{"PAYMENT_VN_PROXY_POOL", "MOMO_PROXY_POOL"} {
		raw := strings.TrimSpace(util.EnvOrDefault(envKey, ""))
		if raw == "" {
			continue
		}
		for _, line := range strings.Split(strings.ReplaceAll(raw, `\n`, "\n"), "\n") {
			p := strings.TrimSpace(line)
			if p == "" || strings.HasPrefix(p, "#") {
				continue
			}
			lines = append(lines, "VN|VND|vi-VN|"+p)
		}
	}
	return strings.Join(lines, "\n")
}

// normalizeProxy 归一化代理为 标准 URL；DIRECT/空 → disabled=true。
// 支持 host:port:user:pass、user:pass@host:port 与标准 http/socks5 URL。
func normalizeProxy(raw string) (proxyURL string, disabled bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.EqualFold(raw, "DIRECT") {
		return "", true
	}
	if strings.Contains(raw, "://") {
		return raw, false
	}
	parts := strings.Split(raw, ":")
	// host:port:user:pass
	if len(parts) == 4 && parts[1] != "" && isAllDigits(parts[1]) {
		return "http://" + parts[2] + ":" + parts[3] + "@" + parts[0] + ":" + parts[1], false
	}
	// user:pass@host:port（无 scheme）
	if len(parts) == 3 && strings.Contains(parts[0], "@") == false && strings.Contains(raw, "@") {
		return "http://" + raw, false
	}
	return "http://" + raw, false
}

func appendUnique(xs []string, v string) []string {
	for _, x := range xs {
		if x == v {
			return xs
		}
	}
	return append(xs, v)
}

func isTwoLetterUpper(s string) bool {
	return len(s) == 2 && s[0] >= 'A' && s[0] <= 'Z' && s[1] >= 'A' && s[1] <= 'Z'
}

func isAllDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(s) > 0
}
