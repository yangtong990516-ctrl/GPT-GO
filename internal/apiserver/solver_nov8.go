//go:build !v8

package apiserver

import (
	"context"

	"gpt-go/internal/service/signup/sentinel"

	"gpt-go/internal/util"
)

// buildSolver（非 v8 构建）无可用 solver，返回 nil。
//
// 这是【降级路径，不应出现在生产】：v8 是硬依赖，部署必须用 Makefile（固定 -tags v8）
// 构建。本分支只在「不带 v8 tag 的开发/快速编译」时编译到——此时注册会在 sentinel
// 步骤返回明确错误（NewFlowRunner 对 nil solver 的处理），提醒构建漏了 -tags v8。
func buildSolver(ctx context.Context) sentinel.Solver {
	util.Logger().Warn("未以 -tags v8 构建，Sentinel solver 不可用。" +
		"生产部署请用 `make build`（默认带 v8）；否则注册会在 sentinel 步骤失败")
	return nil
}
