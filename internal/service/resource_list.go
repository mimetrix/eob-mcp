package service

import (
	"context"
	"fmt"
	"sort"

	"google.golang.org/protobuf/types/known/structpb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	eobv1 "github.com/mimetrix/eob-mcp/gen/go/eob/v1"
)

// ResourceList lists resources of a given kind. Returns a slim summary
// (name, namespace, age, top-level status fields when present). Items
// are sorted by name for stable output.
func (s *Server) ResourceList(ctx context.Context, req *eobv1.ResourceListRequest) (*eobv1.ResourceListResponse, error) {
	gvr, namespaced, err := s.resolveKind(req.ApiGroup, req.Kind)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := s.withResourceTimeout(ctx)
	defer cancel()
	opts := metav1.ListOptions{LabelSelector: req.LabelSelector}
	var list *unstructured.UnstructuredList
	if namespaced {
		list, err = s.dyn.Dyn.Resource(gvr).Namespace(req.Namespace).List(callCtx, opts)
	} else {
		list, err = s.dyn.Dyn.Resource(gvr).List(callCtx, opts)
	}
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", gvr.Resource, err)
	}
	items := make([]*structpb.Struct, 0, len(list.Items))
	for i := range list.Items {
		st, err := toStruct(summarizeResource(&list.Items[i]))
		if err != nil {
			return nil, fmt.Errorf("convert item %d: %w", i, err)
		}
		items = append(items, st)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Fields["name"].GetStringValue() < items[j].Fields["name"].GetStringValue()
	})
	return &eobv1.ResourceListResponse{
		Cluster:    s.clusterRef(),
		Kind:       req.Kind,
		ApiGroup:   firstNonEmpty(req.ApiGroup, s.cfg.CRDAPIGroup),
		Namespaced: namespaced,
		Count:      int32(len(items)),
		Items:      items,
	}, nil
}
