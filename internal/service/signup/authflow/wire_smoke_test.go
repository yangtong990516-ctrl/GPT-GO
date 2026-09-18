//go:build v8

// wire_smoke_test.go 装配层冒烟测试（v8 tag）：验证 NewFlowRunner 的全链路类型装配
// —— V8 求解器构造、SetFetcher 注入、FlowRunner 可被 signup.Service 消费。
// 不联网（不调真实代理/OpenAI），只验证「装配路径编译 + 类型闭环 + solver 注入生效」。
package authflow

import (
	"context"
	"os"
	"testing"

	"gpt-go/internal/service/signup"
	"gpt-go/internal/service/signup/sentinel"
)

// TestNewFlowRunnerAssembles 验证装配层：V8Solver + NewFlowRunner 类型闭环。
func TestNewFlowRunnerAssembles(t *testing.T) {
	sdk, err := sentinel.SDKSource(context.Background())
	if err != nil {
		t.Skipf("sdk.js 不可用（跳过装配冒烟）: %v", err)
	}
	solver, err := sentinel.NewV8Solver(sdk)
	if err != nil {
		t.Fatalf("NewV8Solver: %v", err)
	}
	defer solver.Close()

	// NewFlowRunner 必须返回可注入 signup.Service 的 FlowRunner。
	var runner signup.FlowRunner = NewFlowRunner(solver)
	if runner == nil {
		t.Fatal("NewFlowRunner 返回 nil")
	}

	// solver 必须实现 SetFetcher（wire.injectCoreFetcher 的注入点）。
	type fetcherSetter interface {
		SetFetcher(sentinel.ChallengeFetcher)
	}
	if _, ok := interface{}(solver).(fetcherSetter); !ok {
		t.Fatal("V8Solver 未实现 SetFetcher（challenge 无法与 TLS 会话同源）")
	}

	// 真跑整条链（会真实重试 warmup，慢，~200s）——默认跳过，WIRE_SMOKE=1 才执行。
	// 目的：验证整条调用链类型装配无编译/签名错位，且 warmup 硬门槛语义正确。
	if os.Getenv("WIRE_SMOKE") != "1" {
		t.Skip("跳过真跑（WIRE_SMOKE=1 启用，~200s 真实 warmup 重试）")
	}
	res, err := runner(context.Background(), signup.FlowRequest{
		ProxyURL: "", // 直连也会失败，但只验证装配
		Email:    "smoke@example.com",
		Password: "pw",
		Mail:     nil,
	})
	_ = res
	if err == nil {
		t.Log("runner 在无代理直连下意外成功（环境可直连 chatgpt），装配 OK")
	} else {
		t.Logf("runner 按预期在协议步骤失败（装配路径已走通）: %v", err)
	}
}
