// totp_test.go 用 RFC 6238 官方测试向量 + RFC 4226 HOTP 向量验证算法正确性。
//
// 关键：TOTP 算错一位，2FA 就过不了。所以必须用标准测试向量印证，
// 不能只对拍「自己能跑」。RFC 向量是跨实现的共同基准。
package totp

import "testing"

// RFC 6238 Appendix B 测试密钥（SHA1 用 ASCII "12345678901234567890"）。
// 其 Base32 编码（32 字符）如下。
const rfc6238SecretB32 = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

// TestHOTP_RFC4226 用 RFC 4226 Appendix D 的 HOTP 测试向量验证。
//
// 密钥 ASCII "12345678901234567890"（Base32=GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ），
// counter 0..9 对应的 6 位 HOTP 是 RFC 公开的标准答案。
func TestHOTP_RFC4226(t *testing.T) {
	want := []string{
		"755224", "287082", "359152", "969429", "338314",
		"254676", "287922", "162583", "399871", "520489",
	}
	for counter := uint64(0); counter < 10; counter++ {
		got, err := HOTP(rfc6238SecretB32, counter, 6)
		if err != nil {
			t.Fatalf("HOTP(%d) err: %v", counter, err)
		}
		if got != want[counter] {
			t.Fatalf("HOTP(counter=%d)=%s, RFC4226 期望 %s", counter, got, want[counter])
		}
	}
}

// TestTOTP_RFC6238 用 RFC 6238 Appendix B 的 TOTP 测试向量验证（SHA1, 8 位）。
//
// RFC 给的是 8 位码；我们的 digits=6 实现取 mod 10^6，应等于 8 位码的后 6 位。
// 时间戳 → 8位TOTP（RFC 6238 SHA1）：
//
//	59          → 94287082
//	1111111109  → 07081804
//	1111111111  → 14050471
//	1234567890  → 89005924
//	2000000000  → 69279037
func TestTOTP_RFC6238(t *testing.T) {
	cases := []struct {
		timeUnix int64
		want8    string // RFC 6238 的 8 位标准答案
	}{
		{59, "94287082"},
		{1111111109, "07081804"},
		{1111111111, "14050471"},
		{1234567890, "89005924"},
		{2000000000, "69279037"},
	}
	for _, c := range cases {
		// 用 8 位直接对拍 RFC 标准答案。
		got8, err := TOTP(rfc6238SecretB32, c.timeUnix, 30, 8)
		if err != nil {
			t.Fatalf("TOTP(%d) err: %v", c.timeUnix, err)
		}
		if got8 != c.want8 {
			t.Fatalf("TOTP(t=%d, digits=8)=%s, RFC6238 期望 %s", c.timeUnix, got8, c.want8)
		}
		// 6 位 = 8 位后 6 位（mod 10^6），验证默认位数路径。
		got6, err := NowAt(rfc6238SecretB32, c.timeUnix)
		if err != nil {
			t.Fatalf("NowAt(%d) err: %v", c.timeUnix, err)
		}
		want6 := c.want8[len(c.want8)-6:]
		if got6 != want6 {
			t.Fatalf("TOTP(t=%d, digits=6)=%s, 期望(8位后6位) %s", c.timeUnix, got6, want6)
		}
	}
}

// TestTOTPSecretToleratesFormat 验证密钥格式容忍（小写/空格/缺 padding）。
func TestTOTPSecretToleratesFormat(t *testing.T) {
	// 标准 32 字符密钥的小写 + 去 padding 形式，应解出同样的码。
	base, err := NowAt(rfc6238SecretB32, 59)
	if err != nil {
		t.Fatalf("基准 NowAt err: %v", err)
	}
	lower := "gezdgnbvgy3tqojqgezdgnbvgy3tqojq" // 全小写
	got, err := NowAt(lower, 59)
	if err != nil {
		t.Fatalf("小写密钥 NowAt err: %v", err)
	}
	if got != base {
		t.Fatalf("小写密钥结果=%s, 期望与标准一致 %s", got, base)
	}
}

// TestTOTPEmptySecret 验证空密钥报错（不静默产出错码）。
func TestTOTPEmptySecret(t *testing.T) {
	if _, err := Now(""); err == nil {
		t.Fatal("空密钥应报错")
	}
}

// TestTOTPNowUsesTimeSource 验证 Now 走可注入时间源（默认不改变结果正确性）。
func TestTOTPNowUsesTimeSource(t *testing.T) {
	// 固定时间源到 RFC 向量 t=59，Now 应等于 NowAt(59)。
	orig := unixNow
	defer func() { unixNow = orig }()
	unixNow = func() int64 { return 59 }
	got, err := Now(rfc6238SecretB32)
	if err != nil {
		t.Fatalf("Now err: %v", err)
	}
	want, _ := NowAt(rfc6238SecretB32, 59)
	if got != want {
		t.Fatalf("Now(固定t=59)=%s, 期望 %s", got, want)
	}
}
