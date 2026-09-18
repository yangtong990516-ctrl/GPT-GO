// Package totp 实现 RFC 6238 TOTP / RFC 4226 HOTP（用于已有账号的 2FA mfa-challenge）。
//
// 对齐 codex-auto auth_flow.py 的 _hotp/_totp_now：纯算法，无网络依赖。
// 用 Go 标准库实现（crypto/hmac + crypto/sha1 + encoding/base32 + encoding/binary），
// 不引第三方，便于审计与单测。
package totp

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

// unixNow 是时间源（默认 time.Now().Unix()）。测试可替换为固定值以对齐 RFC 测试向量。
// 生产代码勿改——它是为可测试性预留的接缝（对齐 Python time.time() 的可注入性）。
var unixNow = func() int64 { return time.Now().Unix() }

// 默认参数（对齐 RFC 6238 与 Google Authenticator 事实标准）。
const (
	// DefaultDigits 是验证码位数（OpenAI/Authy 均用 6 位）。
	DefaultDigits = 6
	// DefaultPeriod 是时间窗口秒数（RFC 6238 默认 30s）。
	DefaultPeriod = 30
)

// HOTP 计算 HMAC-based One-Time Password（RFC 4226）。
//
//	secretB32  Base32 编码的密钥（容忍省略 padding、大小写、空格）
//	counter    计数器（TOTP 时 = unixTime/30）
//	digits     验证码位数（通常 6）
//
// 算法：key=base32decode(secret) → HMAC-SHA1(key, bigEndian(counter))
// → 动态截断（offset=h[19]&0x0F，取 4 字节大端 & 0x7FFFFFFF）→ mod 10^digits → 左补零。
func HOTP(secretB32 string, counter uint64, digits int) (string, error) {
	key, err := decodeSecret(secretB32)
	if err != nil {
		return "", err
	}
	if digits <= 0 {
		digits = DefaultDigits
	}

	// 计数器转 8 字节大端。
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)

	// HMAC-SHA1。
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	h := mac.Sum(nil) // 20 字节

	// 动态截断（RFC 4226 §5.3）。
	offset := h[len(h)-1] & 0x0F
	code := binary.BigEndian.Uint32(h[offset:offset+4]) & 0x7FFFFFFF

	// 取模 + 左补零。
	otp := code % uint32(pow10(digits))
	return padLeft(otp, digits), nil
}

// TOTP 计算指定 Unix 时间的 Time-based OTP（RFC 6238）。
// timeUnix 通常传 time.Now().Unix()；period 通常 30。
func TOTP(secretB32 string, timeUnix int64, period, digits int) (string, error) {
	if period <= 0 {
		period = DefaultPeriod
	}
	counter := uint64(timeUnix / int64(period))
	return HOTP(secretB32, counter, digits)
}

// Now 返回当前 30 秒窗口的 6 位 TOTP 码（对齐 Python _totp_now）。
// 这是最常用入口：mfa-challenge 提交用。
func Now(secretB32 string) (string, error) {
	return TOTP(secretB32, unixNow(), DefaultPeriod, DefaultDigits)
}

// NowAt 同 Now，但显式给时间（测试用，可注入固定时间戳对齐 RFC 测试向量）。
func NowAt(secretB32 string, timeUnix int64) (string, error) {
	return TOTP(secretB32, timeUnix, DefaultPeriod, DefaultDigits)
}

// decodeSecret 解码 Base32 密钥：去空格、转大写、自动补 padding。
// 对齐 Python base64.b32decode(secret + "=" * (-len(secret) % 8))。
func decodeSecret(secretB32 string) ([]byte, error) {
	s := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(secretB32), " ", ""))
	if s == "" {
		return nil, fmt.Errorf("totp: 密钥为空")
	}
	// 补 padding 到 8 的倍数。
	if rem := len(s) % 8; rem != 0 {
		s += strings.Repeat("=", 8-rem)
	}
	key, err := base32.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("totp: Base32 密钥解码失败: %w", err)
	}
	if len(key) == 0 {
		return nil, fmt.Errorf("totp: 密钥解码后为空")
	}
	return key, nil
}

// pow10 返回 10^n（digits≤9，uint32 不溢出）。
func pow10(n int) int {
	p := 1
	for i := 0; i < n; i++ {
		p *= 10
	}
	return p
}

// padLeft 把数字左补零到 digits 位。
func padLeft(code uint32, digits int) string {
	s := fmt.Sprintf("%d", code)
	for len(s) < digits {
		s = "0" + s
	}
	return s
}
