package proxy

import (
	"context"
	"testing"

	"gpt-go/internal/model"
	"gpt-go/internal/store"
)

// 用户提供的两组代理（做测试用）：
//   - ipipbright: host:port:username:password（无 :// 无 @ → http）
//   - iprocket:   host:port:username:password（9595 端口 → 厂商纠正 socks5h）
const ipipbrightProxies = `sp.ipipbright.net:1000:bru36433_area-VN_life-5_session-U0nM1dHxwW:bzqbku
sp.ipipbright.net:1000:bru36433_area-VN_life-5_session-sbP0qPEmP0:bzqbku
sp.ipipbright.net:1000:bru36433_area-VN_life-5_session-LnBXang5tL:bzqbku
sp.ipipbright.net:1000:bru36433_area-VN_life-5_session-fOObNOypFP:bzqbku
sp.ipipbright.net:1000:bru36433_area-VN_life-5_session-FZzpX4rr5C:bzqbku
sp.ipipbright.net:1000:bru36433_area-VN_life-5_session-vN4JATXJao:bzqbku
sp.ipipbright.net:1000:bru36433_area-VN_life-5_session-5ScwyN2jIf:bzqbku
sp.ipipbright.net:1000:bru36433_area-VN_life-5_session-UdvCYmaWCl:bzqbku
sp.ipipbright.net:1000:bru36433_area-VN_life-5_session-Y4GfWOh11z:bzqbku
sp.ipipbright.net:1000:bru36433_area-VN_life-5_session-HDmWJzXrX1:bzqbku`

const iprocketProxies = `prem.country.iprocket.io:9595:com44840385-res-VN-Lsid-967708287-TTL-1800:Sy3p57oVo24q1tY
prem.country.iprocket.io:9595:com44840385-res-VN-Lsid-853376111-TTL-1800:Sy3p57oVo24q1tY
prem.country.iprocket.io:9595:com44840385-res-VN-Lsid-940895940-TTL-1800:Sy3p57oVo24q1tY
prem.country.iprocket.io:9595:com44840385-res-VN-Lsid-831000509-TTL-1800:Sy3p57oVo24q1tY
prem.country.iprocket.io:9595:com44840385-res-VN-Lsid-812626642-TTL-1800:Sy3p57oVo24q1tY
prem.country.iprocket.io:9595:com44840385-res-VN-Lsid-877237616-TTL-1800:Sy3p57oVo24q1tY
prem.country.iprocket.io:9595:com44840385-res-VN-Lsid-841788203-TTL-1800:Sy3p57oVo24q1tY
prem.country.iprocket.io:9595:com44840385-res-VN-Lsid-928839262-TTL-1800:Sy3p57oVo24q1tY
prem.country.iprocket.io:9595:com44840385-res-VN-Lsid-815129176-TTL-1800:Sy3p57oVo24q1tY
prem.country.iprocket.io:9595:com44840385-res-VN-Lsid-905436945-TTL-1800:Sy3p57oVo24q1tY`

func newTestService() *Service {
	return NewService(store.NewMockProxyStore())
}

// TestImportProxiesIpipbright verifies the ipipbright group parses as http with
// country inferred as VN.
func TestImportProxiesIpipbright(t *testing.T) {
	svc := newTestService()
	res, err := svc.ImportProxies(context.Background(), ipipbrightProxies, nil, nil)
	if err != nil {
		t.Fatalf("ImportProxies error: %v", err)
	}
	if res.Total != 10 {
		t.Errorf("Total = %d, want 10", res.Total)
	}
	if res.Imported != 10 {
		t.Errorf("Imported = %d, want 10", res.Imported)
	}
	if res.ErrorCount != 0 {
		t.Errorf("ErrorCount = %d, want 0", res.ErrorCount)
	}

	// 列出来校验解析结果。
	page, err := svc.ListProxies(context.Background(), 1, 20, "", "")
	if err != nil {
		t.Fatalf("ListProxies error: %v", err)
	}
	if page.Total != 10 {
		t.Fatalf("list total = %d, want 10", page.Total)
	}
	for _, p := range page.Items {
		if p.Host != "sp.ipipbright.net" {
			t.Errorf("host = %q, want sp.ipipbright.net", p.Host)
		}
		if p.Port != 1000 {
			t.Errorf("port = %d, want 1000", p.Port)
		}
		if p.Username == "" || p.Password != "bzqbku" {
			t.Errorf("user/pass = %q/%q, want non-empty/bzqbku", p.Username, p.Password)
		}
		if p.Scheme != "http" {
			t.Errorf("scheme = %q, want http (无 :// 无 @ → host:port:user:pass)", p.Scheme)
		}
		if p.Country != "VN" {
			t.Errorf("country = %q, want VN (inferred from _area-VN)", p.Country)
		}
	}
}

// TestImportProxiesIprocket verifies the iprocket group parses with 9595 → socks5h.
func TestImportProxiesIprocket(t *testing.T) {
	svc := newTestService()
	res, err := svc.ImportProxies(context.Background(), iprocketProxies, nil, nil)
	if err != nil {
		t.Fatalf("ImportProxies error: %v", err)
	}
	if res.Imported != 10 {
		t.Errorf("Imported = %d, want 10", res.Imported)
	}

	page, err := svc.ListProxies(context.Background(), 1, 20, "", "")
	if err != nil {
		t.Fatalf("ListProxies error: %v", err)
	}
	for _, p := range page.Items {
		if p.Host != "prem.country.iprocket.io" {
			t.Errorf("host = %q", p.Host)
		}
		if p.Port != 9595 {
			t.Errorf("port = %d, want 9595", p.Port)
		}
		if p.Scheme != "socks5h" {
			t.Errorf("scheme = %q, want socks5h (IPRocket 9595 厂商纠正)", p.Scheme)
		}
		if p.Country != "VN" {
			t.Errorf("country = %q, want VN (inferred from -res-VN)", p.Country)
		}
	}
}

// TestImportDuplicate verifies repeated import does not duplicate.
func TestImportDuplicate(t *testing.T) {
	svc := newTestService()
	_, _ = svc.ImportProxies(context.Background(), ipipbrightProxies, nil, nil)
	res, err := svc.ImportProxies(context.Background(), ipipbrightProxies, nil, nil)
	if err != nil {
		t.Fatalf("ImportProxies error: %v", err)
	}
	if res.Imported != 0 {
		t.Errorf("Imported = %d, want 0 (all duplicates)", res.Imported)
	}
	if res.DuplicateCount != 10 {
		t.Errorf("DuplicateCount = %d, want 10", res.DuplicateCount)
	}
}

// TestImportMixedFormat verifies mixed ipipbright + iprocket import and that
// candidates are sorted country asc.
func TestImportMixedAndCandidates(t *testing.T) {
	svc := newTestService()
	raw := ipipbrightProxies + "\n" + iprocketProxies
	res, err := svc.ImportProxies(context.Background(), raw, nil, nil)
	if err != nil {
		t.Fatalf("ImportProxies error: %v", err)
	}
	if res.Imported != 20 {
		t.Errorf("Imported = %d, want 20", res.Imported)
	}

	// all_eligible_proxy_candidates：初始 status=unknown，不算 eligible（只有 available）。
	candidates, err := svc.AllEligibleProxyCandidates(context.Background(), "")
	if err != nil {
		t.Fatalf("AllEligibleProxyCandidates error: %v", err)
	}
	if len(candidates) != 0 {
		t.Errorf("candidates = %d, want 0 (导入后 status=unknown, 非 available)", len(candidates))
	}
}

// TestProxyURL verifies model.ProxyURL mirrors plan_check_service.proxy_url.
func TestProxyURL(t *testing.T) {
	// ipipbright http，无特殊字符。
	got := model.ProxyURL("sp.ipipbright.net", 1000, "bru36433_area-VN_life-5_session-U0nM1dHxwW", "bzqbku", "http")
	want := "http://bru36433_area-VN_life-5_session-U0nM1dHxwW:bzqbku@sp.ipipbright.net:1000"
	if got != want {
		t.Errorf("ProxyURL = %q, want %q", got, want)
	}
	// iprocket socks5h。
	got2 := model.ProxyURL("prem.country.iprocket.io", 9595, "com44840385-res-VN-Lsid-967708287-TTL-1800", "Sy3p57oVo24q1tY", "socks5")
	if got2 != "socks5h://com44840385-res-VN-Lsid-967708287-TTL-1800:Sy3p57oVo24q1tY@prem.country.iprocket.io:9595" {
		t.Errorf("ProxyURL socks5 = %q", got2)
	}
}

// TestAcquireLease verifies the lease cycle: acquire → consume (used) → release.
func TestAcquireLease(t *testing.T) {
	svc := newTestService()
	_, _ = svc.ImportProxies(context.Background(), ipipbrightProxies, nil, nil)

	// 手动把第一个置为 available 以便 acquire。
	list, _ := svc.ListProxies(context.Background(), 1, 20, "", "")
	id := list.Items[0].ID
	_, _ = svc.SetProxyStatus(context.Background(), id, model.ProxyStatusAvailable)

	lease, err := svc.AcquireProxy(context.Background(), "owner-1", nil, 180, "", "")
	if err != nil {
		t.Fatalf("AcquireProxy error: %v", err)
	}
	if lease == nil {
		t.Fatalf("AcquireProxy = nil, want a lease")
	}
	if lease.ID != id {
		t.Errorf("lease.ID = %q, want %q", lease.ID, id)
	}

	// consume 标记 used（独占策略）。
	if err := svc.ConsumeProxy(context.Background(), id, "owner-1"); err != nil {
		t.Fatalf("ConsumeProxy error: %v", err)
	}
	// 之后 available 候选为空。
	candidates, _ := svc.AllEligibleProxyCandidates(context.Background(), "")
	if len(candidates) != 0 {
		t.Errorf("candidates after consume = %d, want 0", len(candidates))
	}

	// restore-used 放回池。
	restored, err := svc.RestoreUsed(context.Background(), model.RestoreUsedProxiesInput{})
	if err != nil {
		t.Fatalf("RestoreUsed error: %v", err)
	}
	if restored.Restored != 1 {
		t.Errorf("Restored = %d, want 1", restored.Restored)
	}
}
