package rebind

import (
	"context"
	"testing"
	"time"

	"gpt-go/internal/store"
)

// TestDecodeAccountID 验证 JWT 解码（对齐 Python _decode_account_id_from_at）。
func TestDecodeAccountID(t *testing.T) {
	// 构造一个最小 JWT：payload 含 https://api.AI Platform.com/auth.chatgpt_account_id。
	// {"https://api.AI Platform.com/auth":{"chatgpt_account_id":"acc-123"}}
	// {"https://api.AI Platform.com/auth":{"chatgpt_account_id":"acc-123"}} 的 base64url 编码。
	payload := `eyJodHRwczovL2FwaS5BSSBQbGF0Zm9ybS5jb20vYXV0aCI6eyJjaGF0Z3B0X2FjY291bnRfaWQiOiJhY2MtMTIzIn19`
	token := "header." + payload + ".sig"
	if got := DecodeAccountID(token); got != "acc-123" {
		t.Fatalf("应解出 acc-123, got %q", got)
	}
	// 非法 token → 空串。
	if got := DecodeAccountID("not-a-jwt"); got != "" {
		t.Fatalf("非法 token 应空串, got %q", got)
	}
}

// TestAccessTokenExpiry 验证 exp 解析。
func TestAccessTokenExpiry(t *testing.T) {
	// {"exp": 2000000000} = 2033-05-18
	payload := `eyJleHAiOjIwMDAwMDAwMDB9`
	token := "h." + payload + ".s"
	exp := AccessTokenExpiry(token)
	if exp == nil {
		t.Fatal("应解出 exp")
	}
	if exp.Unix() != 2000000000 {
		t.Fatalf("exp 应 2000000000, got %d", exp.Unix())
	}
	// exp<=0 → nil。
	payload = `eyJleHAiOjB9`
	if AccessTokenExpiry("h."+payload+".s") != nil {
		t.Fatal("exp=0 应 nil")
	}
}

// TestAppendWaitParam 验证 wait 参数合并保留原 query（对齐 Python _append_wait_param）。
func TestAppendWaitParam(t *testing.T) {
	u := appendWaitParam("https://mail.local/api?email=a%40b.com", 5)
	if u != "https://mail.local/api?email=a%40b.com&wait=5" {
		t.Fatalf("应保留 email 参数, got %s", u)
	}
}

// TestCodeCandidates 验证验证码提取（简单响应 + 多邮件列表）。
func TestCodeCandidates(t *testing.T) {
	// 简单响应：顶层单 code。
	simple := map[string]any{"code": "123456", "success": true}
	cands := codeCandidates(simple, 0)
	if len(cands) != 1 || cands[0].code != "123456" {
		t.Fatalf("简单响应应提取 123456, got %+v", cands)
	}
	// 多邮件列表：按时间戳过滤。
	list := map[string]any{
		"messages": []any{
			map[string]any{"code": "111111", "received_at": float64(1000)},
			map[string]any{"code": "222222", "received_at": float64(2000)},
		},
	}
	cands = codeCandidates(list, 0)
	if len(cands) != 2 {
		t.Fatalf("应提取 2 个候选, got %d", len(cands))
	}
}

// TestRebindStoreFlow 端到端 store 流：认领 → 换绑落库 → 成功。
func TestRebindStoreFlow(t *testing.T) {
	m := store.NewMockAccountStore()
	_, _ = m.Create(context.Background(), store.AccountDocument{
		ID: "a1", Email: "old@x.com", EmailNormalized: "old@x.com",
	})

	// 认领。
	d, err := m.ClaimRebind(context.Background(), "a1", 5)
	if err != nil || d == nil {
		t.Fatalf("认领应成功: %v", err)
	}
	if d.RebindStatus == nil || *d.RebindStatus != "running" {
		t.Fatalf("认领后应 running, got %v", d.RebindStatus)
	}
	// 未卡死时重复认领应拒绝。
	d2, _ := m.ClaimRebind(context.Background(), "a1", 5)
	if d2 != nil {
		t.Fatal("未卡死时重复认领应拒绝")
	}

	// 换绑落库。
	reboundAt := time.Now().UTC()
	_ = m.MarkRebindEmailChanged(context.Background(), "a1", store.RebindEmailChangedUpdate{
		NewEmail: "new@x.com", NewAccessURL: "https://mail/new",
		PreviousEmail: "old@x.com", ProxyCountry: "VN", ReboundAt: reboundAt,
	})
	got, _ := m.Get(context.Background(), "a1")
	if got.Email != "new@x.com" || got.RebindStatus == nil || *got.RebindStatus != "email_changed_token_pending" {
		t.Fatalf("换绑落库失败: %+v", got)
	}

	// 成功落库。
	_ = m.MarkRebindSuccess(context.Background(), "a1", store.RebindSuccessUpdate{
		RebindEmailChangedUpdate: store.RebindEmailChangedUpdate{
			NewEmail: "new@x.com", PreviousEmail: "old@x.com", ReboundAt: reboundAt,
		},
		AccessToken: "tok-new",
	})
	got, _ = m.Get(context.Background(), "a1")
	if got.RebindStatus == nil || *got.RebindStatus != "success" || got.AccessToken != "tok-new" {
		t.Fatalf("成功落库失败: %+v", got)
	}
}

// TestEmailReserveConsume 邮箱预留/消费/释放流。
func TestEmailReserveConsume(t *testing.T) {
	m := store.NewMockEmailStore()
	_, _ = m.Upsert(context.Background(), store.EmailDocument{
		ID: "e1", Email: "m1@x.com", EmailNormalized: "m1@x.com", Status: "available",
	})

	// 预留。
	d, err := m.ReserveForRebind(context.Background(), "run-1")
	if err != nil || d == nil {
		t.Fatalf("预留应成功: %v", err)
	}
	if d.Status != "reserved" || d.RebindReservedBy != "run-1" {
		t.Fatalf("预留状态错误: %+v", d)
	}
	// 无可用邮箱时应返回 nil。
	d2, _ := m.ReserveForRebind(context.Background(), "run-2")
	if d2 != nil {
		t.Fatal("无可用邮箱应返回 nil")
	}

	// 消费（错误 runID 应失败）。
	ok, _ := m.ConsumeRebindEmail(context.Background(), "e1", "wrong-run")
	if ok {
		t.Fatal("错误 runID 消费应失败")
	}
	// 正确 runID 消费成功。
	ok, _ = m.ConsumeRebindEmail(context.Background(), "e1", "run-1")
	if !ok {
		t.Fatal("正确 runID 消费应成功")
	}
}
