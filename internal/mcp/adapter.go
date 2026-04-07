package mcp

import (
	"context"
	"encoding/json"

	dmcp "github.com/SOULOFCINDERS/agent/internal/domain/mcp"
	"github.com/SOULOFCINDERS/agent/internal/domain/tool"
)

// MCPToolAdapter adapts a discovered MCP tool to the project's tool.Tool interface.
// This allows MCP tools to be registered in the standard tools.Registry and
// invoked by LoopAgent without any awareness of the MCP protocol.
type MCPToolAdapter struct {
	manager dmcp.Manager
	tool    dmcp.DiscoveredTool
}

// NewToolAdapter creates an adapter for a single MCP tool.
func NewToolAdapter(manager dmcp.Manager, t dmcp.DiscoveredTool) *MCPToolAdapter {
	return &MCPToolAdapter{manager: manager, tool: t}
}

// Name returns the qualified tool name (e.g. "myserver/tool_name").
func (a *MCPToolAdapter) Name() string {
	return a.tool.Name
}

// Description returns the tool's description from the MCP Server.
func (a *MCPToolAdapter) Description() string {
	return a.tool.Description
}

// ParameterSchema returns the JSON Schema for the tool's input parameters.
func (a *MCPToolAdapter) ParameterSchema() json.RawMessage {
	if a.tool.InputSchema == nil {
		return json.RawMessage(`{"type":"object"}`)
	}
	b, err := json.Marshal(a.tool.InputSchema)
	if err != nil {
		return json.RawMessage(`{"type":"object"}`)
	}
	return b
}

// Execute invokes the MCP tool through the manager, which routes to the correct server.
func (a *MCPToolAdapter) Execute(ctx context.Context, args map[string]any) (any, error) {
	result, err := a.manager.CallTool(ctx, a.tool.Name, args)
	if err != nil {
		return nil, &tool.Error{
			Kind:    tool.ErrNotRetryable,
			Tool:    a.tool.Name,
			Message: err.Error(),
			Cause:   err,
		}
	}
	return result, nil
}

// Compile-time interface checks
var _ tool.Tool = (*MCPToolAdapter)(nil)
var _ tool.ToolWithSchema = (*MCPToolAdapter)(nil)
