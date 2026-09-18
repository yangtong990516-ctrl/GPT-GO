// checkout.go 实现优惠 Custom Checkout 建单与支付方式读取
// （对齐支付类型检测实现说明 §8~§11）。
//
// 流程：
//
//	POST /backend-api/payments/checkout（promo_campaign=plus-1-month-free）
//	  → oaics_  会话：GET /backend-api/payments/checkout/{processor}/{sessionID}
//	  → cs_live_/cs_test_ 会话：POST api.stripe.com/v1/payment_pages/{id}/init
//
// 边界红线（与文档 §1.1 一致）：只建单并读取状态——不确认 Checkout、
// 不创建 PaymentMethod、不提交支付信息、不绑定支付方式、不扣款。
//
// Sentinel 是 best-effort：获取失败不阻断建单（文档 §7）。
package paymentcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"gpt-go/internal/service/signup/core"
	signupsentinel "gpt-go/internal/service/signup/sentinel"
	"gpt-go/internal/util"
)

// checkoutURL 是 Custom Checkout 建单端点（文档 §8）。
const checkoutURL = "https://chatgpt.com/backend-api/payments/checkout"

// checkoutStateBase 是 OAICS 状态接口基址（文档 §10）。
const checkoutStateBase = "https://chatgpt.com/backend-api/payments/checkout"

// stripeInitBase 是 Stripe Payment Page init 端点（文档 §11）。
const stripeInitBase = "https://api.stripe.com/v1/payment_pages"

// stripeAPIVersion 对齐文档 §11 的 _stripe_version。
const stripeAPIVersion = "2025-03-31.basil; checkout_server_update_beta=v1; checkout_manual_approval_preview=v1"

// freeTrialCampaignID 是免费试用活动 id（与 plan.go 的 plus-1-month-free 同源）。
const freeTrialCampaignID = "plus-1-month-free"

// sessionIDRe 从完整 JSON/URL 中正则提取会话 ID（文档 §9 兜底）。
var sessionIDRe = regexp.MustCompile(`\b(oaics_[A-Za-z0-9_-]+|cs_live_[A-Za-z0-9_-]+|cs_test_[A-Za-z0-9_-]+)\b`)

// publishableKeyRe 提取 Stripe publishable key（公开客户端标识，非私钥）。
var publishableKeyRe = regexp.MustCompile(`pk_(?:live|test)_[A-Za-z0-9]+`)

// knownStripePKs 是已知的 AI Platform Stripe shard publishable keys（文档 §11 兜底顺序 3）。
// publishable key 是公开客户端标识，可硬编码；按序尝试，首个 HTTP 200 生效。
var knownStripePKs = []string{
	"pk_live_TYo0CCXdFvCEaMqXOBeYTyMt00r1SjTuBq", // AI Platform 主 shard（线上观测值）
}

// CheckError 是建单/读取阶段的分类错误（对齐文档 §8.2 状态分类表）。
type CheckError struct {
	Status     string // token_invalid / already_paid / risk_blocked / rate_limited / checkout_rejected / checkout_failed
	HTTPStatus int
	Message    string // 已脱敏（≤280 字符）
}

func (e *CheckError) Error() string {
	if e.Message != "" {
		return e.Status + ": " + e.Message
	}
	return e.Status
}

// checkoutResult 是单线路建单+读取的完整产出。
type checkoutResult struct {
	SessionType    string // oaics_ / cs_live_ / cs_test_
	Methods        []string
	MethodsInferred []string
	AmountDue      *int64
	ZeroStatus     string
	HTTPStatus     int
	// ProcessorEntity 处理实体(processor_entity/processorEntity,回退 openai_llc/openai_ie)。
	ProcessorEntity string
}

// checkoutSession 抽象建单所需的 HTTP 能力（*core.Session 满足；测试注入替身）。
type checkoutSession interface {
	Get(ctx context.Context, url, referer string) (*core.Response, error)
	Post(ctx context.Context, url, referer, contentType string, body []byte) (*core.Response, error)
	PostWithHeaders(ctx context.Context, url, referer, contentType string, body []byte, extra map[string]string) (*core.Response, error)
}

// checkoutBody 生成建单请求体（文档 §8：billing_details 由线路国家/币种生成，
// promo_campaign 走 plus-1-month-free 活动）。
func checkoutBody(route Route) map[string]any {
	return map[string]any{
		"entry_point": "all_plans_pricing_modal",
		"plan_name":   "chatgptplusplan",
		"billing_details": map[string]any{
			"country":  route.Country,
			"currency": route.Currency,
		},
		"checkout_ui_mode": "custom",
		"promo_campaign": map[string]any{
			"promo_campaign_id":          freeTrialCampaignID,
			"is_coupon_from_query_param": false, // 文档 §8.1 已知上下文差异，保持与资格检测一致
		},
	}
}

// sentinelHeaderProvider 抽象 Sentinel 请求头获取（best-effort）。
// 实现方：复用 signup/sentinel 的 V8Solver；nil 时跳过（不阻断建单）。
type sentinelHeaderProvider interface {
	// CheckoutHeaders 返回建单所需的风控请求头（可能为空 map）。
	CheckoutHeaders(ctx context.Context, env signupsentinel.EnvPayload) map[string]string
}

// runCheckout 执行单条线路的建单 + 支付方式读取。
//
//	sess:        已绑定线路代理+指纹的 core.Session
//	accessToken: 账号 AT（Bearer）
//	route:       本线路（国家/币种/语言）
//	sentinel:    风控头提供者（可 nil，best-effort）
func runCheckout(ctx context.Context, sess checkoutSession, accessToken string, route Route, sentinel sentinelHeaderProvider, deviceID string) (*checkoutResult, *CheckError) {
	body, _ := json.Marshal(checkoutBody(route))

	extra := map[string]string{
		"Authorization":             "Bearer " + strings.TrimSpace(accessToken),
		"Content-Type":              "application/json",
		"Origin":                    "https://chatgpt.com",
		"x-AI Platform-target-path":  "/backend-api/payments/checkout",
		"x-AI Platform-target-route": "/backend-api/payments/checkout",
	}
	// Sentinel best-effort：有则写入，无则直接建单（文档 §7）。
	if sentinel != nil && deviceID != "" {
		env := signupsentinel.EnvPayload{
			DeviceID: deviceID,
			Language: route.Locale,
		}
		for k, v := range sentinel.CheckoutHeaders(ctx, env) {
			extra[k] = v
		}
	}

	resp, err := sess.PostWithHeaders(ctx, checkoutURL,
		"https://chatgpt.com/?promo_campaign="+freeTrialCampaignID,
		"application/json", body, extra)
	if err != nil {
		return nil, &CheckError{Status: "checkout_failed", Message: sanitizeError(err.Error())}
	}
	httpStatus := resp.StatusCode

	if httpStatus >= 400 {
		return nil, classifyCheckoutError(httpStatus, resp.Text())
	}

	var payload map[string]any
	if jerr := json.Unmarshal(resp.Bytes(), &payload); jerr != nil {
		return nil, &CheckError{Status: "checkout_failed", HTTPStatus: httpStatus, Message: "invalid_response"}
	}

	sessionID := extractSessionID(payload)
	if sessionID == "" {
		return nil, &CheckError{Status: "checkout_failed", HTTPStatus: httpStatus, Message: "invalid_checkout_session"}
	}

	out := &checkoutResult{HTTPStatus: httpStatus}
	switch {
	case strings.HasPrefix(sessionID, "oaics_"):
		out.SessionType = "oaics_"
		if cerr := readOAICS(ctx, sess, accessToken, route, payload, sessionID, out); cerr != nil {
			return nil, cerr
		}
	case strings.HasPrefix(sessionID, "cs_live_"), strings.HasPrefix(sessionID, "cs_test_"):
		out.SessionType = sessionID[:strings.Index(sessionID, "_")+1] // cs_live_ / cs_test_
		if cerr := readStripeInit(ctx, sess, route, payload, sessionID, out); cerr != nil {
			return nil, cerr
		}
	default:
		return nil, &CheckError{Status: "checkout_failed", HTTPStatus: httpStatus, Message: "不支持的会话类型"}
	}
	return out, nil
}

// getWithAuth 发起带 Bearer 的 GET（core.Session 的 Get 只带公共头，OAICS 状态
// 接口需要 Authorization——用 PostWithHeaders 同款的 extra 头机制，方法为 GET）。
func getWithAuth(ctx context.Context, sess checkoutSession, url, referer, accessToken string) (*core.Response, error) {
	if gs, ok := sess.(interface {
		GetWithHeaders(ctx context.Context, url, referer string, extra map[string]string) (*core.Response, error)
	}); ok {
		return gs.GetWithHeaders(ctx, url, referer, map[string]string{
			"Authorization": "Bearer " + strings.TrimSpace(accessToken),
		})
	}
	// 替身会话（测试）没有 GetWithHeaders：退回普通 Get（测试不校验 Authorization）。
	return sess.Get(ctx, url, referer)
}

// readOAICS 读取 AI Platform Custom Checkout 状态（文档 §10）。
// 支付方式同时从建单响应与状态接口读取并合并；状态读取失败但建单响应已明确
// 支付方式时，保留建单结果不把检测判失败。
func readOAICS(ctx context.Context, sess checkoutSession, accessToken string, route Route, checkoutPayload map[string]any, sessionID string, out *checkoutResult) *CheckError {
	merged := map[string]bool{}
	var inferred []string
	mergeFrom := func(payload any) {
		pr := ParsePaymentMethods(payload, route.Country)
		for _, m := range pr.Methods {
			merged[m] = true
		}
		inferred = appendUniqueAll(inferred, pr.Inferred)
	}
	mergeFrom(checkoutPayload)

	// processor_entity：建单响应优先；缺失时 美国→openai_llc，其他→openai_ie（文档 §10）。
	entity := strOfAny(checkoutPayload, "processor_entity")
	if entity == "" {
		entity = strOfAny(checkoutPayload, "processorEntity")
	}
	if entity == "" {
		if route.Country == "US" {
			entity = "openai_llc"
		} else {
			entity = "openai_ie"
		}
	}
	out.ProcessorEntity = entity

	stateURL := fmt.Sprintf("%s/%s/%s", checkoutStateBase, entity, sessionID)
	stateResp, err := getWithAuth(ctx, sess, stateURL,
		fmt.Sprintf("https://chatgpt.com/checkout/%s/%s", entity, sessionID),
		accessToken)
	if err == nil && stateResp.StatusCode == 200 {
		var statePayload map[string]any
		if json.Unmarshal(stateResp.Bytes(), &statePayload) == nil {
			mergeFrom(statePayload)
			// 金额以状态接口为准（更完整的嵌套快照）。
			obs := ObserveAmounts(statePayload)
			out.ZeroStatus, out.AmountDue = JudgeZero(obs)
		}
	} else if err == nil && stateResp.StatusCode == 401 {
		return &CheckError{Status: "token_invalid", HTTPStatus: 401, Message: "OAICS 状态读取 401"}
	}
	// 状态接口失败但建单已有渠道 → 保留建单结果（文档 §10 合并规则）。

	if out.ZeroStatus == "" {
		obs := ObserveAmounts(checkoutPayload)
		out.ZeroStatus, out.AmountDue = JudgeZero(obs)
	}
	out.Methods = keysOfSet(merged)
	out.MethodsInferred = inferred
	return nil
}

// readStripeInit 读取 Stripe Hosted Checkout 的 payment_pages/{id}/init（文档 §11）。
func readStripeInit(ctx context.Context, sess checkoutSession, route Route, checkoutPayload map[string]any, sessionID string, out *checkoutResult) *CheckError {
	keys := stripePublishableKeys(checkoutPayload)
	if len(keys) == 0 {
		return &CheckError{Status: "checkout_failed", HTTPStatus: out.HTTPStatus, Message: "no_publishable_key"}
	}

	var lastErr string
	for _, pk := range keys {
		form := fmt.Sprintf(
			"browser_locale=%s&key=%s&_stripe_version=%s",
			urlQueryEscape(route.Locale), pk, urlQueryEscape(stripeAPIVersion),
		)
		initURL := fmt.Sprintf("%s/%s/init", stripeInitBase, sessionID)
		resp, err := sess.PostWithHeaders(ctx, initURL, "https://js.stripe.com/",
			"application/x-www-form-urlencoded", []byte(form), map[string]string{
				"Origin": "https://js.stripe.com",
			})
		if err != nil {
			lastErr = sanitizeError(err.Error())
			continue
		}
		if resp.StatusCode != 200 {
			lastErr = fmt.Sprintf("stripe_init_http_%d", resp.StatusCode)
			continue
		}
		var payload map[string]any
		if json.Unmarshal(resp.Bytes(), &payload) != nil {
			lastErr = "stripe_init_invalid_json"
			continue
		}
		// 首个 HTTP 200 的响应进入支付方式解析（文档 §11）。
		pr := ParsePaymentMethods(payload, route.Country)
		out.Methods = pr.Methods
		out.MethodsInferred = pr.Inferred
		obs := ObserveAmounts(payload)
		out.ZeroStatus, out.AmountDue = JudgeZero(obs)
		out.HTTPStatus = 200
		// Stripe Hosted Checkout 的处理实体即 Stripe。
		out.ProcessorEntity = "stripe"
		return nil
	}
	return &CheckError{Status: "checkout_failed", HTTPStatus: out.HTTPStatus, Message: lastErr}
}

// classifyCheckoutError 按 HTTP 状态分类建单错误（对齐文档 §8.2 表）。
func classifyCheckoutError(status int, body string) *CheckError {
	lower := strings.ToLower(body)
	switch {
	case status == 400 && strings.Contains(lower, "already") && strings.Contains(lower, "paid"):
		return &CheckError{Status: "already_paid", HTTPStatus: status, Message: sanitizeError(extractErrorMessage(body))}
	case status == 400:
		return &CheckError{Status: "checkout_rejected", HTTPStatus: status, Message: sanitizeError(extractErrorMessage(body))}
	case status == 401:
		return &CheckError{Status: "token_invalid", HTTPStatus: status}
	case status == 403:
		return &CheckError{Status: "risk_blocked", HTTPStatus: status}
	case status == 429:
		return &CheckError{Status: "rate_limited", HTTPStatus: status}
	default:
		return &CheckError{Status: "checkout_failed", HTTPStatus: status, Message: sanitizeError(extractErrorMessage(body))}
	}
}

// extractSessionID 按文档 §9 的顺序提取会话 ID：
// checkout_session_id → checkoutSessionId → session_id → checkout_session.id →
// checkout_session.checkout_session_id → 完整 JSON 正则兜底。
func extractSessionID(payload map[string]any) string {
	for _, key := range []string{"checkout_session_id", "checkoutSessionId", "session_id", "id"} {
		if s := strOfAny(payload, key); isCheckoutSessionID(s) {
			return s
		}
	}
	if nested, ok := payload["checkout_session"].(map[string]any); ok {
		for _, key := range []string{"id", "checkout_session_id"} {
			if s := strOfAny(nested, key); isCheckoutSessionID(s) {
				return s
			}
		}
	}
	if blob, err := json.Marshal(payload); err == nil {
		if m := sessionIDRe.FindSubmatch(blob); len(m) >= 2 {
			return string(m[1])
		}
	}
	return ""
}

// isCheckoutSessionID 判断字符串是否是支持的会话 ID（oaics_/cs_live_/cs_test_）。
func isCheckoutSessionID(s string) bool {
	return strings.HasPrefix(s, "oaics_") ||
		strings.HasPrefix(s, "cs_live_") ||
		strings.HasPrefix(s, "cs_test_")
}

// envStripePKs 读环境变量 PAYMENT_STRIPE_PUBLISHABLE_KEYS（文档 §11 顺序 2）。
func envStripePKs() string {
	return util.EnvOrDefault("PAYMENT_STRIPE_PUBLISHABLE_KEYS", "")
}

// stripePublishableKeys 按文档 §11 的顺序收集 publishable key：
// 建单响应 → 环境变量 PAYMENT_STRIPE_PUBLISHABLE_KEYS → 已知 shard keys。
func stripePublishableKeys(payload map[string]any) []string {
	var out []string
	addPK := func(s string) {
		if m := publishableKeyRe.FindString(s); m != "" {
			out = appendUnique(out, m)
		}
	}
	if blob, err := json.Marshal(payload); err == nil {
		for _, m := range publishableKeyRe.FindAllString(string(blob), -1) {
			out = appendUnique(out, m)
		}
	}
	for _, k := range strings.Split(envStripePKs(), ",") {
		addPK(strings.TrimSpace(k))
	}
	for _, k := range knownStripePKs {
		out = appendUnique(out, k)
	}
	return out
}

// extractErrorMessage 仅从 error/message/detail/reason/code 字段提取错误详情
// （文档 §8.2：清除 Bearer/JWT、压缩空白、限长 280 字符）。
func extractErrorMessage(body string) string {
	var payload map[string]any
	if json.Unmarshal([]byte(body), &payload) == nil {
		for _, key := range []string{"error", "message", "detail", "reason", "code"} {
			if s := strOfAny(payload, key); s != "" {
				return s
			}
		}
	}
	if len(body) > 280 {
		return body[:280]
	}
	return body
}

// sanitizeError 脱敏 + 限长（清 Bearer/JWT 样式 token、压缩空白、≤280 字符）。
var bearerRe = regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._~+/=-]+`)
var jwtRe = regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]*`)
var whitespaceRe = regexp.MustCompile(`\s+`)

func sanitizeError(s string) string {
	s = bearerRe.ReplaceAllString(s, "bearer_***")
	s = jwtRe.ReplaceAllString(s, "jwt_***")
	s = whitespaceRe.ReplaceAllString(strings.TrimSpace(s), " ")
	if len(s) > 280 {
		s = s[:280]
	}
	return s
}

func keysOfSet(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	return out
}

func appendUniqueAll(xs []string, more []string) []string {
	for _, m := range more {
		xs = appendUnique(xs, m)
	}
	return xs
}

func urlQueryEscape(s string) string {
	r := strings.NewReplacer(
		"%", "%25", " ", "%20", "&", "%26", "=", "%3D", "+", "%2B",
		";", "%3B", "/", "%2F", "?", "%3F", "#", "%23", "@", "%40",
	)
	return r.Replace(s)
}
