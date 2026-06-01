package service

import (
	"strings"
	"testing"

	eobv1 "github.com/mimetrix/eob-mcp/gen/go/eob/v1"
)

func TestResourceApply_RequiresManifest(t *testing.T) {
	t.Parallel()
	svc := newResourceFixture(t, kindsForDirective()).newServer()
	_, err := svc.ResourceApply(t.Context(), &eobv1.ResourceApplyRequest{})
	if err == nil {
		t.Fatal("expected error when manifest is empty")
	}
}

func TestResourceApply_ManifestMissingKind(t *testing.T) {
	t.Parallel()
	svc := newResourceFixture(t, kindsForDirective()).newServer()
	_, err := svc.ResourceApply(t.Context(), &eobv1.ResourceApplyRequest{
		Manifest: `{"apiVersion":"tawon.mantisnet.com/v1","metadata":{"name":"x"}}`,
	})
	if err == nil {
		t.Fatal("expected error when kind is absent from manifest")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "kind") {
		t.Errorf("error: got %q, want substring \"kind\"", err.Error())
	}
}

func TestResourceApply_ManifestMissingName(t *testing.T) {
	t.Parallel()
	svc := newResourceFixture(t, kindsForDirective()).newServer()
	_, err := svc.ResourceApply(t.Context(), &eobv1.ResourceApplyRequest{
		Manifest: `{"apiVersion":"tawon.mantisnet.com/v1","kind":"ClusterDirective"}`,
	})
	if err == nil {
		t.Fatal("expected error when metadata.name is absent")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("error: got %q, want substring \"name\"", err.Error())
	}
}

// TestResourceApply_NoDynReturnsError documents the no-backend path.
// We don't need the dynamic-fake Apply support to verify this, which
// is good because client-go's fake dynamic client has historically had
// gaps in Apply handling — the gating check happens before that path.
func TestResourceApply_NoDynReturnsError(t *testing.T) {
	t.Parallel()
	// Build a service with config (group set) but no dyn client.
	svc := newResourceFixture(t, kindsForDirective()).newServer()
	svc.dyn = nil

	_, err := svc.ResourceApply(t.Context(), &eobv1.ResourceApplyRequest{
		Manifest: `{
			"apiVersion": "tawon.mantisnet.com/v1",
			"kind": "ClusterDirective",
			"metadata": {"name": "x"}
		}`,
	})
	if err == nil {
		t.Fatal("expected error when no dyn client")
	}
}
