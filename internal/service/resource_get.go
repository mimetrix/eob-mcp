package service

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	eobv1 "github.com/mimetrix/eob-mcp/gen/go/eob/v1"
)

// ResourceGet fetches one resource by Kind/name(+namespace) and returns
// the full unstructured object inside a structpb.Struct.
func (s *Server) ResourceGet(ctx context.Context, req *eobv1.ResourceGetRequest) (*eobv1.ResourceGetResponse, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	gvr, namespaced, err := s.resolveKind(req.ApiGroup, req.Kind)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := s.withResourceTimeout(ctx)
	defer cancel()
	var obj *unstructured.Unstructured
	if namespaced {
		obj, err = s.dyn.Dyn.Resource(gvr).Namespace(req.Namespace).Get(callCtx, req.Name, metav1.GetOptions{})
	} else {
		obj, err = s.dyn.Dyn.Resource(gvr).Get(callCtx, req.Name, metav1.GetOptions{})
	}
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("%s %q not found", req.Kind, req.Name)
		}
		return nil, fmt.Errorf("get %s %q: %w", req.Kind, req.Name, err)
	}
	st, err := toStruct(obj.Object)
	if err != nil {
		return nil, fmt.Errorf("convert object: %w", err)
	}
	return &eobv1.ResourceGetResponse{
		Cluster: s.clusterRef(),
		Object:  st,
	}, nil
}
