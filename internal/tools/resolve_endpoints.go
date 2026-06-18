package tools

import (
	"context"
	"encoding/json"
	"fmt"

	eobv1 "github.com/mimetrix/eob-mcp/gen/go/eob/v1"
	"github.com/mimetrix/eob-mcp/internal/mcp"
	"github.com/mimetrix/eob-mcp/internal/service"
)

// ResolveEndpoints is the MCP wrapper for EoBService.ResolveEndpoints —
// IP[:port] -> Kubernetes workload + identity. Impl in
// internal/service/resolve_endpoints.go.
type ResolveEndpoints struct {
	svc *service.Server
}

func NewResolveEndpoints(svc *service.Server) *ResolveEndpoints { return &ResolveEndpoints{svc: svc} }

func (t *ResolveEndpoints) Name() string { return "resolve_endpoints" }

func (t *ResolveEndpoints) Description() string {
	return "Resolve a batch of IP[:port] endpoints to the owning Kubernetes workload and identity: pods -> {namespace, owner workload (Deployment/DaemonSet/…), serviceAccount}; Service ClusterIPs -> the Service; node IPs -> the node; otherwise 'external'. Turns raw kernel flow tuples (TRACE east-west, MCP downstream reach) into named, identity-tagged edges. Argument: {\"endpoints\":[{\"ip\":\"10.3.0.1\",\"port\":443}, ...]}."
}

func (t *ResolveEndpoints) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"endpoints":{"type":"array","items":{"type":"object","properties":{"ip":{"type":"string"},"port":{"type":"integer"}},"required":["ip"]}}},"required":["endpoints"],"additionalProperties":false}`)
}

type resolveArgs struct {
	Endpoints []struct {
		IP   string `json:"ip"`
		Port int32  `json:"port"`
	} `json:"endpoints"`
}

func (t *ResolveEndpoints) Call(ctx context.Context, args json.RawMessage) (mcp.CallToolResult, error) {
	var a resolveArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return mcp.CallToolResult{}, fmt.Errorf("decode arguments: %w", err)
		}
	}
	req := &eobv1.ResolveEndpointsRequest{}
	for _, e := range a.Endpoints {
		req.Endpoints = append(req.Endpoints, &eobv1.Endpoint{Ip: e.IP, Port: e.Port})
	}
	resp, err := t.svc.ResolveEndpoints(ctx, req)
	if err != nil {
		return mcp.CallToolResult{}, err
	}
	body, err := protoMarshal.Marshal(resp)
	if err != nil {
		return mcp.CallToolResult{}, err
	}
	return mcp.CallToolResult{Content: []mcp.Content{{Type: "text", Text: string(body)}}}, nil
}

var _ mcp.ToolHandler = (*ResolveEndpoints)(nil)
