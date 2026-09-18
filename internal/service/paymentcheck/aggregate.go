// aggregate.go 实现多线路结果的聚合规则（对齐支付类型检测实现说明 §14）。
//
// 聚合：
//   - 全部线路的渠道去重后写入 PaymentMethods；
//   - 任意线路返回渠道 → 总体 available；有渠道但非全部线路完成 → partial；
//   - 全部明确完成但渠道为空 → not_returned；
//   - token_invalid / already_paid 优先终止后续线路（熔断）；
//   - 0 元确认（zero_confirmed）线路的渠道单独聚合为 PaymentZeroMethods。
package paymentcheck

import (
	"time"

	"gpt-go/internal/model"
)

// 线路/聚合状态常量（对齐文档 §18 状态速查表）。
const (
	StatusAvailable       = "available"        // 已解析出至少一种支付渠道
	StatusPartial         = "partial"          // 至少一条线路有结果，其他线路未正常完成
	StatusNotReturned     = "not_returned"     // 上游明确有渠道字段但内容为空
	StatusAlreadyPaid     = "already_paid"     // 账号已有付费状态
	StatusTokenInvalid    = "token_invalid"    // Access Token 无效或过期
	StatusRiskBlocked     = "risk_blocked"     // 被安全校验拦截
	StatusRateLimited     = "rate_limited"     // 上游 429
	StatusCheckoutReject  = "checkout_rejected" // 建单 400
	StatusCheckoutFailed  = "checkout_failed"  // 其他建单失败
	StatusProxyUnavail    = "proxy_unavailable"
	StatusProxyMismatch   = "proxy_country_mismatch"
	StatusDisabled        = "disabled" // 没有有效代理线路
	StatusCanceled        = "canceled"
	StatusRunning         = "running"
)

// terminalForAccount 是熔断状态集合：检出即终止该账号剩余线路。
var terminalForAccount = map[string]bool{
	StatusTokenInvalid: true,
	StatusAlreadyPaid:  true,
}

// IsTerminalStatus 判断该状态是否触发账号级熔断。
func IsTerminalStatus(status string) bool { return terminalForAccount[status] }

// completedStates 是「明确完成」的线路状态（聚合 not_returned 判定用）。
var completedStates = map[string]bool{
	"payment_methods_available": true,
	"payment_methods_empty":     true,
}

// Summary 是单账号全部线路的聚合产出。
type Summary struct {
	Status      string
	Methods     []string
	ZeroMethods []string
	CheckedAt   time.Time
	TokenInvalid bool
}

// Aggregate 把单账号的线路结果集合聚合成 Summary。
// totalRoutes 是该账号应跑的线路总数（含被熔断未跑的）。
func Aggregate(routes []model.PaymentRouteResult, totalRoutes int) Summary {
	summary := Summary{CheckedAt: time.Now().UTC()}
	if len(routes) == 0 {
		summary.Status = StatusDisabled
		return summary
	}

	methodsSeen := map[string]bool{}
	zeroSeen := map[string]bool{}
	completed := 0
	anyMethods := false
	firstStatus := ""

	for _, r := range routes {
		if firstStatus == "" {
			firstStatus = r.Status
		}
		if r.Status == StatusTokenInvalid {
			summary.TokenInvalid = true
		}
		if len(r.Methods) > 0 || len(r.MethodsInferred) > 0 {
			anyMethods = true
			for _, m := range append(append([]string{}, r.Methods...), r.MethodsInferred...) {
				if !methodsSeen[m] {
					methodsSeen[m] = true
					summary.Methods = append(summary.Methods, m)
				}
			}
			if r.ZeroStatus == ZeroConfirmed {
				for _, m := range append(append([]string{}, r.Methods...), r.MethodsInferred...) {
					if !zeroSeen[m] {
						zeroSeen[m] = true
						summary.ZeroMethods = append(summary.ZeroMethods, m)
					}
				}
			}
		}
		if completedStates[r.State] || r.Status == StatusAvailable || r.Status == StatusNotReturned {
			completed++
		}
	}

	// 熔断状态优先（token_invalid / already_paid 直接作为总体状态）。
	for _, r := range routes {
		if r.Status == StatusTokenInvalid {
			summary.Status = StatusTokenInvalid
			return summary
		}
	}
	for _, r := range routes {
		if r.Status == StatusAlreadyPaid {
			summary.Status = StatusAlreadyPaid
			return summary
		}
	}

	switch {
	case anyMethods && completed >= totalRoutes:
		summary.Status = StatusAvailable
	case anyMethods:
		summary.Status = StatusPartial
	case completed >= totalRoutes:
		summary.Status = StatusNotReturned
	default:
		// 全部失败/未完成：取首个非空状态作为总体（便于排障）。
		if firstStatus != "" {
			summary.Status = firstStatus
		} else {
			summary.Status = StatusCheckoutFailed
		}
	}
	return summary
}
