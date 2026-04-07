package ctxwindow

import (
	"strings"
	"testing"
	"time"

	"github.com/SOULOFCINDERS/agent/internal/llm"
)

// =============================================================================
// TruncationCache 决策冻结测试
// =============================================================================

func TestTruncationCache_FreezeDecision(t *testing.T) {
	mgr := NewManager(ManagerConfig{
		MaxInputTokens:      500,
		ToolResultMaxTokens: 30,
		ProtectRecentRounds: 2,
		Model: ModelProfile{
			MaxContextTokens: 800,
			MaxOutputTokens:  300,
			ReserveRatio:     0.37,
		},
	})

	longResult := strings.Repeat("这是很长的工具结果内容。", 50)

	history := []llm.Message{
		{Role: "system", Content: "hi"},
		{Role: "user", Content: "查天气"},
		{Role: "assistant", Content: "", ToolCalls: []llm.ToolCall{
			{ID: "call_1", Type: "function", Function: llm.FunctionCall{Name: "weather", Arguments: `{"loc":"BJ"}`}},
		}},
		{Role: "tool", Content: longResult, ToolCallID: "call_1"},
		{Role: "user", Content: "ok"},
	}

	// 直接调用 truncateLongToolResults 来测试缓存行为
	copied := make([]llm.Message, len(history))
	copy(copied, history)
	mgr.truncateLongToolResults(copied)

	if mgr.TruncationCacheSize() != 1 {
		t.Errorf("expected cache size=1 after first truncation, got %d", mgr.TruncationCacheSize())
	}

	var truncated1 string
	for _, msg := range copied {
		if msg.ToolCallID == "call_1" {
			truncated1 = msg.Content
			break
		}
	}
	if truncated1 == "" {
		t.Fatal("tool result not found after truncation")
	}
	if !strings.Contains(truncated1, "[truncated") {
		t.Error("expected truncation marker")
	}

	// 第二次调用：使用原始长内容，但 cache 应该覆盖
	copied2 := make([]llm.Message, len(history))
	copy(copied2, history)
	mgr.truncateLongToolResults(copied2)

	var truncated2 string
	for _, msg := range copied2 {
		if msg.ToolCallID == "call_1" {
			truncated2 = msg.Content
			break
		}
	}

	if truncated2 != truncated1 {
		t.Errorf("TruncationCache violated: second call produced different result\n  first:  %q\n  second: %q",
			truncate(truncated1, 80), truncate(truncated2, 80))
	}

	t.Logf("Cache size: %d", mgr.TruncationCacheSize())
	t.Logf("Truncated content (frozen): %s", truncate(truncated1, 60))
}

func TestTruncationCache_ClearOnNewSession(t *testing.T) {
	mgr := NewManager(ManagerConfig{
		MaxInputTokens:      200,
		ToolResultMaxTokens: 20,
		Model: ModelProfile{
			MaxContextTokens: 300,
			MaxOutputTokens:  100,
			ReserveRatio:     0.33,
		},
	})

	mgr.mu.Lock()
	mgr.truncationCache["call_old"] = "cached content"
	mgr.mu.Unlock()

	if mgr.TruncationCacheSize() != 1 {
		t.Fatal("expected cache size 1")
	}

	mgr.ClearTruncationCache()
	if mgr.TruncationCacheSize() != 0 {
		t.Errorf("expected cache cleared, got size %d", mgr.TruncationCacheSize())
	}
}

func TestTruncationCache_NoToolCallID(t *testing.T) {
	mgr := NewManager(ManagerConfig{
		MaxInputTokens:      200,
		ToolResultMaxTokens: 20,
		Model: ModelProfile{
			MaxContextTokens: 300,
			MaxOutputTokens:  100,
			ReserveRatio:     0.33,
		},
	})

	longResult := strings.Repeat("very long content here ", 100)
	history := []llm.Message{
		{Role: "system", Content: "assistant"},
		{Role: "user", Content: "hello"},
		{Role: "tool", Content: longResult},
		{Role: "user", Content: "latest"},
	}

	mgr.Fit(history)
	if mgr.TruncationCacheSize() != 0 {
		t.Errorf("tool without ToolCallID should not be cached, got size %d", mgr.TruncationCacheSize())
	}
}

// =============================================================================
// Cache 冷热感知测试
// =============================================================================

func TestCacheTemperature_Cold(t *testing.T) {
	mgr := NewManager(ManagerConfig{
		Model:         DefaultProfile,
		ColdThreshold: 100 * time.Millisecond,
	})

	if mgr.CacheTemperature() != CacheCold {
		t.Error("initial state should be CacheCold")
	}

	mgr.UpdateLastAssistantTime(time.Now())
	if mgr.CacheTemperature() != CacheHot {
		t.Error("just updated should be CacheHot")
	}

	time.Sleep(150 * time.Millisecond)
	if mgr.CacheTemperature() != CacheCold {
		t.Error("after threshold should be CacheCold")
	}
}

func TestCacheTemperature_AffectsTruncation(t *testing.T) {
	// ToolResultMaxTokens=400 * ColdAggressiveRatio=0.5 = 200 (> minimum 100)
	mgr := NewManager(ManagerConfig{
		MaxInputTokens:      1000,
		ToolResultMaxTokens: 400,
		ColdThreshold:       100 * time.Millisecond,
		ColdAggressiveRatio: 0.5,
		Model: ModelProfile{
			MaxContextTokens: 1500,
			MaxOutputTokens:  500,
			ReserveRatio:     0.33,
		},
	})

	// 冷启动: effectiveToolResultMaxTokens = 400 * 0.5 = 200
	coldMax := mgr.effectiveToolResultMaxTokens()
	if coldMax != 200 {
		t.Errorf("cold max should be 200, got %d", coldMax)
	}

	mgr.UpdateLastAssistantTime(time.Now())
	hotMax := mgr.effectiveToolResultMaxTokens()
	if hotMax != 400 {
		t.Errorf("hot max should be 400, got %d", hotMax)
	}

	if coldMax >= hotMax {
		t.Errorf("cold max (%d) should be less than hot max (%d)", coldMax, hotMax)
	}

	t.Logf("Cold max: %d, Hot max: %d", coldMax, hotMax)
}

func TestCacheTemperature_StatusIncluded(t *testing.T) {
	mgr := NewManager(ManagerConfig{
		Model:         DefaultProfile,
		ColdThreshold: 1 * time.Hour,
	})

	history := []llm.Message{
		{Role: "user", Content: "hello"},
	}

	status := mgr.Status(history)
	if status.CacheTemp != CacheCold {
		t.Error("status should show CacheCold initially")
	}

	mgr.UpdateLastAssistantTime(time.Now())
	status = mgr.Status(history)
	if status.CacheTemp != CacheHot {
		t.Error("status should show CacheHot after update")
	}
}

// =============================================================================
// Nudge 消息生成测试
// =============================================================================

func TestNudgeMessage_BelowThreshold(t *testing.T) {
	status := WindowStatus{
		MaxInputTokens:  10000,
		EstimatedTokens: 5000,
		UsagePercent:    0.50,
		RemainingTokens: 5000,
	}

	msg := NudgeMessage(status)
	if msg != "" {
		t.Errorf("expected no nudge below threshold, got: %s", msg)
	}
}

func TestNudgeMessage_NormalThreshold(t *testing.T) {
	status := WindowStatus{
		MaxInputTokens:  10000,
		EstimatedTokens: 7000,
		UsagePercent:    0.70,
		RemainingTokens: 3000,
	}

	msg := NudgeMessage(status)
	if msg == "" {
		t.Fatal("expected nudge at 70% usage")
	}
	if !strings.Contains(msg, "[CONTEXT EFFICIENCY]") {
		t.Errorf("expected CONTEXT EFFICIENCY marker, got: %s", msg)
	}
	if strings.Contains(msg, "[CONTEXT CRITICAL]") {
		t.Error("should not be critical at 70%")
	}
	t.Logf("Nudge message: %s", msg)
}

func TestNudgeMessage_CriticalThreshold(t *testing.T) {
	status := WindowStatus{
		MaxInputTokens:  10000,
		EstimatedTokens: 9000,
		UsagePercent:    0.90,
		RemainingTokens: 1000,
	}

	msg := NudgeMessage(status)
	if msg == "" {
		t.Fatal("expected critical nudge at 90% usage")
	}
	if !strings.Contains(msg, "[CONTEXT CRITICAL]") {
		t.Errorf("expected CONTEXT CRITICAL marker, got: %s", msg)
	}
	if !strings.Contains(msg, "MUST be extremely concise") {
		t.Error("critical nudge should contain strong wording")
	}
	t.Logf("Critical nudge: %s", msg)
}

func TestNudgeMessage_ExactThreshold(t *testing.T) {
	// 低于 60% — 不触发
	status := WindowStatus{
		MaxInputTokens:  10000,
		EstimatedTokens: 5999,
		UsagePercent:    0.5999,
		RemainingTokens: 4001,
	}

	msg := NudgeMessage(status)
	if msg != "" {
		t.Errorf("expected no nudge below 60%%, got: %s", truncate(msg, 40))
	}

	// 恰好 60%: NudgeMessage uses < NudgeThreshold (0.60)
	// 0.60 < 0.60 = false → nudge IS triggered (60% and above)
	status.UsagePercent = 0.60
	msg = NudgeMessage(status)
	if msg == "" {
		t.Error("expected nudge at exactly 60%")
	}

	// 刚过 60%
	status.UsagePercent = 0.601
	msg = NudgeMessage(status)
	if msg == "" {
		t.Error("expected nudge just above 60%")
	}
}

// =============================================================================
// FormatStatus 含 cache 温度测试
// =============================================================================

func TestFormatStatus_WithCacheTemp(t *testing.T) {
	s := WindowStatus{
		MaxInputTokens:  4096,
		EstimatedTokens: 1024,
		UsagePercent:    0.25,
		MessageCount:    5,
		RemainingTokens: 3072,
		CacheTemp:       CacheHot,
	}
	formatted := FormatStatus(s)
	if !strings.Contains(formatted, "cache: hot") {
		t.Errorf("expected 'cache: hot' in: %s", formatted)
	}

	s.CacheTemp = CacheCold
	formatted = FormatStatus(s)
	if !strings.Contains(formatted, "cache: cold") {
		t.Errorf("expected 'cache: cold' in: %s", formatted)
	}
	t.Logf("Formatted: %s", formatted)
}
