package tools

import (
	"context"
	"encoding/json"

	eobv1 "github.com/mimetrix/eob-mcp/gen/go/eob/v1"
	"github.com/mimetrix/eob-mcp/internal/mcp"
	"github.com/mimetrix/eob-mcp/internal/service"
)

// EoBHealth is the MCP wrapper for the EoBService.EoBHealth RPC. The
// implementation lives in internal/service/eob_health.go.
type EoBHealth struct {
	svc *service.Server
}

// NewEoBHealth constructs the tool. The service is the only dependency;
// cfg and kube are reached through it. When the service has no kube
// client wiring, the response carries cluster_state="no-cluster".
func NewEoBHealth(svc *service.Server) *EoBHealth {
	return &EoBHealth{svc: svc}
}

// Name implements mcp.ToolHandler.
func (t *EoBHealth) Name() string { return "eob_health" }

// Description implements mcp.ToolHandler.
func (t *EoBHealth) Description() string {
	return "Returns a health snapshot of the EoB stack: operator, dashboard, streamstore, webhook, and per-node agent readiness under 'components'; per-directive DaemonSet breakdown under 'directives'; pod counts per node under 'agents_per_node'. Each component reports ready/desired counts plus a coarse status (ok|degraded|absent|error). cluster_state='no-cluster' indicates no Kubernetes client available. No arguments."
}

// InputSchema implements mcp.ToolHandler.
func (t *EoBHealth) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
}

// Call implements mcp.ToolHandler.
func (t *EoBHealth) Call(ctx context.Context, _ json.RawMessage) (mcp.CallToolResult, error) {
	resp, err := t.svc.EoBHealth(ctx, &eobv1.EoBHealthRequest{})
	if err != nil {
		return mcp.CallToolResult{}, err
	}
	body, err := protoMarshal.Marshal(resp)
	if err != nil {
		return mcp.CallToolResult{}, err
	}
	return mcp.CallToolResult{
		Content: []mcp.Content{{Type: "text", Text: string(body)}},
	}, nil
}

// Compile-time assertion.
var _ mcp.ToolHandler = (*EoBHealth)(nil)
