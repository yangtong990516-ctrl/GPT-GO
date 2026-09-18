//go:build v8

package sentinel

import (
	"log"
	"os"
	"strings"
)

// anchorMissLogger 是 patch 锚点未命中的告警通道（默认 stderr，测试可替换）。
var anchorMissLogger = log.New(os.Stderr, "", log.LstdFlags)

func replaceOne(s, old, new string) string { return strings.Replace(s, old, new, 1) }
