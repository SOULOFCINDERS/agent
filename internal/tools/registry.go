package tools

import (
	"sync"

	dtool "github.com/SOULOFCINDERS/agent/internal/domain/tool"
)

// ---------- 类型别名：从 domain/tool 引入 ----------

type Tool = dtool.Tool
type ToolWithSchema = dtool.ToolWithSchema

// Registry 工具注册表（具体实现，满足 domain/tool.Registry 接口）
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

func NewRegistry() *Registry {
	return &Registry{tools: map[string]Tool{}}
}

func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name()] = t
}

func (r *Registry) Get(name string) Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tools[name]
}
