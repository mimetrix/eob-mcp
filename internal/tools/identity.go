// Package tools holds the MCP tool wrappers exposed by eob-mcp.
//
// Each tool is a thin MCP-protocol adapter that delegates to the
// in-process gRPC service in internal/service/. Tool wrappers own no
// business logic — they translate JSON args in, call the service, and
// marshal the proto response back out as MCP content.
package tools

import (
	"context"
	"encoding/json"

	"google.golang.org/protobuf/encoding/protojson"

	eobv1 "github.com/mimetrix/eob-mcp/gen/go/eob/v1"
	"github.com/mimetrix/eob-mcp/internal/mcp"
	"github.com/mimetrix/eob-mcp/internal/service"
)

// protoMarshal is the MCP-side JSON encoder for proto responses. Settings:
//   - UseProtoNames keeps snake_case field names (matches the .proto
//     declarations and the pre-migration tool contract).
//   - EmitUnpopulated emits zero-valued scalars instead of omitting them,
//     so consumers see a stable shape regardless of whether a version was
//     populated.
//   - Indent makes the MCP `text` content block readable.
var protoMarshal = protojson.MarshalOptions{
	UseProtoNames:   true,
	EmitUnpopulated: true,
	Indent:          "  ",
}

// ClusterIdentity is the MCP wrapper for the EoBService.ClusterIdentity
// RPC. The implementation lives in internal/service/cluster_identity.go.
type ClusterIdentity struct {
	svc *service.Server
}

// NewClusterIdentity constructs the tool. The service is the only
// dependency; cfg and kube are reached through it.
func NewClusterIdentity(svc *service.Server) *ClusterIdentity {
	return &ClusterIdentity{svc: svc}
}

// Name implements mcp.ToolHandler.
func (t *ClusterIdentity) Name() string { return "cluster_identity" }

// Description implements mcp.ToolHandler.
func (t *ClusterIdentity) Description() string {
	return "Returns the cluster's identity for fleet identification: cluster (site_id, tenant, region), k8s_version, eob_version, mcp_version. Lets a console enumerate connected clusters and know which one it's talking to. No arguments."
}

// InputSchema implements mcp.ToolHandler.
func (t *ClusterIdentity) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
}

// Call implements mcp.ToolHandler.
func (t *ClusterIdentity) Call(ctx context.Context, _ json.RawMessage) (mcp.CallToolResult, error) {
	resp, err := t.svc.ClusterIdentity(ctx, &eobv1.ClusterIdentityRequest{})
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
var _ mcp.ToolHandler = (*ClusterIdentity)(nil)
