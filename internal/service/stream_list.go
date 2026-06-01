package service

import (
	"context"
	"fmt"
	"time"

	eobv1 "github.com/mimetrix/eob-mcp/gen/go/eob/v1"
)

// StreamList returns metadata for every NATS JetStream stream this site
// can see. No decoding, no filtering — just the catalog. Consumers use
// the result to pick which stream to read.
func (s *Server) StreamList(ctx context.Context, _ *eobv1.StreamListRequest) (*eobv1.StreamListResponse, error) {
	if s.streams == nil {
		return nil, fmt.Errorf("streams backend not configured (set EOB_NATS_URL)")
	}
	infos, err := s.streams.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*eobv1.StreamInfo, len(infos))
	for i, info := range infos {
		out[i] = &eobv1.StreamInfo{
			Name:     info.Name,
			Messages: info.Messages,
			Bytes:    info.Bytes,
			FirstTs:  rfc3339OrEmpty(info.FirstTS),
			LastTs:   rfc3339OrEmpty(info.LastTS),
		}
	}
	return &eobv1.StreamListResponse{
		Cluster: s.clusterRef(),
		Streams: out,
	}, nil
}

// rfc3339OrEmpty formats a time, returning "" for the zero value so
// the wire shape is unambiguous about "no data here yet".
func rfc3339OrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}
