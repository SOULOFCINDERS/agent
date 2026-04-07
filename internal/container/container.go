// Package container 负责依赖注入装配（DI Container）。
// 对应 DDD 的 Container/Composition Root 层。
// 所有对象创建和依赖关系在此集中管理。
package container

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/SOULOFCINDERS/agent/internal/agent"
	"github.com/SOULOFCINDERS/agent/internal/guardrail"
	"github.com/SOULOFCINDERS/agent/internal/ctxwindow"
	"github.com/SOULOFCINDERS/agent/internal/llm"
	"github.com/SOULOFCINDERS/agent/internal/memory"
	"github.com/SOULOFCINDERS/agent/internal/multiagent"
	"github.com/SOULOFCINDERS/agent/internal/tools"
	"github.com/SOULOFCINDERS/agent/internal/web"
)

// Config 容器配置（从 CLI 参数或环境变量填充）
type Config struct {
	// LLM 配置
	BaseURL string
	APIKey  string
	Model   string
	Mock    bool

	// 功能开关
	FeishuMode     bool
	SearchMode     bool
	MemoryMode     bool
	MultiAgentMode bool
	GuardrailMode  bool
	StreamMode     bool

	// 路径与地址
	Root   string // 工具根目录
	MemDir string // 记忆存储路径
	Addr   string // Web 服务监听地址

	// 资源配置
	Budget         int64                  // Token 预算（0=无限制）
	BlockKeywords  []guardrail.KeywordRule // 敏感词规则（可选）
	MaxInputChars  int                     // 输入最大字符数（0=不限）
	CtxWindow int   // 上下文窗口大小覆盖

	// 运行时
	Trace       bool
	TraceWriter io.Writer

	// System Prompt
	SystemPrompt string
}

// App 装配完成的应用实例
type App struct {
	LLMClient      llm.Client
	Registry       *tools.Registry
	MemStore       *memory.Store
	Compressor     *memory.Compressor
	UsageTracker   *llm.UsageTracker
	LoopAgent      *agent.LoopAgent
	Orchestrator   *multiagent.Orchestrator
	TraceWriter    io.Writer

	Guardrails *guardrail.GuardPipeline

	Config Config
}

// DefaultSystemPrompt 默认系统提示
const DefaultSystemPrompt = `你是一个智能助手，可以使用各种工具来帮助用户完成任务。
你可以读取文件、列出目录、搜索代码、做计算、查天气。
如果启用了飞书功能，你还可以读取和创建飞书文档。

重要规则：
1. 当用户的请求需要使用工具时，请调用对应的工具
2. 如果不确定需要哪个工具，可以先问用户
3. 返回结果时请用中文，简洁明了
4. 文件路径都是相对于工作根目录的
5. 当用户说"记住"、"帮我记一下"等，使用 save_memory 保存记忆
6. 当用户引用过去的信息、问"你还记得吗"时，使用 search_memory 查找
7. 保存记忆时用第三人称描述（如"用户喜欢..."），topic 要简洁
8. 当用户询问最新信息、新闻、或你不确定的事实时，使用 web_search 搜索
9. 搜索后如需详细内容，用 web_fetch 抓取具体网页
10. 搜索结果要注明来源链接`

// Build 根据配置装配所有依赖，返回 App
func Build(cfg Config) (*App, error) {
	absRoot, err := filepath.Abs(cfg.Root)
	if err != nil {
		return nil, fmt.Errorf("resolve root: %w", err)
	}

	if cfg.TraceWriter == nil {
		cfg.TraceWriter = io.Discard
	}
	if cfg.SystemPrompt == "" {
		cfg.SystemPrompt = DefaultSystemPrompt
	}

	app := &App{Config: cfg, TraceWriter: cfg.TraceWriter}

	// 1. 工具注册表
	app.Registry = buildRegistry(absRoot, cfg)

	// 2. 记忆存储
	if cfg.MemoryMode {
		memDir := cfg.MemDir
		if memDir == "" {
			memDir = filepath.Join(absRoot, ".agent-memory")
		}
		app.MemStore, err = memory.NewStore(memDir)
		if err != nil {
			return nil, fmt.Errorf("memory store: %w", err)
		}
		app.Registry.Register(tools.NewSaveMemoryTool(app.MemStore))
		app.Registry.Register(tools.NewSearchMemoryTool(app.MemStore))
		app.Registry.Register(tools.NewDeleteMemoryTool(app.MemStore))
	}

	// 3. LLM 客户端
	if cfg.Mock {
		app.LLMClient = llm.NewMockClient()
	} else {
		app.LLMClient = llm.NewOpenAICompatClient(cfg.BaseURL, cfg.APIKey, cfg.Model)
	}

	// 4. 记忆压缩器
	if app.MemStore != nil {
		app.Compressor = memory.NewCompressor(app.LLMClient, memory.CompressorConfig{
			WindowSize:  3,
			MaxMessages: 12,
		})
	}

	// 5. Token 用量追踪
	app.UsageTracker = llm.NewUsageTracker(cfg.Budget)

	// 6. Guardrails（可选）
	if cfg.GuardrailMode {
		app.Guardrails = guardrail.DefaultPipeline(cfg.BlockKeywords, cfg.MaxInputChars)
	}

	// 7. LoopAgent
	app.LoopAgent = agent.NewLoopAgent(
		app.LLMClient, app.Registry, cfg.SystemPrompt,
		cfg.TraceWriter, app.MemStore, app.Compressor,
	)

	// 7.1 注入 Guardrails
	if app.Guardrails != nil {
		app.LoopAgent.SetGuardrails(app.Guardrails)
	}

	// 8. 上下文窗口管理
	var modelName string
	if oai, ok := app.LLMClient.(*llm.OpenAICompatClient); ok {
		modelName = oai.Model
	}
	profile := ctxwindow.LookupModel(modelName)
	if cfg.CtxWindow > 0 {
		profile.MaxContextTokens = cfg.CtxWindow
	}
	ctxMgr := ctxwindow.NewManager(ctxwindow.ManagerConfig{
		Model:               profile,
		ProtectRecentRounds: 2,
		ToolResultMaxTokens: 2000,
	})
	app.LoopAgent.SetContextManager(ctxMgr)
	app.LoopAgent.SetUsageTracker(app.UsageTracker)

	// 9. Multi-Agent（可选）
	if cfg.MultiAgentMode {
		agentDefs := []multiagent.AgentDef{
			multiagent.ResearchAgentDef(),
			multiagent.CodeAgentDef(),
			multiagent.WriterAgentDef(),
		}
		orch, err := multiagent.NewOrchestrator(multiagent.OrchestratorConfig{
			LLMClient:      app.LLMClient,
			GlobalRegistry: app.Registry,
			AgentDefs:      agentDefs,
			DirectTools:    []string{"calc", "weather"},
			MemStore:       app.MemStore,
			Compressor:     app.Compressor,
			TraceWriter:    cfg.TraceWriter,
		})
		if err != nil {
			return nil, fmt.Errorf("multi-agent orchestrator: %w", err)
		}
		orchAgent := orch.GetAgent()
		orchAgent.SetUsageTracker(app.UsageTracker)
		orchAgent.SetContextManager(ctxMgr)
		app.Orchestrator = orch
	}

	return app, nil
}

// BuildWebServer 基于 App 创建 Web 服务器
func (a *App) BuildWebServer() *web.Server {
	return web.NewServer(web.ServerConfig{
		LLMClient:    a.LLMClient,
		Registry:     a.Registry,
		MemStore:     a.MemStore,
		Addr:         a.Config.Addr,
		TraceWriter:  a.TraceWriter,
		SystemPrompt: a.Config.SystemPrompt,
		UsageTracker: a.UsageTracker,
	})
}

// ChatAgent 返回用于对话的 Agent（Multi-Agent 模式返回 Orchestrator）
func (a *App) ChatAgent() ChatAgent {
	if a.Orchestrator != nil {
		return a.Orchestrator
	}
	return a.LoopAgent
}

// buildRegistry 构建工具注册表
func buildRegistry(absRoot string, cfg Config) *tools.Registry {
	reg := tools.NewRegistry()
	reg.Register(tools.NewEchoTool())
	reg.Register(tools.NewCalcTool())
	reg.Register(tools.NewReadFileTool(absRoot))
	reg.Register(tools.NewListDirTool(absRoot))
	reg.Register(tools.NewGrepRepoTool(absRoot))
	reg.Register(tools.NewSummarizeTool())
	reg.Register(tools.NewWeatherTool())

	if cfg.SearchMode {
		reg.Register(tools.NewWebSearchTool())
		reg.Register(tools.NewWebFetchTool())
	}

	if cfg.FeishuMode {
		reg.Register(tools.NewFeishuReadDocTool())
		reg.Register(tools.NewFeishuCreateDocTool())
	}

	return reg
}
