package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/nats-io/nats.go"

	eobv1 "github.com/mimetrix/eob-mcp/gen/go/eob/v1"
)

// traceEnvelope is the slice of the TRACE NATS envelope we need.
type traceEnvelope struct {
	K8sNamespace string `json:"k8s_namespace"`
	PodName      string `json:"pod_name"`
	Event        struct {
		Kind string `json:"kind"`
		Rec  struct {
			Comm string `json:"comm"`
		} `json:"rec"`
		Endpoint struct {
			DstAddr []int `json:"dst_addr"`
			DstPort int32 `json:"dst_port"`
		} `json:"endpoint"`
	} `json:"event"`
}

type ewEdge struct {
	srcNS, srcName, dstIP string
	dstPort               int32
	count                 int64
}

func dottedV4(a []int) string {
	if len(a) < 4 {
		return ""
	}
	q := a[len(a)-4:]
	return fmt.Sprintf("%d.%d.%d.%d", q[0], q[1], q[2], q[3])
}

// EastWestGraph samples the defense bus for connect events over a window,
// aggregates the service-call graph, and resolves every destination to a
// named, identity-tagged workload — tap + aggregate + resolve in one call.
func (s *Server) EastWestGraph(ctx context.Context, req *eobv1.EastWestGraphRequest) (*eobv1.EastWestGraphResponse, error) {
	resp := &eobv1.EastWestGraphResponse{
		Cluster: &eobv1.ClusterRef{SiteId: s.cfg.SiteID, Tenant: s.cfg.Tenant, Region: s.cfg.Region},
	}
	if s.cfg.DefenseNATSURL == "" {
		resp.ClusterState = "no-defense-bus"
		return resp, nil
	}
	window := time.Duration(req.GetWindowSeconds()) * time.Second
	if window <= 0 {
		window = 10 * time.Second
	}
	if window > 30*time.Second {
		window = 30 * time.Second
	}
	maxEdges := int(req.GetMaxEdges())
	if maxEdges <= 0 {
		maxEdges = 50
	}

	nc, err := nats.Connect(s.cfg.DefenseNATSURL,
		nats.Name("eob-mcp-ewg"), nats.Timeout(5*time.Second))
	if err != nil {
		resp.ClusterState = "no-defense-bus"
		return resp, fmt.Errorf("connect defense NATS: %w", err)
	}
	defer nc.Close()
	sub, err := nc.SubscribeSync("trace.events.connect")
	if err != nil {
		return resp, fmt.Errorf("subscribe: %w", err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	edges := map[string]*ewEdge{}
	var seen int64
	deadline := time.Now().Add(window)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		msg, err := sub.NextMsg(remaining)
		if err != nil {
			break // timeout / closed
		}
		var env traceEnvelope
		if json.Unmarshal(msg.Data, &env) != nil || env.Event.Kind == "" {
			continue
		}
		seen++
		dip := dottedV4(env.Event.Endpoint.DstAddr)
		if dip == "" || dip == "0.0.0.0" {
			continue
		}
		src := env.PodName
		if src == "" {
			src = env.Event.Rec.Comm
		}
		key := fmt.Sprintf("%s|%s|%s|%d", env.K8sNamespace, src, dip, env.Event.Endpoint.DstPort)
		e := edges[key]
		if e == nil {
			e = &ewEdge{srcNS: env.K8sNamespace, srcName: src, dstIP: dip, dstPort: env.Event.Endpoint.DstPort}
			edges[key] = e
		}
		e.count++
	}
	resp.EventsSeen = seen
	resp.WindowSeconds = int32(window / time.Second)

	// Resolve all destinations against one cluster snapshot.
	resp.ClusterState = "connected"
	var idx *endpointIndex
	if s.kube != nil {
		kctx, cancel := context.WithTimeout(ctx, k8sCallTimeout)
		idx = s.buildEndpointIndex(kctx)
		cancel()
	}
	all := make([]*ewEdge, 0, len(edges))
	for _, e := range edges {
		all = append(all, e)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].count > all[j].count })
	if len(all) > maxEdges {
		all = all[:maxEdges]
	}
	for _, e := range all {
		out := &eobv1.EastWestEdge{
			SrcNamespace: e.srcNS, SrcName: e.srcName,
			DstIp: e.dstIP, DstPort: e.dstPort, Count: e.count, DstKind: "external",
		}
		if idx != nil {
			r := idx.resolve(e.dstIP, e.dstPort)
			out.DstKind, out.DstName, out.DstNamespace = r.Kind, r.Name, r.Namespace
			out.DstWorkload, out.DstServiceAccount = r.Workload, r.ServiceAccount
		}
		resp.Edges = append(resp.Edges, out)
	}
	return resp, nil
}
