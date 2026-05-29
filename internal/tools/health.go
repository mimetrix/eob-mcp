package tools

import (
	"context"
	"encoding/json"

	"github.com/mimetrix/eob-mcp/internal/mcp"
)

// EoBHealth is the stubbed health tool. Phase 1b replaces this with a
// real implementation that queries Kubernetes for operator, dashboard,
// streamstore, and webhook readiness, plus directive counts and agent
// pod readiness across nodes.
type EoBHealth struct{}

// NewEoBHealth constructs the stubbed health tool.
func NewEoBHealth() *EoBHealth { return &EoBHealth{} }

// Name implements mcp.ToolHandler.
func (t *EoBHealth) Name() string { return "eob_health" }

// Description implements mcp.ToolHandler.
func (t *EoBHealth) Description() string {
	return "Returns a health snapshot of the EoB stack: operator, dashboard, streamstore, webhook, registry, and per-node agent readiness. Stub in Phase 1a; populated by the Kubernetes integration in Phase 1b. No arguments."
}

// InputSchema implements mcp.ToolHandler.
func (t *EoBHealth) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
}

// Call implements mcp.ToolHandler.
func (t *EoBHealth) Call(_ context.Context, _ json.RawMessage) (mcp.CallToolResult, error) {
	stub := map[string]any{
		"status": "stub",
		"note":   "Phase 1b will replace this with a real Kubernetes-backed health check.",
		"planned_fields": []string{
			"operator", "dashboard", "streamstore", "webhook",
			"registry", "directive_count", "agents_per_node",
		},
	}
	body, err := json.MarshalIndent(stub, "", "  ")
	if err != nil {
		return mcp.CallToolResult{}, err
	}
	return mcp.CallToolResult{
		Content: []mcp.Content{{Type: "text", Text: string(body)}},
	}, nil
}
