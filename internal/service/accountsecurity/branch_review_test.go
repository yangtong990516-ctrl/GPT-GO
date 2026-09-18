package accountsecurity

import (
	"context"
	"errors"
	"testing"

	"gpt-go/internal/service/signup"
	"gpt-go/internal/service/signup/authflow"
	"gpt-go/internal/service/signup/core"
	"gpt-go/internal/service/signup/otp"
	"gpt-go/internal/store"
)

// ── mock Flow:记录走了哪个分支(不调真实网络)────────────────────────────────

type branchFlow struct {
	called          string // "login" / "backfill" / ""
	loginPwd        string
	succeedBackfill bool // true 时 backfill 返回成功(用于验证 StorePassword 落库)
	partialBackfill bool // true 时 backfill 密码已生效但返回错误(验证部分成功落密码)
}

func (f *branchFlow) RunLogin(_ context.Context, p authflow.LoginParams) (*signup.AuthResult, error) {
	f.called = "login"
	f.loginPwd = p.Password
	// 分支验证到此为止:返回错误,避免进入需要真实 Session 的 enroll 阶段。
	return nil, errBranchProbe
}

func (f *branchFlow) RunBackfill(_ context.Context, _ authflow.BackfillParams) (*signup.AuthResult, error) {
	f.called = "backfill"
	if f.succeedBackfill {
		return &signup.AuthResult{AccessToken: fakeAT(), Password: "NewGenPwd#123"}, nil
	}
	if f.partialBackfill {
		// 密码已生效但没拿到 AT:返回带密码的 result + error。
		return &signup.AuthResult{Password: "PartialPwd#456"}, errBranchProbe
	}
	return nil, errBranchProbe
}

func (f *branchFlow) Boot() *core.Bootstrap { return &core.Bootstrap{} }
func (f *branchFlow) Close()                 {}

func fakeAT() string {
	return "eyJhbGciOiJIUzI1NiJ9.eyJleHAiOjk5OTk5OTk5OTl9.sig"
}

var errBranchProbe = errors.New("branch_probe_stop")

// ── mock OTP Provider ───────────────────────────────────────────────────────

type fakeOTP struct{}

func (fakeOTP) Caps() otp.Capabilities { return otp.Capabilities{} }
func (fakeOTP) WaitForOTP(_ context.Context, _ string, _ int, _ int64) (string, error) {
	return "123456", nil
}
func (fakeOTP) CreateMailbox(_ context.Context) (string, error) { return "m@x.com", nil }
func (fakeOTP) PeekOTP(_ context.Context, _ string, _ int64, _ float64) (string, error) {
	return "123456", nil
}
func (fakeOTP) MarkDead(_ string) {}
func (fakeOTP) Exhausted() bool   { return false }

// ── 分支路由测试 ────────────────────────────────────────────────────────────

func TestEnsureOne_BranchRouting(t *testing.T) {
	pwd := "haspassword"
	totp := "JBSWY3DPEHPK3PXP"
	emailURL := "https://mail.example.com/access"

	cases := []struct {
		name       string
		doc        store.AccountDocument
		wantCalled string
	}{
		{"有密码无2FA_走登录分支", store.AccountDocument{ID: "a1", Email: "a1@x.com", ChatgptPassword: pwd, EmailAccessURL: emailURL}, "login"},
		{"有2FA无密码_走backfill", store.AccountDocument{ID: "a2", Email: "a2@x.com", TotpSecret: totp, EmailAccessURL: emailURL}, "backfill"},
		{"无密码无2FA_走backfill", store.AccountDocument{ID: "a3", Email: "a3@x.com", EmailAccessURL: emailURL}, "backfill"},
		{"有密码有2FA_跳过", store.AccountDocument{ID: "a4", Email: "a4@x.com", ChatgptPassword: pwd, TotpSecret: totp}, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := store.NewMockAccountStore()
			if _, err := st.Create(context.Background(), tc.doc); err != nil {
				t.Fatalf("seed account: %v", err)
			}
			bf := &branchFlow{}
			svc := New(st, nil, nil, WithFlowFactory(func(_ *signup.Config) (Flow, error) {
				return bf, nil
			}))
			res := svc.EnsureOne(context.Background(), tc.doc.ID, "", func(_ string) otp.Provider {
				return fakeOTP{}
			})
			if bf.called != tc.wantCalled {
				t.Errorf("分支路由: got called=%q want %q (status=%s err=%s)",
					bf.called, tc.wantCalled, res.Status, res.Error)
			}
			if tc.doc.ChatgptPassword != "" && bf.called == "login" && bf.loginPwd != tc.doc.ChatgptPassword {
				t.Errorf("登录分支密码: got %q want %q", bf.loginPwd, tc.doc.ChatgptPassword)
			}
			t.Logf("账号=%s 分支=%s 状态=%s err=%s", tc.doc.ID, bf.called, res.Status, res.Error)
		})
	}

	// ── 有2FA无密码落库验证:backfill 成功 → 应写密码、保留已有 totp、不重复 enroll ──
	t.Run("有2FA无密码_backfill成功落密码保留totp", func(t *testing.T) {
		st := store.NewMockAccountStore()
		doc := store.AccountDocument{ID: "a9", Email: "a9@x.com", TotpSecret: totp, EmailAccessURL: emailURL}
		if _, err := st.Create(context.Background(), doc); err != nil {
			t.Fatalf("seed: %v", err)
		}
		bf := &branchFlow{succeedBackfill: true}
		svc := New(st, nil, nil, WithFlowFactory(func(_ *signup.Config) (Flow, error) { return bf, nil }))
		res := svc.EnsureOne(context.Background(), "a9", "", func(_ string) otp.Provider { return fakeOTP{} })
		if res.Status != "success" {
			t.Errorf("期望 success, got %s err=%s", res.Status, res.Error)
		}
		got, _ := st.Get(context.Background(), "a9")
		if got.ChatgptPassword != "NewGenPwd#123" {
			t.Errorf("密码未落库: got %q", got.ChatgptPassword)
		}
		if got.TotpSecret != totp {
			t.Errorf("已有 totp 被覆盖: got %q want %q", got.TotpSecret, totp)
		}
		if got.PasswordConfiguredAt == nil {
			t.Errorf("passwordConfiguredAt 未设置")
		}
		t.Logf("落库验证: 密码=%s totp保留=%s passwordConfiguredAt已设", got.ChatgptPassword, got.TotpSecret)
	})

	// ── 部分成功(密码已生效无AT)落密码:验证 Bug1 修复(StorePassword 而非 StoreTotp)──
	t.Run("无密码backfill部分成功_密码落库不报错", func(t *testing.T) {
		st := store.NewMockAccountStore()
		doc := store.AccountDocument{ID: "b1", Email: "b1@x.com", EmailAccessURL: emailURL}
		if _, err := st.Create(context.Background(), doc); err != nil {
			t.Fatalf("seed: %v", err)
		}
		bf := &branchFlow{partialBackfill: true}
		svc := New(st, nil, nil, WithFlowFactory(func(_ *signup.Config) (Flow, error) { return bf, nil }))
		res := svc.EnsureOne(context.Background(), "b1", "", func(_ string) otp.Provider { return fakeOTP{} })
		if res.Status != "failed" {
			t.Errorf("部分成功应返回 failed, got %s", res.Status)
		}
		got, _ := st.Get(context.Background(), "b1")
		if got.ChatgptPassword != "PartialPwd#456" {
			t.Errorf("部分成功的密码未落库(Bug1回归): got %q want %q", got.ChatgptPassword, "PartialPwd#456")
		}
		t.Logf("部分成功落库: 密码=%s 状态=%s", got.ChatgptPassword, res.Status)
	})
}
