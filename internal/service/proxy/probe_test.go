package proxy

import "testing"

// TestGradePurity mirrors _probe_proxy purity grading over the Cloudflare sliver.
func TestGradePurity(t *testing.T) {
	cases := []struct {
		sliver string
		purity string
		reason string
	}{
		{"", "clean", "sliver-unknown"},
		{"none", "clean", ""},
		{"xxx-tier1", "clean", ""},
		{"datacenter", "dirty", "cloudflare-sliver=datacenter"},
		{"cloud", "dirty", "cloudflare-sliver=cloud"},
		{"hosting", "dirty", "cloudflare-sliver=hosting"},
		{"vpn", "dirty", "cloudflare-sliver=vpn"},
		{"proxy", "dirty", "cloudflare-sliver=proxy"},
		{"tier2", "dirty", "cloudflare-sliver=tier2"},
		{"tier3", "dirty", "cloudflare-sliver=tier3"},
		{"tier4", "dirty", "cloudflare-sliver=tier4"},
		{"tier5", "dirty", "cloudflare-sliver=tier5"},
		{"tunnel", "dirty", "cloudflare-sliver=tunnel"},
	}
	for _, c := range cases {
		purity, reason := gradePurity(c.sliver)
		if purity != c.purity || reason != c.reason {
			t.Errorf("gradePurity(%q) = (%q, %q), want (%q, %q)", c.sliver, purity, reason, c.purity, c.reason)
		}
	}
}

// TestParseTrace mirrors the cdn-cgi/trace field extraction.
func TestParseTrace(t *testing.T) {
	text := "fl=123\nh=chatgpt.com\nip=1.2.3.4\nloc=US\nsliver=none\nts=1700000000\n"
	if got := matchGroup(traceIP, text); got != "1.2.3.4" {
		t.Errorf("ip = %q, want 1.2.3.4", got)
	}
	if got := matchGroup(traceLoc, text); got != "US" {
		t.Errorf("loc = %q, want US", got)
	}
	if got := matchGroup(traceSliver, text); got != "none" {
		t.Errorf("sliver = %q, want none", got)
	}
}
