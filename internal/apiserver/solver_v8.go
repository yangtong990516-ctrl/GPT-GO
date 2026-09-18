//go:build v8

package apiserver

import (
	"context"

	"gpt-go/internal/service/signup/sentinel"

	"go.uber.org/zap"

	"gpt-go/internal/util"
)

// buildSolver（v8 构建）装配真 Sentinel PoW 求解器：v8go 跑 sdk.js。
//
// v8 是硬依赖（用户决策）：注册真数据时必须 V8 解 PoW，故默认构建（Makefile 固定
// -tags v8）走这里。SDKSource 从 embed / 项目路径读 sdk.js；失败返回 nil + 记日志
// （不致命——注册时 sentinel 步骤会再报明确错误，此处不在启动期崩服务）。
func buildSolver(ctx context.Context) sentinel.Solver {
	src, err := sentinel.SDKSource(ctx)
	if err != nil {
		util.Logger().Warn("加载 sentinel sdk.js 失败，solver 置 nil", zap.Error(err))
		return nil
	}
	sv, ready, err := sentinel.NewV8SolverAsync(src)
	if err != nil {
		util.Logger().Warn("创建 V8 solver 失败，solver 置 nil", zap.Error(err))
		return nil
	}
	// 异步预热：后台 goroutine 编译首个 worker 入池（编译结果落盘 code cache 加速后续），
	// 不阻塞服务启动。预热完成仅记日志；首个注册若赶在预热前会懒编译（见 NewV8SolverAsync）。
	go func() {
		<-ready
		util.Logger().Info("Sentinel V8 solver 后台预热完成（首个 worker 已入池）")
	}()
	util.Logger().Info("Sentinel V8 solver 已装配（v8go + sdk.js，后台异步预热中）")
	return sv
}
