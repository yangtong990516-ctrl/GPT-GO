package sentinel

import (
	"context"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

//go:embed testdata/sdk.js
var embeddedSDK embed.FS

const embeddedSDKPath = "testdata/sdk.js"

// sdkCache 缓存 sdk.js 源码（进程内），避免重复读文件/下载。
var (
	sdkCacheOnce sync.Once
	sdkCacheSrc  []byte
	sdkCacheErr  error
)

// SDKSource 返回 sdk.js 源码。
//
// 优先级（对齐 Python _ensure_sdk_file）：
//  1. 进程内缓存
//  2. 项目内固定路径 _runtime/sdk/<ver>/sdk.js（SDK_VERSION_PATH 指定）
//  3. 内置 embed（testdata/sdk.js，随二进制打包）
//
// 注意：Python 还会联网下载 sdk.js 到项目路径；Go 侧以 embed 为主，
// 但保留 SDKVersionPath 环境变量让运维可覆盖为更新版本（对齐 Scheduler 自动拉新）。
func SDKSource(ctx context.Context) ([]byte, error) {
	sdkCacheOnce.Do(func() {
		sdkCacheSrc, sdkCacheErr = loadSDKSource(ctx)
	})
	return sdkCacheSrc, sdkCacheErr
}

func loadSDKSource(ctx context.Context) ([]byte, error) {
	// 1) 项目内固定路径覆盖（运维/巡检更新 SDK 用）。
	if p := os.Getenv("SENTINEL_SDK_FILE"); p != "" {
		if data, err := os.ReadFile(p); err == nil && len(data) > 0 {
			return data, nil
		}
	}
	// 也检查默认的项目内路径（对齐 Python _sdk_dir）。
	if p := defaultSDKDir(); p != "" {
		if data, err := os.ReadFile(filepath.Join(p, "sdk.js")); err == nil && len(data) > 0 {
			return data, nil
		}
	}

	// 2) 内置 embed。
	data, err := embeddedSDK.ReadFile(embeddedSDKPath)
	if err != nil {
		return nil, fmt.Errorf("sentinel: 加载内置 sdk.js 失败: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("sentinel: 内置 sdk.js 为空")
	}
	return data, nil
}

// defaultSDKDir 返回项目内 sdk 落盘目录（对齐 Python _sdk_dir）。
// 返回空表示未找到（无此目录则回退 embed）。
func defaultSDKDir() string {
	// 与 Python 保持一致：protocol_signup/_runtime/sdk/<version>。
	candidates := []string{
		"sdk/" + Version,       // 相对于运行目录
		"../../sdk/" + Version, // 相对某些部署目录
		"internal/service/signup/sentinel/sdk/" + Version,
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && st.IsDir() {
			abs, err := filepath.Abs(c)
			if err == nil {
				return abs
			}
		}
	}
	return ""
}
