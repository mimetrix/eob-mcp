package main

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	eobv1 "github.com/mimetrix/eob-mcp/gen/go/eob/v1"
	"github.com/mimetrix/eob-mcp/internal/config"
	"github.com/mimetrix/eob-mcp/internal/service"
)

// TestGRPCListenerEndToEnd boots the gRPC server with a degraded-mode
// service (no kube wiring) and exercises one RPC over a real TCP
// connection. Proves the dual-front-door wiring: a caller speaking gRPC
// reaches the same in-process service the MCP wrappers use.
func TestGRPCListenerEndToEnd(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		SiteID:     "site-test",
		Tenant:     "tenant-test",
		Region:     "us-east-2",
		MCPVersion: "test",
	}
	svc := service.New(cfg, nil, nil, nil)
	grpcSrv := buildGRPCServer(svc, nil)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { grpcSrv.Stop() })

	go func() {
		_ = grpcSrv.Serve(lis)
	}()

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	client := eobv1.NewEoBServiceClient(conn)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	resp, err := client.ClusterIdentity(ctx, &eobv1.ClusterIdentityRequest{})
	if err != nil {
		t.Fatalf("ClusterIdentity rpc: %v", err)
	}
	if resp.GetCluster().GetSiteId() != "site-test" {
		t.Errorf("site_id: got %q, want %q", resp.GetCluster().GetSiteId(), "site-test")
	}
	if resp.GetCluster().GetTenant() != "tenant-test" {
		t.Errorf("tenant: got %q, want %q", resp.GetCluster().GetTenant(), "tenant-test")
	}
	if resp.GetMcpVersion() != "test" {
		t.Errorf("mcp_version: got %q, want %q", resp.GetMcpVersion(), "test")
	}
}
