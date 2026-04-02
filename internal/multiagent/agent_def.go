package multiagent

import (
	"github.com/SOULOFCINDERS/agent/internal/tools"
)

// AgentDef 定义一个专业 Agent 的配置
//
// 每个 AgentDef 描述了一个拥有特定能力和人格的子 Agent。
// Orchestrator 会把 AgentDef 列表展示给编排 Agent，
// 编排 Agent 通过 tool call 选择委派给哪个子 Agent。
type AgentDef struct {
	// Name 唯一标识符，会作为 tool 名使用（如 "research_agent"）
	// 规则：小写字母+下划线，不超过 40 字符
	Name string

	// Description 对这个 Agent 能力的描述
	// 编排 Agent 根据此描述决定是否委派
	Description string

	// SystemPrompt 子 Agent 的 system prompt
	// 定义子 Agent 的角色、能力边界、输出格式要求
	SystemPrompt string

	// ToolNames 子 Agent 可用的工具名列表
	// 从全局 Registry 中选取子集，实现权限隔离
	// 为空则继承所有工具
	ToolNames []string

	// MaxIterations 子 Agent 最大迭代次数（默认 10）
	MaxIterations int
}

// SubRegistry 根据 ToolNames 从全局 Registry 创建子集 Registry
// 如果 ToolNames 为空，返回全局 Registry 的完整副本
func (d *AgentDef) SubRegistry(global *tools.Registry) *tools.Registry {
	sub := tools.NewRegistry()

	if len(d.ToolNames) == 0 {
		// 继承全部：遍历 global 中所有已知工具名
		// 由于 Registry 没有暴露 List 方法，我们用 BuiltinSchemas 的 key 来枚举
		allNames := collectAllToolNames()
		for _, name := range allNames {
			if t := global.Get(name); t != nil {
				sub.Register(t)
			}
		}
		return sub
	}

	for _, name := range d.ToolNames {
		if t := global.Get(name); t != nil {
			sub.Register(t)
		}
	}
	return sub
}

// collectAllToolNames 收集所有可能的工具名（从各 schema 源）
func collectAllToolNames() []string {
	seen := map[string]bool{}
	for name := range tools.BuiltinSchemas() {
		seen[name] = true
	}
	for name := range tools.MemoryToolSchemas() {
		seen[name] = true
	}
	// 飞书工具名硬编码（因为 schema 在 agent 包内定义）
	seen["feishu_read_doc"] = true
	seen["feishu_create_doc"] = true

	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	return names
}

// Validate 校验 AgentDef 配置
func (d *AgentDef) Validate() error {
	if d.Name == "" {
		return &ValidationError{Field: "Name", Message: "agent name is required"}
	}
	if len(d.Name) > 40 {
		return &ValidationError{Field: "Name", Message: "agent name too long (max 40)"}
	}
	if d.Description == "" {
		return &ValidationError{Field: "Description", Message: "agent description is required"}
	}
	if d.SystemPrompt == "" {
		return &ValidationError{Field: "SystemPrompt", Message: "system prompt is required"}
	}
	return nil
}

// ValidationError 配置校验错误
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return "invalid AgentDef." + e.Field + ": " + e.Message
}

// ---- 预置 Agent 模板 ----

// ResearchAgentDef 研究型 Agent —— 擅长搜索和信息整合
func ResearchAgentDef() AgentDef {
	return AgentDef{
		Name:        "research_agent",
		Description: "擅长联网搜索、信息收集和整理。适合回答需要查找最新资料、对比多个来源、做调研报告的任务。",
		SystemPrompt: `你是一个专业的研究助手。你的任务是根据用户需求，通过搜索和信息收集给出全面、准确的回答。

工作原则：
1. 先用 web_search 搜索相关信息
2. 对关键搜索结果用 web_fetch 获取详细内容
3. 综合多个来源，给出结构化的回答
4. 注明信息来源
5. 区分事实和观点`,
		ToolNames:     []string{"web_search", "web_fetch", "summarize"},
		MaxIterations: 10,
	}
}

// CodeAgentDef 代码型 Agent —— 擅长代码阅读和分析
func CodeAgentDef() AgentDef {
	return AgentDef{
		Name:        "code_agent",
		Description: "擅长代码阅读、搜索、分析和文件操作。适合需要理解代码库、查找特定实现、分析代码结构的任务。",
		SystemPrompt: `你是一个专业的代码分析助手。你的任务是帮助用户理解和分析代码。

工作原则：
1. 先用 list_dir 了解项目结构
2. 用 grep_repo 定位关键代码
3. 用 read_file 读取具体文件
4. 给出清晰的代码分析和建议
5. 引用具体的文件路径和行号`,
		ToolNames:     []string{"read_file", "list_dir", "grep_repo", "summarize"},
		MaxIterations: 10,
	}
}

// WriterAgentDef 写作型 Agent —— 擅长文档写作
func WriterAgentDef() AgentDef {
	return AgentDef{
		Name:        "writer_agent",
		Description: "擅长文档写作、内容整理和飞书文档操作。适合需要将信息整理成文档、写报告、创建飞书文档的任务。",
		SystemPrompt: `你是一个专业的技术写作助手。你的任务是将信息整理成高质量的文档。

工作原则：
1. 根据输入的素材和要求，组织文档结构
2. 使用清晰的标题层级
3. 重要信息用列表或表格呈现
4. 如需创建飞书文档，调用 feishu_create_doc
5. 确保文档可读性和专业性`,
		ToolNames:     []string{"feishu_read_doc", "feishu_create_doc", "read_file", "summarize"},
		MaxIterations: 8,
	}
}
