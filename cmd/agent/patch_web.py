with open("/Users/bytedance/go/src/agent/internal/web/server.go", "r") as f:
    src = f.read()

# 1. Add UsageTracker field to ServerConfig
src = src.replace(
    '''// ServerConfig 服务器配置
type ServerConfig struct {
	LLMClient   llm.Client
	Registry    *tools.Registry
	MemStore    *memory.Store
	Addr        string
	TraceWriter io.Writer
	SystemPrompt string
}''',
    '''// ServerConfig 服务器配置
type ServerConfig struct {
	LLMClient    llm.Client
	Registry     *tools.Registry
	MemStore     *memory.Store
	Addr         string
	TraceWriter  io.Writer
	SystemPrompt string
	UsageTracker *llm.UsageTracker
}'''
)

# 2. After loopAgent creation in NewServer, set UsageTracker
src = src.replace(
    '''	loopAgent := agent.NewLoopAgent(cfg.LLMClient, cfg.Registry, cfg.SystemPrompt, cfg.TraceWriter, cfg.MemStore, compressor)

	return &Server{''',
    '''	loopAgent := agent.NewLoopAgent(cfg.LLMClient, cfg.Registry, cfg.SystemPrompt, cfg.TraceWriter, cfg.MemStore, compressor)

	// 设置 Token 用量追踪
	if cfg.UsageTracker != nil {
		loopAgent.SetUsageTracker(cfg.UsageTracker)
	}

	return &Server{'''
)

with open("/Users/bytedance/go/src/agent/internal/web/server.go", "w") as f:
    f.write(src)

print("OK - server.go patched")
