package signup

import "testing"

func TestLookupErrorKnown(t *testing.T) {
	info := LookupError("no_eligible_proxy")
	if info.Label != "无可用代理" {
		t.Errorf("label 不匹配: %q", info.Label)
	}
	if info.Category != CategoryResource {
		t.Errorf("category 不匹配: %q", info.Category)
	}
	if info.Description == "" || info.Suggestion == "" {
		t.Error("description/suggestion 不应为空")
	}
}

func TestLookupErrorUnknown(t *testing.T) {
	info := LookupError("some_unknown_code")
	if info.Category != CategoryUnknown {
		t.Errorf("未知错误码应归类 unknown: %q", info.Category)
	}
	if info.Label != "未知错误" {
		t.Errorf("未知错误码 label 应为 '未知错误': %q", info.Label)
	}
}

func TestLookupErrorAllCodesPresent(t *testing.T) {
	codes := []string{
		"no_eligible_proxy", "no_available_email", "bad_proxy_lease",
		"reserve_email_failed", "warmup_failed", "network", "rate_limited",
		"existing_account", "sentinel", "challenge", "protocol_failed",
		"persist_failed", "not_trial", "cancelled",
	}
	for _, code := range codes {
		info := LookupError(code)
		if info.Label == "未知错误" {
			t.Errorf("错误码 %q 未登记", code)
		}
	}
}

func TestRegistrationErrorUnwrap(t *testing.T) {
	inner := &RegistrationError{Code: "x", Email: "e", Err: nil}
	_ = inner.Error()

	// Unwrap 返回 nil（无内层错误）。
	if inner.Unwrap() != nil {
		t.Error("Unwrap 应返回 nil")
	}
}

func TestProxyLeaseURL(t *testing.T) {
	tests := []struct {
		name string
		l    ProxyLease
		want string
	}{
		{"带认证", ProxyLease{Host: "1.2.3.4", Port: 8080, Scheme: "socks5", Username: "u", Password: "p"}, "socks5://u:p@1.2.3.4:8080"},
		{"无认证", ProxyLease{Host: "5.6.7.8", Port: 1080, Scheme: "socks5"}, "socks5://5.6.7.8:1080"},
		{"默认 scheme", ProxyLease{Host: "9.9.9.9", Port: 3128}, "socks5://9.9.9.9:3128"},
		{"空 lease", ProxyLease{}, ""},
		{"缺 port", ProxyLease{Host: "1.1.1.1"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.l.ProxyURL(); got != tt.want {
				t.Errorf("ProxyURL() = %q, want %q", got, tt.want)
			}
		})
	}
}
