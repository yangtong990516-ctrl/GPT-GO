package signup

import (
	"context"
	"sync"
)

// BatchParams 是批量注册的参数，对齐 Python run_protocol_batch。
type BatchParams struct {
	RunParams

	// Count 是目标注册数量。
	Count int
	// Concurrency 是并发数。
	Concurrency int
	// StopOnError 首错即停。
	StopOnError bool
	// Cancel 是取消信号（对齐 Python cancel_event）。
	Cancel <-chan struct{}
	// Progress 是单账号完成回调（对齐 Python progress_callback）。
	Progress ProgressCallback
}

// RunBatch 批量执行注册，对齐 Python run_protocol_batch。
//
// 骨架阶段：并发编排结构已就绪，单账号 Run 未接入真实状态机。
func (s *Service) RunBatch(ctx context.Context, params BatchParams) (*BatchResult, error) {
	if params.Count <= 0 {
		return &BatchResult{}, nil
	}
	if params.Concurrency <= 0 {
		params.Concurrency = 1
	}

	result := &BatchResult{}
	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		sem    = make(chan struct{}, clampConcurrency(params.Concurrency))
		cancel = make(chan struct{})
	)
	defer close(cancel)

	ctx, cancelCtx := context.WithCancel(ctx)
	defer cancelCtx()

	// 监听外部取消信号。
	go func() {
		select {
		case <-params.Cancel:
			cancelCtx()
		case <-cancel:
		}
	}()

	dispatched := 0
	for i := 0; i < params.Count; i++ {
		// 取信号量时同时监听取消：sem 满阻塞也能被取消即时打断（对齐 cancel_event）。
		select {
		case <-ctx.Done():
			// 已取消：未派发的（本个及之后）计入 Cancelled，停止派发；已派发的
			// 由下方 wg.Wait() 收编（它们各自完成时自行计数），不重不漏。
			mu.Lock()
			result.Cancelled += params.Count - dispatched
			mu.Unlock()
			i = params.Count // 结束循环
		case sem <- struct{}{}:
		}
		if i >= params.Count {
			break
		}
		dispatched++
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			defer func() { <-sem }()

			item := BatchItemResult{Index: index}
			_, err := s.Run(ctx, params.RunParams)

			if ctx.Err() != nil {
				item.Cancelled = true
				item.Code = "cancelled"
			} else if err != nil {
				item.OK = false
				if re, ok := err.(*RegistrationError); ok {
					item.Code = re.Code
					item.Email = re.Email
				} else {
					item.Code = "protocol_failed"
				}
				item.Error = err.Error()
			} else {
				item.OK = true
			}

			mu.Lock()
			switch {
			case item.Cancelled:
				result.Cancelled++
			case item.OK:
				result.OK++
				result.Results = append(result.Results, item)
			default:
				result.Failed++
				result.Errors = append(result.Errors, item)
			}
			mu.Unlock()

			if params.Progress != nil {
				params.Progress(item)
			}
		}(i)
	}

	// 收编所有已派发 goroutine（含取消前已派发的）——保证计数完整、无 goroutine 泄漏。
	wg.Wait()
	return result, nil
}

// clampConcurrency 把并发数钳制到 settings 合法边界（对齐 model.ValidateExecutionSettings
// 的 concurrency ∈ [1,12]）。防御性双保险：正常路径 settings API 已校验；此处兜住
// 「绕过 API 直接构造 BatchParams」的调用方，避免无界创建 goroutine（契约 6.5.6）。
func clampConcurrency(n int) int {
	if n < 1 {
		return 1
	}
	if n > 12 {
		return 12
	}
	return n
}
