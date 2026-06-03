package service

import (
	"context"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	eobv1 "github.com/mimetrix/eob-mcp/gen/go/eob/v1"
	"github.com/mimetrix/eob-mcp/internal/audit"
)

// EventStream surfaces a unified event stream covering two sources:
//
//   1. k8s Events API (watch over corev1.Event)
//   2. eob-mcp's own audit events (resource_apply / resource_delete /
//      etc., published via the in-process audit.Broker)
//
// Both are merged into one outgoing gRPC stream so an aggregator sees
// "what happened in the cluster" (k8s reasons) and "what the fleet did
// to this site" (audit reasons) in one place.
func (s *Server) EventStream(req *eobv1.EventStreamRequest, srv eobv1.EoBService_EventStreamServer) error {
	ctx := srv.Context()
	clusterRef := s.clusterRef()
	source := strings.ToLower(req.GetSource())

	var k8sCh <-chan watch.Event
	if source == "" || source == "k8s" {
		ch, stop, err := s.startEventsWatch(ctx, req.GetNamespace())
		if err != nil {
			return status.Errorf(codes.Internal, "events watch: %v", err)
		}
		defer stop()
		k8sCh = ch
	}

	var auditCh <-chan *audit.Event
	var auditCancel func()
	if source == "" || source == "audit" {
		ac, cancel := s.audit.Subscribe(0)
		auditCh = ac
		auditCancel = cancel
		defer auditCancel()
	}

	kindFilter := req.GetKind()

	for {
		select {
		case <-ctx.Done():
			return nil

		case ev, ok := <-k8sCh:
			if !ok {
				k8sCh = nil // server-side close; continue with audit only
				continue
			}
			ke, ok := ev.Object.(*corev1.Event)
			if !ok || ke == nil {
				continue
			}
			if kindFilter != "" && ke.InvolvedObject.Kind != kindFilter {
				continue
			}
			if err := srv.Send(buildK8sEvent(clusterRef, ke)); err != nil {
				return err
			}

		case ae, ok := <-auditCh:
			if !ok {
				auditCh = nil
				continue
			}
			if kindFilter != "" && ae.InvolvedKind != kindFilter {
				continue
			}
			if err := srv.Send(buildAuditEvent(clusterRef, ae)); err != nil {
				return err
			}
		}
		// If both sources are gone, end the stream cleanly.
		if k8sCh == nil && auditCh == nil {
			return nil
		}
	}
}

// startEventsWatch opens a corev1.Event watch. namespace="" means
// cluster-wide. Returns a channel + a stop func the caller must invoke
// when finished.
func (s *Server) startEventsWatch(ctx context.Context, namespace string) (<-chan watch.Event, func(), error) {
	if s.kube == nil {
		// Audit-only mode: return a never-firing channel so the
		// caller's select still works.
		empty := make(chan watch.Event)
		return empty, func() {}, nil
	}
	w, err := s.kube.CoreV1().Events(namespace).Watch(ctx, metav1.ListOptions{
		AllowWatchBookmarks: true,
	})
	if err != nil {
		return nil, nil, err
	}
	return w.ResultChan(), w.Stop, nil
}

func buildK8sEvent(cluster *eobv1.ClusterRef, e *corev1.Event) *eobv1.EventStreamResponse {
	ts := e.LastTimestamp.Time
	if ts.IsZero() {
		ts = e.EventTime.Time
	}
	if ts.IsZero() {
		ts = e.CreationTimestamp.Time
	}
	return &eobv1.EventStreamResponse{
		Cluster:           cluster,
		Source:            "k8s",
		Type:              e.Type,
		Reason:            e.Reason,
		Message:           e.Message,
		Timestamp:         rfc3339OrEmpty(ts),
		InvolvedKind:      e.InvolvedObject.Kind,
		InvolvedName:      e.InvolvedObject.Name,
		InvolvedNamespace: e.InvolvedObject.Namespace,
	}
}

func buildAuditEvent(cluster *eobv1.ClusterRef, e *audit.Event) *eobv1.EventStreamResponse {
	return &eobv1.EventStreamResponse{
		Cluster:           cluster,
		Source:            "audit",
		Type:              "Audit",
		Reason:            e.Reason,
		Message:           e.Message,
		Timestamp:         rfc3339OrEmpty(e.Timestamp),
		InvolvedKind:      e.InvolvedKind,
		InvolvedName:      e.InvolvedName,
		InvolvedNamespace: e.InvolvedNamespace,
		Actor:             e.Actor,
	}
}
