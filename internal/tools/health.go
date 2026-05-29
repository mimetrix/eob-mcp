package tools

import (
	"context"
	"encoding/json"
	"sort"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/mimetrix/eob-mcp/internal/config"
	"github.com/mimetrix/eob-mcp/internal/mcp"
)

// Tawon component layout. Most of these are the chart's resource names;
// override via env if a downstream chart renames them.
const (
	tawonDashboardDeployment    = "tawon-dashboard"
	tawonStreamstoreStatefulSet = "tawon-streamstore"
)

// componentStatus is the per-component slice of the health snapshot.
// Kind is included so a consumer can render a uniform table without
// knowing the workload type up-front. Status is one of "ok", "degraded",
// "absent", or "error".
type componentStatus struct {
	Kind    string `json:"kind"`
	Ready   int32  `json:"ready"`
	Desired int32  `json:"desired"`
	Status  string `json:"status"`
	Detail  string `json:"detail,omitempty"`
}

// directiveStatus is the per-directive slice in the directives breakdown.
// One entry per DaemonSet matched by Config.DirectiveLabelSelector.
type directiveStatus struct {
	Name    string `json:"name"`
	Ready   int32  `json:"ready"`
	Desired int32  `json:"desired"`
	Status  string `json:"status"`
}

// nodeAgentSummary is the per-node entry in agents_per_node. With one
// DaemonSet per directive, a node may host multiple agent pods, so the
// shape is counts rather than a single per-pod status string.
type nodeAgentSummary struct {
	Ready int `json:"ready"`
	Total int `json:"total"`
}

// EoBHealth returns a structured health snapshot of the EoB stack.
type EoBHealth struct {
	cfg  *config.Config
	kube kubernetes.Interface
}

// NewEoBHealth constructs the health tool. A nil kube client puts the
// tool in "no cluster" mode: it returns a stub response indicating that
// no Kubernetes endpoint is available rather than fabricating data.
func NewEoBHealth(cfg *config.Config, kube kubernetes.Interface) *EoBHealth {
	return &EoBHealth{cfg: cfg, kube: kube}
}

// Name implements mcp.ToolHandler.
func (t *EoBHealth) Name() string { return "eob_health" }

// Description implements mcp.ToolHandler.
func (t *EoBHealth) Description() string {
	return "Returns a health snapshot of the EoB stack: operator, dashboard, streamstore, webhook, and per-node agent readiness. Each component reports ready/desired counts plus a coarse status (ok|degraded|absent|error). No arguments."
}

// InputSchema implements mcp.ToolHandler.
func (t *EoBHealth) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
}

// Call implements mcp.ToolHandler.
func (t *EoBHealth) Call(ctx context.Context, _ json.RawMessage) (mcp.CallToolResult, error) {
	if t.kube == nil {
		return jsonResult(map[string]any{
			"status": "no-cluster",
			"note":   "no Kubernetes client available; eob-mcp was started without in-cluster or kubeconfig access",
		})
	}

	callCtx, cancel := context.WithTimeout(ctx, k8sCallTimeout)
	defer cancel()

	agent, directives := t.directiveStatus(callCtx)
	snapshot := map[string]any{
		"operator":        t.deploymentStatus(callCtx, t.cfg.OperatorNamespace, t.cfg.OperatorDeploymentName),
		"dashboard":       t.deploymentStatus(callCtx, t.cfg.TawonNamespace, tawonDashboardDeployment),
		"streamstore":     t.statefulSetStatus(callCtx, t.cfg.TawonNamespace, tawonStreamstoreStatefulSet),
		"webhook":         t.webhookConfigStatus(callCtx, t.cfg.WebhookConfigName),
		"agent":           agent,
		"directives":      directives,
		"agents_per_node": t.agentReadinessByNode(callCtx),
	}
	return jsonResult(snapshot)
}

func (t *EoBHealth) deploymentStatus(ctx context.Context, ns, name string) componentStatus {
	cs := componentStatus{Kind: "Deployment"}
	dep, err := t.kube.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return absentOrError(cs, err)
	}
	desired := int32(1)
	if dep.Spec.Replicas != nil {
		desired = *dep.Spec.Replicas
	}
	cs.Desired = desired
	cs.Ready = dep.Status.ReadyReplicas
	cs.Status = readyStatus(cs.Ready, cs.Desired)
	return cs
}

func (t *EoBHealth) statefulSetStatus(ctx context.Context, ns, name string) componentStatus {
	cs := componentStatus{Kind: "StatefulSet"}
	ss, err := t.kube.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return absentOrError(cs, err)
	}
	desired := int32(1)
	if ss.Spec.Replicas != nil {
		desired = *ss.Spec.Replicas
	}
	cs.Desired = desired
	cs.Ready = ss.Status.ReadyReplicas
	cs.Status = readyStatus(cs.Ready, cs.Desired)
	return cs
}

// webhookConfigStatus reports presence of the cluster-scoped
// MutatingWebhookConfiguration installed by the EoB stack. The chart
// installs a single MWC carrying one-or-more webhook entries (per
// directive). Health is binary at the config-object level: present
// counts as "ok"; missing is "absent". Endpoint reachability is not
// probed here.
func (t *EoBHealth) webhookConfigStatus(ctx context.Context, name string) componentStatus {
	cs := componentStatus{Kind: "MutatingWebhookConfiguration"}
	mwc, err := t.kube.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return absentOrError(cs, err)
	}
	count := int32(len(mwc.Webhooks))
	cs.Desired = count
	cs.Ready = count
	if count == 0 {
		cs.Status = "absent"
	} else {
		cs.Status = "ok"
	}
	return cs
}

// directiveStatus discovers every per-directive DaemonSet in the Tawon
// namespace via the configured label selector, then returns both an
// aggregate componentStatus (suitable for the legacy "agent" key) and a
// per-directive breakdown sorted by name for stable output.
func (t *EoBHealth) directiveStatus(ctx context.Context) (componentStatus, []directiveStatus) {
	cs := componentStatus{Kind: "DaemonSet"}
	list, err := t.kube.AppsV1().DaemonSets(t.cfg.TawonNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: t.cfg.DirectiveLabelSelector,
	})
	if err != nil {
		cs.Status = "error"
		cs.Detail = err.Error()
		return cs, nil
	}
	if len(list.Items) == 0 {
		cs.Status = "absent"
		return cs, []directiveStatus{}
	}
	perDirective := make([]directiveStatus, 0, len(list.Items))
	var totalReady, totalDesired int32
	for i := range list.Items {
		ds := &list.Items[i]
		perDirective = append(perDirective, directiveStatus{
			Name:    ds.Name,
			Ready:   ds.Status.NumberReady,
			Desired: ds.Status.DesiredNumberScheduled,
			Status:  readyStatus(ds.Status.NumberReady, ds.Status.DesiredNumberScheduled),
		})
		totalReady += ds.Status.NumberReady
		totalDesired += ds.Status.DesiredNumberScheduled
	}
	sort.Slice(perDirective, func(i, j int) bool {
		return perDirective[i].Name < perDirective[j].Name
	})
	cs.Ready = totalReady
	cs.Desired = totalDesired
	cs.Status = readyStatus(totalReady, totalDesired)
	return cs, perDirective
}

// agentReadinessByNode lists agent pods (across all directive
// DaemonSets) and groups them by spec.nodeName, reporting ready vs
// total counts. Pods without an assigned node appear under "<pending>".
// Returns an error placeholder map if the List call fails.
func (t *EoBHealth) agentReadinessByNode(ctx context.Context) any {
	pods, err := t.kube.CoreV1().Pods(t.cfg.TawonNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: t.cfg.DirectiveLabelSelector,
	})
	if err != nil {
		return map[string]string{"error": err.Error()}
	}
	byNode := make(map[string]*nodeAgentSummary)
	for i := range pods.Items {
		p := &pods.Items[i]
		node := p.Spec.NodeName
		if node == "" {
			node = "<pending>"
		}
		s := byNode[node]
		if s == nil {
			s = &nodeAgentSummary{}
			byNode[node] = s
		}
		s.Total++
		if isPodReady(p) {
			s.Ready++
		}
	}
	out := make(map[string]nodeAgentSummary, len(byNode))
	for k, v := range byNode {
		out[k] = *v
	}
	return out
}

func absentOrError(cs componentStatus, err error) componentStatus {
	if apierrors.IsNotFound(err) {
		cs.Status = "absent"
		return cs
	}
	cs.Status = "error"
	cs.Detail = err.Error()
	return cs
}

func readyStatus(ready, desired int32) string {
	switch {
	case desired == 0:
		return "absent"
	case ready >= desired:
		return "ok"
	default:
		return "degraded"
	}
}

func isPodReady(p *corev1.Pod) bool {
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func jsonResult(v any) (mcp.CallToolResult, error) {
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return mcp.CallToolResult{}, err
	}
	return mcp.CallToolResult{
		Content: []mcp.Content{{Type: "text", Text: string(body)}},
	}, nil
}

var _ mcp.ToolHandler = (*EoBHealth)(nil)
