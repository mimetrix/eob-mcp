package service

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	eobv1 "github.com/mimetrix/eob-mcp/gen/go/eob/v1"
)

// Co-resident RACE/TRACE defense-stack component locations. Hardcoded
// like the Tawon component names in eob_health.go; presence-aware, so a
// site without the defense stack simply reports these "absent".
const (
	traceNamespace = "trace-system"
	traceAgentDS   = "trace-agent"   // TRACE host-observation DaemonSet
	raceNamespace  = "defense-system"
	raceAgentDS    = "defense-agent" // RACE perimeter-denial DaemonSet
)

// TraceHealth returns a health snapshot of the co-resident RACE/TRACE
// defense stack. This is a parallel stack to EoB (its own namespaces and
// NATS bus), so it gets its own rollup rather than being folded into
// EoBHealth. Same no-cluster / connected semantics.
func (s *Server) TraceHealth(ctx context.Context, _ *eobv1.TraceHealthRequest) (*eobv1.TraceHealthResponse, error) {
	resp := &eobv1.TraceHealthResponse{
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

	traceCS, traceNodes := s.daemonSetStatusAndNodes(callCtx, traceNamespace, traceAgentDS)
	raceCS, raceNodes := s.daemonSetStatusAndNodes(callCtx, raceNamespace, raceAgentDS)
	resp.Components = map[string]*eobv1.ComponentStatus{
		"trace_agent": traceCS,
		"race_agent":  raceCS,
	}
	resp.AgentsPerNode = mergeNodeSummaries(traceNodes, raceNodes)
	return resp, nil
}

// daemonSetStatusAndNodes reports a single DaemonSet's aggregate status
// plus its per-node ready/total breakdown (pods matched via the DS's own
// selector). Returns absent/error status with nil nodes on lookup failure.
func (s *Server) daemonSetStatusAndNodes(ctx context.Context, ns, name string) (*eobv1.ComponentStatus, map[string]*eobv1.NodeAgentSummary) {
	cs := &eobv1.ComponentStatus{Kind: "DaemonSet"}
	ds, err := s.kube.AppsV1().DaemonSets(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return absentOrError(cs, err), nil
	}
	cs.Desired = ds.Status.DesiredNumberScheduled
	cs.Ready = ds.Status.NumberReady
	cs.Status = readyStatus(cs.Ready, cs.Desired)

	nodes := make(map[string]*eobv1.NodeAgentSummary)
	sel, err := metav1.LabelSelectorAsSelector(ds.Spec.Selector)
	if err != nil {
		return cs, nodes
	}
	pods, err := s.kube.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: sel.String()})
	if err != nil {
		return cs, nodes
	}
	for i := range pods.Items {
		p := &pods.Items[i]
		node := p.Spec.NodeName
		if node == "" {
			node = "<pending>"
		}
		sm := nodes[node]
		if sm == nil {
			sm = &eobv1.NodeAgentSummary{}
			nodes[node] = sm
		}
		sm.Total++
		if isPodReady(p) {
			sm.Ready++
		}
	}
	return cs, nodes
}

// mergeNodeSummaries sums per-node ready/total across DaemonSets.
func mergeNodeSummaries(maps ...map[string]*eobv1.NodeAgentSummary) map[string]*eobv1.NodeAgentSummary {
	out := make(map[string]*eobv1.NodeAgentSummary)
	for _, m := range maps {
		for node, sm := range m {
			if sm == nil {
				continue
			}
			o := out[node]
			if o == nil {
				o = &eobv1.NodeAgentSummary{}
				out[node] = o
			}
			o.Ready += sm.Ready
			o.Total += sm.Total
		}
	}
	return out
}
