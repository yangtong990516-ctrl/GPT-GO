// Package remail implements the ReMail 接码平台 service, mirroring reference
// remail_service.py 1:1. It is fully independent of mailcode/sms.
package remail

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"gpt-go/internal/model"
	"gpt-go/internal/store"
	"gpt-go/internal/util"
)

const (
	configKey = "remail_config"
	apiBase   = "https://remail.aishop6.com"
)

// resolveBase mirrors Python's `(raw or API_BASE).rstrip("/")`: fall back to
// apiBase when empty, then strip trailing slashes. Single source of truth for
// base-URL resolution (previously duplicated across 5 call sites). Note that
// get_config intentionally does NOT rstrip (see GetConfig).
func resolveBase(raw string) string {
	if raw == "" {
		raw = apiBase
	}
	return strings.TrimRight(raw, "/")
}

// EmailUpserter is the minimal contract remail needs from the email pool.
type EmailUpserter interface {
	UpsertMailbox(ctx context.Context, email, accessURL, mailboxKind, sourceType string) (bool, error)
}

// httpDo performs an outbound HTTP request and returns status + raw body.
// Injectable for tests; the default uses util.HTTPClient.
type httpDo func(ctx context.Context, method, url string, headers map[string]string, body any) (int, []byte, error)

// Service mirrors remail_service.RemailService.
type Service struct {
	store  store.RemailStore
	upsert EmailUpserter
	do     httpDo
}

// NewService returns a remail Service using the default HTTP executor.
func NewService(s store.RemailStore, up EmailUpserter) *Service {
	return &Service{store: s, upsert: up, do: defaultHTTPDo}
}

// defaultHTTPDo uses the unified util.HTTPClient.
func defaultHTTPDo(ctx context.Context, method, rawURL string, headers map[string]string, body any) (int, []byte, error) {
	client := util.NewHTTPClient(0, "")
	req, err := client.NewRequest(ctx, method, rawURL, body)
	if err != nil {
		return 0, nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return client.DoRaw(req)
}

// GetConfig mirrors get_config (remail_service.py:82).
func (s *Service) GetConfig(ctx context.Context) (model.RemailConfig, error) {
	doc, err := s.store.Load(ctx)
	if err != nil {
		return model.RemailConfig{}, err
	}
	base := doc.BaseURL
	if base == "" {
		base = apiBase
	}
	return model.RemailConfig{
		APIKey:      doc.APIKey,
		ProjectID:   doc.ProjectID,
		EmailSuffix: doc.EmailSuffix,
		BaseURL:     base,
		UpdatedAt:   doc.UpdatedAt,
	}, nil
}

// SaveConfig mirrors save_config (remail_service.py:92).
func (s *Service) SaveConfig(ctx context.Context, in model.RemailConfigInput) (model.RemailConfig, error) {
	projectID := in.ProjectID
	now := time.Now().UTC()
	base := resolveBase(in.BaseURL)
	doc := store.RemailConfigDocument{
		APIKey:      strings.TrimSpace(in.APIKey),
		ProjectID:   &projectID,
		EmailSuffix: normalizeSuffix(in.EmailSuffix),
		BaseURL:     base,
		UpdatedAt:   &now,
	}
	if err := s.store.Save(ctx, doc); err != nil {
		return model.RemailConfig{}, err
	}
	return s.GetConfig(ctx)
}

// BuildAccessURL mirrors build_access_url (remail_service.py:137).
// Returns base + "/v1/pickup?" + urlencode({"email": email, "token": token}).
func BuildAccessURL(baseURL, email, token string) string {
	base := resolveBase(baseURL)
	q := url.Values{
		"email": {normalizeEmail(email)},
		"token": {token},
	}.Encode()
	return base + "/v1/pickup?" + q
}

// Probe mirrors probe (remail_service.py:432). GET /v1/open/apikey/profile.
func (s *Service) Probe(ctx context.Context, apiKey string) (model.RemailProbeResult, error) {
	incoming := strings.TrimSpace(apiKey)
	doc, err := s.store.Load(ctx)
	if err != nil {
		return model.RemailProbeResult{}, err
	}
	key := incoming
	if key == "" {
		key = doc.APIKey
	}
	base := resolveBase(doc.BaseURL)
	status, _, err := s.do(ctx, http.MethodGet, base+"/v1/open/apikey/profile", map[string]string{
		"Authorization": "Bearer " + key,
		"Accept":        "application/json",
	}, nil)
	if err != nil {
		return model.RemailProbeResult{OK: false, Message: err.Error(), Reachable: false}, nil
	}
	ok := status == http.StatusOK
	msg := "ReMail API 可达（HTTP " + strconv.Itoa(status) + "）"
	if ok {
		msg = "ReMail API Key 有效，连接正常"
	}
	return model.RemailProbeResult{OK: ok, Message: msg, Reachable: true}, nil
}

// GetBalance mirrors get_balance (remail_service.py:415). GET /v1/open/wallet.
func (s *Service) GetBalance(ctx context.Context) (model.RemailWallet, error) {
	doc, err := s.store.Load(ctx)
	if err != nil {
		return model.RemailWallet{}, err
	}
	base := resolveBase(doc.BaseURL)
	headers, aerr := s.authHeaders(doc)
	if aerr != nil {
		// Python get_balance catches all exceptions -> ok=false, message=str(exc).
		return model.RemailWallet{OK: false, Message: aerr.Error()}, nil
	}
	status, body, err := s.do(ctx, http.MethodGet, base+"/v1/open/wallet", headers, nil)
	if err != nil {
		return model.RemailWallet{OK: false, Message: err.Error()}, nil
	}
	if status != http.StatusOK {
		return model.RemailWallet{OK: false, Message: "ReMail 请求失败 HTTP " + strconv.Itoa(status)}, nil
	}
	// The wallet endpoint returns these three fields as strings (e.g. "25.00").
	// Decode into a typed struct; a missing/non-string field yields "" (matching
	// Python's str(data.get(...) or "")).
	var data struct {
		ConsumerBalance string `json:"consumerBalance"`
		TotalRecharged  string `json:"totalRecharged"`
		HistoricalSpend string `json:"historicalSpend"`
	}
	if len(body) > 0 {
		if err := util.UnmarshalLenient(body, &data); err != nil {
			util.Logger().Warn("remail 响应解析失败", zap.Error(err))
		}
	}
	return model.RemailWallet{
		OK:              true,
		Message:         "",
		ConsumerBalance: data.ConsumerBalance,
		TotalRecharged:  data.TotalRecharged,
		HistoricalSpend: data.HistoricalSpend,
	}, nil
}

// ImportPurchasedOrders mirrors import_purchased_orders (remail_service.py:146).
func (s *Service) ImportPurchasedOrders(ctx context.Context, in model.RemailImportRequest) ([]model.RemailMailboxRecord, error) {
	doc, err := s.store.Load(ctx)
	if err != nil {
		return nil, err
	}
	base := resolveBase(doc.BaseURL)

	maxOrders := in.MaxOrders
	if maxOrders < 0 {
		maxOrders = 0
	}
	wantType := strings.ToLower(strings.TrimSpace(in.ProductType))
	if in.OnlyIcloud && wantType == "" {
		wantType = "icloud"
	}
	wantSuffix := strings.ToLower(strings.TrimLeft(strings.TrimSpace(in.EmailSuffix), "@"))

	invalid := map[string]struct{}{"refunded": {}, "failed": {}, "closed": {}}
	limit := in.Limit
	if limit < 1 {
		limit = 100
	}
	if limit > 100 {
		limit = 100
	}

	type orderItem struct {
		orderNo       string
		deliveryEmail string
		serviceToken  string
		productType   string
		status        string
	}
	var orders []orderItem
	afterID := ""
	headers, aerr := s.authHeaders(doc)
	if aerr != nil {
		return nil, aerr
	}
	for page := 0; page < 100; page++ {
		url := base + "/v1/open/orders?limit=" + strconv.Itoa(limit) + "&serviceMode=purchase"
		if afterID != "" {
			url += "&afterId=" + afterID
		}
		status, body, gerr := s.do(ctx, http.MethodGet, url, headers, nil)
		if gerr != nil {
			return nil, &util.HTTPError{Code: "remail_net_error", Message: "ReMail 网络错误: " + gerr.Error(), Status: 502}
		}
		if status != http.StatusOK {
			return nil, &util.HTTPError{Code: "remail_http_error", Message: "ReMail 请求失败 HTTP " + strconv.Itoa(status) + ": " + truncate(string(body), 200), Status: 502}
		}
		var pageData struct {
			Items []map[string]any `json:"items"`
		}
		if err := util.UnmarshalLenient(body, &pageData); err != nil {
			util.Logger().Warn("remail 响应解析失败", zap.Error(err))
		}
		items := pageData.Items
		if len(items) == 0 {
			break
		}
		for _, o := range items {
			st := strVal(o["status"])
			if _, bad := invalid[st]; bad {
				continue
			}
			pt := strings.ToLower(strVal(o["productType"]))
			if wantType != "" && pt != wantType {
				continue
			}
			em := normalizeEmail(strVal(o["deliveryEmail"]))
			if wantSuffix != "" && !strings.HasSuffix(em, "@"+wantSuffix) {
				continue
			}
			orders = append(orders, orderItem{
				orderNo:       strVal(o["orderNo"]),
				deliveryEmail: em,
				serviceToken:  strVal(o["serviceToken"]),
				productType:   pt,
				status:        st,
			})
		}
		if maxOrders > 0 && len(orders) >= maxOrders {
			break
		}
		if len(items) < limit {
			break
		}
		afterID = strVal(items[len(items)-1]["id"])
		if afterID == "" {
			break
		}
	}
	if maxOrders > 0 && len(orders) > maxOrders {
		orders = orders[:maxOrders]
	}

	// 2. fetch serviceToken in parallel (semaphore = 8). Since the default HTTP
	// executor is synchronous, we iterate; the mock/tests inject a fast executor.
	records := make([]model.RemailMailboxRecord, 0, len(orders))
	for _, o := range orders {
		email := o.deliveryEmail
		if email == "" {
			continue
		}
		token := o.serviceToken
		if token == "" && o.orderNo != "" {
			status, body, gerr := s.do(ctx, http.MethodGet, base+"/v1/open/orders/"+o.orderNo, headers, nil)
			if gerr != nil {
				msg := gerr.Error()
				records = append(records, model.RemailMailboxRecord{Email: email, AccessURL: "", Imported: false, OrderNo: o.orderNo, Error: &msg})
				continue
			}
			if status == http.StatusOK {
				var detail map[string]any
				if err := util.UnmarshalLenient(body, &detail); err != nil {
					util.Logger().Warn("remail 响应解析失败", zap.Error(err))
				}
				token = strVal(detail["serviceToken"])
			}
		}
		if token == "" {
			msg := "订单无 serviceToken: " + o.orderNo
			records = append(records, model.RemailMailboxRecord{Email: email, AccessURL: "", Imported: false, OrderNo: o.orderNo, Error: &msg})
			continue
		}
		accessURL := BuildAccessURL(base, email, token)
		inserted, uerr := s.upsert.UpsertMailbox(ctx, email, accessURL, "url", "remail")
		if uerr != nil {
			msg := uerr.Error()
			records = append(records, model.RemailMailboxRecord{Email: email, AccessURL: "", Imported: false, OrderNo: o.orderNo, Error: &msg})
			continue
		}
		records = append(records, model.RemailMailboxRecord{
			Email:     email,
			AccessURL: accessURL,
			Imported:  inserted,
			Duplicate: !inserted,
			OrderNo:   o.orderNo,
		})
	}
	return records, nil
}

// CreateMailboxes mirrors create_mailboxes (remail_service.py:354).
func (s *Service) CreateMailboxes(ctx context.Context, in model.RemailMailboxCreate) ([]model.RemailMailboxRecord, error) {
	doc, err := s.store.Load(ctx)
	if err != nil {
		return nil, err
	}
	base := resolveBase(doc.BaseURL)
	count := in.Count
	if count < 1 {
		count = 1
	}
	if count > 1000 {
		count = 1000
	}
	suffix := strings.TrimSpace(in.EmailSuffix)
	if suffix == "" {
		suffix = doc.EmailSuffix
	}

	records := make([]model.RemailMailboxRecord, 0, count)
	remaining := count
	for remaining > 0 {
		chunk := min(remaining, 100)

		var orders []model.RemailOrderResult
		var oerr error
		if chunk == 1 {
			// Batch endpoint requires quantity >= 2; a single remaining order
			// goes through the single-order endpoint (place_order).
			var o *model.RemailOrderResult
			o, oerr = s.placeOrder(ctx, doc, base, suffix)
			if o != nil {
				orders = []model.RemailOrderResult{*o}
			}
		} else {
			orders, oerr = s.placeOrders(ctx, doc, base, chunk, suffix)
		}
		if oerr != nil {
			msg := oerr.Error()
			records = append(records, model.RemailMailboxRecord{Email: "", AccessURL: "", Imported: false, Error: &msg})
			break
		}
		for _, order := range orders {
			if order.DeliveryEmail == "" || order.ServiceToken == "" {
				msg := "下单返回缺邮箱/token: " + order.OrderNo
				if order.OrderNo == "" {
					msg = "下单返回缺邮箱/token: " + order.Status
				}
				records = append(records, model.RemailMailboxRecord{Email: "", AccessURL: "", Imported: false, OrderNo: order.OrderNo, Error: &msg})
				continue
			}
			email := normalizeEmail(order.DeliveryEmail)
			accessURL := BuildAccessURL(base, email, order.ServiceToken)
			inserted, uerr := s.upsert.UpsertMailbox(ctx, email, accessURL, "url", "remail")
			if uerr != nil {
				msg := uerr.Error()
				records = append(records, model.RemailMailboxRecord{Email: email, AccessURL: "", Imported: false, OrderNo: order.OrderNo, Error: &msg})
				continue
			}
			records = append(records, model.RemailMailboxRecord{
				Email:     email,
				AccessURL: accessURL,
				Imported:  inserted,
				Duplicate: !inserted,
				OrderNo:   order.OrderNo,
			})
		}
		remaining -= chunk
	}
	return records, nil
}

// placeOrder places a single order via /v1/open/orders, mirroring
// place_order (remail_service.py:251). Used when exactly one order remains
// (the batch endpoint requires quantity >= 2).
func (s *Service) placeOrder(ctx context.Context, doc store.RemailConfigDocument, base string, suffix string) (*model.RemailOrderResult, error) {
	if doc.ProjectID == nil {
		return nil, &util.HTTPError{Code: "remail_no_project", Message: "ReMail 项目 projectId 未配置", Status: 400}
	}
	if strings.TrimSpace(suffix) == "" {
		return nil, &util.HTTPError{Code: "remail_no_suffix", Message: "ReMail 邮箱后缀 emailSuffix 未配置", Status: 400}
	}

	idemKey := "rk-auto-" + randomHex(16)
	payload := map[string]any{
		"projectId":   *doc.ProjectID,
		"emailSuffix": strings.TrimSpace(suffix),
	}
	headers, aerr := s.authHeaders(doc)
	if aerr != nil {
		return nil, aerr
	}
	headers["Idempotency-Key"] = idemKey
	status, body, err := s.do(ctx, http.MethodPost, base+"/v1/open/orders?serviceMode=purchase", headers, payload)
	if err != nil {
		return nil, &util.HTTPError{Code: "remail_order_net", Message: "ReMail 下单网络错误: " + err.Error(), Status: 502}
	}
	if status != 200 && status != 201 {
		return nil, &util.HTTPError{Code: "remail_order_failed", Message: "ReMail 下单失败 HTTP " + strconv.Itoa(status) + ": " + truncate(string(body), 200), Status: 502}
	}
	var data struct {
		OrderNo          string `json:"orderNo"`
		DeliveryEmail    string `json:"deliveryEmail"`
		ServiceToken     string `json:"serviceToken"`
		VerificationCode string `json:"verificationCode"`
		Status           string `json:"status"`
	}
	if len(body) > 0 {
		if err := util.UnmarshalLenient(body, &data); err != nil {
			util.Logger().Warn("remail 响应解析失败", zap.Error(err))
		}
	}
	return &model.RemailOrderResult{
		OrderNo:          data.OrderNo,
		DeliveryEmail:    data.DeliveryEmail,
		ServiceToken:     data.ServiceToken,
		VerificationCode: data.VerificationCode,
		Status:           data.Status,
	}, nil
}

// placeOrders places orders via the batch endpoint, mirroring
// place_order_batch (remail_service.py:289). quantity must be in [2, 100].
func (s *Service) placeOrders(ctx context.Context, doc store.RemailConfigDocument, base string, quantity int, suffix string) ([]model.RemailOrderResult, error) {
	if doc.ProjectID == nil {
		return nil, &util.HTTPError{Code: "remail_no_project", Message: "ReMail 项目 projectId 未配置", Status: 400}
	}
	if strings.TrimSpace(suffix) == "" {
		return nil, &util.HTTPError{Code: "remail_no_suffix", Message: "ReMail 邮箱后缀 emailSuffix 未配置", Status: 400}
	}
	if quantity < 2 || quantity > 100 {
		return nil, &util.HTTPError{Code: "remail_batch_qty", Message: "ReMail 批量下单数量须在 2~100 之间", Status: 400}
	}

	idemKey := "rk-batch-" + randomHex(16)
	payload := map[string]any{
		"projectId":   *doc.ProjectID,
		"emailSuffix": strings.TrimSpace(suffix),
		"quantity":    quantity,
	}
	headers, aerr := s.authHeaders(doc)
	if aerr != nil {
		return nil, aerr
	}
	headers["Idempotency-Key"] = idemKey
	status, body, err := s.do(ctx, http.MethodPost, base+"/v1/open/orders/batch?serviceMode=purchase&supply=private_first", headers, payload)
	if err != nil {
		return nil, &util.HTTPError{Code: "remail_order_net", Message: "ReMail 批量下单网络错误: " + err.Error(), Status: 502}
	}
	if status != 200 && status != 201 && status != 207 {
		return nil, &util.HTTPError{Code: "remail_order_failed", Message: "ReMail 批量下单失败 HTTP " + strconv.Itoa(status) + ": " + truncate(string(body), 200), Status: 502}
	}
	var items []map[string]any
	if len(body) > 0 {
		if err := util.UnmarshalLenient(body, &items); err != nil {
			util.Logger().Warn("remail 响应解析失败", zap.Error(err))
		}
	}
	results := make([]model.RemailOrderResult, 0, len(items))
	for _, item := range items {
		order, _ := item["order"].(map[string]any)
		if strVal(item["status"]) == "succeeded" && order != nil {
			results = append(results, model.RemailOrderResult{
				OrderNo:          strVal(order["orderNo"]),
				DeliveryEmail:    strVal(order["deliveryEmail"]),
				ServiceToken:     strVal(order["serviceToken"]),
				VerificationCode: strVal(order["verificationCode"]),
				Status:           strVal(order["status"]),
			})
			continue
		}
		errObj, _ := item["error"].(map[string]any)
		code := strVal(errObj["code"])
		msg := strVal(errObj["message"])
		if msg == "" {
			msg = "未知"
		}
		if code == "insufficient_balance" || code == "insufficient_inventory" {
			reason := "ReMail 余额不足，请先到 ReMail 充值"
			if code == "insufficient_inventory" {
				reason = "ReMail 该后缀库存不足"
			}
			return nil, &util.HTTPError{Code: "remail_balance_low", Message: reason + "（" + code + ": " + msg + "）", Status: 402}
		}
		results = append(results, model.RemailOrderResult{Status: "failed:" + msg})
	}
	return results, nil
}

// authHeaders mirrors _auth_headers (remail_service.py:108). It returns an
// error when the API key is empty (remail_no_api_key, 400).
func (s *Service) authHeaders(doc store.RemailConfigDocument) (map[string]string, error) {
	key := strings.TrimSpace(doc.APIKey)
	if key == "" {
		return nil, &util.HTTPError{Code: "remail_no_api_key", Message: "ReMail API Key 未配置", Status: 400}
	}
	return map[string]string{
		"Authorization": "Bearer " + key,
		"Content-Type":  "application/json",
		"Accept":        "application/json",
	}, nil
}

// normalizeSuffix mirrors the emailSuffix normalization in save_config:
// strip -> lower -> lstrip("@") -> if "@" keep part after last "@".
func normalizeSuffix(value string) string {
	s := strings.ToLower(strings.TrimSpace(value))
	s = strings.TrimLeft(s, "@")
	if idx := strings.LastIndex(s, "@"); idx >= 0 {
		s = strings.TrimSpace(s[idx+1:])
	}
	return s
}

// normalizeEmail mirrors remail_service.normalize_email (strip+lower).
func normalizeEmail(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// randomHex mirrors secrets.token_hex(n) -> hex string.
func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// truncate limits a string to n bytes (for error message bodies).
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// strVal safely extracts a string from an any map value.
//
// JSON 数字被 UnmarshalLenient 解为 float64,fmt.Sprint 对 7 位及以上整数会输出
// 科学计数法(如 6569953 -> "6.569953e+06"),用作 afterId/orderNo 会被 Remail 判为
// 非法参数(HTTP 400)。故对 float64 的整数值用十进制无科学计数格式。
func strVal(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	if f, ok := v.(float64); ok {
		if f == math.Trunc(f) && !math.IsInf(f, 0) {
			return strconv.FormatInt(int64(f), 10)
		}
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return fmt.Sprint(v)
}
