package protocol

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"regexp"
	"strings"
	"time"
)

func randomToken(bytesLen int) (string, error) {
	buf := make([]byte, bytesLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return strings.TrimRight(base64.URLEncoding.EncodeToString(buf), "="), nil
}

func randomUUID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	hexed := make([]byte, 32)
	hex.Encode(hexed, buf)
	return string(hexed[0:8]) + "-" + string(hexed[8:12]) + "-" + string(hexed[12:16]) + "-" + string(hexed[16:20]) + "-" + string(hexed[20:32]), nil
}

func maskSecret(value string, keep int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if keep <= 0 || len(value) <= keep {
		return "********"
	}
	return "********" + value[len(value)-keep:]
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func normalizeAccountIDSelection(primary string, values []string) []string {
	rawValues := make([]string, 0, 1+len(values))
	rawValues = append(rawValues, primary)
	rawValues = append(rawValues, values...)
	seen := map[string]struct{}{}
	out := make([]string, 0, len(rawValues))
	for _, raw := range rawValues {
		for _, token := range splitAccountIDTokens(raw) {
			if _, ok := seen[token]; ok {
				continue
			}
			seen[token] = struct{}{}
			out = append(out, token)
		}
	}
	return out
}

func splitAccountIDTokens(value string) []string {
	return strings.FieldsFunc(value, func(r rune) bool {
		switch r {
		case ',', '，', ';', '；', '|', '\n', '\r', '\t', ' ':
			return true
		default:
			return false
		}
	})
}

var (
	contextOTPRegex = regexp.MustCompile(`(?i)(?:openai|chatgpt|otp|code|verification|验证码|验证|代码)[^\d]{0,80}(\d{6})`)
	plainOTPRegex   = regexp.MustCompile(`\b(\d{6})\b`)
)

func ExtractOTP(text string) string {
	if matches := contextOTPRegex.FindStringSubmatch(text); len(matches) == 2 && validOTP(matches[1]) {
		return matches[1]
	}
	for _, matches := range plainOTPRegex.FindAllStringSubmatch(text, -1) {
		if len(matches) == 2 && validOTP(matches[1]) {
			return matches[1]
		}
	}
	return ""
}

func extractOTP(text string) string {
	return ExtractOTP(text)
}

func validOTP(code string) bool {
	return len(code) == 6 && code != "000000"
}

func firstNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}

func normalizeICloudIMAPEmail(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
