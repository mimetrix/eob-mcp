package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"google.golang.org/protobuf/types/known/structpb"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	eobv1 "github.com/mimetrix/eob-mcp/gen/go/eob/v1"
)

// resourceTimeout bounds every dynamic-client call so a slow apiserver
// cannot stall request processing.
const resourceTimeout = 10 * time.Second

// withResourceTimeout wraps ctx with the package-level resourceTimeout.
func (s *Server) withResourceTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, resourceTimeout)
}

// clusterRef returns the per-response ClusterRef envelope built from
// the server's static config.
func (s *Server) clusterRef() *eobv1.ClusterRef {
	return &eobv1.ClusterRef{
		SiteId: s.cfg.SiteID,
		Tenant: s.cfg.Tenant,
		Region: s.cfg.Region,
	}
}

// resolveKind maps (group?, kind) to a GVR + namespaced flag. An empty
// group falls back to the configured CRD default. Returns a clear error
// if no dynamic client is wired (the resource_* RPCs can't operate
// without it).
func (s *Server) resolveKind(group, kind string) (schema.GroupVersionResource, bool, error) {
	if kind == "" {
		return schema.GroupVersionResource{}, false, fmt.Errorf("kind is required")
	}
	if s.dyn == nil {
		return schema.GroupVersionResource{}, false, fmt.Errorf("no dynamic client (no kube wiring)")
	}
	if group == "" {
		group = s.cfg.CRDAPIGroup
	}
	return s.dyn.ResolveGVR(group, kind)
}

// summarizeResource extracts a slim per-item view from an unstructured
// resource: name, namespace, creationTimestamp, plus top-level status
// (if present) and a few common spec keys.
func summarizeResource(u *unstructured.Unstructured) map[string]any {
	out := map[string]any{
		"name":              u.GetName(),
		"namespace":         u.GetNamespace(),
		"creationTimestamp": u.GetCreationTimestamp().Format("2006-01-02T15:04:05Z"),
	}
	if status, ok, _ := unstructured.NestedMap(u.Object, "status"); ok {
		out["status"] = status
	}
	for _, key := range []string{"source", "sink", "stream"} {
		if v, ok, _ := unstructured.NestedString(u.Object, "spec", key); ok {
			out[key] = v
		}
	}
	return out
}

// toStruct converts an arbitrary value (typically a map[string]any or
// an unstructured.Unstructured.Object) into a structpb.Struct.
//
// Round-trips through JSON so that exotic numeric types (int64 from
// k8s) are normalized to JSON numbers (float64) — structpb.NewStruct
// rejects values it doesn't recognize, and post-JSON the type space is
// already structpb-compatible.
func toStruct(v any) (*structpb.Struct, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encode for struct conversion: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("decode for struct conversion: %w", err)
	}
	st, err := structpb.NewStruct(m)
	if err != nil {
		return nil, fmt.Errorf("struct conversion: %w", err)
	}
	return st, nil
}

// firstNonEmpty returns a if non-empty, else b.
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
