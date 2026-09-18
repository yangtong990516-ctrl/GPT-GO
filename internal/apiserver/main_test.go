package apiserver

import (
	"os"
	"testing"
)

// TestMain 在跑 apiserver 测试前关闭全局控制台登录门禁。
//
// 这些测试直接构造 Server 并断言业务 API 返回 200,不携带会话 cookie;
// 全局门禁(默认开启)会把它们全部挡成 401。测试进程内统一禁用,
// 不影响生产默认行为(生产默认开启门禁,需 GPT_GO_AUTH_DISABLED=1 才关)。
func TestMain(m *testing.M) {
	_ = os.Setenv("GPT_GO_AUTH_DISABLED", "1")
	os.Exit(m.Run())
}
