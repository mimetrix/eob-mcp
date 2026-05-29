// Package tools holds the built-in MCP tools exposed by eob-mcp.
package tools

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/mimetrix/eob-mcp/internal/config"
	"github.com/mimetrix/eob-mcp/internal/mcp"
)

// k8sCallTimeout bounds every API call this tool makes so that a slow or
// unreachable apiserver cannot stall MCP request processing.
const k8sCallTimeout = 5 * time.Second

// operatorVersionLabel is the standard Helm/Kubebuilder label carrying
// the chart's appVersion (e.g. "v2.39.36-rc1"). When present on the
// operator Deployment, it is preferred over the container image tag
// because it tracks the released version rather than whatever tag the
// in-cluster registry happens to ship under.
const operatorVersionLabel = "app.kubernetes.io/version"

// operatorManagerContainer is the conventional name of the controller
// container in a kubebuilder-generated operator. eob_version falls back
// to this container's image tag when no version label is set.
const operatorManagerContainer = "manager"

// ClusterIdentity returns the cluster identity block used by fleet
// consumers to label results coming back from this server.
type ClusterIdentity struct {
	cfg  *config.Config
	kube kubernetes.Interface
}

// NewClusterIdentity constructs the tool. Pass nil for kube to disable
// live cluster lookups (k8s_version and eob_version will be reported as
// empty); the Phase 1a behavior. With a non-nil kube, the version fields
// are populated on each call.
func NewClusterIdentity(cfg *config.Config, kube kubernetes.Interface) *ClusterIdentity {
	return &ClusterIdentity{cfg: cfg, kube: kube}
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
func (t *ClusterIdentity) Call(ctx context.Context, _ json.RawMessage) (mcp.CallToolResult, error) {
	identity := map[string]string{
		"site_id":     t.cfg.SiteID,
		"tenant":      t.cfg.Tenant,
		"region":      t.cfg.Region,
		"k8s_version": t.k8sVersion(ctx),
		"eob_version": t.eobVersion(ctx),
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

// k8sVersion returns the server's reported GitVersion (e.g. "v1.31.4"),
// or "" if no kube client is wired or the call fails. Failures are
// silenced because identity is best-effort metadata — callers should not
// have to handle a partial result as an error.
func (t *ClusterIdentity) k8sVersion(_ context.Context) string {
	if t.kube == nil {
		return ""
	}
	// ServerVersion has no context-aware variant in the typed Discovery
	// interface; the call is bounded by the underlying REST client's
	// timeout. A future refactor can use a rest.Config with explicit
	// per-call deadlines if this proves too coarse.
	info, err := t.kube.Discovery().ServerVersion()
	if err != nil {
		return ""
	}
	return info.GitVersion
}

// eobVersion derives the EoB platform version from the operator
// Deployment. It prefers the Helm-stamped app.kubernetes.io/version
// label (most stable across re-tagged images) and falls back to the
// "manager" container's image tag, then the first container's image
// tag. Returns "" if no kube client is wired or the deployment is
// absent.
func (t *ClusterIdentity) eobVersion(ctx context.Context) string {
	if t.kube == nil {
		return ""
	}
	callCtx, cancel := context.WithTimeout(ctx, k8sCallTimeout)
	defer cancel()
	dep, err := t.kube.AppsV1().
		Deployments(t.cfg.OperatorNamespace).
		Get(callCtx, t.cfg.OperatorDeploymentName, metav1.GetOptions{})
	if err != nil {
		return ""
	}
	if v := dep.Labels[operatorVersionLabel]; v != "" {
		return v
	}
	containers := dep.Spec.Template.Spec.Containers
	for i := range containers {
		if containers[i].Name == operatorManagerContainer {
			if tag := imageTag(containers[i].Image); tag != "" {
				return tag
			}
			break
		}
	}
	if len(containers) == 0 {
		return ""
	}
	return imageTag(containers[0].Image)
}

// imageTag returns the tag portion of an OCI image reference, or "" if no
// tag is present. Handles registries with a port (host:port/path:tag) by
// splitting on the rightmost colon after the last slash.
func imageTag(ref string) string {
	if ref == "" {
		return ""
	}
	// Strip any digest suffix; eob_version targets the tag, not the digest.
	if at := strings.Index(ref, "@"); at >= 0 {
		ref = ref[:at]
	}
	slash := strings.LastIndex(ref, "/")
	tail := ref
	if slash >= 0 {
		tail = ref[slash+1:]
	}
	colon := strings.LastIndex(tail, ":")
	if colon < 0 {
		return ""
	}
	return tail[colon+1:]
}

// Ensure ClusterIdentity satisfies mcp.ToolHandler at compile time.
var _ mcp.ToolHandler = (*ClusterIdentity)(nil)
