// Local GeoIP lookup mirroring geoip_lookup.py: offline MaxMind GeoLite2-City
// mmdb query (country code + IANA timezone). Lazily loaded, hot-reloaded on file
// change, and degrades to nil (caller falls back to cdn-cgi/trace loc) when the
// database is missing.
package proxy

import (
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/oschwald/geoip2-golang"

	"gpt-go/internal/util"
)

// geoIPDBPath mirrors geoip_lookup._CANDIDATE_PATHS priority. It searches, in
// order: the AUTOREGISTER_GEOIP_DB env var, then walks up from the current
// working directory (covering both `go test` from a package dir and running the
// server from the project root), then the absolute /data/geoip path.
func geoIPDBPath() string {
	if p := util.EnvOrDefault("AUTOREGISTER_GEOIP_DB", ""); p != "" && isRealMMDB(p) {
		return p
	}
	// 向上搜索 data/geoip（覆盖测试与运行时两种工作目录）。
	dir, err := os.Getwd()
	if err == nil {
		for {
			candidate := dir + "/data/geoip/GeoLite2-City.mmdb"
			if isRealMMDB(candidate) {
				return candidate
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	// 绝对路径兜底（服务器 /data 盘）。
	if isRealMMDB("/data/geoip/GeoLite2-City.mmdb") {
		return "/data/geoip/GeoLite2-City.mmdb"
	}
	return ""
}

// isRealMMDB reports whether p is a real GeoLite2 database (excludes the ~133B
// git-lfs pointer placeholder, which is plain text).
func isRealMMDB(p string) bool {
	fi, err := os.Stat(p)
	if err != nil {
		return false
	}
	return fi.Size() > 1024
}

// GeoInfo mirrors geoip_lookup.GeoInfo.
type GeoInfo struct {
	CountryCode string
	TimezoneID  string
	Region      string
}

// geoipCache is a lazily-loaded, hot-reloaded GeoLite2 reader (mutex-guarded).
type geoipCache struct {
	mu     sync.Mutex
	path   string
	reader *geoip2.Reader
	mtime  int64
	size   int64
}

var geoip = &geoipCache{path: geoIPDBPath()}

// lookupGeoIP resolves an IP to country/timezone, or nil when unavailable.
func lookupGeoIP(ip string) *GeoInfo {
	if ip == "" {
		return nil
	}
	return geoip.lookup(ip)
}

func (c *geoipCache) lookup(ip string) *GeoInfo {
	if c.path == "" {
		return nil
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.reader == nil {
		if !c.load() {
			return nil
		}
	} else {
		c.maybeReload()
	}

	rec, err := c.reader.City(parsed)
	if err != nil {
		return nil
	}
	return &GeoInfo{
		CountryCode: rec.Country.IsoCode,
		TimezoneID:  rec.Location.TimeZone,
	}
}

// load opens the mmdb and records mtime/size for hot-reload detection.
func (c *geoipCache) load() bool {
	fi, err := os.Stat(c.path)
	if err != nil {
		return false
	}
	reader, err := geoip2.Open(c.path)
	if err != nil {
		return false
	}
	c.reader = reader
	c.mtime = fi.ModTime().Unix()
	c.size = fi.Size()
	return true
}

// maybeReload reopens the mmdb when the file changed on disk.
func (c *geoipCache) maybeReload() {
	fi, err := os.Stat(c.path)
	if err != nil {
		return
	}
	if fi.ModTime().Unix() != c.mtime || fi.Size() != c.size {
		old := c.reader
		c.reader = nil
		if c.load() && old != nil {
			_ = old.Close()
		}
	}
}
