package service

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	eobv1 "github.com/mimetrix/eob-mcp/gen/go/eob/v1"
)

// ResourceApply applies a YAML/JSON manifest via Server-Side Apply.
// dry_run=true runs a server-side validation pass without persisting;
// force takes ownership of conflicting fields from other managers.
//
// The manifest's own apiVersion drives group resolution. The configured
// CRD default group is only used when the manifest itself omits
// apiVersion entirely (uncommon).
func (s *Server) ResourceApply(ctx context.Context, req *eobv1.ResourceApplyRequest) (*eobv1.ResourceApplyResponse, error) {
	if req.Manifest == "" {
		return nil, fmt.Errorf("manifest is required")
	}
	// Convert YAML→JSON; sigs.k8s.io/yaml round-trips JSON safely too.
	jsonBytes, err := yaml.YAMLToJSON([]byte(req.Manifest))
	if err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	obj := &unstructured.Unstructured{}
	if err := obj.UnmarshalJSON(jsonBytes); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	gvk := obj.GroupVersionKind()
	if gvk.Kind == "" {
		return nil, fmt.Errorf("manifest missing kind")
	}
	name := obj.GetName()
	if name == "" {
		return nil, fmt.Errorf("manifest missing metadata.name")
	}
	group := gvk.Group
	if group == "" && obj.GetAPIVersion() == "" {
		group = s.cfg.CRDAPIGroup
	}
	if s.dyn == nil {
		return nil, fmt.Errorf("no dynamic client (no kube wiring)")
	}
	gvr, namespaced, err := s.dyn.ResolveGVR(group, gvk.Kind)
	if err != nil {
		return nil, err
	}
	applyOpts := metav1.ApplyOptions{
		FieldManager: s.cfg.FieldManager,
		Force:        req.Force,
	}
	if req.DryRun {
		applyOpts.DryRun = []string{metav1.DryRunAll}
	}
	callCtx, cancel := s.withResourceTimeout(ctx)
	defer cancel()
	var applied *unstructured.Unstructured
	if namespaced {
		ns := obj.GetNamespace()
		if ns == "" {
			ns = "default"
		}
		applied, err = s.dyn.Dyn.Resource(gvr).Namespace(ns).Apply(callCtx, name, obj, applyOpts)
	} else {
		applied, err = s.dyn.Dyn.Resource(gvr).Apply(callCtx, name, obj, applyOpts)
	}
	if err != nil {
		return nil, fmt.Errorf("apply %s %q: %w", gvk.Kind, name, err)
	}
	return &eobv1.ResourceApplyResponse{
		Cluster:         s.clusterRef(),
		Kind:            gvk.Kind,
		ApiGroup:        group,
		Namespace:       applied.GetNamespace(),
		Name:            applied.GetName(),
		Namespaced:      namespaced,
		DryRun:          req.DryRun,
		Uid:             string(applied.GetUID()),
		ResourceVersion: applied.GetResourceVersion(),
		Generation:      applied.GetGeneration(),
	}, nil
}
