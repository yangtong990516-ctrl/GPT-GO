package store

import (
	"context"
	"testing"
	"time"

	"gpt-go/internal/icloud/config"
	"gpt-go/internal/icloud/domain"
)

// 评审集成测试:在真实 Mongo 上验证 review 关注的核心路径。
// 依赖本地 127.0.0.1:27017 Mongo;不可达时自动 Skip(不阻断无 Mongo 的 CI)。
func openReviewDB(t *testing.T) *Store {
	t.Helper()
	cfg := config.Default()
	st, err := Open("mongodb://127.0.0.1:27017", "review_smoke", cfg)
	if err != nil {
		t.Skipf("mongo 不可达,跳过集成测试: %v", err)
	}
	t.Cleanup(func() {
		_ = st.client.Database("review_smoke").Drop(context.Background())
		_ = st.Close(context.Background())
	})
	return st
}

func TestReviewAdminAndSession(t *testing.T) {
	st := openReviewDB(t)
	if _, ok := st.Admin(); ok {
		t.Fatalf("expected no admin initially")
	}
	adm, err := st.SetupAdmin("Admin", "hash-x")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if adm.Username != "admin" {
		t.Fatalf("username not lowercased: %q", adm.Username)
	}
	if _, err := st.SetupAdmin("admin2", "h"); err == nil {
		t.Fatalf("expected duplicate setup error")
	}
	if err := st.SaveSession("tokhash123", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("save session: %v", err)
	}
	if _, ok := st.ValidateSession("tokhash123"); !ok {
		t.Fatalf("validate session failed")
	}
	if _, ok := st.ValidateSession("wrong"); ok {
		t.Fatalf("expected invalid token rejected")
	}
	t.Logf("admin+session ok")
}

func TestReviewLeaseStateMachine(t *testing.T) {
	st := openReviewDB(t)
	mk := func(email string) {
		if _, _, err := st.UpsertMailboxFromRemote("acc", domain.RemoteMailbox{Email: email, IsActive: true}, ""); err != nil {
			t.Fatalf("upsert %s: %v", email, err)
		}
	}
	mk("a@icloud.com")
	mk("b@icloud.com")

	_, lease, created, err := st.ClaimMailboxLease("p1", "t", "", "", time.Hour, time.Now())
	if err != nil || !created {
		t.Fatalf("claim: %v created=%v", err, created)
	}
	cmb, clease, _, err := st.CommitMailboxLease(lease.ID, "p1", "used it", time.Now())
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if clease.State != domain.MailboxLeaseCommitted || cmb.Status != domain.StatusUsed {
		t.Fatalf("commit state wrong: lease=%s mb=%s", clease.State, cmb.Status)
	}

	_, lease2, _, err := st.ClaimMailboxLease("p2", "t", "", "", time.Hour, time.Now())
	if err != nil {
		t.Fatalf("claim2: %v", err)
	}
	rmb, rlease, _, err := st.ReleaseMailboxLease(lease2.ID, "p2", "", time.Now())
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if rlease.State != domain.MailboxLeaseReleased || rmb.Status != domain.StatusAvailable {
		t.Fatalf("release state wrong: lease=%s mb=%s", rlease.State, rmb.Status)
	}
	t.Logf("lease state machine ok: used->committed, released->available")
}

func TestReviewLeaseProjectGuard(t *testing.T) {
	st := openReviewDB(t)
	if _, _, err := st.UpsertMailboxFromRemote("acc", domain.RemoteMailbox{Email: "g@icloud.com", IsActive: true}, ""); err != nil {
		t.Fatal(err)
	}
	_, lease, _, err := st.ClaimMailboxLease("projA", "t", "", "", time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := st.CommitMailboxLease(lease.ID, "projB", "", time.Now()); err != ErrLeaseProjectMismatch {
		t.Fatalf("expected ErrLeaseProjectMismatch, got %v", err)
	}
	t.Logf("project guard ok")
}

func TestReviewMessageDedup(t *testing.T) {
	st := openReviewDB(t)
	mb, _, err := st.UpsertMailboxFromRemote("acc", domain.RemoteMailbox{Email: "m@icloud.com", IsActive: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	m1, c1, err := st.UpsertMessage(mb.ID, "r-100", "imap", "hello", "x@y.com", "body", time.Now())
	if err != nil || !c1 {
		t.Fatalf("first insert: created=%v err=%v", c1, err)
	}
	m2, _, err := st.UpsertMessage(mb.ID, "r-100", "imap", "hello", "x@y.com", "full body", time.Now())
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if m1.ID != m2.ID {
		t.Fatalf("dedup failed: different IDs %s vs %s", m1.ID, m2.ID)
	}
	t.Logf("message dedup ok: same_id=%v", m1.ID == m2.ID)
}

func TestReviewSettingsAndDashboard(t *testing.T) {
	st := openReviewDB(t)
	s := st.Settings()
	s.EnablePublicMailboxAPI = true
	s.PublicAPIKey = "k-1"
	if _, err := st.SaveSettings(s); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	got := st.Settings()
	if !got.EnablePublicMailboxAPI || got.PublicAPIKey != "k-1" {
		t.Fatalf("settings roundtrip failed")
	}
	if _, _, err := st.UpsertMailboxFromRemote("acc", domain.RemoteMailbox{Email: "d@icloud.com", IsActive: true}, ""); err != nil {
		t.Fatal(err)
	}
	dash := st.Dashboard()
	if dash.MailboxCount != 1 || dash.AvailableCount != 1 {
		t.Fatalf("dashboard counts wrong: %+v", dash)
	}
	t.Logf("settings+dashboard ok")
}
