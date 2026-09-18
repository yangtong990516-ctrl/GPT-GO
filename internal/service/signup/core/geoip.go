package core

import (
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/oschwald/geoip2-golang"

	"gpt-go/internal/util"
)

// GeoIP 时区/国家解析器（git-lfs 数据源的正式接入点）。
//
// 数据源：GeoLite2-City.mmdb（~67MB 真实 MaxMind 库），提供 CountryCode + IANA TimezoneID。
// 在 GPT-GO 中该文件经符号链接接入 data/geoip/GeoLite2-City.mmdb（指向 codex-auto 的 git-lfs 实体）。
//
// 与 codex-auto 的关系：Python 版 _runtime/fingerprint.py 的「时区/语言联动」国家码来自
// check_proxy 探测（cloudflare trace 的 loc=），再查 _COUNTRY_PROFILES 表。本包升级为：
// 优先用本地 GeoIP 直接由出口 IP 解出「国家码 + IANA 时区」，时区不再依赖固定映射表——
// 更准（真实 IP 地理位置），也免去维护多值加权时区表。
//
// 设计说明：
//   - 本解析器是 core 私有实现，与 internal/service/proxy/geoip.go 逻辑同构但独立
//     （proxy 包那份服务于代理体检，未导出；core 需要自己的、面向「身份装配」语义的最小查询）。
//   - 懒加载 + 文件变更热重载；DB 缺失（如 git-lfs 未拉取、仍是 ~133B 指针占位）时返回 nil，
//     由调用方回退（country 用代理自带国家码、时区走 _COUNTRY_PROFILES 兜底）。
//   - 线程安全（批量注册多 goroutine 并发查询）。

// GeoLocation 是一次 GeoIP 查询结果（国家码 + IANA 时区）。
type GeoLocation struct {
	CountryCode string // ISO 3166-1 alpha-2，如 "US" / "JP" / "DE"
	TimezoneID  string // IANA 时区名，如 "America/New_York" / "Asia/Tokyo"
}

// geoResolver 是懒加载、热重载、互斥保护的 GeoLite2 reader。
type geoResolver struct {
	mu     sync.Mutex
	path   string
	reader *geoip2.Reader
	mtime  int64
	size   int64
	loaded bool
}

var defaultGeoResolver = &geoResolver{path: resolveGeoIPDBPath()}

// resolveGeoIPDBPath 按优先级定位 mmdb：
//  1. AUTOREGISTER_GEOIP_DB 环境变量（与 proxy 包对齐，便于服务器统一配置）；
//  2. 从当前工作目录向上查找 data/geoip/GeoLite2-City.mmdb（覆盖 go test 与项目根运行）；
//  3. /data/geoip/GeoLite2-City.mmdb 绝对路径兜底（服务器 /data 盘）。
func resolveGeoIPDBPath() string {
	if p := util.EnvOrDefault("AUTOREGISTER_GEOIP_DB", ""); p != "" && isRealMMDBFile(p) {
		return p
	}
	dir, err := os.Getwd()
	if err == nil {
		for {
			candidate := dir + "/data/geoip/GeoLite2-City.mmdb"
			if isRealMMDBFile(candidate) {
				return candidate
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	if isRealMMDBFile("/data/geoip/GeoLite2-City.mmdb") {
		return "/data/geoip/GeoLite2-City.mmdb"
	}
	return ""
}

// isRealMMDBFile 排除 git-lfs 指针占位（~133B 纯文本），只认真实数据库（>1KB）。
func isRealMMDBFile(p string) bool {
	fi, err := os.Stat(p)
	if err != nil {
		return false
	}
	return fi.Size() > 1024
}

// LookupGeo 由出口 IP 解出国家码 + IANA 时区；DB 缺失或 IP 非法时返回 nil。
//
// 这是「git-lfs 时区内容自动填充」的唯一入口：拿到 TimezoneID 后，装配层直接把它
// 写进 Identity.Timezone（喂 JS 沙箱的 Intl/Date 对齐）与 Identity.AcceptLanguage 的
// 国家语言联动（languages.go），实现「IP 在哪国，时区/语言就像哪国」。
func LookupGeo(ip string) *GeoLocation {
	return defaultGeoResolver.lookup(ip)
}

func (g *geoResolver) lookup(ip string) *GeoLocation {
	if g.path == "" || ip == "" {
		return nil
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return nil
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if !g.loaded {
		if !g.load() {
			return nil
		}
	} else {
		g.maybeReloadLocked()
	}
	if g.reader == nil {
		return nil
	}

	rec, err := g.reader.City(parsed)
	if err != nil {
		return nil
	}
	return &GeoLocation{
		CountryCode: rec.Country.IsoCode,
		TimezoneID:  rec.Location.TimeZone,
	}
}

func (g *geoResolver) load() bool {
	fi, err := os.Stat(g.path)
	if err != nil {
		return false
	}
	reader, err := geoip2.Open(g.path)
	if err != nil {
		return false
	}
	g.reader = reader
	g.mtime = fi.ModTime().Unix()
	g.size = fi.Size()
	g.loaded = true
	return true
}

func (g *geoResolver) maybeReloadLocked() {
	fi, err := os.Stat(g.path)
	if err != nil {
		return
	}
	if fi.ModTime().Unix() != g.mtime || fi.Size() != g.size {
		old := g.reader
		g.reader = nil
		g.loaded = false
		if g.load() && old != nil {
			_ = old.Close()
		}
	}
}
