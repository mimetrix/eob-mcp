package service

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	eobv1 "github.com/mimetrix/eob-mcp/gen/go/eob/v1"
)

// ResourceDelete deletes a resource by Kind/name(+namespace). Idempotent:
// surfaces NotFound as status="notFound" rather than an error so retries
// don't have to special-case the already-gone case.
func (s *Server) ResourceDelete(ctx context.Context, req *eobv1.ResourceDeleteRequest) (*eobv1.ResourceDeleteResponse, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	gvr, namespaced, err := s.resolveKind(req.ApiGroup, req.Kind)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := s.withResourceTimeout(ctx)
	defer cancel()
	if namespaced {
		err = s.dyn.Dyn.Resource(gvr).Namespace(req.Namespace).Delete(callCtx, req.Name, metav1.DeleteOptions{})
	} else {
		err = s.dyn.Dyn.Resource(gvr).Delete(callCtx, req.Name, metav1.DeleteOptions{})
	}
	if err != nil {
		if apierrors.IsNotFound(err) {
			return &eobv1.ResourceDeleteResponse{
				Cluster: s.clusterRef(),
				Kind:    req.Kind,
				Name:    req.Name,
				Status:  "notFound",
			}, nil
		}
		return nil, fmt.Errorf("delete %s %q: %w", req.Kind, req.Name, err)
	}
	return &eobv1.ResourceDeleteResponse{
		Cluster: s.clusterRef(),
		Kind:    req.Kind,
		Name:    req.Name,
		Status:  "deleted",
	}, nil
}
