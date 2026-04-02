package agent

import (
	"fmt"
	"sync"
)

const defaultMaxRetries = 3

// retryTracker 跟踪每个工具的重试次数
// 按 "工具名+参数特征" 计数，防止 LLM 用完全相同的参数反复重试
type retryTracker struct {
	mu       sync.Mutex
	counts   map[string]int // key = "toolName:argsSignature"
	maxRetry int
}

func newRetryTracker(maxRetry int) *retryTracker {
	if maxRetry <= 0 {
		maxRetry = defaultMaxRetries
	}
	return &retryTracker{
		counts:   make(map[string]int),
		maxRetry: maxRetry,
	}
}

// retryKey 生成重试计数的 key
// 使用工具名 + 参数的前 200 字符作为签名，防止同参数死循环
func retryKey(toolName, args string) string {
	sig := args
	if len(sig) > 200 {
		sig = sig[:200]
	}
	return fmt.Sprintf("%s:%s", toolName, sig)
}

// record 记录一次失败，返回当前重试次数
func (rt *retryTracker) record(toolName, args string) int {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	key := retryKey(toolName, args)
	rt.counts[key]++
	return rt.counts[key]
}

// canRetry 检查是否还能重试
func (rt *retryTracker) canRetry(toolName, args string) bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	key := retryKey(toolName, args)
	return rt.counts[key] < rt.maxRetry
}

// getCount 获取当前重试次数
func (rt *retryTracker) getCount(toolName, args string) int {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	key := retryKey(toolName, args)
	return rt.counts[key]
}

// reset 重置某个工具的重试计数（成功后调用）
func (rt *retryTracker) reset(toolName, args string) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	key := retryKey(toolName, args)
	delete(rt.counts, key)
}
