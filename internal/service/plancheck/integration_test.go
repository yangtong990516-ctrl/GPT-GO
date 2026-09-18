// integration_test.go 跨包联动闭环测试（P0 断链修复验证）：
// 注册产物 token 经 ResourceAdapter 落库 → plancheck 能 claim(accessToken 非空)
// → CheckCombined 跑通并写 alive+plan 双字段。
//
// 这正是「账号池 ↔ 套餐检查」联动链路：此前 PersistAccount 丢弃 token，导致
// 账号 accessToken 为空 → ClaimPlanCheck 永远返回 nil(skip) → 套餐检查失效。
package plancheck

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"gpt-go/internal/model"
	"gpt-go/internal/service/account"
	"gpt-go/internal/service/signup"
	"gpt-go/internal/store"
)

// mkJWT 造带 exp 的测试 JWT。
func mkJWT(expUnix int64) string {
	h := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256"}`))
	b := base64.RawURLEncoding.EncodeToString([]byte(`{"exp":` + itoaInt(expUnix) + `}`))
	return h + "." + b + ".sig"
}

func itoaInt(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// TestSignupPersistToPlancheckClosedLoop 验证完整联动闭环。
func TestSignupPersistToPlancheckClosedLoop(t *testing.T) {
	ctx := context.Background()
	// 邮箱池 + 账号池 + 资源适配器（模拟注册落库路径）。
	emailStore := store.NewMockEmailStore()
	_, _ = emailStore.Upsert(ctx, store.EmailDocument{
		ID: "e1", Email: "loop@x.com", EmailNormalized: "loop@x.com",
		Status: string(model.EmailStatusAvailable), SourceType: "mailcode",
	})
	accStore := store.NewMockAccountStore()
	ra := signup.NewResourceAdapter(emailStore, account.NewService(accStore))

	// 1) 预留邮箱 + 模拟注册成功落库（带 token）。
	emails, err := ra.ReserveEmails(ctx, 1, "run-1", "mailcode")
	if err != nil || len(emails) == 0 {
		t.Fatalf("ReserveEmails: %v", err)
	}
	jwt := mkJWT(time.Now().UTC().Add(24 * time.Hour).Unix())
	accID, err := ra.PersistAccount(ctx, &signup.AuthResultLike{
		Email: "loop@x.com", Password: "pw", AccessToken: jwt,
	}, emails[0].ID, "US")
	if err != nil {
		t.Fatalf("PersistAccount: %v", err)
	}

	// 2) 落库的 accessToken 非空（联动修复的直接证据：此前 PersistAccount 丢 token，
	//    此处会为空导致 plancheck claim skip）。
	doc0, err := accStore.Get(ctx, accID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if doc0.AccessToken != jwt {
		t.Fatalf("联动断链：落库 accessToken 应非空, got %q", doc0.AccessToken)
	}

	// 3) CheckCombined 全链路（mock 代理 + mock session 喂 plus 响应）。
	//    内部 ClaimPlanCheck 因 accessToken 非空而成功认领（此前会 skip）。
	svc := New(accStore, &mockProxyStore{}, WithSessionFactory(sessionReturning(plusPaidBody)))
	res, err := svc.CheckCombined(ctx, []string{accID}, "")
	if err != nil {
		t.Fatalf("CheckCombined: %v", err)
	}
	if res.Alive != 1 || res.Failed != 0 {
		t.Fatalf("闭环应 1 alive 0 failed, got %+v", res)
	}
	doc, _ := accStore.Get(ctx, accID)
	if doc.AccountType != "plus" {
		t.Fatalf("套餐检查应判 plus(entitlement 活跃), got %q", doc.AccountType)
	}
	if *doc.AliveStatus != "alive" {
		t.Fatalf("验活应 alive, got %v", *doc.AliveStatus)
	}
}

// 复用 service_test.go 的 mockProxyStore / sessionReturning / plusPaidBody（同包）。
