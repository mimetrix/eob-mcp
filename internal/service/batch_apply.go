package service

import (
	"context"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	eobv1 "github.com/mimetrix/eob-mcp/gen/go/eob/v1"
)

// BatchApply applies multiple manifests in one RPC. Per-item independent
// — one failure does not abort the rest. Returns a result per item with
// kind/name when known, status, and an error string for failed items.
//
// Per-item dry_run / force OR with the batch defaults: a true per-item
// flag wins; a false per-item flag falls back to the batch value. (Bool
// proto fields cannot represent "unset," so we treat per-item booleans
// as monotonic overrides.)
func (s *Server) BatchApply(ctx context.Context, req *eobv1.BatchApplyRequest) (*eobv1.BatchApplyResponse, error) {
	resp := &eobv1.BatchApplyResponse{
		Cluster: s.clusterRef(),
		Items:   make([]*eobv1.BatchApplyResult, 0, len(req.GetItems())),
	}
	for _, item := range req.GetItems() {
		dryRun := req.GetDryRun() || item.GetDryRun()
		force := req.GetForce() || item.GetForce()

		applyResp, err := s.ResourceApply(ctx, &eobv1.ResourceApplyRequest{
			Manifest: item.GetManifest(),
			DryRun:   dryRun,
			Force:    force,
		})
		if err != nil {
			kind, ns, name := parseManifestHeader(item.GetManifest())
			resp.Items = append(resp.Items, &eobv1.BatchApplyResult{
				Kind:      kind,
				Namespace: ns,
				Name:      name,
				Status:    "error",
				Error:     err.Error(),
				DryRun:    dryRun,
			})
			resp.Errors++
			continue
		}
		resp.Items = append(resp.Items, &eobv1.BatchApplyResult{
			Kind:            applyResp.GetKind(),
			ApiGroup:        applyResp.GetApiGroup(),
			Namespace:       applyResp.GetNamespace(),
			Name:            applyResp.GetName(),
			Status:          "applied",
			Uid:             applyResp.GetUid(),
			ResourceVersion: applyResp.GetResourceVersion(),
			Generation:      applyResp.GetGeneration(),
			DryRun:          applyResp.GetDryRun(),
		})
		resp.Applied++
	}
	return resp, nil
}

// parseManifestHeader extracts (kind, namespace, name) from a manifest
// best-effort, so we can identify failed items in the response. Returns
// empty strings on any parse error — the per-item error string still
// communicates what went wrong.
func parseManifestHeader(manifest string) (kind, namespace, name string) {
	jsonBytes, err := yaml.YAMLToJSON([]byte(manifest))
	if err != nil {
		return "", "", ""
	}
	obj := &unstructured.Unstructured{}
	if err := obj.UnmarshalJSON(jsonBytes); err != nil {
		return "", "", ""
	}
	return obj.GetKind(), obj.GetNamespace(), obj.GetName()
}
