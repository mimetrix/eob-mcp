package service

import (
	"context"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	eobv1 "github.com/mimetrix/eob-mcp/gen/go/eob/v1"
)

// heartbeatProbeTimeout is the per-call timeout for the cheap backend
// probes Heartbeat performs. Each Heartbeat may issue at most one kube
// discovery call and one streams List, so the worst-case latency is
// 2 × heartbeatProbeTimeout. Kept tight so the aggregator can poll
// every 30s without paying for slow sites.
const heartbeatProbeTimeout = 2 * time.Second

// Heartbeat is the cheap liveness + drift endpoint. By design it issues
// at most two outbound calls (kube discovery + streams List); everything
// else comes from already-cached config. Aggregator-side polling
// interval expectation: ~30s.
func (s *Server) Heartbeat(ctx context.Context, _ *eobv1.HeartbeatRequest) (*eobv1.HeartbeatResponse, error) {
	resp := &eobv1.HeartbeatResponse{
		Cluster:       s.clusterRef(),
		ServerTime:    time.Now().UTC().Format(time.RFC3339),
		UptimeSeconds: int64(time.Since(s.startTime).Seconds()),
		McpVersion:    s.cfg.MCPVersion,
	}

	resp.KubeReachable = s.probeKube(ctx)
	resp.DirectiveCount = s.countDirectives(ctx)
	resp.EobVersion = s.eobVersion(ctx)

	resp.StreamsReachable, resp.StreamCount = s.probeStreams(ctx)

	// ErrorCount24h: not yet wired. The plumbing requires hooking the
	// slog handler to count error-level records over a sliding window;
	// left at zero until that lands so the field is unambiguous about
	// being unimplemented vs. a true zero. See TODO.md.
	resp.ErrorCount_24H = 0

	return resp, nil
}

func (s *Server) probeKube(ctx context.Context) bool {
	if s.kube == nil {
		return false
	}
	callCtx, cancel := context.WithTimeout(ctx, heartbeatProbeTimeout)
	defer cancel()
	// Discovery().ServerVersion() is the same call ClusterIdentity makes;
	// reachable apiserver returns in <50ms in practice.
	_ = callCtx
	if _, err := s.kube.Discovery().ServerVersion(); err != nil {
		return false
	}
	return true
}

func (s *Server) countDirectives(ctx context.Context) int32 {
	if s.kube == nil {
		return 0
	}
	callCtx, cancel := context.WithTimeout(ctx, heartbeatProbeTimeout)
	defer cancel()
	list, err := s.kube.AppsV1().DaemonSets(s.cfg.TawonNamespace).List(callCtx, metav1.ListOptions{
		LabelSelector: s.cfg.DirectiveLabelSelector,
	})
	if err != nil {
		return 0
	}
	return int32(len(list.Items))
}

// probeStreams returns (reachable, count). Wired-but-unreachable is
// surfaced as (false, 0); not-configured is (false, 0) too — the
// caller can disambiguate via the eob_version / cluster_state when it
// matters.
func (s *Server) probeStreams(ctx context.Context) (bool, int32) {
	if s.streams == nil {
		return false, 0
	}
	callCtx, cancel := context.WithTimeout(ctx, heartbeatProbeTimeout)
	defer cancel()
	infos, err := s.streams.List(callCtx)
	if err != nil {
		return false, 0
	}
	return true, int32(len(infos))
}

