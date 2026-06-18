package service

import (
	"context"
	"fmt"
	"regexp"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	eobv1 "github.com/mimetrix/eob-mcp/gen/go/eob/v1"
)

// rsHashSuffix matches the trailing "-<hash>" a Deployment's ReplicaSet
// name carries, so we can report the Deployment name rather than the RS.
var rsHashSuffix = regexp.MustCompile(`-[0-9a-f]{6,10}$`)

// endpointIndex is a cluster snapshot for IP[:port] -> workload resolution.
// Built once; reused for every lookup (ResolveEndpoints, EastWestGraph).
type endpointIndex struct {
	byPodIP    map[string]*eobv1.ResolvedEndpoint // pod-network pods, keyed by podIP
	byHostPort map[string]*eobv1.ResolvedEndpoint // hostNetwork pods, keyed by "nodeIP:port"
	bySvcIP    map[string]*eobv1.ResolvedEndpoint
	byNodeIP   map[string]*eobv1.ResolvedEndpoint
}

// buildEndpointIndex snapshots pods/services/nodes into an endpointIndex.
func (s *Server) buildEndpointIndex(ctx context.Context) *endpointIndex {
	idx := &endpointIndex{
		byPodIP:    map[string]*eobv1.ResolvedEndpoint{},
		byHostPort: map[string]*eobv1.ResolvedEndpoint{},
		bySvcIP:    map[string]*eobv1.ResolvedEndpoint{},
		byNodeIP:   map[string]*eobv1.ResolvedEndpoint{},
	}
	if pods, err := s.kube.CoreV1().Pods("").List(ctx, metav1.ListOptions{}); err == nil {
		for i := range pods.Items {
			p := &pods.Items[i]
			wl, wlKind := workloadOf(p)
			r := &eobv1.ResolvedEndpoint{
				Kind: "pod", Name: p.Name, Namespace: p.Namespace,
				Workload: wl, WorkloadKind: wlKind, ServiceAccount: p.Spec.ServiceAccountName,
			}
			// hostNetwork pods share the node IP — disambiguate by declared port.
			if p.Spec.HostNetwork {
				for c := range p.Spec.Containers {
					for _, port := range p.Spec.Containers[c].Ports {
						if port.ContainerPort != 0 {
							idx.byHostPort[fmt.Sprintf("%s:%d", p.Status.HostIP, port.ContainerPort)] = r
						}
						if port.HostPort != 0 {
							idx.byHostPort[fmt.Sprintf("%s:%d", p.Status.HostIP, port.HostPort)] = r
						}
					}
				}
				continue
			}
			for _, pip := range p.Status.PodIPs {
				if pip.IP != "" {
					idx.byPodIP[pip.IP] = r
				}
			}
			if p.Status.PodIP != "" {
				idx.byPodIP[p.Status.PodIP] = r
			}
		}
	}
	if svcs, err := s.kube.CoreV1().Services("").List(ctx, metav1.ListOptions{}); err == nil {
		for i := range svcs.Items {
			sv := &svcs.Items[i]
			if ip := sv.Spec.ClusterIP; ip != "" && ip != "None" {
				idx.bySvcIP[ip] = &eobv1.ResolvedEndpoint{Kind: "service", Name: sv.Name, Namespace: sv.Namespace}
			}
		}
	}
	if nodes, err := s.kube.CoreV1().Nodes().List(ctx, metav1.ListOptions{}); err == nil {
		for i := range nodes.Items {
			n := &nodes.Items[i]
			for _, a := range n.Status.Addresses {
				if a.Type == corev1.NodeInternalIP {
					idx.byNodeIP[a.Address] = &eobv1.ResolvedEndpoint{Kind: "node", Name: n.Name}
				}
			}
		}
	}
	return idx
}

// resolve maps one IP[:port] to a workload. Order: pod-network pod ->
// Service ClusterIP -> hostNetwork pod (port-matched) -> bare node ->
// external. Always returns a non-nil result (kind="external" if unknown).
func (idx *endpointIndex) resolve(ip string, port int32) *eobv1.ResolvedEndpoint {
	out := &eobv1.ResolvedEndpoint{Ip: ip, Port: port, Kind: "external"}
	if r, ok := idx.byPodIP[ip]; ok {
		out.Kind, out.Name, out.Namespace = "pod", r.Name, r.Namespace
		out.Workload, out.WorkloadKind, out.ServiceAccount = r.Workload, r.WorkloadKind, r.ServiceAccount
	} else if r, ok := idx.bySvcIP[ip]; ok {
		out.Kind, out.Name, out.Namespace = "service", r.Name, r.Namespace
	} else if r, ok := idx.byHostPort[fmt.Sprintf("%s:%d", ip, port)]; ok {
		out.Kind, out.Name, out.Namespace = "pod", r.Name, r.Namespace
		out.Workload, out.WorkloadKind, out.ServiceAccount = r.Workload, r.WorkloadKind, r.ServiceAccount
	} else if r, ok := idx.byNodeIP[ip]; ok {
		out.Kind, out.Name = "node", r.Name
	}
	return out
}

// ResolveEndpoints turns raw IP[:port] tuples (from TRACE east-west /
// MCP downstream-reach) into named, identity-tagged Kubernetes workloads.
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
	idx := s.buildEndpointIndex(callCtx)

	for _, ep := range req.GetEndpoints() {
		resp.Results = append(resp.Results, idx.resolve(ep.GetIp(), ep.GetPort()))
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
