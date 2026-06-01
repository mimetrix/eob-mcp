package service

import (
	"testing"

	eobv1 "github.com/mimetrix/eob-mcp/gen/go/eob/v1"
	"github.com/mimetrix/eob-mcp/internal/config"
)

func TestResourceList_RequiresDynClient(t *testing.T) {
	t.Parallel()
	svc := New(&config.Config{CRDAPIGroup: testGroup}, nil, nil, nil)
	if _, err := svc.ResourceList(t.Context(), &eobv1.ResourceListRequest{Kind: "ClusterDirective"}); err == nil {
		t.Fatal("expected error when no dyn client")
	}
}

func TestResourceList_RequiresKind(t *testing.T) {
	t.Parallel()
	svc := newResourceFixture(t, kindsForDirective()).newServer()
	if _, err := svc.ResourceList(t.Context(), &eobv1.ResourceListRequest{}); err == nil {
		t.Fatal("expected error when kind is empty")
	}
}

func TestResourceList_EmptyClusterReturnsZero(t *testing.T) {
	t.Parallel()
	svc := newResourceFixture(t, kindsForDirective()).newServer()
	resp, err := svc.ResourceList(t.Context(), &eobv1.ResourceListRequest{Kind: "ClusterDirective"})
	if err != nil {
		t.Fatalf("ResourceList: %v", err)
	}
	if resp.Count != 0 {
		t.Errorf("count: got %d, want 0", resp.Count)
	}
	if resp.Namespaced {
		t.Error("ClusterDirective should report namespaced=false")
	}
}

func TestResourceList_ReturnsItemsSortedByName(t *testing.T) {
	t.Parallel()
	fx := newResourceFixture(t, kindsForDirective(),
		directive("z-last", "", nil),
		directive("a-first", "", nil),
		directive("m-mid", "", nil),
	)
	resp, err := fx.newServer().ResourceList(t.Context(), &eobv1.ResourceListRequest{Kind: "ClusterDirective"})
	if err != nil {
		t.Fatalf("ResourceList: %v", err)
	}
	if resp.Count != 3 {
		t.Fatalf("count: got %d, want 3", resp.Count)
	}
	wantOrder := []string{"a-first", "m-mid", "z-last"}
	for i, it := range resp.Items {
		got := it.GetFields()["name"].GetStringValue()
		if got != wantOrder[i] {
			t.Errorf("item[%d].name: got %q, want %q", i, got, wantOrder[i])
		}
	}
}

func TestResourceList_FallsBackToDefaultAPIGroup(t *testing.T) {
	t.Parallel()
	// Caller passes no api_group; service should substitute cfg.CRDAPIGroup
	// and still resolve. Verifies the default-group plumbing.
	fx := newResourceFixture(t, kindsForDirective(), directive("foo", "", nil))
	resp, err := fx.newServer().ResourceList(t.Context(), &eobv1.ResourceListRequest{Kind: "ClusterDirective"})
	if err != nil {
		t.Fatalf("ResourceList: %v", err)
	}
	if resp.ApiGroup != testGroup {
		t.Errorf("api_group: got %q, want %q", resp.ApiGroup, testGroup)
	}
}

func TestResourceList_UnknownKindIsError(t *testing.T) {
	t.Parallel()
	svc := newResourceFixture(t, kindsForDirective()).newServer()
	_, err := svc.ResourceList(t.Context(), &eobv1.ResourceListRequest{Kind: "Nonexistent"})
	if err == nil {
		t.Fatal("expected error for unknown kind")
	}
}
