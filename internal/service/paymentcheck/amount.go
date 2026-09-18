// amount.go 实现应付金额采集与 0 元判定（对齐 djblook amount_observations 思路）。
//
// OAICS/Stripe 响应是嵌套快照，可能同时存在新旧多个金额字段（旧快照 0 元、
// 新快照非 0）。规则：
//   - 所有可核验金额都为 0 → zero_confirmed（0 元试用真实生效）；
//   - 金额互相矛盾 → zero_unknown（金额不可信，渠道不算 0 元渠道）；
//   - 未返回任何金额 → zero_unknown；
//   - 唯一非 0 金额 → not_zero（优惠未生效或活动非 0 元）。
package paymentcheck

import (
	"strconv"
	"strings"
)

// ZeroStatus 常量（写入 PaymentRouteResult.ZeroStatus）。
const (
	ZeroConfirmed = "zero_confirmed" // 全部金额观测一致为 0
	ZeroNotZero   = "not_zero"       // 金额一致且非 0
	ZeroUnknown   = "zero_unknown"   // 无观测 / 观测矛盾
)

// amountPaths 是金额字段的候选路径（小写匹配，覆盖 OAICS 与 Stripe init 两种形态）。
var amountPaths = [][]string{
	{"checkout_amount_minor"},
	{"total_summary", "due"},
	{"totalsummary", "due"},
	{"invoice", "amount_due"},
	{"invoice", "amountdue"},
	{"amount_due"},
	{"amountdue"},
	{"amount_total"},
	{"amounttotal"},
	{"total", "total"},
	{"total", "due"},
	{"total", "taxinclusive"},
	{"total", "taxinclusiveamount"},
	{"amount"},
}

// wrapperKeys 是嵌套包裹层字段名（递归下探用）。
var wrapperKeys = []string{
	"checkout_session", "checkoutsession", "session", "checkout",
	"data", "result", "payload", "response",
	"checkout_state", "checkoutstate", "checkout_snapshot", "checkoutsnapshot",
	"elements_options", "elementsoptions",
}

// AmountObservation 是一条金额观测（路径 + minor 单位金额）。
type AmountObservation struct {
	Path   string
	Amount int64
}

// maxObserveDepth 限制递归下探深度（嵌套快照层级有限，白名单 wrapper + 深度
// 上限双保险防环/防爆炸）。
const maxObserveDepth = 10

// ObserveAmounts 递归采集 payload 中全部可核验金额（对齐 djblook amount_observations）。
func ObserveAmounts(payload any) []AmountObservation {
	var out []AmountObservation
	seenPaths := map[string]bool{}

	var visit func(v any, prefix string, depth int)
	visit = func(v any, prefix string, depth int) {
		if depth > maxObserveDepth {
			return
		}
		dict, ok := v.(map[string]any)
		if !ok {
			return
		}
		for _, path := range amountPaths {
			amount, ok := minorAmount(nestedValue(dict, path))
			if !ok {
				continue
			}
			key := prefix + strings.Join(path, ".")
			if seenPaths[key] {
				continue
			}
			seenPaths[key] = true
			out = append(out, AmountObservation{Path: key, Amount: amount})
		}
		for _, key := range wrapperKeys {
			if nested, ok := dict[key].(map[string]any); ok {
				visit(nested, prefix+key+".", depth+1)
			}
		}
		for _, nested := range dict {
			if list, ok := nested.([]any); ok {
				for _, item := range list {
					visit(item, prefix, depth+1)
				}
			}
		}
	}
	visit(payload, "", 0)
	return out
}

// JudgeZero 按金额观测集合判定 0 元状态与金额值。
// 返回 (zeroStatus, amount)；amount 仅当观测一致时非 nil。
func JudgeZero(obs []AmountObservation) (string, *int64) {
	if len(obs) == 0 {
		return ZeroUnknown, nil
	}
	distinct := map[int64]bool{}
	for _, o := range obs {
		distinct[o.Amount] = true
	}
	if len(distinct) > 1 {
		return ZeroUnknown, nil // 观测矛盾：不采信任何金额
	}
	var amount int64
	for a := range distinct {
		amount = a
	}
	if amount == 0 {
		return ZeroConfirmed, &amount
	}
	return ZeroNotZero, &amount
}

// nestedValue 按小写键路径取值（键比较时忽略大小写与下划线风格差异）。
func nestedValue(dict map[string]any, path []string) any {
	current := any(dict)
	for _, want := range path {
		m, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		var found any
		for k, v := range m {
			if strings.EqualFold(k, want) {
				found = v
				break
			}
		}
		if found == nil {
			return nil
		}
		current = found
	}
	return current
}

// minorAmount 把任意 JSON 值解析为 minor 单位整数金额。
// 支持数字、数字字符串、{"amount": x}、{"minorUnitsAmount": x} 形态。
func minorAmount(v any) (int64, bool) {
	switch t := v.(type) {
	case float64:
		return int64(t), true
	case int64:
		return t, true
	case int:
		return int64(t), true
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return 0, false
		}
		// 允许 "0" / "123" / "123.0"（小数部分必须全 0，对齐 djblook _minor_amount）。
		if strings.Contains(s, ".") {
			parts := strings.SplitN(s, ".", 2)
			frac := strings.TrimRight(parts[1], "0")
			if frac != "" {
				return 0, false
			}
			s = parts[0]
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return 0, false
		}
		return n, true
	case map[string]any:
		for _, k := range []string{"minorUnitsAmount", "minor_units_amount", "amount"} {
			if sub, ok := t[k]; ok {
				return minorAmount(sub)
			}
		}
	}
	return 0, false
}


