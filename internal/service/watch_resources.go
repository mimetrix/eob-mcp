package service

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/watch"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	eobv1 "github.com/mimetrix/eob-mcp/gen/go/eob/v1"
)

// WatchResources streams kubernetes-watch-shaped events for a Kind so
// the aggregator does not poll. Backed by the dynamic client's Watch
// (Tail-style; the apiserver is the source of truth and decides when
// events fire).
//
// The stream closes when the client cancels, the apiserver closes the
// watch (in which case the aggregator should re-open with the last
// resource_version), or the underlying watch errors.
func (s *Server) WatchResources(req *eobv1.WatchResourcesRequest, srv eobv1.EoBService_WatchResourcesServer) error {
	if s.dyn == nil {
		return status.Error(codes.FailedPrecondition, "no dynamic client (no kube wiring)")
	}
	if req.GetKind() == "" {
		return status.Error(codes.InvalidArgument, "kind is required")
	}
	group := req.GetApiGroup()
	if group == "" {
		group = s.cfg.CRDAPIGroup
	}
	gvr, namespaced, err := s.dyn.ResolveGVR(group, req.GetKind())
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "%v", err)
	}

	listOpts := metav1.ListOptions{
		LabelSelector:   req.GetLabelSelector(),
		ResourceVersion: req.GetResourceVersion(),
		Watch:           true,
		// AllowWatchBookmarks lets the apiserver push periodic
		// BOOKMARK events so consumers can advance resource_version
		// without seeing every object.
		AllowWatchBookmarks: true,
	}

	ctx := srv.Context()
	var w watch.Interface
	if namespaced && req.GetNamespace() != "" {
		w, err = s.dyn.Dyn.Resource(gvr).Namespace(req.GetNamespace()).Watch(ctx, listOpts)
	} else {
		w, err = s.dyn.Dyn.Resource(gvr).Watch(ctx, listOpts)
	}
	if err != nil {
		return status.Errorf(codes.Internal, "watch %s: %v", req.GetKind(), err)
	}
	defer w.Stop()

	clusterRef := s.clusterRef()
	for {
		select {
		case <-ctx.Done():
			return nil
		case evt, ok := <-w.ResultChan():
			if !ok {
				return nil
			}
			out := buildWatchEvent(clusterRef, req.GetKind(), group, evt)
			if out == nil {
				continue
			}
			if err := srv.Send(out); err != nil {
				return err
			}
		}
	}
}

// buildWatchEvent translates a k8s watch.Event into a WatchResourcesResponse.
// Returns nil for events whose object can't be expressed as a
// google.protobuf.Struct (defensive — shouldn't happen with normal
// unstructured objects).
func buildWatchEvent(cluster *eobv1.ClusterRef, kind, group string, evt watch.Event) *eobv1.WatchResourcesResponse {
	resp := &eobv1.WatchResourcesResponse{
		Cluster:   cluster,
		EventType: string(evt.Type),
		Kind:      kind,
		ApiGroup:  group,
	}
	u, ok := evt.Object.(*unstructured.Unstructured)
	if !ok || u == nil {
		// ERROR events carry a Status object instead of the watched
		// resource; surface just the type so the aggregator can act.
		return resp
	}
	resp.Namespace = u.GetNamespace()
	resp.Name = u.GetName()
	resp.ResourceVersion = u.GetResourceVersion()
	st, err := structpb.NewStruct(u.Object)
	if err == nil {
		resp.Object = st
	}
	return resp
}
