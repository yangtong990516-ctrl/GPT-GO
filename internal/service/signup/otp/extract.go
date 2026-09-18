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
	// reStyleBlock/reScriptBlock/reHeadBlock 剥离 <style>/<script>/<head> 块
	//（防 CSS 颜色值/脚本数字被误当验证码；DOTALL 跨行，非贪婪）。
	reStyleBlock  = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	reScriptBlock = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	reHeadBlock   = regexp.MustCompile(`(?is)<head[^>]*>.*?</head>`)
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

	// 1) 最可靠：<span>6位</span>（HTML 标签包裹的验证码,基本是有意放置,直接采用,
	//    不做平凡码排除——平凡码排除留给下方打分路径的兜底场景)。
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

	// 4.5) 剔除 <style>/<script>/<head> 整块(治本):ChatGPT OTP 邮件模板头部有
	//    大量 CSS 颜色值(color:#667085 / #202123 / #353740),是 6 位十六进制,
	//    会被误当验证码 → wrong_email_otp_code。真码在邮件正文可见区,先剥掉这些块。
	body = reStyleBlock.ReplaceAllString(body, " ")
	body = reScriptBlock.ReplaceAllString(body, " ")
	body = reHeadBlock.ReplaceAllString(body, " ")

	// 5) 自定义正则：调用方明确给了格式,取第一个匹配（保持兼容）。
	if codePattern != nil {
		if m := codePattern.FindStringSubmatch(body); m != nil {
			if len(m) >= 2 {
				return m[1]
			}
			return m[0]
		}
		return ""
	}

	// 6) 内置路径：打分制选最佳候选（对齐 codex-auto mailbox_client._extract_code_generic）。
	//    历史 bug:旧版 FindStringSubmatch 取第一个 6 位数,邮件里有 CSS 颜色
	//    (color:#202123)、多封历史邮件并存时,会取到旧码/干扰码 → wrong_email_otp_code。
	//    改为:收集全部候选 → 剔除平凡码/日期 → 按「独立成词 + 软提示词上下文 + 位置」打分,
	//    取得分最高者（阈值 3.0,即至少独立成词）。
	return bestOTPByScore(body)
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

// ═══════════════════════════════════════════════════════════════════════════
// 打分制候选筛选（对齐 codex-auto mailbox_client.py）
// ═══════════════════════════════════════════════════════════════════════════

// trivialCodes 平凡数字：占位符/示例/顺序/回文/全同,绝不可能是真实验证码。
// 对齐 codex _TRIVIAL_CODES + _ALL_SAME_CODES。
var trivialCodes = map[string]bool{
	"000000": true, "111111": true, "222222": true, "333333": true, "444444": true,
	"555555": true, "666666": true, "777777": true, "888888": true, "999999": true,
	"123456": true, "234567": true, "345678": true, "456789": true, "567890": true,
	"678901": true, "654321": true, "765432": true, "876543": true, "987654": true,
	"121212": true, "112233": true, "123123": true,
}

// softCodeHints 软提示词:候选码附近(±80字符)出现则加分(跨语言)。
// 对齐 codex _SOFT_CODE_HINTS + VERIFICATION_PATTERN 的核心词。
var softCodeHints = []string{
	"code", "verify", "verification", "otp",
	"验证码", "驗證碼", "临时验证码", "認証", "検証", "確認",
}

// isTrivialCode 判断是否平凡码。
func isTrivialCode(code string) bool { return trivialCodes[code] }

// isDateLikeCode 剔除日期成分:YYYYMM(202408)/YYMMDD 这类不是验证码。
func isDateLikeCode(code string) bool {
	if len(code) != 6 {
		return false
	}
	n := func(s string) int {
		v := 0
		for _, c := range s {
			if c < '0' || c > '9' {
				return -1
			}
			v = v*10 + int(c-'0')
		}
		return v
	}
	y, m := n(code[:4]), n(code[4:6])
	if y >= 1900 && y <= 2099 && m >= 1 && m <= 12 { // YYYYMM
		return true
	}
	mm, dd := n(code[2:4]), n(code[4:6])
	if mm >= 1 && mm <= 12 && dd >= 1 && dd <= 31 { // YYMMDD
		return true
	}
	return false
}

// otpCandidate 是一个 6 位数字候选及其在文本中的位置。
type otpCandidate struct {
	code  string
	start int
	end   int
}

// reCandidate6 匹配独立 6 位数字(前后非数字,前缀也不能是 # 防 CSS 颜色值
// 如 .meta{color:#667085} —— ChatGPT 邮件模板头部有大量 color:#XXXXXX,是 wrong
// email_otp_code 的统一根因:颜色码被误当验证码提交)。
var reCandidate6 = regexp.MustCompile(`(^|[^\d#])(\d{6})($|[^\d])`)

// bestOTPByScore 收集全部 6 位候选,剔除平凡/日期,按打分选最佳。
// 打分(对齐 codex _score):独立成词+3、软提示词上下文+1、位置靠后轻微加权。
// 阈值 3.0(独立成词)才返回,否则空。
func bestOTPByScore(text string) string {
	if text == "" {
		return ""
	}
	cands := map[string]otpCandidate{} // 按 code 去重,保留最早位置
	for _, m := range reCandidate6.FindAllStringSubmatchIndex(text, -1) {
		code := text[m[4]:m[5]]
		if isDateLikeCode(code) {
			continue // 日期成分永远不是验证码
		}
		start, end := m[4], m[5]
		if old, ok := cands[code]; !ok || start < old.start {
			cands[code] = otpCandidate{code: code, start: start, end: end}
		}
	}
	if len(cands) == 0 {
		return ""
	}
	best := ""
	bestScore := 3.0 // 阈值:独立成词
	for _, c := range cands {
		score := scoreOTP(text, c)
		if score > bestScore {
			bestScore = score
			best = c.code
		}
	}
	return best
}

// scoreOTP 给一个候选打分。
// 平凡码(000000/123456/顺序/回文)独立成词不直接给满分——必须附近有软提示词
// (「code/验证码/認証」等)才采信,防把占位示例数字当真码;非平凡码独立成词即 +3。
func scoreOTP(text string, c otpCandidate) float64 {
	score := 0.0
	isAlnum := func(b byte) bool {
		return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
	}
	beforeOK := c.start == 0 || !isAlnum(text[c.start-1])
	afterOK := c.end >= len(text) || !isAlnum(text[c.end])
	trivial := isTrivialCode(c.code)
	// 软提示词上下文(±80 字符)。
	lo := c.start - 80
	if lo < 0 {
		lo = 0
	}
	hi := c.end + 80
	if hi > len(text) {
		hi = len(text)
	}
	ctx := lowerASCII(text[lo:hi])
	hasHint := false
	for _, h := range softCodeHints {
		if index(ctx, h) >= 0 {
			hasHint = true
			break
		}
	}

	if beforeOK && afterOK {
		if trivial && !hasHint {
			// 平凡码且无上下文提示:不给独立成词分(视为占位/示例数字)。
			score += 0.0
		} else {
			score += 3.0
		}
	} else if beforeOK || afterOK {
		score += 1.5
	}
	if hasHint {
		score += 1.0
	}
	// 位置靠后(正文)轻微加权。
	if len(text) > 0 {
		score += minF(1.0, float64(c.start)/float64(len(text)))
	}
	return score
}

// lowerASCII 转小写(仅 ASCII;中文提示词不受影响,直接子串匹配)。
func lowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
