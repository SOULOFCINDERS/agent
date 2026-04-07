package mcp

import (
	"context"
	"fmt"
	"sync"

	dmcp "github.com/SOULOFCINDERS/agent/internal/domain/mcp"
)

// MCPManager manages multiple MCP Server connections.
// It implements domain/mcp.Manager.
type MCPManager struct {
	mu      sync.RWMutex
	clients map[string]dmcp.Client // serverID -> client
	routing map[string]string      // qualifiedToolName -> serverID
}

// NewManager creates a new MCPManager.
func NewManager() *MCPManager {
	return &MCPManager{
		clients: make(map[string]dmcp.Client),
		routing: make(map[string]string),
	}
}

// AddServer connects to an MCP Server and registers it.
func (m *MCPManager) AddServer(ctx context.Context, cfg dmcp.ServerConfig) error {
	var client dmcp.Client
	switch cfg.Transport {
	case "stdio":
		client = NewStdioClient(cfg.ID, cfg.Command, cfg.Args...)
	case "sse", "http":
		client = NewSSEClient(cfg.ID, cfg.URL)
	default:
		return fmt.Errorf("mcp: unsupported transport %q for server %s", cfg.Transport, cfg.ID)
	}

	if err := client.Connect(ctx); err != nil {
		return err
	}

	m.mu.Lock()
	m.clients[cfg.ID] = client
	m.mu.Unlock()

	return nil
}

// DiscoverAllTools queries all connected servers and returns their tools.
// Builds an internal routing table mapping qualifiedName -> serverID.
// Tool names are qualified as "serverID/toolName" to avoid collisions.
func (m *MCPManager) DiscoverAllTools(ctx context.Context) ([]dmcp.DiscoveredTool, error) {
	m.mu.RLock()
	clients := make(map[string]dmcp.Client, len(m.clients))
	for k, v := range m.clients {
		clients[k] = v
	}
	m.mu.RUnlock()

	var allTools []dmcp.DiscoveredTool
	newRouting := make(map[string]string)

	for serverID, client := range clients {
		tools, err := client.ListTools(ctx)
		if err != nil {
			return nil, fmt.Errorf("mcp discover tools from %s: %w", serverID, err)
		}
		for _, t := range tools {
			qualifiedName := serverID + "/" + t.Name
			t.Name = qualifiedName
			newRouting[qualifiedName] = serverID
			allTools = append(allTools, t)
		}
	}

	m.mu.Lock()
	m.routing = newRouting
	m.mu.Unlock()

	return allTools, nil
}

// CallTool routes a tool call to the correct MCP Server.
// toolName must be in "serverID/toolName" format.
func (m *MCPManager) CallTool(ctx context.Context, toolName string, args map[string]any) (string, error) {
	m.mu.RLock()
	serverID, ok := m.routing[toolName]
	client := m.clients[serverID]
	m.mu.RUnlock()

	if !ok || client == nil {
		return "", fmt.Errorf("mcp: no server found for tool %q", toolName)
	}

	// Strip "serverID/" prefix to get the original tool name
	originalName := toolName[len(serverID)+1:]

	return client.CallTool(ctx, originalName, args)
}

// Close disconnects all MCP Servers.
func (m *MCPManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var firstErr error
	for id, client := range m.clients {
		if err := client.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("mcp close %s: %w", id, err)
		}
	}
	m.clients = make(map[string]dmcp.Client)
	m.routing = make(map[string]string)
	return firstErr
}
