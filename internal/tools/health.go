package tools

import (
	"context"
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/mimetrix/eob-mcp/internal/config"
	"github.com/mimetrix/eob-mcp/internal/mcp"
)

// Tawon component layout. These names match the chart's resource names;
// override via Helm values if a deployment renames them.
const (
	tawonDashboardDeployment   = "tawon-dashboard"
	tawonStreamstoreStatefulSet = "tawon-streamstore"
	tawonWebhookDeployment     = "tawon-webhook"
	tawonAgentDaemonSet        = "tawon-agent"
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

	snapshot := map[string]any{
		"operator":        t.deploymentStatus(callCtx, t.cfg.OperatorNamespace, tawonOperatorDeployment),
		"dashboard":       t.deploymentStatus(callCtx, t.cfg.TawonNamespace, tawonDashboardDeployment),
		"streamstore":     t.statefulSetStatus(callCtx, t.cfg.TawonNamespace, tawonStreamstoreStatefulSet),
		"webhook":         t.deploymentStatus(callCtx, t.cfg.TawonNamespace, tawonWebhookDeployment),
		"agent":           t.daemonSetStatus(callCtx, t.cfg.TawonNamespace, tawonAgentDaemonSet),
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

func (t *EoBHealth) daemonSetStatus(ctx context.Context, ns, name string) componentStatus {
	cs := componentStatus{Kind: "DaemonSet"}
	ds, err := t.kube.AppsV1().DaemonSets(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return absentOrError(cs, err)
	}
	cs.Desired = ds.Status.DesiredNumberScheduled
	cs.Ready = ds.Status.NumberReady
	cs.Status = readyStatus(cs.Ready, cs.Desired)
	return cs
}

// agentReadinessByNode lists agent pods in the Tawon namespace and groups
// them by spec.nodeName, reporting Ready/NotReady. Pods without a node
// (still pending scheduling) appear under the "<pending>" key. Returns
// an error placeholder map if the List call fails.
func (t *EoBHealth) agentReadinessByNode(ctx context.Context) any {
	selector := fmt.Sprintf("app=%s", tawonAgentDaemonSet)
	pods, err := t.kube.CoreV1().Pods(t.cfg.TawonNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		return map[string]string{"error": err.Error()}
	}
	byNode := make(map[string]string, len(pods.Items))
	for i := range pods.Items {
		p := &pods.Items[i]
		node := p.Spec.NodeName
		if node == "" {
			node = "<pending>"
		}
		byNode[node] = podReady(p)
	}
	return byNode
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

func podReady(p *corev1.Pod) string {
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
			return "Ready"
		}
	}
	return "NotReady"
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
