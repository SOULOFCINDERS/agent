package ctxwindow

import (
	"github.com/SOULOFCINDERS/agent/internal/llm"
)

func FitMessages(history []llm.Message, modelName string) []llm.Message {
	profile := LookupModel(modelName)
	mgr := NewManager(ManagerConfig{Model: profile})
	return mgr.Fit(history)
}

func FitMessagesWithBudget(history []llm.Message, maxInputTokens int) []llm.Message {
	mgr := NewManager(ManagerConfig{
		MaxInputTokens: maxInputTokens,
	})
	return mgr.Fit(history)
}

func QuickEstimate(history []llm.Message) int {
	mgr := NewManager(ManagerConfig{Model: DefaultProfile})
	return mgr.EstimateHistory(history)
}

func QuickStatus(history []llm.Message, modelName string) WindowStatus {
	profile := LookupModel(modelName)
	mgr := NewManager(ManagerConfig{Model: profile})
	return mgr.Status(history)
}
