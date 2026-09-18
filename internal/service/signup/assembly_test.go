// assembly_test.go 端到端装配编译验证：证明 docs/SESSION-PROXY-PIPELINE.md §5 的
// 接入示例真的能拼起来——proxy.Service / email / account 经适配器接入 signup.Service，
// 所有接口匹配、类型闭环。不联网、不跑真实协议（只验证装配编译 + 接口断言）。
package signup

import (
	"testing"

	"gpt-go/internal/service/account"
	"gpt-go/internal/service/email"
	"gpt-go/internal/service/proxy"
	"gpt-go/internal/store"
)

// TestFullAssembly 验证完整装配链（无 solver/dialer，仅资源与代理层）。
func TestFullAssembly(t *testing.T) {
	// 存储（生产换 Mongo；Mock 足够验证接口匹配）。
	emailStore := store.NewMockEmailStore()
	accStore := store.NewMockAccountStore()
	proxyStore := store.NewMockProxyStore()

	// 服务。
	emailSvc := email.NewService(emailStore)
	accSvc := account.NewService(accStore)
	proxySvc := proxy.NewService(proxyStore)
	_ = emailSvc // 邮箱池经 emailStore 进 ResourceAdapter（见下）

	// 代理适配器：proxy.Service → signup.ProxyStore（编译期接口断言已在适配器内）。
	var proxyPort ProxyStore = NewProxyServiceAdapter(proxySvc)

	// 资源适配器：emailStore + account.Service → signup.ResourceStore。
	var resPort ResourceStore = NewResourceAdapter(emailStore, accSvc)

	// 拨号器（模型 B）。
	dialer := NewSessionDialer(NewCDNProber())

	// 落库适配器（与 resPort 同一实例）。
	adapter := NewResourceAdapter(emailStore, accSvc)

	// 注册服务：注入代理/资源/拨号器/落库适配器（solver 由 P4 提供，这里 nil 验证可选注入）。
	svc := NewService(proxyPort, resPort, nil,
		WithDialer(dialer),
		WithResourceAdapter(adapter),
	)
	if svc == nil {
		t.Fatal("NewService 返回 nil")
	}

	// 编译期断言：account.Service 必须满足 AccountCreator（PersistAccount 用）。
	var _ AccountCreator = accSvc
}

// TestProxyStoreInterfaceMatch 单独验证 proxy.Service 适配后满足 signup.ProxyStore。
func TestProxyStoreInterfaceMatch(t *testing.T) {
	proxySvc := proxy.NewService(store.NewMockProxyStore())
	// 若适配器没实现全接口，这里编译失败。
	var ps ProxyStore = NewProxyServiceAdapter(proxySvc)
	if ps == nil {
		t.Fatal("proxy adapter nil")
	}
}

// TestAccountServiceSatisfiesCreator 验证 account.Service 满足 AccountCreator 接口。
func TestAccountServiceSatisfiesCreator(t *testing.T) {
	accSvc := account.NewService(store.NewMockAccountStore())
	var ac AccountCreator = accSvc
	if ac == nil {
		t.Fatal("account.Service 不满足 AccountCreator")
	}
}
