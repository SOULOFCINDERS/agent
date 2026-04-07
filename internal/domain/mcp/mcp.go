// Package mcp defines domain interfaces for Model Context Protocol integration.
// MCP enables the Agent to dynamically discover and invoke tools provided by
// external MCP Servers, following the open MCP specification.
package mcp

import "context"

// ServerConfig describes how to connect to a single MCP Server.
type ServerConfig struct {
	ID        string   // unique identifier
	Transport string   // "stdio" or "sse"
	Command   string   // stdio: executable path
	Args      []string // stdio: command arguments
	URL       string   // sse: server endpoint URL
}

// DiscoveredTool represents a tool discovered from an MCP Server.
type DiscoveredTool struct {
	Name        string         // tool name (unique within server)
	Description string         // human-readable description
	InputSchema map[string]any // JSON Schema for input parameters
	ServerID    string         // which MCP Server provides this tool
}

// Client abstracts a connection to a single MCP Server.
type Client interface {
	// Connect establishes the connection and performs initialization handshake.
	Connect(ctx context.Context) error
	// ListTools discovers all tools the server provides.
	ListTools(ctx context.Context) ([]DiscoveredTool, error)
	// CallTool invokes a named tool with the given arguments.
	CallTool(ctx context.Context, name string, args map[string]any) (string, error)
	// Close terminates the connection.
	Close() error
}

// Manager manages multiple MCP Server connections and routes tool calls.
type Manager interface {
	// AddServer registers and connects to an MCP Server.
	AddServer(ctx context.Context, cfg ServerConfig) error
	// DiscoverAllTools returns tools from all connected servers.
	DiscoverAllTools(ctx context.Context) ([]DiscoveredTool, error)
	// CallTool routes a tool call to the correct server.
	CallTool(ctx context.Context, toolName string, args map[string]any) (string, error)
	// Close disconnects all servers.
	Close() error
}
