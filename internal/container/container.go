// Package container 负责依赖注入装配（DI Container）。
// 对应 DDD 的 Container/Composition Root 层。
// 所有对象创建和依赖关系在此集中管理。
package container

import (
	"context"
	"fmt"
	"io"
	"log"
	"path/filepath"

	"github.com/SOULOFCINDERS/agent/internal/agent"
	dmcp "github.com/SOULOFCINDERS/agent/internal/domain/mcp"
	"github.com/SOULOFCINDERS/agent/internal/guardrail"
	"github.com/SOULOFCINDERS/agent/internal/ctxwindow"
	"strings"
	"time"

	"github.com/SOULOFCINDERS/agent/internal/persist"
	"github.com/SOULOFCINDERS/agent/internal/llm"
	mcpinfra "github.com/SOULOFCINDERS/agent/internal/mcp"
	"github.com/SOULOFCINDERS/agent/internal/session"
	"github.com/SOULOFCINDERS/agent/internal/rag"
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
	VerifyMode     bool   // 启用 Verification Agent
	RAGMode        bool

	// 路径与地址
	Root   string // 工具根目录
	MemDir     string // 记忆存储路径
	SessionDir string // 会话持久化目录
	RAGDir     string // RAG 索引存储目录
	Addr   string // Web 服务监听地址

	// 资源配置
	Budget         int64                  // Token 预算（0=无限制）
	BlockKeywords  []guardrail.KeywordRule // 敏感词规则（可选）
	MaxInputChars  int                     // 输入最大字符数（0=不限）
	CtxWindow int   // 上下文窗口大小覆盖
	EmbeddingModel string // RAG embedding 模型名（可选）

	MCPServers []dmcp.ServerConfig // MCP Server 连接列表（可选）
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
	PersistStore   *persist.Store
	LoopAgent      *agent.LoopAgent
	Orchestrator   *multiagent.Orchestrator
	TraceWriter    io.Writer

	Guardrails *guardrail.GuardPipeline
	MCPManager     *mcpinfra.MCPManager
	SessionManager *session.Manager
	RAGEngine      *rag.Engine

	Config Config
}

// BuildDefaultSystemPrompt 构建默认系统提示（注入当前日期，避免模型时间认知偏差）
func BuildDefaultSystemPrompt() string {
	now := time.Now()
	dateStr := now.Format("2006年1月2日")
	weekday := [...]string{"日", "一", "二", "三", "四", "五", "六"}[now.Weekday()]

	return fmt.Sprintf(`你是一个联网的智能助手。你可以通过 web_search 和 web_fetch 工具访问互联网获取最新信息。

## 当前环境
- **今天是：%s（星期%s）**
- **你的训练数据有截止日期，不包含截止日期之后的信息。你不知道训练截止日期之后发生了什么。**
- 如果用户问到任何你不确定的事实（产品、事件、人物、新闻等），你必须先搜索，不能凭记忆回答。

## ⚠️ 最重要的规则：先搜索，再回答
1. **当用户提到任何具体的产品名称、型号、事件、人物时**，即使你"认为"自己知道答案，也必须先调用 web_search 验证。因为该产品/事件可能在你的训练截止日期之后才发布。
2. **绝对禁止说"截至我的知识截止日期，XX不存在"**。正确做法是调用 web_search 搜索后再回答。
3. **工具返回的事实 > 你的内部知识**。如果 web_search 或 web_fetch 返回的内容与你的记忆冲突，必须以工具返回的内容为准。你的记忆可能过时。
4. **不要质疑或否认工具结果**。如果 web_fetch 从官方网站（如 apple.com）抓取到了产品信息，该信息就是真实的。

## 必须调用 web_search 的场景
以下任一条件成立时，你**必须**先调用 web_search，不得跳过：
- 用户提到任何具体产品名称或型号（如 MacBook、iPhone、Galaxy、Pixel、Surface、ThinkPad、RTX、Ryzen、PS5/PS6、Xbox、Switch 等），无论你是否"认为"自己知道
- 用户消息包含品牌关键词：华为、小米、苹果、三星、联想、戴尔、惠普、索尼、任天堂、微软、英伟达、特斯拉、比亚迪、理想、蔚来、小鹏等
- 用户在询问某个具体事物并带有评价/购买意图词：值得买、怎么样、好不好、推荐吗、发布了吗、上市了吗、多少钱、什么时候出、有没有、参数、配置、评测
- 用户询问某个事件是否发生过
- 用户询问最新的新闻、价格、发布日期
- 用户的问题涉及具体的时间（"今年"、"最近"、"上个月"等）
- 用户询问任何你内部知识中找不到的东西

## ⚠️ 必须调用 calc 工具的场景
当用户的消息涉及任何数字计算时，你**必须**使用 calc 工具，绝不能心算：
- 包含以下关键词时立即使用 calc：计算、算一下、帮我算、算算、总共多少、合计、平均、总计、加上、减去、乘以、除以、百分之、年利率、月供、利息、复利、收益率、面积是多少、体积是多少
- 英文关键词同理：calculate、total、sum、average
- 涉及价格汇总、利息计算、面积/体积、统计数值、百分比换算等
- **你的心算能力不可靠，即使是简单的乘除法也必须用 calc 工具**

## 回答流程（思维链）
在回答任何非简单寒暄的问题前，你必须先在心中完成以下推理步骤：

**第一步：理解完整语境**
- 用户的真实意图是什么？不要只看字面意思。
- 有没有隐含的前提条件？（例如：去洗车店 → 需要带车 → 必须开车）
- 问题中的每个实体之间有什么逻辑关系？

**第二步：检查常识约束**
- 有没有常识性的约束被忽略了？
- 行为的目的是否与建议的方式矛盾？
- 如果把你的答案代入实际场景，能走通吗？

**第三步：选择回答路径**
- 事实性问题：先 web_search → 如需详情则 web_fetch → 基于工具结果回答（注明来源链接）
- 计算类问题：先 calc → 基于工具结果回答
- 推理/建议类问题：先完成上述思维链推理，再给出建议
- 工具操作：直接调用对应工具

## 引用规则（严格遵守）
1. **绝对禁止编造名人名言**。如果你不确定某人是否说过某句话，不要引用。
2. **绝对禁止编造书籍/论文内容**。如果要引用《某本书》中的观点，必须先用 web_search 验证。
3. **所有具体引用都要有来源**。如果你无法提供可验证的来源，就不要使用引号引用。

## 禁止行为
- **绝对不要用"截至我的知识截止日期"、"我的训练数据中没有"等说辞来否认某个产品或事件的存在**。你不知道的不代表不存在，必须先搜索验证。
- **绝对不要心算**。任何数值计算都使用 calc 工具。
- **绝对不要编造 URL**。只使用工具实际返回的链接。

## 记忆冲突处理
- 保存记忆时如果工具返回 conflict_type 字段，必须告知用户冲突情况
- conflict_type="explicit_override"：已自动处理，简要告知用户旧记忆已更新
- conflict_type="semantic_conflict"：已自动裁决，告知用户检测到了冲突并说明处理结果
- conflict_type="need_confirm"：**必须**向用户确认是否删除旧的矛盾记忆
- 用户当前的明确指令永远优先于已保存的记忆
- 记忆标注 🔴 表示超过 90 天未更新，使用时需要向用户确认是否仍然有效

## 其他工具规则
1. 返回结果时请用中文，简洁明了
2. 文件路径都是相对于工作根目录的
3. 当用户说"记住"、"帮我记一下"等，使用 save_memory 保存记忆
4. 当用户引用过去的信息、问"你还记得吗"时，使用 search_memory 查找
5. 保存记忆时用第三人称描述（如"用户喜欢..."），topic 要简洁
6. 当用户要求对文件或文本建立知识库索引时，使用 rag_index 工具
7. 当用户询问已索引文档的内容时，优先使用 rag_query 检索相关片段
8. 使用 rag_list 查看已索引的文档列表`, dateStr, weekday)
}

// AppendToolBoundary 根据已注册的工具列表，向 System Prompt 追加能力边界声明
// 明确告知 LLM 只能使用列出的工具，禁止声称拥有未列出的能力
func AppendToolBoundary(prompt string, toolNames []string) string {
	if len(toolNames) == 0 {
		return prompt
	}
	var sb strings.Builder
	sb.WriteString(prompt)
	sb.WriteString("\n\n## 能力边界（严格遵守）\n")
	sb.WriteString("你只能使用以下工具，不能声称拥有未列出的能力：\n")
	for _, name := range toolNames {
		sb.WriteString("- ")
		sb.WriteString(name)
		sb.WriteString("\n")
	}
	sb.WriteString("\n**禁止行为：**\n")
	sb.WriteString("- 不要声称你可以生成图片、音频、视频（除非上面列出了对应工具）\n")
	sb.WriteString("- 不要声称你可以发送邮件、创建日历事件（除非上面列出了对应工具）\n")
	sb.WriteString("- 不要声称你可以直接访问数据库或API（除非上面列出了对应工具）\n")
	sb.WriteString("- 如果用户要求的功能不在上述工具列表中，请如实告知：\"抱歉，我目前没有这个能力\"\n")
	sb.WriteString("- **引用来源时，只能使用工具实际返回的 URL，绝不能自行编造链接**\n")
	return sb.String()
}

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
		cfg.SystemPrompt = BuildDefaultSystemPrompt()
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

	// 2.5 会话持久化
	sessDir := cfg.SessionDir
	if sessDir == "" {
		sessDir = filepath.Join(absRoot, ".agent-sessions")
	}
	sessStore, err := session.NewJSONStore(sessDir)
	if err != nil {
		return nil, fmt.Errorf("session store: %w", err)
	}
	app.SessionManager = session.NewManager(sessStore)

	// 2.8 RAG 引擎
	if cfg.RAGMode {
		ragDir := cfg.RAGDir
		if ragDir == "" {
			ragDir = filepath.Join(absRoot, ".agent-rag")
		}
		ragCfg := rag.EngineConfig{
			DataDir:   ragDir,
			ChunkSize: 500,
			Overlap:   50,
		}
		// 如果配置了 embedding 模型，使用 API embedder（延迟到 LLM 客户端创建后）
		app.RAGEngine, err = rag.NewEngine(ragCfg)
		if err != nil {
			return nil, fmt.Errorf("rag engine: %w", err)
		}
		app.Registry.Register(tools.NewRAGIndexTool(app.RAGEngine))
		app.Registry.Register(tools.NewRAGQueryTool(app.RAGEngine))
		app.Registry.Register(tools.NewRAGListTool(app.RAGEngine))
		app.Registry.Register(tools.NewRAGDeleteTool(app.RAGEngine))
		app.Registry.Register(tools.NewRAGImportTool(app.RAGEngine))
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

	// 会话持久化存储
	persistDir := persist.DefaultDir()
	pStore, err := persist.NewStore(persistDir)
	if err != nil {
		log.Printf("⚠️  会话持久化初始化失败: %v (将使用内存存储)", err)
	} else {
		app.PersistStore = pStore
		log.Printf("💾 会话持久化: %s (%d 条历史会话)", persistDir, pStore.Count())
	}

	// 6. Guardrails（可选）
	if cfg.GuardrailMode {
		app.Guardrails = guardrail.DefaultPipeline(cfg.BlockKeywords, cfg.MaxInputChars)
	}

	// 6.5 MCP éæï¼å¯éï¼
	if len(cfg.MCPServers) > 0 {
		app.MCPManager = mcpinfra.NewManager()
		ctx := context.Background()
		for _, sc := range cfg.MCPServers {
			if err := app.MCPManager.AddServer(ctx, sc); err != nil {
				log.Printf("[MCP] warning: failed to connect server %s: %v", sc.ID, err)
			}
		}
	}

	// 7. LoopAgent
	// P0: 动态追加工具能力边界声明
	{
		var registeredTools []string
		schemas := tools.BuiltinSchemas()
		for name := range schemas {
			if app.Registry.Get(name) != nil {
				registeredTools = append(registeredTools, name)
			}
		}
		cfg.SystemPrompt = AppendToolBoundary(cfg.SystemPrompt, registeredTools)
	}

	app.LoopAgent = agent.NewLoopAgent(
		app.LLMClient, app.Registry, cfg.SystemPrompt,
		cfg.TraceWriter, app.MemStore, app.Compressor,
	)

	// 7.1 注入 Guardrails
	if app.Guardrails != nil {
		app.LoopAgent.SetGuardrails(app.Guardrails)
	}

	// 7.2 注入 Verification Agent
	if cfg.VerifyMode {
		verifier := agent.NewVerifier(app.LLMClient)
		app.LoopAgent.SetVerifier(verifier)
		log.Printf("🔍 Verification Agent 已启用")
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
		LoopAgent:    a.LoopAgent, // 传入已配好 ContextManager 的 Agent
		PersistStore: a.PersistStore,
	})
}

// ChatAgent 返回用于对话的 Agent（Multi-Agent 模式返回 Orchestrator）
func (a *App) ChatAgent() ChatAgent {
	if a.Orchestrator != nil {
		return a.Orchestrator
	}
	return a.LoopAgent
}

// Close æ¸ç App ææçèµæºï¼å¦ MCP è¿æ¥ï¼
func (a *App) Close() error {
	if a.MCPManager != nil {
		return a.MCPManager.Close()
	}
	return nil
}

// buildRegistry 构建工具注册表
func buildRegistry(absRoot string, cfg Config) *tools.Registry {
	reg := tools.NewRegistry()
	reg.Register(tools.NewEchoTool())
	reg.Register(tools.NewCalcTool())
	reg.Register(tools.NewReadFileTool(absRoot))
	reg.Register(tools.NewListDirTool(absRoot))
	reg.Register(tools.NewGrepRepoTool(absRoot))
	reg.Register(tools.NewWriteFileTool(absRoot))
	reg.Register(tools.NewEditFileTool(absRoot))
	reg.Register(tools.NewSummarizeTool())
	reg.Register(tools.NewWeatherTool())

	// 联网搜索默认启用（web_search + web_fetch）
	reg.Register(tools.NewWebSearchTool())
	reg.Register(tools.NewWebFetchTool())

	if cfg.FeishuMode {
		reg.Register(tools.NewFeishuReadDocTool())
		reg.Register(tools.NewFeishuCreateDocTool())
	}

	return reg
}
