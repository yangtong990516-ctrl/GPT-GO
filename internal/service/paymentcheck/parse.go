// parse.go 实现支付方式解析与归一化（对齐支付类型检测实现说明 §12）。
//
// 解析器递归搜索以下字段：
//
//	payment_method_types / paymentMethodTypes
//	ordered_payment_method_types
//	payment_method_specs
//	custom_payment_methods（cpmt_... 不透明 ID 的处理见 §12.2）
//
// 归一化规则：转小写、- / 空格→_、去重保序、忽略不透明 cpmt_... ID；
// 别名映射：card_payment→card, direct_card→card, kakao→kakao_pay,
// go_pay→gopay, grab_pay→grabpay。gcash 含一条区域启发式推断（菲律宾线路
// 已知 cpmt ID → gcash，标记为 inferred）。
package paymentcheck

import (
	"encoding/json"
	"strings"
)

// gcashInferredCPMT 是菲律宾线路已知的不透明 Custom Payment Method ID → gcash
// 启发式推断（对齐文档 §12.2/§19.4；越南线路不做同类钱包推断）。
const gcashInferredCPMT = "cpmt_1togstc6h1nxgoi3wuvey2cj"

// methodCandidates 是解析时递归搜索的字段名（大小写两种风格）。
var methodListKeys = []string{
	"payment_method_types",
	"paymentMethodTypes",
	"ordered_payment_method_types",
	"orderedPaymentMethodTypes",
	"payment_method_specs",
	"paymentMethodSpecs",
}

// customMethodKeys 是 custom_payment_methods 的字段名。
var customMethodKeys = []string{
	"custom_payment_methods",
	"customPaymentMethods",
}

// specEntryKeys 是对象型条目的取值键顺序（对齐文档 §12）。
var specEntryKeys = []string{"type", "payment_method_type", "name", "label", "display_name", "id"}

// knownWalletNames 是在 custom payment method JSON 文本中可识别的钱包名。
var knownWalletNames = []string{
	"gcash", "momo", "gopay", "grabpay", "paypal", "kakao", "upi", "pix",
	"ideal", "blik", "twint", "bizum", "cashapp",
}

// ParseResult 是支付方式解析结果。
type ParseResult struct {
	Methods  []string // 归一化去重后的渠道（上游明确返回）
	Inferred []string // 启发式推断的渠道（gcash@PH）
}

// ParsePaymentMethods 从 Checkout/OAICS/Stripe init 响应中递归解析支付渠道。
// country 用于区域启发式（仅 PH 线路做 gcash 推断）。
func ParsePaymentMethods(payload any, country string) ParseResult {
	seen := map[string]bool{}
	var methods []string
	var inferred []string
	add := func(m string) {
		m = normalizeMethod(m)
		if m == "" || strings.HasPrefix(m, "cpmt_") || seen[m] {
			return
		}
		seen[m] = true
		methods = append(methods, m)
	}
	addInferred := func(m string) {
		m = normalizeMethod(m)
		if m == "" || seen[m] {
			return
		}
		seen[m] = true
		inferred = append(inferred, m)
	}

	walkPayload(payload, func(dict map[string]any) {
		// 1) 列表型字段（字符串数组 / 对象数组）
		for _, key := range methodListKeys {
			raw, ok := dict[key]
			if !ok {
				continue
			}
			list, ok := raw.([]any)
			if !ok {
				continue
			}
			for _, item := range list {
				switch v := item.(type) {
				case string:
					add(v)
				case map[string]any:
					for _, ek := range specEntryKeys {
						if s, ok := v[ek].(string); ok && s != "" {
							add(s)
							break
						}
					}
					// 条目 JSON 中明确出现 paypal → 补充 paypal（对齐文档 §12）。
					if blob, err := json.Marshal(v); err == nil && strings.Contains(strings.ToLower(string(blob)), "paypal") {
						add("paypal")
					}
				}
			}
		}
		// 2) custom_payment_methods（cpmt_... 不透明 ID）
		for _, key := range customMethodKeys {
			raw, ok := dict[key]
			if !ok {
				continue
			}
			list, ok := raw.([]any)
			if !ok {
				continue
			}
			for _, item := range list {
				obj, ok := item.(map[string]any)
				if !ok {
					continue
				}
				id := strings.ToLower(strings.TrimSpace(strOfAny(obj, "id")))
				if id == gcashInferredCPMT && strings.EqualFold(country, "PH") {
					addInferred("gcash") // 已知 ID + 菲律宾线路 → 启发式 gcash
					continue
				}
				// 文本可识别钱包名（对齐 djblook _oaics_method_names 的文本扫描）。
				if blob, err := json.Marshal(obj); err == nil {
					text := strings.ToLower(string(blob))
					for _, name := range knownWalletNames {
						if strings.Contains(text, name) {
							add(name)
							break
						}
					}
				}
			}
		}
	})
	return ParseResult{Methods: methods, Inferred: inferred}
}

// normalizeMethod 归一化支付渠道名（对齐文档 §12.1 + 别名映射表）。
func normalizeMethod(value string) string {
	m := strings.ToLower(strings.TrimSpace(value))
	m = strings.NewReplacer("-", "_", " ", "_").Replace(m)
	switch m {
	case "card_payment", "direct_card":
		return "card"
	case "kakao":
		return "kakao_pay"
	case "go_pay":
		return "gopay"
	case "grab_pay":
		return "grabpay"
	default:
		return m
	}
}

// walkPayload 递归遍历嵌套 JSON（dict/list），对每个 dict 调 fn。
func walkPayload(v any, fn func(map[string]any)) {
	switch t := v.(type) {
	case map[string]any:
		fn(t)
		for _, nested := range t {
			walkPayload(nested, fn)
		}
	case []any:
		for _, nested := range t {
			walkPayload(nested, fn)
		}
	}
}

// strOfAny 取 dict 字符串字段。
func strOfAny(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}
