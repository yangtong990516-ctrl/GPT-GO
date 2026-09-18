// helpers.go authflow 的小工具：随机姓名/生日、uuid、嵌套 JSON 取值、限停等。
package authflow

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"math/big"
	"regexp"
	"strings"
	"time"
)

// uuidv4 生成随机 UUIDv4（对齐 Python uuid.uuid4()，用于 device_id 兜底 / logging_id）。
func uuidv4() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "00000000-0000-4000-8000-000000000000"
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// extractDeviceIDFromHTML 从 authorize 响应 HTML 提取 oai-did（对齐 Python 的兜底正则）。
var deviceIDRe = regexp.MustCompile(`oai-did["\s:=]+([a-f0-9-]{36})`)

func extractDeviceIDFromHTML(html string) string {
	if m := deviceIDRe.FindStringSubmatch(html); m != nil {
		return m[1]
	}
	return ""
}

// nestedStr 从 map 里按路径取字符串（对齐 Python data.get("page",{}).get("type","")）。
func nestedStr(m map[string]any, path ...string) (string, bool) {
	cur := any(m)
	for _, k := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			return "", false
		}
		cur, ok = mm[k]
		if !ok {
			return "", false
		}
	}
	s, ok := cur.(string)
	return s, ok
}

// pageTypeOf 从 authorize/verify 响应提取 page.type（认证分支判断的核心信号）。
func pageTypeOf(data map[string]any) string {
	s, _ := nestedStr(data, "page", "type")
	return s
}

// continueURLOf 从响应提取 continue_url（下一步跳转地址）。
func continueURLOf(data map[string]any) string {
	if v, ok := data["continue_url"].(string); ok {
		return v
	}
	return ""
}

// nestedStrOf 是 nestedStr 的单值便捷版（拿不到返回空串）。
func nestedStrOf(data map[string]any, path ...string) string {
	s, _ := nestedStr(data, path...)
	return s
}

// nowUnix 返回当前 Unix 秒（OTP 防串号时间窗用）。
// 独立成函数便于测试替换（对齐 otp 包的 issuedAfter 语义）。
var nowUnix = func() int64 { return time.Now().Unix() }

// truncate 截断字符串到 n 字节（日志用）。
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// headerGet 大小写不敏感取响应头第一个值。
func headerGet(h map[string][]string, key string) string {
	for k, v := range h {
		if strings.EqualFold(k, key) && len(v) > 0 {
			return v[0]
		}
	}
	return ""
}

// originOf 返回 scheme://host（用于同源/跨站判定 sec-fetch-site）。
func originOf(u string) string {
	i := strings.Index(u, "://")
	if i < 0 {
		return u
	}
	rest := u[i+3:]
	if j := strings.Index(rest, "/"); j >= 0 {
		rest = rest[:j]
	}
	return u[:i+3] + rest
}

// sentinelHeaders 组装 sentinel + oai-device-id 额外头，喂给 core.Session.PostWithHeaders。
//
// 红线落地：
//   - `oai-device-id` 恒等于 deviceID（== oai-did cookie），auth.openai.com 域必带（三处同源）。
//   - `OpenAI-sentinel-so-token` 仅 soToken 非空时带（服务端本 flow 没要求就不带，不凑数）。
func sentinelHeaders(deviceID, sentinelToken, soToken string) map[string]string {
	h := map[string]string{}
	if deviceID != "" {
		h["oai-device-id"] = deviceID
	}
	if sentinelToken != "" {
		h["OpenAI-sentinel-token"] = sentinelToken
	}
	if soToken != "" {
		h["OpenAI-sentinel-so-token"] = soToken
	}
	return h
}

// sleepCtx 可被取消的 sleep（尊重 ctx 取消，避免批量注册时无法中断）。
func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// ── 随机姓名/生日（对齐 Python create_account 的 _FIRST/_LAST + birthdate）──

var firstNames = []string{
	"James", "John", "Robert", "Michael", "William", "David", "Richard",
	"Joseph", "Thomas", "Charles", "Mary", "Patricia", "Jennifer", "Linda",
	"Elizabeth", "Barbara", "Susan", "Jessica", "Sarah", "Karen",
}
var lastNames = []string{
	"Smith", "Johnson", "Williams", "Brown", "Jones", "Garcia", "Miller",
	"Davis", "Rodriguez", "Martinez", "Wilson", "Anderson", "Taylor", "Thomas",
}

func randomFullName() string {
	return pick(firstNames) + " " + pick(lastNames)
}

func randomBirthdate() string {
	y := 1985 + randInt(16) // 1985-2000
	mo := 1 + randInt(12)   // 1-12
	d := 1 + randInt(28)    // 1-28
	return sprintf4(y, mo, d)
}

func pick(s []string) string { return s[randInt(len(s))] }

// randomPassword 生成补密码用的强随机密码(对齐 codex _random_password):
// 字母+数字+符号混合,长度 16,保证各类字符至少出现一次。
func randomPassword() string {
	const (
		lower  = "abcdefghijkmnpqrstuvwxyz"
		upper  = "ABCDEFGHJKLMNPQRSTUVWXYZ"
		digits = "23456789"
		syms   = "!@#$%^&*"
	)
	all := lower + upper + digits + syms
	pickFrom := func(set string) byte { return set[randInt(len(set))] }
	buf := []byte{pickFrom(lower), pickFrom(upper), pickFrom(digits), pickFrom(syms)}
	for len(buf) < 16 {
		buf = append(buf, pickFrom(all))
	}
	// Fisher-Yates 打乱(避免前 4 位固定类别)。
	for i := len(buf) - 1; i > 0; i-- {
		j := randInt(i + 1)
		buf[i], buf[j] = buf[j], buf[i]
	}
	return string(buf)
}

// randInt 返回 [0,n) 的 crypto 随机整数。
func randInt(n int) int {
	if n <= 0 {
		return 0
	}
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return int(time.Now().UnixNano() % int64(n))
	}
	return int(v.Int64())
}

func sprintf4(y, mo, d int) string {
	pad := func(x int) string {
		if x < 10 {
			return "0" + itoa(x)
		}
		return itoa(x)
	}
	return itoa(y) + "-" + pad(mo) + "-" + pad(d)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
