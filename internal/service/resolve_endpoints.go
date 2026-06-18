package service

import (
	"context"
	"regexp"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	eobv1 "github.com/mimetrix/eob-mcp/gen/go/eob/v1"
)

// rsHashSuffix matches the trailing "-<hash>" a Deployment's ReplicaSet
// name carries, so we can report the Deployment name rather than the RS.
var rsHashSuffix = regexp.MustCompile(`-[0-9a-f]{6,10}$`)

// ResolveEndpoints turns raw IP[:port] tuples (from TRACE east-west /
// MCP downstream-reach) into named, identity-tagged Kubernetes workloads.
// Builds podIP / serviceIP / nodeIP indices once from a cluster snapshot,
// then resolves every requested endpoint. Pod match wins over node match
// (hostNetwork pods share the node IP but are more specific).
func (s *Server) ResolveEndpoints(ctx context.Context, req *eobv1.ResolveEndpointsRequest) (*eobv1.ResolveEndpointsResponse, error) {
	resp := &eobv1.ResolveEndpointsResponse{
		Cluster: &eobv1.ClusterRef{SiteId: s.cfg.SiteID, Tenant: s.cfg.Tenant, Region: s.cfg.Region},
	}
	if s.kube == nil {
		resp.ClusterState = "no-cluster"
		return resp, nil
	}
	resp.ClusterState = "connected"

	callCtx, cancel := context.WithTimeout(ctx, k8sCallTimeout)
	defer cancel()

	byPodIP := map[string]*eobv1.ResolvedEndpoint{}
	bySvcIP := map[string]*eobv1.ResolvedEndpoint{}
	byNodeIP := map[string]*eobv1.ResolvedEndpoint{}

	if pods, err := s.kube.CoreV1().Pods("").List(callCtx, metav1.ListOptions{}); err == nil {
		for i := range pods.Items {
			p := &pods.Items[i]
			wl, wlKind := workloadOf(p)
			r := &eobv1.ResolvedEndpoint{
				Kind: "pod", Name: p.Name, Namespace: p.Namespace,
				Workload: wl, WorkloadKind: wlKind, ServiceAccount: p.Spec.ServiceAccountName,
			}
			for _, pip := range p.Status.PodIPs {
				if pip.IP != "" {
					byPodIP[pip.IP] = r
				}
			}
			if p.Status.PodIP != "" {
				byPodIP[p.Status.PodIP] = r
			}
		}
	}
	if svcs, err := s.kube.CoreV1().Services("").List(callCtx, metav1.ListOptions{}); err == nil {
		for i := range svcs.Items {
			sv := &svcs.Items[i]
			if ip := sv.Spec.ClusterIP; ip != "" && ip != "None" {
				bySvcIP[ip] = &eobv1.ResolvedEndpoint{Kind: "service", Name: sv.Name, Namespace: sv.Namespace}
			}
		}
	}
	if nodes, err := s.kube.CoreV1().Nodes().List(callCtx, metav1.ListOptions{}); err == nil {
		for i := range nodes.Items {
			n := &nodes.Items[i]
			for _, a := range n.Status.Addresses {
				if a.Type == corev1.NodeInternalIP {
					byNodeIP[a.Address] = &eobv1.ResolvedEndpoint{Kind: "node", Name: n.Name}
				}
			}
		}
	}

	for _, ep := range req.GetEndpoints() {
		out := &eobv1.ResolvedEndpoint{Ip: ep.GetIp(), Port: ep.GetPort(), Kind: "external"}
		if r, ok := byPodIP[ep.GetIp()]; ok {
			out.Kind, out.Name, out.Namespace = "pod", r.Name, r.Namespace
			out.Workload, out.WorkloadKind, out.ServiceAccount = r.Workload, r.WorkloadKind, r.ServiceAccount
		} else if r, ok := bySvcIP[ep.GetIp()]; ok {
			out.Kind, out.Name, out.Namespace = "service", r.Name, r.Namespace
		} else if r, ok := byNodeIP[ep.GetIp()]; ok {
			out.Kind, out.Name = "node", r.Name
		}
		resp.Results = append(resp.Results, out)
	}
	return resp, nil
}

// workloadOf walks a pod's ownerReferences to the controlling workload,
// collapsing a ReplicaSet to its Deployment name. Returns ("","") for a
// bare pod.
func workloadOf(p *corev1.Pod) (name, kind string) {
	for _, o := range p.OwnerReferences {
		if o.Controller == nil || !*o.Controller {
			continue
		}
		if o.Kind == "ReplicaSet" {
			return rsHashSuffix.ReplaceAllString(o.Name, ""), "Deployment"
		}
		return o.Name, o.Kind
	}
	return "", ""
}
