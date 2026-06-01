package k8s

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/restmapper"
)

// DynClient wraps the dynamic client plus a RESTMapper for kind→resource
// resolution. Used by tools that operate generically on any CRD kind
// (Tawon's ClusterDirective, Dashboard, Stream, etc.) without compiling
// in their typed clients.
type DynClient struct {
	Dyn    dynamic.Interface
	Mapper meta.RESTMapper
}

// NewDynClient builds a DynClient from an existing Client. Reuses the
// loaded REST config so we don't re-run the in-cluster vs kubeconfig
// resolution.
func NewDynClient(c *Client) (*DynClient, error) {
	if c == nil || c.Config == nil {
		return nil, fmt.Errorf("k8s: nil client or config")
	}
	dynIfc, err := dynamic.NewForConfig(c.Config)
	if err != nil {
		return nil, fmt.Errorf("k8s: dynamic client: %w", err)
	}
	dc, err := discovery.NewDiscoveryClientForConfig(c.Config)
	if err != nil {
		return nil, fmt.Errorf("k8s: discovery client: %w", err)
	}
	apiResources, err := restmapper.GetAPIGroupResources(dc)
	if err != nil {
		return nil, fmt.Errorf("k8s: api group resources: %w", err)
	}
	return &DynClient{
		Dyn:    dynIfc,
		Mapper: restmapper.NewDiscoveryRESTMapper(apiResources),
	}, nil
}

// ResolveGVR maps (group, kind) to a concrete GroupVersionResource and
// reports whether the resource is namespaced. Uses the discovery
// RESTMapper so callers don't have to know the version or pluralization.
func (d *DynClient) ResolveGVR(group, kind string) (gvr schema.GroupVersionResource, namespaced bool, err error) {
	gk := schema.GroupKind{Group: group, Kind: kind}
	mapping, err := d.Mapper.RESTMapping(gk)
	if err != nil {
		return schema.GroupVersionResource{}, false, fmt.Errorf("resolve %s/%s: %w", group, kind, err)
	}
	return mapping.Resource, mapping.Scope.Name() == meta.RESTScopeNameNamespace, nil
}
