package service

import (
	"context"
	"testing"

	"github.com/mimetrix/eob-mcp/internal/config"
	eobv1 "github.com/mimetrix/eob-mcp/gen/go/eob/v1"
)

func TestBatchApply_NoDynClient(t *testing.T) {
	// Without a dyn client, every item should fail with the same error
	// but the batch should still complete.
	srv := New(&config.Config{CRDAPIGroup: testGroup, FieldManager: "test"}, nil, nil, nil)
	resp, err := srv.BatchApply(context.Background(), &eobv1.BatchApplyRequest{
		Items: []*eobv1.BatchApplyItem{
			{Manifest: testManifest("foo")},
			{Manifest: testManifest("bar")},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetApplied() != 0 {
		t.Errorf("Applied=%d, want 0", resp.GetApplied())
	}
	if resp.GetErrors() != 2 {
		t.Errorf("Errors=%d, want 2", resp.GetErrors())
	}
	for i, item := range resp.GetItems() {
		if item.GetStatus() != "error" {
			t.Errorf("items[%d].Status=%q, want error", i, item.GetStatus())
		}
		if item.GetKind() != "ClusterDirective" {
			t.Errorf("items[%d].Kind=%q, want ClusterDirective (parsed from manifest)", i, item.GetKind())
		}
	}
}

func TestBatchApply_PerItemOverrideMonotonic(t *testing.T) {
	// Even when applies will fail (no dyn client), confirm that per-item
	// dry_run wins when set and OR-s with batch default.
	srv := New(&config.Config{CRDAPIGroup: testGroup, FieldManager: "test"}, nil, nil, nil)
	resp, err := srv.BatchApply(context.Background(), &eobv1.BatchApplyRequest{
		DryRun: false,
		Items: []*eobv1.BatchApplyItem{
			{Manifest: testManifest("a"), DryRun: true},
			{Manifest: testManifest("b")},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.GetItems()[0].GetDryRun() {
		t.Errorf("items[0] should report dry_run=true (per-item override)")
	}
	if resp.GetItems()[1].GetDryRun() {
		t.Errorf("items[1] should report dry_run=false (no override, batch=false)")
	}
}

func TestBatchApply_EmptyBatch(t *testing.T) {
	srv := New(&config.Config{}, nil, nil, nil)
	resp, err := srv.BatchApply(context.Background(), &eobv1.BatchApplyRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.GetItems()) != 0 {
		t.Errorf("Items len=%d, want 0", len(resp.GetItems()))
	}
	if resp.GetApplied() != 0 || resp.GetErrors() != 0 {
		t.Errorf("counters non-zero on empty batch: applied=%d errors=%d", resp.GetApplied(), resp.GetErrors())
	}
}

func TestParseManifestHeader(t *testing.T) {
	kind, ns, name := parseManifestHeader(testManifest("foo"))
	if kind != "ClusterDirective" || name != "foo" {
		t.Errorf("got kind=%q name=%q, want ClusterDirective/foo", kind, name)
	}
	if ns != "" {
		t.Errorf("got ns=%q, want empty (cluster-scoped)", ns)
	}
	if k, n, nm := parseManifestHeader("not yaml ::"); k != "" || n != "" || nm != "" {
		t.Errorf("garbage manifest should return all empty, got kind=%q ns=%q name=%q", k, n, nm)
	}
}

func testManifest(name string) string {
	return `apiVersion: tawon.mantisnet.com/v1
kind: ClusterDirective
metadata:
  name: ` + name + `
spec:
  duration: 5m0s
`
}
