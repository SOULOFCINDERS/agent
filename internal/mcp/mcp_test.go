package mcp

import (
	"context"
	"testing"

	dmcp "github.com/SOULOFCINDERS/agent/internal/domain/mcp"
)

// ---------- Mock MCP Client ----------

type mockMCPClient struct {
	serverID string
	tools    []dmcp.DiscoveredTool
	callResp map[string]string
}

func (m *mockMCPClient) Connect(ctx context.Context) error { return nil }

func (m *mockMCPClient) ListTools(ctx context.Context) ([]dmcp.DiscoveredTool, error) {
	return m.tools, nil
}

func (m *mockMCPClient) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	if resp, ok := m.callResp[name]; ok {
		return resp, nil
	}
	return "ok", nil
}

func (m *mockMCPClient) Close() error { return nil }

// ---------- Tests ----------

func TestManagerDiscoverTools(t *testing.T) {
	mgr := NewManager()
	mock := &mockMCPClient{
		serverID: "test-server",
		tools: []dmcp.DiscoveredTool{
			{Name: "tool_a", Description: "Tool A", ServerID: "test-server"},
			{Name: "tool_b", Description: "Tool B", ServerID: "test-server"},
		},
	}
	mgr.mu.Lock()
	mgr.clients["test-server"] = mock
	mgr.mu.Unlock()

	ctx := context.Background()
	tools, err := mgr.DiscoverAllTools(ctx)
	if err != nil {
		t.Fatalf("DiscoverAllTools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}
	if tools[0].Name != "test-server/tool_a" {
		t.Errorf("expected test-server/tool_a, got %s", tools[0].Name)
	}
	if tools[1].Name != "test-server/tool_b" {
		t.Errorf("expected test-server/tool_b, got %s", tools[1].Name)
	}
}

func TestManagerCallTool(t *testing.T) {
	mgr := NewManager()
	mock := &mockMCPClient{
		serverID: "srv1",
		tools: []dmcp.DiscoveredTool{
			{Name: "greet", Description: "Greeting", ServerID: "srv1"},
		},
		callResp: map[string]string{"greet": "Hello, World!"},
	}
	mgr.mu.Lock()
	mgr.clients["srv1"] = mock
	mgr.mu.Unlock()

	ctx := context.Background()
	_, _ = mgr.DiscoverAllTools(ctx) // build routing

	result, err := mgr.CallTool(ctx, "srv1/greet", map[string]any{"name": "test"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result != "Hello, World!" {
		t.Errorf("expected Hello, World!, got %s", result)
	}
}

func TestManagerCallToolNotFound(t *testing.T) {
	mgr := NewManager()
	_, err := mgr.CallTool(context.Background(), "nonexistent/tool", nil)
	if err == nil {
		t.Fatal("expected error for nonexistent tool")
	}
}

func TestManagerMultipleServers(t *testing.T) {
	mgr := NewManager()
	mock1 := &mockMCPClient{
		serverID: "db",
		tools:    []dmcp.DiscoveredTool{{Name: "query", Description: "DB Query", ServerID: "db"}},
		callResp: map[string]string{"query": "rows=42"},
	}
	mock2 := &mockMCPClient{
		serverID: "fs",
		tools:    []dmcp.DiscoveredTool{{Name: "read", Description: "File Read", ServerID: "fs"}},
		callResp: map[string]string{"read": "file content"},
	}
	mgr.mu.Lock()
	mgr.clients["db"] = mock1
	mgr.clients["fs"] = mock2
	mgr.mu.Unlock()

	ctx := context.Background()
	tools, err := mgr.DiscoverAllTools(ctx)
	if err != nil {
		t.Fatalf("DiscoverAllTools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}

	r1, err := mgr.CallTool(ctx, "db/query", nil)
	if err != nil {
		t.Fatalf("CallTool db/query: %v", err)
	}
	if r1 != "rows=42" {
		t.Errorf("expected rows=42, got %s", r1)
	}

	r2, err := mgr.CallTool(ctx, "fs/read", nil)
	if err != nil {
		t.Fatalf("CallTool fs/read: %v", err)
	}
	if r2 != "file content" {
		t.Errorf("expected file content, got %s", r2)
	}
}

func TestToolAdapter(t *testing.T) {
	mgr := NewManager()
	mock := &mockMCPClient{
		serverID: "test",
		tools: []dmcp.DiscoveredTool{{
			Name:        "echo",
			Description: "Echo tool",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"text": map[string]any{"type": "string"},
				},
			},
			ServerID: "test",
		}},
		callResp: map[string]string{"echo": "echoed"},
	}
	mgr.mu.Lock()
	mgr.clients["test"] = mock
	mgr.mu.Unlock()

	ctx := context.Background()
	tools, _ := mgr.DiscoverAllTools(ctx)

	adapter := NewToolAdapter(mgr, tools[0])

	if adapter.Name() != "test/echo" {
		t.Errorf("expected test/echo, got %s", adapter.Name())
	}
	if adapter.Description() != "Echo tool" {
		t.Errorf("expected Echo tool, got %s", adapter.Description())
	}
	schema := adapter.ParameterSchema()
	if len(schema) == 0 {
		t.Error("expected non-empty schema")
	}

	result, err := adapter.Execute(ctx, map[string]any{"text": "hello"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result != "echoed" {
		t.Errorf("expected echoed, got %v", result)
	}
}

func TestManagerClose(t *testing.T) {
	mgr := NewManager()
	mock := &mockMCPClient{serverID: "srv"}
	mgr.mu.Lock()
	mgr.clients["srv"] = mock
	mgr.mu.Unlock()

	if err := mgr.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	mgr.mu.RLock()
	count := len(mgr.clients)
	mgr.mu.RUnlock()
	if count != 0 {
		t.Errorf("expected 0 clients after close, got %d", count)
	}
}
