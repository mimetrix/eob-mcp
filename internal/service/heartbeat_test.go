package service

import (
	"context"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes/fake"

	eobv1 "github.com/mimetrix/eob-mcp/gen/go/eob/v1"
)

func TestHeartbeat_NoBackends(t *testing.T) {
	srv := New(testHealthConfig(), nil, nil, nil)
	resp, err := srv.Heartbeat(context.Background(), &eobv1.HeartbeatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetKubeReachable() {
		t.Errorf("KubeReachable should be false when kube nil")
	}
	if resp.GetStreamsReachable() {
		t.Errorf("StreamsReachable should be false when streams nil")
	}
	if resp.GetDirectiveCount() != 0 {
		t.Errorf("DirectiveCount=%d, want 0", resp.GetDirectiveCount())
	}
	if resp.GetServerTime() == "" {
		t.Errorf("ServerTime should be populated")
	}
	if _, perr := time.Parse(time.RFC3339, resp.GetServerTime()); perr != nil {
		t.Errorf("ServerTime not RFC3339: %v", perr)
	}
	if resp.GetUptimeSeconds() < 0 {
		t.Errorf("UptimeSeconds=%d, want >=0", resp.GetUptimeSeconds())
	}
}

func TestHeartbeat_KubeWired(t *testing.T) {
	cs := fake.NewSimpleClientset(
		directiveDS("tawon-directive-foo", 3, 3),
		directiveDS("tawon-directive-bar", 3, 1),
	)
	srv := New(testHealthConfig(), cs, nil, nil)
	resp, err := srv.Heartbeat(context.Background(), &eobv1.HeartbeatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.GetKubeReachable() {
		t.Errorf("KubeReachable should be true with fake clientset")
	}
	if resp.GetDirectiveCount() != 2 {
		t.Errorf("DirectiveCount=%d, want 2", resp.GetDirectiveCount())
	}
}

func TestHeartbeat_ClusterRefMatchesConfig(t *testing.T) {
	cfg := testHealthConfig()
	srv := New(cfg, nil, nil, nil)
	resp, err := srv.Heartbeat(context.Background(), &eobv1.HeartbeatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	cr := resp.GetCluster()
	if cr.GetSiteId() != cfg.SiteID || cr.GetTenant() != cfg.Tenant || cr.GetRegion() != cfg.Region {
		t.Errorf("ClusterRef mismatch: got %+v, want site=%s tenant=%s region=%s",
			cr, cfg.SiteID, cfg.Tenant, cfg.Region)
	}
}
