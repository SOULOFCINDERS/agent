package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/SOULOFCINDERS/agent/internal/agent"
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

	// 会话管理（简单实现：每个 session_id 对应一个消息历史）
	mu       sync.Mutex
	sessions map[string][]llm.Message
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

	loopAgent := agent.NewLoopAgent(cfg.LLMClient, cfg.Registry, cfg.SystemPrompt, cfg.TraceWriter, cfg.MemStore, compressor)

	// 设置 Token 用量追踪
	if cfg.UsageTracker != nil {
		loopAgent.SetUsageTracker(cfg.UsageTracker)
	}

	return &Server{
		loopAgent:   loopAgent,
		memStore:    cfg.MemStore,
		addr:        cfg.Addr,
		traceWriter: cfg.TraceWriter,
		sessions:    make(map[string][]llm.Message),
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

// handleChatStream SSE 流式对话
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

	// 检查是否支持流式
	_, canStream := s.loopAgent.GetClient().(interface {
		StreamChat(ctx context.Context, messages []llm.Message, tools []llm.ToolDef) (*llm.StreamReader, error)
	})

	if canStream {
		reply, newHistory, err := s.loopAgent.ChatStream(ctx, req.Message, history, func(delta string) {
			sendSSE(w, flusher, "delta", delta)
		})

		if err != nil {
			sendSSE(w, flusher, "error", err.Error())
			return
		}

		s.setSession(req.SessionID, newHistory)

		// 发送上下文信息（在 done 之前）
		ctxData := s.buildContextInfo(req.SessionID)
		ctxJSON, _ := json.Marshal(ctxData)
		sendSSE(w, flusher, "context", string(ctxJSON))

		sendSSE(w, flusher, "done", reply)
	} else {
		// 降级：非流式
		reply, newHistory, err := s.loopAgent.Chat(ctx, req.Message, history)
		if err != nil {
			sendSSE(w, flusher, "error", err.Error())
			return
		}

		s.setSession(req.SessionID, newHistory)
		sendSSE(w, flusher, "delta", reply)

		// 发送上下文信息（在 done 之前）
		ctxData := s.buildContextInfo(req.SessionID)
		ctxJSON, _ := json.Marshal(ctxData)
		sendSSE(w, flusher, "context", string(ctxJSON))

		sendSSE(w, flusher, "done", reply)
	}
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

	if req.SessionID != "" {
		s.mu.Lock()
		delete(s.sessions, req.SessionID)
		s.mu.Unlock()
	}

	writeJSON(w, 200, map[string]string{"status": "cleared"})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	sessionCount := len(s.sessions)
	s.mu.Unlock()

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

	// 如果没有上下文窗口管理器，提供基于消息数的基础估算
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

func (s *Server) getSession(id string) []llm.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.sessions[id]
	cp := make([]llm.Message, len(h))
	copy(cp, h)
	return cp
}

func (s *Server) setSession(id string, history []llm.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[id] = history
}

// ---------- 工具函数 ----------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func sendSSE(w http.ResponseWriter, flusher http.Flusher, event, data string) {
	// SSE 中 data 字段需要每行一个 "data:" 前缀
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
