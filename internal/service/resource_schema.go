package service

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	eobv1 "github.com/mimetrix/eob-mcp/gen/go/eob/v1"
)

// crdGVR is the cluster-scoped CRD list/get target. Hard-coded because
// it's part of the apiextensions API and never changes.
var crdGVR = schema.GroupVersionResource{
	Group:    "apiextensions.k8s.io",
	Version:  "v1",
	Resource: "customresourcedefinitions",
}

// ResourceSchema returns the OpenAPI v3 schema for a CRD. Uses the
// dynamic client to enumerate CRDs (so the apiextensions Go types stay
// out of go.mod) and matches by spec.group + spec.names.kind.
func (s *Server) ResourceSchema(ctx context.Context, req *eobv1.ResourceSchemaRequest) (*eobv1.ResourceSchemaResponse, error) {
	if req.Kind == "" {
		return nil, fmt.Errorf("kind is required")
	}
	if s.dyn == nil {
		return nil, fmt.Errorf("no dynamic client (no kube wiring)")
	}
	group := req.ApiGroup
	if group == "" {
		group = s.cfg.CRDAPIGroup
	}
	callCtx, cancel := s.withResourceTimeout(ctx)
	defer cancel()
	list, err := s.dyn.Dyn.Resource(crdGVR).List(callCtx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list CRDs: %w", err)
	}
	for i := range list.Items {
		crd := &list.Items[i]
		gotGroup, _, _ := unstructured.NestedString(crd.Object, "spec", "group")
		gotKind, _, _ := unstructured.NestedString(crd.Object, "spec", "names", "kind")
		if gotGroup != group || gotKind != req.Kind {
			continue
		}
		// Prefer the storage version's schema; fall back to first version.
		versions, _, _ := unstructured.NestedSlice(crd.Object, "spec", "versions")
		var pickSchema map[string]any
		var pickedVersion string
		for _, v := range versions {
			vm, ok := v.(map[string]any)
			if !ok {
				continue
			}
			isStorage, _ := vm["storage"].(bool)
			name, _ := vm["name"].(string)
			if sch, ok := vm["schema"].(map[string]any); ok {
				if openapi, ok := sch["openAPIV3Schema"].(map[string]any); ok {
					if pickSchema == nil || isStorage {
						pickSchema = openapi
						pickedVersion = name
					}
				}
			}
		}
		if pickSchema == nil {
			return nil, fmt.Errorf("CRD %s/%s has no openAPIV3Schema", group, req.Kind)
		}
		st, err := toStruct(pickSchema)
		if err != nil {
			return nil, fmt.Errorf("convert schema: %w", err)
		}
		return &eobv1.ResourceSchemaResponse{
			Cluster:         s.clusterRef(),
			Group:           group,
			Kind:            req.Kind,
			StorageVersion:  pickedVersion,
			OpenapiV3Schema: st,
		}, nil
	}
	return nil, fmt.Errorf("CRD for %s/%s not found", group, req.Kind)
}
