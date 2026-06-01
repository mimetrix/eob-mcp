package service

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	eobv1 "github.com/mimetrix/eob-mcp/gen/go/eob/v1"
)

// crd builds an unstructured apiextensions/v1 CRD object with the
// given group/kind and a list of versions. Each version is a map that
// must include "name" and may include "storage" (bool) and a "schema"
// block carrying "openAPIV3Schema". The versions slice is converted to
// []any inline because the dynamic fake client's deep-copy chokes on
// []map[string]any specifically.
func crd(name, group, kind string, versions []map[string]any) *unstructured.Unstructured {
	versionsAny := make([]any, len(versions))
	for i, v := range versions {
		versionsAny[i] = v
	}
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "apiextensions.k8s.io/v1",
			"kind":       "CustomResourceDefinition",
			"metadata":   map[string]any{"name": name},
			"spec": map[string]any{
				"group":    group,
				"names":    map[string]any{"kind": kind},
				"versions": versionsAny,
			},
		},
	}
}

func TestResourceSchema_RequiresKind(t *testing.T) {
	t.Parallel()
	svc := newResourceFixture(t, kindsForDirective()).newServer()
	_, err := svc.ResourceSchema(t.Context(), &eobv1.ResourceSchemaRequest{})
	if err == nil {
		t.Fatal("expected error when kind is empty")
	}
}

func TestResourceSchema_NotFound(t *testing.T) {
	t.Parallel()
	// Fixture with NO CRDs seeded.
	svc := newResourceFixture(t, kindsForDirective()).newServer()
	_, err := svc.ResourceSchema(t.Context(), &eobv1.ResourceSchemaRequest{
		Kind: "ClusterDirective",
	})
	if err == nil {
		t.Fatal("expected error when CRD is absent")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error: got %q, want substring \"not found\"", err.Error())
	}
}

func TestResourceSchema_ReturnsOpenAPIV3Schema(t *testing.T) {
	t.Parallel()
	openapi := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"spec": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"enabled": map[string]any{"type": "boolean"},
				},
			},
		},
	}
	fx := newResourceFixture(t, kindsForDirective(),
		crd("clusterdirectives.tawon.mantisnet.com", testGroup, "ClusterDirective",
			[]map[string]any{
				{
					"name":    "v1",
					"storage": true,
					"schema": map[string]any{
						"openAPIV3Schema": openapi,
					},
				},
			}),
	)
	resp, err := fx.newServer().ResourceSchema(t.Context(), &eobv1.ResourceSchemaRequest{
		Kind: "ClusterDirective",
	})
	if err != nil {
		t.Fatalf("ResourceSchema: %v", err)
	}
	if resp.StorageVersion != "v1" {
		t.Errorf("storage_version: got %q, want \"v1\"", resp.StorageVersion)
	}
	got := resp.OpenapiV3Schema.AsMap()
	if got["type"] != "object" {
		t.Errorf("schema.type: got %v, want \"object\"", got["type"])
	}
}

func TestResourceSchema_PrefersStorageVersion(t *testing.T) {
	t.Parallel()
	// Two versions, v1 (not storage) and v2 (storage). Service should
	// pick v2's schema.
	fx := newResourceFixture(t, kindsForDirective(),
		crd("clusterdirectives.tawon.mantisnet.com", testGroup, "ClusterDirective",
			[]map[string]any{
				{
					"name":    "v1",
					"storage": false,
					"schema": map[string]any{
						"openAPIV3Schema": map[string]any{"description": "old"},
					},
				},
				{
					"name":    "v2",
					"storage": true,
					"schema": map[string]any{
						"openAPIV3Schema": map[string]any{"description": "current"},
					},
				},
			}),
	)
	resp, err := fx.newServer().ResourceSchema(t.Context(), &eobv1.ResourceSchemaRequest{
		Kind: "ClusterDirective",
	})
	if err != nil {
		t.Fatalf("ResourceSchema: %v", err)
	}
	if resp.StorageVersion != "v2" {
		t.Errorf("storage_version: got %q, want \"v2\"", resp.StorageVersion)
	}
	if resp.OpenapiV3Schema.AsMap()["description"] != "current" {
		t.Errorf("schema selection: got %v, want \"current\"",
			resp.OpenapiV3Schema.AsMap()["description"])
	}
}
