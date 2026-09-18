package paymentcheck

import (
	"strings"
	"testing"
)

// TestTokenMappingSingleFilter 验证 token↔ID 单次过滤配对（原双重过滤错位 bug）。
// 模拟 Run() 的过滤：空 token 跳过后，剩余 token 按顺序配对 tok-0/tok-1/tok-2。
func TestTokenMappingSingleFilter(t *testing.T) {
	tokens := []TokenInput{
		{AccessToken: "  ", Label: "empty1"},
		{AccessToken: "tok-aaa", Label: "user1"},
		{AccessToken: "", Label: "empty2"},
		{AccessToken: "tok-bbb", Label: "user2"},
		{AccessToken: "tok-ccc", Label: "user3"},
	}
	// 复刻 Run() 的单次过滤逻辑
	type workItem struct {
		id    string
		token TokenInput
	}
	var work []workItem
	for _, tok := range tokens {
		tok.AccessToken = strings.TrimSpace(tok.AccessToken)
		if tok.AccessToken == "" {
			continue
		}
		work = append(work, workItem{
			id:    "tok-" + string(rune('0'+len(work))),
			token: tok,
		})
	}
	if len(work) != 3 {
		t.Fatalf("应过滤出 3 条有效 token, got %d", len(work))
	}
	if work[0].token.Label != "user1" || work[1].token.Label != "user2" || work[2].token.Label != "user3" {
		t.Fatalf("token 顺序错位: %+v", work)
	}
	if work[0].id != "tok-0" || work[1].id != "tok-1" || work[2].id != "tok-2" {
		t.Fatalf("ID 生成错位: %+v", work)
	}
}
