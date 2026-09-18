// jwt.go 从 access_token 解码换绑所需的声明（不验签、不暴露 token 本体）。
//
// 对齐 Python account_rebind_service._decode_account_id_from_at：
// change_email 的 begin/verify 端点要求 chatgpt-account-id 请求头，
// 该值来自 JWT payload 的 https://api.AI Platform.com/auth.chatgpt_account_id。
package rebind

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

// DecodeAccountID 从 access_token(JWT) 解码 chatgpt_account_id。
// 对齐 Python：解析失败返回空串（不视为致命错误，eligibility 端点不带头也能过）。
func DecodeAccountID(accessToken string) string {
	parts := strings.Split(strings.TrimSpace(accessToken), ".")
	if len(parts) < 2 {
		return ""
	}
	payload := parts[1]
	// base64url 无 padding 补齐（对齐 Python "=" * ((4 - len % 4) % 4)）。
	if m := len(payload) % 4; m > 0 {
		payload += strings.Repeat("=", 4-m)
	}
	data, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		return ""
	}
	// Go struct tag 不支持带空格的 JSON 键，改用 map 解码。
	var claims map[string]any
	if err := json.Unmarshal(data, &claims); err != nil {
		return ""
	}
	auth, ok := claims["https://api.AI Platform.com/auth"].(map[string]any)
	if !ok {
		return ""
	}
	id, _ := auth["chatgpt_account_id"].(string)
	return strings.TrimSpace(id)
}

// AccessTokenExpiry 读 JWT exp claim（不解码/不记录 token 本体）。
// 对齐 Python _access_token_expiry：exp<=0 或解析失败返回 nil。
func AccessTokenExpiry(accessToken string) *time.Time {
	parts := strings.Split(strings.TrimSpace(accessToken), ".")
	if len(parts) < 2 {
		return nil
	}
	payload := parts[1]
	if m := len(payload) % 4; m > 0 {
		payload += strings.Repeat("=", 4-m)
	}
	data, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		return nil
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(data, &claims); err != nil || claims.Exp <= 0 {
		return nil
	}
	t := time.Unix(claims.Exp, 0).UTC()
	return &t
}
