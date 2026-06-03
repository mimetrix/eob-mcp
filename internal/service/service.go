// Package service is the in-process implementation of EoBServiceServer.
// It is the single source of truth for tool behavior; both the MCP front
// door and the gRPC listener delegate here.
//
// Each RPC is implemented in its own file (cluster_identity.go,
// eob_health.go, resource_*.go) for readability. Unimplemented RPCs fall
// through to UnimplementedEoBServiceServer, so the project always builds
// even mid-migration.
package service

import (
	"time"

	"k8s.io/client-go/kubernetes"

	"github.com/mimetrix/eob-mcp/internal/audit"
	"github.com/mimetrix/eob-mcp/internal/config"
	"github.com/mimetrix/eob-mcp/internal/k8s"
	"github.com/mimetrix/eob-mcp/internal/streams"

	eobv1 "github.com/mimetrix/eob-mcp/gen/go/eob/v1"
)

// Server implements eobv1.EoBServiceServer. Construct via New; all
// dependencies are injected so the type is unit-testable without a live
// cluster or NATS endpoint (pass a fake.NewSimpleClientset for kube,
// nil for dyn, nil for streams).
type Server struct {
	eobv1.UnimplementedEoBServiceServer

	cfg       *config.Config
	kube      kubernetes.Interface
	dyn       *k8s.DynClient
	streams   streams.Reader
	audit     *audit.Broker
	startTime time.Time
}

// New constructs the service. Each dependency is independently
// optional; missing ones put their RPCs into a clean error path
// instead of panicking. Specifically:
//   - nil kube → identity/health degrade to empty/no-cluster shape
//   - nil dyn → resource_* RPCs return "no dynamic client" error
//   - nil streamsReader → Stream* RPCs return "no streams backend" error
func New(cfg *config.Config, kube kubernetes.Interface, dyn *k8s.DynClient, streamsReader streams.Reader) *Server {
	return &Server{
		cfg:       cfg,
		kube:      kube,
		dyn:       dyn,
		streams:   streamsReader,
		audit:     audit.New(),
		startTime: time.Now(),
	}
}

// Compile-time assertion that we satisfy the generated interface.
var _ eobv1.EoBServiceServer = (*Server)(nil)
