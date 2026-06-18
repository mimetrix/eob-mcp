package tools

import (
	"context"
	"encoding/json"

	eobv1 "github.com/mimetrix/eob-mcp/gen/go/eob/v1"
	"github.com/mimetrix/eob-mcp/internal/mcp"
	"github.com/mimetrix/eob-mcp/internal/service"
)

// TraceHealth is the MCP wrapper for the EoBService.TraceHealth RPC —
// health of the co-resident RACE/TRACE defense stack. Implementation in
// internal/service/trace_health.go.
type TraceHealth struct {
	svc *service.Server
}

// NewTraceHealth constructs the tool.
func NewTraceHealth(svc *service.Server) *TraceHealth {
	return &TraceHealth{svc: svc}
}

// Name implements mcp.ToolHandler.
func (t *TraceHealth) Name() string { return "trace_health" }

// Description implements mcp.ToolHandler.
func (t *TraceHealth) Description() string {
	return "Returns a health snapshot of the co-resident RACE/TRACE defense stack (separate from the EoB stack): components 'trace_agent' (TRACE host-observation DaemonSet, trace-system) and 'race_agent' (RACE perimeter-denial DaemonSet, defense-system), each with ready/desired counts and a coarse status (ok|degraded|absent|error); per-node agent readiness under 'agents_per_node'. Components report 'absent' on sites where the defense stack is not installed. cluster_state='no-cluster' indicates no Kubernetes client. No arguments."
}

// InputSchema implements mcp.ToolHandler.
func (t *TraceHealth) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
}

// Call implements mcp.ToolHandler.
func (t *TraceHealth) Call(ctx context.Context, _ json.RawMessage) (mcp.CallToolResult, error) {
	resp, err := t.svc.TraceHealth(ctx, &eobv1.TraceHealthRequest{})
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
var _ mcp.ToolHandler = (*TraceHealth)(nil)
