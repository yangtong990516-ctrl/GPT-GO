//go:build v8

package sentinel

// CodeCache 落盘管理：sdk.js 的 V8 预编译字节码缓存。
//
// 路径约定（对齐 Python _ensure_sdk_file 的缓存目录）：
//   <cacheRoot>/sdk/<version>/sdk.v8cache
//
// cacheRoot 解析优先级：
//   1. SENTINEL_CACHE_DIR env
//   2. os.UserCacheDir()/gpt-go
// 缓存随 sdk 版本（Version 常量）与 V8 版本一起失效——
// V8 拒绝过期 cache 时 CompileUnboundScript 不报错但 CachedData.Rejected=true，
// 调用方据此回退全量编译并重写缓存。

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	v8 "github.com/robomotionio/v8go"
)

// codeCacheDir 返回缓存目录（不主动创建——写路径才创建）。
func codeCacheDir(version string) (string, error) {
	root := os.Getenv("SENTINEL_CACHE_DIR")
	if root == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			return "", err
		}
		root = filepath.Join(base, "gpt-go")
	}
	return filepath.Join(root, "sdk", version), nil
}

// cacheFile 布局：前 64 字节是 sdk 源码 sha256 的 hex（防错配），其后是 V8 字节码。
const cacheDigestHexLen = 64

// loadCodeCache 读磁盘缓存并校验 sdk 源码摘要；未命中/损坏/错配返回 (nil, error)。
func loadCodeCache(version, sdkSrc string) (*v8.CompilerCachedData, error) {
	dir, err := codeCacheDir(version)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "sdk.v8cache"))
	if err != nil {
		return nil, err
	}
	if len(data) < cacheDigestHexLen+1 {
		return nil, fmt.Errorf("cache 文件过小")
	}
	if string(data[:cacheDigestHexLen]) != sdkDigest(sdkSrc) {
		return nil, fmt.Errorf("cache 与当前 sdk 源码错配")
	}
	return &v8.CompilerCachedData{Bytes: data[cacheDigestHexLen:]}, nil
}

// saveCodeCache 写磁盘缓存（带 sdk 源码摘要头；best-effort；写路径才创建目录）。
func saveCodeCache(version string, sdkSrc string, cacheBytes []byte) error {
	dir, err := codeCacheDir(version)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	payload := make([]byte, 0, cacheDigestHexLen+len(cacheBytes))
	payload = append(payload, sdkDigest(sdkSrc)...)
	payload = append(payload, cacheBytes...)
	tmp := filepath.Join(dir, ".sdk.v8cache.tmp")
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "sdk.v8cache"))
}

// removeCodeCache 删除磁盘缓存（V8 拒绝坏 cache 时调用，避免反复走慢路径）。
func removeCodeCache(version string) {
	dir, err := codeCacheDir(version)
	if err != nil {
		return
	}
	_ = os.Remove(filepath.Join(dir, "sdk.v8cache"))
}

// sdkDigest 返回 sdk 源码摘要（hex，防缓存错配）。
func sdkDigest(src string) string {
	sum := sha256.Sum256([]byte(src))
	return hex.EncodeToString(sum[:])
}
