// Package mcp provides MCP Client and Manager implementations using the
// official Go MCP SDK (github.com/modelcontextprotocol/go-sdk).
package mcp

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	dmcp "github.com/SOULOFCINDERS/agent/internal/domain/mcp"
)

// ---------- StdioClient ----------

// StdioClient connects to an MCP Server via stdio (subprocess).
// It wraps the official SDK's Client and CommandTransport.
type StdioClient struct {
	serverID string
	command  string
	args     []string
	session  *sdkmcp.ClientSession
	client   *sdkmcp.Client
}

// NewStdioClient creates a stdio-based MCP client.
func NewStdioClient(serverID, command string, args ...string) *StdioClient {
	return &StdioClient{
		serverID: serverID,
		command:  command,
		args:     args,
	}
}

func (c *StdioClient) Connect(ctx context.Context) error {
	c.client = sdkmcp.NewClient(
		&sdkmcp.Implementation{Name: "agent-framework", Version: "v1.0.0"},
		nil,
	)
	transport := &sdkmcp.CommandTransport{
		Command: exec.Command(c.command, c.args...),
	}
	session, err := c.client.Connect(ctx, transport, nil)
	if err != nil {
		return fmt.Errorf("mcp stdio connect %s: %w", c.serverID, err)
	}
	c.session = session
	return nil
}

func (c *StdioClient) ListTools(ctx context.Context) ([]dmcp.DiscoveredTool, error) {
	if c.session == nil {
		return nil, fmt.Errorf("mcp client %s: not connected", c.serverID)
	}
	result, err := c.session.ListTools(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("mcp list tools %s: %w", c.serverID, err)
	}
	return convertTools(result.Tools, c.serverID), nil
}

func (c *StdioClient) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	if c.session == nil {
		return "", fmt.Errorf("mcp client %s: not connected", c.serverID)
	}
	return callToolOnSession(c.session, ctx, c.serverID, name, args)
}

func (c *StdioClient) Close() error {
	if c.session != nil {
		return c.session.Close()
	}
	return nil
}

// ---------- SSEClient ----------

// SSEClient connects to an MCP Server via Streamable HTTP / SSE.
type SSEClient struct {
	serverID string
	url      string
	session  *sdkmcp.ClientSession
	client   *sdkmcp.Client
}

// NewSSEClient creates an SSE/HTTP-based MCP client.
func NewSSEClient(serverID, url string) *SSEClient {
	return &SSEClient{
		serverID: serverID,
		url:      url,
	}
}

func (c *SSEClient) Connect(ctx context.Context) error {
	c.client = sdkmcp.NewClient(
		&sdkmcp.Implementation{Name: "agent-framework", Version: "v1.0.0"},
		nil,
	)
	transport := &sdkmcp.StreamableClientTransport{Endpoint: c.url}
	session, err := c.client.Connect(ctx, transport, nil)
	if err != nil {
		return fmt.Errorf("mcp sse connect %s: %w", c.serverID, err)
	}
	c.session = session
	return nil
}

func (c *SSEClient) ListTools(ctx context.Context) ([]dmcp.DiscoveredTool, error) {
	if c.session == nil {
		return nil, fmt.Errorf("mcp client %s: not connected", c.serverID)
	}
	result, err := c.session.ListTools(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("mcp list tools %s: %w", c.serverID, err)
	}
	return convertTools(result.Tools, c.serverID), nil
}

func (c *SSEClient) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	if c.session == nil {
		return "", fmt.Errorf("mcp client %s: not connected", c.serverID)
	}
	return callToolOnSession(c.session, ctx, c.serverID, name, args)
}

func (c *SSEClient) Close() error {
	if c.session != nil {
		return c.session.Close()
	}
	return nil
}

// ---------- helpers ----------

// convertTools converts SDK Tool structs to domain DiscoveredTool structs.
func convertTools(sdkTools []*sdkmcp.Tool, serverID string) []dmcp.DiscoveredTool {
	tools := make([]dmcp.DiscoveredTool, 0, len(sdkTools))
	for _, t := range sdkTools {
		// InputSchema is typed as `any` in the SDK.
		// From the client side, it is unmarshaled as map[string]any.
		schema, _ := t.InputSchema.(map[string]any)
		tools = append(tools, dmcp.DiscoveredTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: schema,
			ServerID:    serverID,
		})
	}
	return tools
}

// callToolOnSession invokes a tool on a connected session and extracts text results.
func callToolOnSession(session *sdkmcp.ClientSession, ctx context.Context, serverID, name string, args map[string]any) (string, error) {
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		return "", fmt.Errorf("mcp call tool %s/%s: %w", serverID, name, err)
	}
	if res.IsError {
		return "", fmt.Errorf("mcp tool %s/%s returned error", serverID, name)
	}
	var parts []string
	for _, content := range res.Content {
		if tc, ok := content.(*sdkmcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n"), nil
}
