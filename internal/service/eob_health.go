package service

import (
	"context"
	"sort"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	eobv1 "github.com/mimetrix/eob-mcp/gen/go/eob/v1"
)

// Tawon component names. Most match the chart's resource names; override
// via config if a downstream chart renames them.
const (
	tawonDashboardDeployment    = "tawon-dashboard"
	tawonStreamstoreStatefulSet = "tawon-streamstore"
)

// EoBHealth returns a structured health snapshot of the EoB stack.
//
// When the service has no kube client wiring, the response carries
// cluster_state="no-cluster" and empty components/directives — callers
// can detect degraded mode without needing the RPC to fail.
func (s *Server) EoBHealth(ctx context.Context, _ *eobv1.EoBHealthRequest) (*eobv1.EoBHealthResponse, error) {
	resp := &eobv1.EoBHealthResponse{
		Cluster: &eobv1.ClusterRef{
			SiteId: s.cfg.SiteID,
			Tenant: s.cfg.Tenant,
			Region: s.cfg.Region,
		},
	}
	if s.kube == nil {
		resp.ClusterState = "no-cluster"
		return resp, nil
	}
	resp.ClusterState = "connected"

	callCtx, cancel := context.WithTimeout(ctx, k8sCallTimeout)
	defer cancel()

	agent, directives := s.directiveStatus(callCtx)
	resp.Components = map[string]*eobv1.ComponentStatus{
		"operator":    s.deploymentStatus(callCtx, s.cfg.OperatorNamespace, s.cfg.OperatorDeploymentName),
		"dashboard":   s.deploymentStatus(callCtx, s.cfg.TawonNamespace, tawonDashboardDeployment),
		"streamstore": s.statefulSetStatus(callCtx, s.cfg.TawonNamespace, tawonStreamstoreStatefulSet),
		"webhook":     s.webhookConfigStatus(callCtx, s.cfg.WebhookConfigName),
		"agent":       agent,
	}
	resp.Directives = directives
	resp.AgentsPerNode = s.agentReadinessByNode(callCtx)
	return resp, nil
}

func (s *Server) deploymentStatus(ctx context.Context, ns, name string) *eobv1.ComponentStatus {
	cs := &eobv1.ComponentStatus{Kind: "Deployment"}
	dep, err := s.kube.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
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

func (s *Server) statefulSetStatus(ctx context.Context, ns, name string) *eobv1.ComponentStatus {
	cs := &eobv1.ComponentStatus{Kind: "StatefulSet"}
	ss, err := s.kube.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{})
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
// MutatingWebhookConfiguration installed by the EoB stack. Health is
// binary at the config-object level: present counts as "ok"; missing is
// "absent". Endpoint reachability is not probed here.
func (s *Server) webhookConfigStatus(ctx context.Context, name string) *eobv1.ComponentStatus {
	cs := &eobv1.ComponentStatus{Kind: "MutatingWebhookConfiguration"}
	mwc, err := s.kube.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(ctx, name, metav1.GetOptions{})
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
// namespace via the configured label selector and returns both an
// aggregate componentStatus (for components["agent"]) and a per-directive
// breakdown sorted by name for stable output.
func (s *Server) directiveStatus(ctx context.Context) (*eobv1.ComponentStatus, []*eobv1.DirectiveStatus) {
	cs := &eobv1.ComponentStatus{Kind: "DaemonSet"}
	list, err := s.kube.AppsV1().DaemonSets(s.cfg.TawonNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: s.cfg.DirectiveLabelSelector,
	})
	if err != nil {
		cs.Status = "error"
		cs.Detail = err.Error()
		return cs, nil
	}
	if len(list.Items) == 0 {
		cs.Status = "absent"
		return cs, []*eobv1.DirectiveStatus{}
	}
	perDirective := make([]*eobv1.DirectiveStatus, 0, len(list.Items))
	var totalReady, totalDesired int32
	for i := range list.Items {
		ds := &list.Items[i]
		perDirective = append(perDirective, &eobv1.DirectiveStatus{
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
// DaemonSets) and groups them by spec.nodeName, reporting ready vs total
// counts. Pods without an assigned node appear under "<pending>". On
// list failure returns nil; callers see an empty map in the response.
func (s *Server) agentReadinessByNode(ctx context.Context) map[string]*eobv1.NodeAgentSummary {
	pods, err := s.kube.CoreV1().Pods(s.cfg.TawonNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: s.cfg.DirectiveLabelSelector,
	})
	if err != nil {
		return nil
	}
	byNode := make(map[string]*eobv1.NodeAgentSummary)
	for i := range pods.Items {
		p := &pods.Items[i]
		node := p.Spec.NodeName
		if node == "" {
			node = "<pending>"
		}
		sm := byNode[node]
		if sm == nil {
			sm = &eobv1.NodeAgentSummary{}
			byNode[node] = sm
		}
		sm.Total++
		if isPodReady(p) {
			sm.Ready++
		}
	}
	return byNode
}

func absentOrError(cs *eobv1.ComponentStatus, err error) *eobv1.ComponentStatus {
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
