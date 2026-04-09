package agent

// ============================================================
// Proactive Search — 已废弃
//
// 产品名/实体检测的 intent routing 已从代码移至 system prompt
// 和 web_search 工具描述中，由 LLM 自主决策是否调用搜索。
//
// 此文件保留最小存根以保持编译兼容性。
// ============================================================

// hasEntitySignal 保留为内部工具函数（其他模块可能引用）
// 检测消息中是否有实体名称信号
func hasEntitySignal(msg string) bool {
	// 包含英文字母序列（至少2个连续字母）
	for i := 0; i < len(msg)-1; i++ {
		if (msg[i] >= 'a' && msg[i] <= 'z') || (msg[i] >= 'A' && msg[i] <= 'Z') {
			if (msg[i+1] >= 'a' && msg[i+1] <= 'z') || (msg[i+1] >= 'A' && msg[i+1] <= 'Z') {
				return true
			}
		}
	}
	// 包含数字
	for _, r := range msg {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}
