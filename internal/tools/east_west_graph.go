package tools

import (
	"context"
	"encoding/json"
	"fmt"

	eobv1 "github.com/mimetrix/eob-mcp/gen/go/eob/v1"
	"github.com/mimetrix/eob-mcp/internal/mcp"
	"github.com/mimetrix/eob-mcp/internal/service"
)

// EastWestGraph is the MCP wrapper for EoBService.EastWestGraph — sample
// the TRACE defense bus and return the resolved service-call graph.
type EastWestGraph struct {
	svc *service.Server
}

func NewEastWestGraph(svc *service.Server) *EastWestGraph { return &EastWestGraph{svc: svc} }

func (t *EastWestGraph) Name() string { return "east_west_graph" }

func (t *EastWestGraph) Description() string {
	return "Sample the TRACE defense bus for a window and return the east-west service-call graph with every edge resolved to a named, identity-tagged workload (src process/pod -> dst pod/service/node with namespace, owner workload, serviceAccount, and connection count). One call = tap + aggregate + resolve. Arguments: {\"window_seconds\":10,\"max_edges\":50} (both optional). Requires the defense bus configured (else cluster_state='no-defense-bus')."
}

func (t *EastWestGraph) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"window_seconds":{"type":"integer"},"max_edges":{"type":"integer"}},"additionalProperties":false}`)
}

type ewgArgs struct {
	WindowSeconds int32 `json:"window_seconds"`
	MaxEdges      int32 `json:"max_edges"`
}

func (t *EastWestGraph) Call(ctx context.Context, args json.RawMessage) (mcp.CallToolResult, error) {
	var a ewgArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return mcp.CallToolResult{}, fmt.Errorf("decode arguments: %w", err)
		}
	}
	resp, err := t.svc.EastWestGraph(ctx, &eobv1.EastWestGraphRequest{
		WindowSeconds: a.WindowSeconds, MaxEdges: a.MaxEdges,
	})
	if err != nil {
		return mcp.CallToolResult{}, err
	}
	body, err := protoMarshal.Marshal(resp)
	if err != nil {
		return mcp.CallToolResult{}, err
	}
	return mcp.CallToolResult{Content: []mcp.Content{{Type: "text", Text: string(body)}}}, nil
}

var _ mcp.ToolHandler = (*EastWestGraph)(nil)
