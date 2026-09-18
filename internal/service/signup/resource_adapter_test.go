// resource_adapter_test.go 资源适配器单测：邮箱池预留 → 成功落账号池 + 邮箱 consume。
package signup

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"gpt-go/internal/model"
	"gpt-go/internal/service/account"
	"gpt-go/internal/store"
)

// setupPools 构造种子邮箱池 + 账号池 + 适配器。
func setupPools(t *testing.T, seedEmails ...string) (*ResourceAdapter, *store.MockEmailStore, *store.MockAccountStore) {
	t.Helper()
	ctx := context.Background()
	emailStore := store.NewMockEmailStore()
	for _, e := range seedEmails {
		if _, err := emailStore.Upsert(ctx, store.EmailDocument{
			ID: "id-" + e, Email: e, EmailNormalized: e,
			Status: string(model.EmailStatusAvailable), SourceType: "mailcode",
		}); err != nil {
			t.Fatalf("seed email err: %v", err)
		}
	}
	accStore := store.NewMockAccountStore()
	accSvc := account.NewService(accStore)
	return NewResourceAdapter(emailStore, accSvc), emailStore, accStore
}

// TestReserveEmails 验证从邮箱池预留（available → reserved）。
func TestReserveEmails(t *testing.T) {
	ra, _, _ := setupPools(t, "a@x.com", "b@x.com")
	got, err := ra.ReserveEmails(context.Background(), 1, "run1", "mailcode")
	if err != nil || len(got) == 0 {
		t.Fatalf("ReserveEmails err=%v got=%v", err, got)
	}
	if got[0].Email == "" {
		t.Fatal("应预留到一个邮箱")
	}
}

// TestPersistAccountConsumesEmail 验证成功落账号池 + 邮箱被 consume（不可再用）。
func TestPersistAccountConsumesEmail(t *testing.T) {
	ra, emailStore, _ := setupPools(t, "newuser@x.com")
	ctx := context.Background()
	res, err := ra.ReserveEmails(ctx, 1, "run1", "mailcode")
	if err != nil || len(res) == 0 {
		t.Fatalf("Reserve err: %v", err)
	}
	// 成功落库
	accID, err := ra.PersistAccount(ctx, &AuthResultLike{Email: "newuser@x.com", Password: "pw123"}, res[0].ID, "VN")
	if err != nil {
		t.Fatalf("PersistAccount err: %v", err)
	}
	if accID == "" {
		t.Fatal("应返回账号 ID")
	}
	// 邮箱应已被 consume（failed/consumed），不能再被预留。
	res2, err := ra.ReserveEmails(ctx, 1, "run2", "mailcode")
	if err == nil && len(res2) > 0 {
		t.Fatalf("已 consume 的邮箱不应再被预留, got %v", res2)
	}
	_ = emailStore
}

// TestReleaseEmailOnFailure 验证失败时归还可复用（reserved → available）。
func TestReleaseEmailOnFailure(t *testing.T) {
	ra, _, _ := setupPools(t, "retry@x.com")
	ctx := context.Background()
	res, _ := ra.ReserveEmails(ctx, 1, "run1", "mailcode")
	if len(res) == 0 {
		t.Fatal("应预留成功")
	}
	// 失败归还
	if err := ra.ReleaseEmail(ctx, res[0].ID, "run1"); err != nil {
		t.Fatalf("ReleaseEmail err: %v", err)
	}
	// 归还后应能再被预留
	res2, err := ra.ReserveEmails(ctx, 1, "run2", "mailcode")
	if err != nil || len(res2) == 0 {
		t.Fatalf("归还后应可再预留, err=%v", err)
	}
}

// TestPersistAccountCountryDynamic 验证落库国家是每次实测值（动态，不锁死）。
func TestPersistAccountCountryDynamic(t *testing.T) {
	ra, _, accStore := setupPools(t, "c@x.com")
	ctx := context.Background()
	res, _ := ra.ReserveEmails(ctx, 1, "run1", "mailcode")
	if _, err := ra.PersistAccount(ctx, &AuthResultLike{Email: "c@x.com", Password: "pw"}, res[0].ID, "TH"); err != nil {
		t.Fatalf("persist err: %v", err)
	}
	// 账号的 RegistrationCountry 应是传入的实测国家 TH（不是邮箱/通道写死的）。
	doc, _, _ := accStore.List(ctx, store.AccountQuery{}, 1, 10)
	if len(doc) == 0 {
		t.Fatal("账号应已落库")
	}
	if doc[0].RegistrationCountry == nil || *doc[0].RegistrationCountry != "TH" {
		t.Fatalf("RegistrationCountry 应=TH（实测动态值）, got %+v", doc[0].RegistrationCountry)
	}
}

// ── P0 修复验证：注册产物 token 落库（账号池↔套餐检查联动闭环）──

// makeJWT 造一个带 exp 的测试 JWT（不验签，仅 base64 payload）。
func makeJWT(expUnix int64) string {
	head := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	body := base64.RawURLEncoding.EncodeToString([]byte(
		`{"exp":` + itoa(int(expUnix)) + `,"https://api.AI Platform.com/auth":{"chatgpt_plan_type":"free"}}`))
	return head + "." + body + ".sig"
}

// TestPersistAccountStoresToken 验证：注册产物的 accessToken(+JWT exp)/refreshToken
// 落库到账号文档，且 accessTokenConfigured=true —— 这是套餐检查/验活能 claim 的前提。
func TestPersistAccountStoresToken(t *testing.T) {
	ra, _, accStore := setupPools(t, "tok@x.com")
	ctx := context.Background()
	res, _ := ra.ReserveEmails(ctx, 1, "run1", "mailcode")

	exp := time.Now().UTC().Add(24 * time.Hour).Unix()
	jwt := makeJWT(exp)
	rt := "refresh-token-xyz"
	accID, err := ra.PersistAccount(ctx, &AuthResultLike{
		Email: "tok@x.com", Password: "pw", AccessToken: jwt, RefreshToken: rt,
	}, res[0].ID, "US")
	if err != nil {
		t.Fatalf("PersistAccount err: %v", err)
	}

	doc, err := accStore.Get(ctx, accID)
	if err != nil {
		t.Fatalf("Get err: %v", err)
	}
	if doc.AccessToken != jwt {
		t.Fatalf("accessToken 未落库, got %q", doc.AccessToken)
	}
	if !doc.AccessTokenConfigured {
		t.Fatal("accessTokenConfigured 应 true")
	}
	if doc.AccessTokenExpiresAt == nil || doc.AccessTokenExpiresAt.Unix() != exp {
		t.Fatalf("accessTokenExpiresAt 应从 JWT exp 解析, got %+v (want %d)", doc.AccessTokenExpiresAt, exp)
	}
	if doc.AccessTokenUpdatedAt == nil {
		t.Fatal("accessTokenUpdatedAt 应已设置")
	}
	if doc.RefreshToken == nil || *doc.RefreshToken != rt {
		t.Fatalf("refreshToken 未落库, got %+v", doc.RefreshToken)
	}
}

// TestAccessTokenExpiresAtBadToken 验证：非 JWT / 无 exp 的 token 返回 nil（容忍）。
func TestAccessTokenExpiresAtBadToken(t *testing.T) {
	for _, tok := range []string{"", "not-a-jwt", "a.b", "a.b.c"} {
		if got := accessTokenExpiresAt(tok); got != nil {
			t.Fatalf("坏 token %q 应返回 nil, got %v", tok, got)
		}
	}
}
