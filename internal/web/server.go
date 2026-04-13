package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/SOULOFCINDERS/agent/internal/agent"
	"github.com/SOULOFCINDERS/agent/internal/persist"
	"github.com/SOULOFCINDERS/agent/internal/llm"
	"github.com/SOULOFCINDERS/agent/internal/memory"
	"github.com/SOULOFCINDERS/agent/internal/tools"
)

// Server 是 Agent Web UI 服务器
type Server struct {
	loopAgent    *agent.LoopAgent
	memStore     *memory.Store
	addr         string
	traceWriter  io.Writer

	// 会话管理（持久化存储）
	persistStore *persist.Store
}

// ServerConfig 服务器配置
type ServerConfig struct {
	LLMClient    llm.Client
	Registry     *tools.Registry
	MemStore     *memory.Store
	Addr         string
	TraceWriter  io.Writer
	SystemPrompt string
	UsageTracker *llm.UsageTracker
	LoopAgent    *agent.LoopAgent // 可选：直接传入已配置好的 LoopAgent
	PersistStore *persist.Store   // 可选：持久化存储
}


func NewServer(cfg ServerConfig) *Server {
	if cfg.Addr == "" {
		cfg.Addr = ":8080"
	}
	if cfg.TraceWriter == nil {
		cfg.TraceWriter = io.Discard
	}

	// 创建历史压缩器
	var compressor *memory.Compressor
	if cfg.MemStore != nil {
		compressor = memory.NewCompressor(cfg.LLMClient, memory.CompressorConfig{
			WindowSize:  3,
			MaxMessages: 12,
		})
	}

	var loopAgent *agent.LoopAgent
	if cfg.LoopAgent != nil {
		// 使用外部传入的已配置好的 LoopAgent（带 ContextManager 等）
		loopAgent = cfg.LoopAgent
	} else {
		loopAgent = agent.NewLoopAgent(cfg.LLMClient, cfg.Registry, cfg.SystemPrompt, cfg.TraceWriter, cfg.MemStore, compressor)
		if cfg.UsageTracker != nil {
			loopAgent.SetUsageTracker(cfg.UsageTracker)
		}
	}

	return &Server{
		loopAgent:    loopAgent,
		memStore:     cfg.MemStore,
		addr:         cfg.Addr,
		traceWriter:  cfg.TraceWriter,
		persistStore: cfg.PersistStore,
	}
}

// Run 启动 HTTP 服务器
func (s *Server) Run() error {
	mux := http.NewServeMux()

	// 静态文件
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/static/app.js", s.handleJS)
	mux.HandleFunc("/static/style.css", s.handleCSS)

	// API
	mux.HandleFunc("/api/chat", s.handleChat)
	mux.HandleFunc("/api/chat/stream", s.handleChatStream)
	mux.HandleFunc("/api/sessions/clear", s.handleClearSession)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/context", s.handleContext)
	mux.HandleFunc("/api/compact", s.handleCompact)
	mux.HandleFunc("/api/sessions", s.handleListSessions)
	mux.HandleFunc("/api/sessions/rename", s.handleRenameSession)
	mux.HandleFunc("/api/sessions/delete", s.handleDeleteSession)
	mux.HandleFunc("/api/sessions/history", s.handleSessionHistory)

	log.Printf("🌐 Agent Web UI starting at http://localhost%s", s.addr)
	return http.ListenAndServe(s.addr, withCORS(mux))
}

// ---------- 静态文件处理 ----------

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/index.html" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(IndexHTML))
}

func (s *Server) handleJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Write([]byte(AppJS))
}

func (s *Server) handleCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Write([]byte(StyleCSS))
}

// ---------- API 处理 ----------

type chatRequest struct {
	Message   string `json:"message"`
	SessionID string `json:"session_id"`
}

type chatResponse struct {
	Reply     string `json:"reply"`
	SessionID string `json:"session_id"`
	Error     string `json:"error,omitempty"`
}

// contextInfo 上下文窗口和 Token 用量信息
type contextInfo struct {
	// 上下文窗口
	MaxInputTokens  int     `json:"max_input_tokens"`
	EstimatedTokens int     `json:"estimated_tokens"`
	UsagePercent    float64 `json:"usage_percent"`
	RemainingTokens int     `json:"remaining_tokens"`
	MessageCount    int     `json:"message_count"`
	HasRoom         bool    `json:"has_room"`

	// 累计 Token 用量
	TotalTokens      int64 `json:"total_tokens"`
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	CallCount        int64 `json:"call_count"`
	Budget           int64 `json:"budget"`
	BudgetRemaining  int64 `json:"budget_remaining"`

	// 模型信息
	ModelName string `json:"model_name,omitempty"`
}

// handleChat 非流式对话
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}

	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, chatResponse{Error: "invalid request body"})
		return
	}

	if req.Message == "" {
		writeJSON(w, 400, chatResponse{Error: "empty message"})
		return
	}

	if req.SessionID == "" {
		req.SessionID = fmt.Sprintf("s_%d", time.Now().UnixNano())
	}

	history := s.getSession(req.SessionID)

	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()

	reply, newHistory, err := s.loopAgent.Chat(ctx, req.Message, history)
	if err != nil {
		writeJSON(w, 500, chatResponse{Error: err.Error(), SessionID: req.SessionID})
		return
	}

	s.setSession(req.SessionID, newHistory)

	writeJSON(w, 200, chatResponse{
		Reply:     reply,
		SessionID: req.SessionID,
	})
}

// handleChatStream SSE 流式对话（增强版：支持工具调用事件）
func (s *Server) handleChatStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}

	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", 400)
		return
	}

	if req.Message == "" {
		http.Error(w, "empty message", 400)
		return
	}

	if req.SessionID == "" {
		req.SessionID = fmt.Sprintf("s_%d", time.Now().UnixNano())
	}

	history := s.getSession(req.SessionID)

	// 设置 SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", 500)
		return
	}

	// 发送 session_id
	sendSSE(w, flusher, "session", req.SessionID)

	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()

	// 使用增强版流式对话：通过 StreamEventWriter 接收结构化事件
	onEvent := func(event agent.StreamEvent) {
		switch event.Type {
		case agent.EventDelta:
			sendSSE(w, flusher, "delta", event.Content)
		case agent.EventToolStart:
			sendSSE(w, flusher, "tool_start", event.JSON())
		case agent.EventToolEnd:
			sendSSE(w, flusher, "tool_end", event.JSON())
		case agent.EventIteration:
			sendSSE(w, flusher, "iteration", event.JSON())
		case agent.EventThinking:
			sendSSE(w, flusher, "thinking", event.Thinking)
		case agent.EventStatus:
			sendSSE(w, flusher, "status", event.Status)
		}
	}

	reply, newHistory, err := s.loopAgent.ChatStreamV2(ctx, req.Message, history, onEvent)
	if err != nil {
		log.Printf("[ChatStream] error: %v", err)
		sendSSE(w, flusher, "error", err.Error())
		return
	}

	s.setSession(req.SessionID, newHistory)

	// 发送上下文信息（在 done 之前）
	ctxData := s.buildContextInfo(req.SessionID)
	ctxJSON, _ := json.Marshal(ctxData)
	sendSSE(w, flusher, "context", string(ctxJSON))

	sendSSE(w, flusher, "done", reply)
}

func (s *Server) handleClearSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}

	var req struct {
		SessionID string `json:"session_id"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	if req.SessionID != "" && s.persistStore != nil {
		_ = s.persistStore.ClearHistory(req.SessionID)
	}

	writeJSON(w, 200, map[string]string{"status": "cleared"})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	sessionCount := 0
	if s.persistStore != nil {
		sessionCount = s.persistStore.Count()
	}

	memCount := 0
	if s.memStore != nil {
		memCount = s.memStore.Count()
	}

	writeJSON(w, 200, map[string]any{
		"status":   "ok",
		"sessions": sessionCount,
		"memories": memCount,
	})
}

// handleContext 返回指定会话的上下文窗口和 token 用量信息
func (s *Server) handleContext(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	ctxData := s.buildContextInfo(sessionID)
	writeJSON(w, 200, ctxData)
}

// buildContextInfo 构建上下文信息
func (s *Server) buildContextInfo(sessionID string) contextInfo {
	info := contextInfo{}

	// 上下文窗口状态
	if sessionID != "" {
		history := s.getSession(sessionID)
		ws := s.loopAgent.ContextWindowStatus(history)
		if ws != nil {
			info.MaxInputTokens = ws.MaxInputTokens
			info.EstimatedTokens = ws.EstimatedTokens
			info.UsagePercent = ws.UsagePercent
			info.RemainingTokens = ws.RemainingTokens
			info.MessageCount = ws.MessageCount
			info.HasRoom = ws.HasRoom
		}
	}

	if info.MaxInputTokens == 0 {
		ctxMgr := s.loopAgent.GetContextManager()
		if ctxMgr != nil {
			cfg := ctxMgr.Config()
			info.MaxInputTokens = cfg.MaxInputTokens
			info.ModelName = cfg.Model.Name
		}
	}

	// Token 用量
	ut := s.loopAgent.GetUsageTracker()
	if ut != nil {
		info.TotalTokens = ut.TotalTokens()
		info.PromptTokens = ut.PromptTokens()
		info.CompletionTokens = ut.CompletionTokens()
		info.CallCount = ut.CallCount()
		info.Budget = ut.Budget()
		info.BudgetRemaining = ut.Remaining()
	}

	return info
}

// ---------- 会话管理 ----------

// handleCompact 主动压缩指定会话的上下文
func (s *Server) handleCompact(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}

	var req struct {
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid request body"})
		return
	}

	if req.SessionID == "" {
		writeJSON(w, 400, map[string]string{"error": "missing session_id"})
		return
	}

	history := s.getSession(req.SessionID)
	if len(history) == 0 {
		writeJSON(w, 200, map[string]any{
			"original_count":   0,
			"final_count":      0,
			"tokens_before":    0,
			"tokens_after":     0,
			"strategy":         "none",
			"summary_inserted": false,
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	compacted, result, err := s.loopAgent.CompactHistory(ctx, history)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}

	s.setSession(req.SessionID, compacted)

	writeJSON(w, 200, map[string]any{
		"original_count":   result.OriginalCount,
		"final_count":      result.FinalCount,
		"tokens_before":    result.TokensBefore,
		"tokens_after":     result.TokensAfter,
		"strategy":         result.Strategy,
		"summary_inserted": result.SummaryInserted,
	})
}

func (s *Server) getSession(id string) []llm.Message {
	if s.persistStore == nil {
		return nil
	}
	return s.persistStore.GetHistory(id)
}

func (s *Server) setSession(id string, history []llm.Message) {
	if s.persistStore == nil {
		return
	}
	_ = s.persistStore.SaveHistory(id, history)
}


// handleListSessions 返回所有会话列表
func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	if s.persistStore == nil {
		writeJSON(w, 200, []struct{}{})
		return
	}
	items := s.persistStore.List()
	writeJSON(w, 200, items)
}

// handleRenameSession 重命名会话
func (s *Server) handleRenameSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}
	var req struct {
		SessionID string `json:"session_id"`
		Title     string `json:"title"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	if s.persistStore != nil {
		_ = s.persistStore.Rename(req.SessionID, req.Title)
	}

	writeJSON(w, 200, map[string]string{"status": "ok"})
}

// handleDeleteSession 删除会话
func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}
	var req struct {
		SessionID string `json:"session_id"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	if s.persistStore != nil {
		_ = s.persistStore.Delete(req.SessionID)
	}

	writeJSON(w, 200, map[string]string{"status": "deleted"})
}

// handleSessionHistory 返回指定会话的历史消息（仅 user + assistant，用于前端渲染）
func (s *Server) handleSessionHistory(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		writeJSON(w, 400, map[string]string{"error": "missing session_id"})
		return
	}

	history := s.getSession(sessionID)
	if history == nil {
		writeJSON(w, 200, map[string]any{"session_id": sessionID, "messages": []struct{}{}})
		return
	}

	// 过滤：只返回 user 和 assistant 消息（跳过 system、tool 等内部消息）
	type historyMsg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	var msgs []historyMsg
	for _, m := range history {
		if m.Role == "user" || m.Role == "assistant" {
			// 跳过空内容（如 assistant 的纯 tool_call 消息）
			if m.Content == "" {
				continue
			}
			// 跳过系统注入的搜索提醒等内部消息
			if m.Role == "user" && len(m.Content) > 0 && m.Content[0] == '[' {
				if len(m.Content) > 6 && m.Content[:6] == "[系统" {
					continue
				}
			}
			msgs = append(msgs, historyMsg{Role: m.Role, Content: m.Content})
		}
	}

	writeJSON(w, 200, map[string]any{"session_id": sessionID, "messages": msgs})
}

// ---------- 工具函数 ----------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func sendSSE(w http.ResponseWriter, flusher http.Flusher, event, data string) {
	lines := strings.Split(data, "\n")
	fmt.Fprintf(w, "event: %s\n", event)
	for _, line := range lines {
		fmt.Fprintf(w, "data: %s\n", line)
	}
	fmt.Fprintf(w, "\n")
	flusher.Flush()
}

func withCORS(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == "OPTIONS" {
			w.WriteHeader(200)
			return
		}
		handler.ServeHTTP(w, r)
	})
}
