// extract.go 从邮件原文提取 6 位 OTP（防误判正则，对齐 Python extract_otp）。
//
// 历史教训（base.py 注释）：mail_outlook 和 mail_cf 曾各写一份规则且不一致，
// 收敛到这里后所有 provider 共用同一套防误判逻辑。
package otp

import "regexp"

// 防误判正则集合（对齐 Python base.py 的 _RE_*）。
var (
	// reSpanCode 优先匹配 <span>XXXXXX</span>（HTML 标签包裹的验证码，最可靠）。
	reSpanCode = regexp.MustCompile(`<span[^>]*>\s*(\d{6})\s*</span>`)
	// reEmailAddr 剔除邮箱地址（避免 user123456@x.com 的 123456 被误判成验证码）。
	reEmailAddr = regexp.MustCompile(`[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`)
	// reTSBoundary 剔除 MIME 时间戳边界（m=+XXXXXX. 形式）。
	reTSBoundary = regexp.MustCompile(`m=\+\d+\.\d+`)
	// reTSParam 剔除时间戳参数（t=XXXXXXXXXX 形式）。
	reTSParam = regexp.MustCompile(`\bt=\d+\b`)
	// reOTP6 兜底匹配 6 位数字（前后不能是 # 或其它数字，避免 hex 颜色/长数字误判）。
	// 注：Go RE2 不支持负向先行 (?!...)，故用「前缀非数字非# + 捕获6位 + 后文非数字或结尾」
	// 的等价写法：组1=前缀分隔符，组2=6位码，后随必须是非数字或字符串结束。
	reOTP6 = regexp.MustCompile(`(^|[^\d#])(\d{6})($|[^\d])`)
)

// ExtractOTP 从邮件原文提取 6 位 OTP，找不到返回空串。
//
// 防误判五步（顺序固定，对齐 Python extract_otp）：
//  1. 优先匹配 <span>XXXXXX</span>（HTML 标签包裹的验证码）
//  2. 跳过 MIME header（只搜 \r\n\r\n 之后的 body）
//  3. 剔除邮箱地址（避免 user123456@x.com 误判）
//  4. 剔除时间戳模式（m=+XXXXXX. 和 t=XXXXXXXXXX）
//  5. 剔除 hex 颜色（前缀 # 或紧跟其他数字的不算）
//
// codePattern 可选：调用方传自定义正则（如该邮箱服务商验证码格式特殊时）；
// 为空用内置 reOTP6。自定义正则应含一个捕获组（取组 1），无组则取整体匹配。
func ExtractOTP(raw string, codePattern *regexp.Regexp) string {
	if raw == "" {
		return ""
	}

	// 1) 最可靠：<span>6位</span>
	if m := reSpanCode.FindStringSubmatch(raw); m != nil {
		return m[1]
	}

	// 2) 跳过 MIME header
	body := raw
	if idx := indexOfHeaderEnd(raw); idx >= 0 {
		body = raw[idx:]
	}

	// 3-4) 剔除邮箱地址 + 时间戳
	body = reEmailAddr.ReplaceAllString(body, "")
	body = reTSBoundary.ReplaceAllString(body, "")
	body = reTSParam.ReplaceAllString(body, "")

	// 5) 匹配 6 位码
	pat := reOTP6
	if codePattern != nil {
		pat = codePattern
	}
	m := pat.FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	// reOTP6 的组 1 是前缀分隔符、组 2 才是码；自定义正则通常组 1 是码。
	if codePattern == nil && len(m) >= 3 {
		return m[2]
	}
	if len(m) >= 2 {
		return m[1]
	}
	return m[0]
}

// indexOfHeaderEnd 返回 MIME header 结束位置（\r\n\r\n 之后），找不到返回 -1。
// 兼容 \n\n（部分服务商不严格用 \r\n）。
func indexOfHeaderEnd(raw string) int {
	if i := index(raw, "\r\n\r\n"); i >= 0 {
		return i
	}
	if i := index(raw, "\n\n"); i >= 0 {
		return i
	}
	return -1
}

// index 是 strings.Index 的局部别名（避免为本包唯一用途引 strings，保持包内聚）。
func index(s, sep string) int {
	for i := 0; i+len(sep) <= len(s); i++ {
		if s[i:i+len(sep)] == sep {
			return i
		}
	}
	return -1
}
