package agent

import (
	"testing"
)

// Proactive search intent routing 已从代码移至 prompt。
// 仅保留 hasEntitySignal 的基础测试。

func TestHasEntitySignal_WithEnglish(t *testing.T) {
	if !hasEntitySignal("MacBook Pro") {
		t.Error("should detect English letters as entity signal")
	}
}

func TestHasEntitySignal_WithNumber(t *testing.T) {
	if !hasEntitySignal("华为Mate 70") {
		t.Error("should detect numbers as entity signal")
	}
}

func TestHasEntitySignal_PureChinese(t *testing.T) {
	if hasEntitySignal("今天天气怎么样") {
		t.Error("pure Chinese without numbers/letters should not be entity signal")
	}
}
