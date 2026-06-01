package service

import (
	"strings"
	"testing"

	eobv1 "github.com/mimetrix/eob-mcp/gen/go/eob/v1"
)

func TestResourceGet_RequiresName(t *testing.T) {
	t.Parallel()
	svc := newResourceFixture(t, kindsForDirective()).newServer()
	_, err := svc.ResourceGet(t.Context(), &eobv1.ResourceGetRequest{Kind: "ClusterDirective"})
	if err == nil {
		t.Fatal("expected error when name is empty")
	}
}

func TestResourceGet_NotFoundIsError(t *testing.T) {
	t.Parallel()
	svc := newResourceFixture(t, kindsForDirective()).newServer()
	_, err := svc.ResourceGet(t.Context(), &eobv1.ResourceGetRequest{
		Kind: "ClusterDirective", Name: "missing",
	})
	if err == nil {
		t.Fatal("expected error for missing directive")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error text: got %q, want substring \"not found\"", err.Error())
	}
}

func TestResourceGet_ReturnsObjectStruct(t *testing.T) {
	t.Parallel()
	fx := newResourceFixture(t, kindsForDirective(),
		directive("alpha", "", map[string]any{"enabled": true, "duration": "5m"}),
	)
	resp, err := fx.newServer().ResourceGet(t.Context(), &eobv1.ResourceGetRequest{
		Kind: "ClusterDirective", Name: "alpha",
	})
	if err != nil {
		t.Fatalf("ResourceGet: %v", err)
	}
	obj := resp.Object.AsMap()
	if obj["kind"] != "ClusterDirective" {
		t.Errorf("object.kind: got %v, want ClusterDirective", obj["kind"])
	}
	spec, ok := obj["spec"].(map[string]any)
	if !ok {
		t.Fatalf("spec is not a map: %T", obj["spec"])
	}
	if spec["enabled"] != true {
		t.Errorf("spec.enabled: got %v, want true", spec["enabled"])
	}
	if spec["duration"] != "5m" {
		t.Errorf("spec.duration: got %v, want \"5m\"", spec["duration"])
	}
}
