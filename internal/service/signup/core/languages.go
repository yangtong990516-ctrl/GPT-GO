package core

import (
	"fmt"
	"math/rand"
	"strings"
)

// 国家 → 语言画像（IP 地理联动，1:1 对齐 codex-auto _runtime/fingerprint.py 的 _COUNTRY_PROFILES）。
//
// 用途：拿到 GeoIP 解出的国家码后，生成 Accept-Language（② HTTP 层）与 navigator.languages（③ JS 层）。
// 时区不再走本表——时区由 geoip.go 直接由出口 IP 解出真实 IANA 时区（更准）。
//
// 规则（与 Python 一致）：
//   - 主语言固定取 languages[0]；
//   - 从 languages[1:] 随机打乱后补到总数 3~5 个；
//   - 拼 Accept-Language 时主语言无 q，副语言 q 按 0.9/0.8/0.7… 递减。

var countryLanguages = map[string][]string{
	// 亚洲
	"JP": {"ja-JP", "ja", "en-US", "en", "zh-CN"},
	"CN": {"zh-CN", "zh", "en-US", "en"},
	"HK": {"zh-HK", "zh-CN", "zh", "en-US", "en"},
	"TW": {"zh-TW", "zh", "en-US", "en", "ja"},
	"KR": {"ko-KR", "ko", "en-US", "en", "ja"},
	"SG": {"zh-CN", "zh", "en-US", "en", "ms-MY", "ms"},
	"MY": {"ms-MY", "ms", "zh-CN", "zh", "en-US", "en"},
	"TH": {"th-TH", "th", "en-US", "en"},
	"VN": {"vi-VN", "vi", "en-US", "en"},
	"IN": {"en-IN", "en-US", "en", "hi-IN", "hi"},
	"ID": {"id-ID", "id", "en-US", "en"},
	"PH": {"en-US", "en", "tl-PH", "tl"},
	"PK": {"en-US", "en", "ur-PK", "ur"},
	"BD": {"bn-BD", "bn", "en-US", "en"},
	"IL": {"he-IL", "he", "en-US", "en", "ar"},
	"TR": {"tr-TR", "tr", "en-US", "en"},
	"SA": {"ar-SA", "ar", "en-US", "en"},
	"AE": {"ar-AE", "ar", "en-US", "en"},
	// 北美
	"US": {"en-US", "en", "es-US", "es", "zh-CN"},
	"CA": {"en-CA", "en-US", "en", "fr-CA", "fr"},
	"MX": {"es-MX", "es", "en-US", "en"},
	// 南美
	"BR": {"pt-BR", "pt", "en-US", "en", "es"},
	"AR": {"es-AR", "es", "en-US", "en"},
	"CL": {"es-CL", "es", "en-US", "en"},
	"CO": {"es-CO", "es", "en-US", "en"},
	// 欧洲
	"GB": {"en-GB", "en-US", "en", "fr", "de"},
	"DE": {"de-DE", "de", "en-US", "en", "fr"},
	"FR": {"fr-FR", "fr", "en-US", "en", "de"},
	"IT": {"it-IT", "it", "en-US", "en", "fr"},
	"ES": {"es-ES", "es", "en-US", "en", "fr"},
	"NL": {"nl-NL", "nl", "en-US", "en", "de"},
	"BE": {"nl-BE", "fr-BE", "nl", "fr", "en-US", "en"},
	"CH": {"de-CH", "fr-CH", "de", "fr", "it", "en-US", "en"},
	"SE": {"sv-SE", "sv", "en-US", "en"},
	"NO": {"nb-NO", "nb", "en-US", "en"},
	"DK": {"da-DK", "da", "en-US", "en"},
	"FI": {"fi-FI", "fi", "sv", "en-US", "en"},
	"PL": {"pl-PL", "pl", "en-US", "en"},
	"RU": {"ru-RU", "ru", "en-US", "en"},
	"UA": {"uk-UA", "uk", "ru", "en-US", "en"},
	"CZ": {"cs-CZ", "cs", "en-US", "en", "de"},
	"AT": {"de-AT", "de", "en-US", "en"},
	"GR": {"el-GR", "el", "en-US", "en"},
	"PT": {"pt-PT", "pt", "en-US", "en", "es"},
	// 大洋洲
	"AU": {"en-AU", "en-US", "en", "zh-CN", "zh"},
	"NZ": {"en-NZ", "en-US", "en"},
	// 非洲
	"ZA": {"en-ZA", "en-US", "en", "af"},
	"EG": {"ar-EG", "ar", "en-US", "en"},
	"NG": {"en-NG", "en-US", "en"},
	"KE": {"sw-KE", "sw", "en-US", "en"},
}

// defaultLanguages 是未知国家码的兜底语言池（对齐 Python _DEFAULT_COUNTRY_PROFILE.languages）。
var defaultLanguages = []string{"en-US", "en"}

// languagesForCountry 返回国家码对应的语言池；未知国家返回兜底池。
func languagesForCountry(countryCode string) []string {
	cc := strings.ToUpper(strings.TrimSpace(countryCode))
	if pool, ok := countryLanguages[cc]; ok {
		return pool
	}
	return defaultLanguages
}

// buildAcceptLanguage 由语言池构建 Accept-Language 串（② HTTP 层），对齐 Python buildLangFull。
//
// 主语言无 q 固定第一位；副语言随机打乱、q 值按 0.9/0.8/0.7… 递减；总数 3~5 个。
// 返回 (langPrimary, langFull, languagesList)，其中 languagesList 原样喂 JS 沙箱 navigator.languages。
func buildAcceptLanguage(r *rand.Rand, countryCode string) (primary, full string, list []string) {
	pool := append([]string(nil), languagesForCountry(countryCode)...)
	if len(pool) == 0 {
		pool = defaultLanguages
	}

	lo := minInt(3, len(pool))
	hi := minInt(5, len(pool))
	num := lo
	if hi > lo {
		num = lo + r.Intn(hi-lo+1)
	}

	primary = pool[0]
	others := append([]string(nil), pool[1:]...)
	r.Shuffle(len(others), func(i, j int) { others[i], others[j] = others[j], others[i] })

	selected := []string{primary}
	need := num - 1
	if need > len(others) {
		need = len(others)
	}
	selected = append(selected, others[:need]...)

	parts := make([]string, 0, len(selected))
	for i, lang := range selected {
		if i == 0 {
			parts = append(parts, lang)
			continue
		}
		q := 1.0 - float64(i)*0.1
		parts = append(parts, fmt.Sprintf("%s;q=%.1f", lang, q))
	}
	return primary, strings.Join(parts, ","), selected
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
