package service

// Shared test helpers for the resource_* RPC tests. Kept in a _test.go-only
// file so the helpers don't leak into the production build.

import (
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/mimetrix/eob-mcp/internal/config"
	"github.com/mimetrix/eob-mcp/internal/k8s"
)

const (
	testGroup   = "tawon.mantisnet.com"
	testVersion = "v1"
)

// resourceFixture wires a fake dynamic client + RESTMapper that knows
// about (Kind, namespaced) for a single group/version. Sufficient for
// the resource_* RPC tests, which all operate against the configured
// CRD group.
type resourceFixture struct {
	mapper *meta.DefaultRESTMapper
	dyn    *k8s.DynClient
	scheme *runtime.Scheme
}

// newResourceFixture returns a fixture pre-loaded with the given Kinds.
// Each entry maps Kind → (plural, namespaced). Objects passed via the
// objs varargs are seeded into the fake dynamic client.
func newResourceFixture(t *testing.T, kinds map[string]struct {
	Plural     string
	Namespaced bool
}, objs ...runtime.Object) *resourceFixture {
	t.Helper()

	gv := schema.GroupVersion{Group: testGroup, Version: testVersion}
	mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{gv})
	listKinds := map[schema.GroupVersionResource]string{}
	scheme := runtime.NewScheme()

	for kind, spec := range kinds {
		gvk := gv.WithKind(kind)
		gvr := gv.WithResource(spec.Plural)
		scope := meta.RESTScopeNamespace
		if !spec.Namespaced {
			scope = meta.RESTScopeRoot
		}
		mapper.AddSpecific(gvk, gvr, gv.WithResource(spec.Plural+"_singular"), scope)
		// Register the list kind so the fake dynamic client knows what
		// to return for List() calls on this resource.
		listKind := kind + "List"
		listKinds[gvr] = listKind
		scheme.AddKnownTypeWithName(gvk.GroupVersion().WithKind(listKind), &unstructured.UnstructuredList{})
		scheme.AddKnownTypeWithName(gvk, &unstructured.Unstructured{})
	}

	// CRD GVR is needed for resource_schema tests too. Register it.
	crdGV := schema.GroupVersion{Group: "apiextensions.k8s.io", Version: "v1"}
	mapper.AddSpecific(
		crdGV.WithKind("CustomResourceDefinition"),
		crdGV.WithResource("customresourcedefinitions"),
		crdGV.WithResource("customresourcedefinition"),
		meta.RESTScopeRoot,
	)
	crdGVR := crdGV.WithResource("customresourcedefinitions")
	listKinds[crdGVR] = "CustomResourceDefinitionList"
	scheme.AddKnownTypeWithName(crdGV.WithKind("CustomResourceDefinition"), &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(crdGV.WithKind("CustomResourceDefinitionList"), &unstructured.UnstructuredList{})

	fakeDyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds, objs...)

	return &resourceFixture{
		mapper: mapper,
		dyn:    &k8s.DynClient{Dyn: fakeDyn, Mapper: mapper},
		scheme: scheme,
	}
}

// newServerWithDyn returns a service with the fixture's dyn wired and
// default test config. Other backends (kube, streams) are nil.
func (f *resourceFixture) newServer() *Server {
	return New(&config.Config{
		SiteID:       "site-x",
		Tenant:       "tenant-y",
		Region:       "us",
		CRDAPIGroup:  testGroup,
		FieldManager: "eob-mcp-test",
	}, nil, f.dyn, nil)
}

// directive returns an unstructured ClusterDirective with the given
// name (and namespace, blank for cluster-scoped tests). Optional spec
// fields can be embedded as a map.
func directive(name, namespace string, spec map[string]any) *unstructured.Unstructured {
	if spec == nil {
		spec = map[string]any{}
	}
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": testGroup + "/" + testVersion,
			"kind":       "ClusterDirective",
			"metadata": map[string]any{
				"name": name,
			},
			"spec": spec,
		},
	}
	if namespace != "" {
		_ = unstructured.SetNestedField(obj.Object, namespace, "metadata", "namespace")
	}
	return obj
}

// kindsForDirective is the standard kinds map used by most tests:
// ClusterDirective is the only Kind, and it's cluster-scoped (matches
// real Tawon behavior — directives are cluster-scoped CRs).
func kindsForDirective() map[string]struct {
	Plural     string
	Namespaced bool
} {
	return map[string]struct {
		Plural     string
		Namespaced bool
	}{
		"ClusterDirective": {Plural: "clusterdirectives", Namespaced: false},
	}
}
