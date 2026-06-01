package service

import (
	"testing"

	eobv1 "github.com/mimetrix/eob-mcp/gen/go/eob/v1"
)

func TestResourceDelete_RequiresName(t *testing.T) {
	t.Parallel()
	svc := newResourceFixture(t, kindsForDirective()).newServer()
	_, err := svc.ResourceDelete(t.Context(), &eobv1.ResourceDeleteRequest{Kind: "ClusterDirective"})
	if err == nil {
		t.Fatal("expected error when name is empty")
	}
}

func TestResourceDelete_MissingResourceReturnsNotFound(t *testing.T) {
	t.Parallel()
	svc := newResourceFixture(t, kindsForDirective()).newServer()
	resp, err := svc.ResourceDelete(t.Context(), &eobv1.ResourceDeleteRequest{
		Kind: "ClusterDirective", Name: "ghost",
	})
	if err != nil {
		t.Fatalf("ResourceDelete: %v", err)
	}
	if resp.Status != "notFound" {
		t.Errorf("status: got %q, want %q", resp.Status, "notFound")
	}
}

func TestResourceDelete_ExistingResourceReturnsDeleted(t *testing.T) {
	t.Parallel()
	fx := newResourceFixture(t, kindsForDirective(),
		directive("toremove", "", nil),
	)
	resp, err := fx.newServer().ResourceDelete(t.Context(), &eobv1.ResourceDeleteRequest{
		Kind: "ClusterDirective", Name: "toremove",
	})
	if err != nil {
		t.Fatalf("ResourceDelete: %v", err)
	}
	if resp.Status != "deleted" {
		t.Errorf("status: got %q, want %q", resp.Status, "deleted")
	}
}
