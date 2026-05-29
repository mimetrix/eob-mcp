// Package tools holds the built-in MCP tools exposed by eob-mcp.
package tools

import (
	"context"
	"encoding/json"

	"github.com/mimetrix/eob-mcp/internal/config"
	"github.com/mimetrix/eob-mcp/internal/mcp"
)

// ClusterIdentity returns the cluster identity block used by fleet
// consumers to label results coming back from this server.
//
// In Phase 1a, k8s_version and eob_version are empty (Phase 1b adds k8s
// integration and will populate them by querying the cluster).
type ClusterIdentity struct {
	cfg *config.Config
}

// NewClusterIdentity constructs the tool. The Config is shared by
// reference; callers that mutate Config after registration affect this
// tool's output. In practice, Config is built once at startup.
func NewClusterIdentity(cfg *config.Config) *ClusterIdentity {
	return &ClusterIdentity{cfg: cfg}
}

// Name implements mcp.ToolHandler.
func (t *ClusterIdentity) Name() string { return "cluster_identity" }

// Description implements mcp.ToolHandler.
func (t *ClusterIdentity) Description() string {
	return "Returns the cluster's identity for fleet identification: site_id, tenant, region, k8s_version, eob_version, mcp_version. Lets a console enumerate connected clusters and know which one it's talking to. No arguments."
}

// InputSchema implements mcp.ToolHandler.
func (t *ClusterIdentity) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
}

// Call implements mcp.ToolHandler.
func (t *ClusterIdentity) Call(_ context.Context, _ json.RawMessage) (mcp.CallToolResult, error) {
	identity := map[string]string{
		"site_id":     t.cfg.SiteID,
		"tenant":      t.cfg.Tenant,
		"region":      t.cfg.Region,
		"k8s_version": "", // populated in Phase 1b
		"eob_version": "", // populated in Phase 1b
		"mcp_version": t.cfg.MCPVersion,
	}
	body, err := json.MarshalIndent(identity, "", "  ")
	if err != nil {
		return mcp.CallToolResult{}, err
	}
	return mcp.CallToolResult{
		Content: []mcp.Content{{Type: "text", Text: string(body)}},
	}, nil
}
