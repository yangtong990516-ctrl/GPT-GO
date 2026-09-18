//go:build v8

package sentinel

// 测试辅助：对 v8go API 的薄封装，避免测试文件直接依赖 v8 包路径细节。

import (
	"os"
	"testing"

	v8 "github.com/robomotionio/v8go"
)

func newIsolate() *v8.Isolate { return v8.NewIsolate() }

func newV8Context(iso *v8.Isolate) *v8.Context { return v8.NewContext(iso) }

func compileOptsEmpty() v8.CompileOptions { return v8.CompileOptions{} }

func compileOptsCached(c *v8.CompilerCachedData) v8.CompileOptions {
	return v8.CompileOptions{CachedData: c}
}

func removeFile(p string) error { return os.Remove(p) }

func mustRun(t *testing.T, ctx *v8.Context, src, name string) {
	t.Helper()
	if _, err := ctx.RunScript(src, name); err != nil {
		t.Fatalf("run %s: %v", name, err)
	}
}

func mustRunVal(t *testing.T, ctx *v8.Context, src, name string) string {
	t.Helper()
	v, err := ctx.RunScript(src, name)
	if err != nil {
		t.Fatalf("run %s: %v", name, err)
	}
	return v.String()
}
