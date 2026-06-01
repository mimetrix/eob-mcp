package service

import (
	"context"
	"fmt"
	"time"

	eobv1 "github.com/mimetrix/eob-mcp/gen/go/eob/v1"
)

// StreamStats returns count + byte totals for one stream. The backend
// is free to report lifetime totals and echo the requested window
// unchanged — time-windowed iteration can be added later if it turns
// out to be a frequent ask.
func (s *Server) StreamStats(ctx context.Context, req *eobv1.StreamStatsRequest) (*eobv1.StreamStatsResponse, error) {
	if s.streams == nil {
		return nil, fmt.Errorf("streams backend not configured (set EOB_NATS_URL)")
	}
	if req.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	since, until, err := parseTimeWindow(req.Since, req.Until)
	if err != nil {
		return nil, err
	}
	stats, err := s.streams.Stats(ctx, req.Name, since, until)
	if err != nil {
		return nil, err
	}
	return &eobv1.StreamStatsResponse{
		Cluster:  s.clusterRef(),
		Name:     req.Name,
		Messages: stats.Messages,
		Bytes:    stats.Bytes,
		Since:    rfc3339OrEmpty(stats.Since),
		Until:    rfc3339OrEmpty(stats.Until),
	}, nil
}

// parseTimeWindow accepts two optional RFC3339 strings and returns
// the parsed bounds (zero Time for an empty input). An invalid value
// is an error.
func parseTimeWindow(since, until string) (time.Time, time.Time, error) {
	var s, u time.Time
	if since != "" {
		t, err := time.Parse(time.RFC3339, since)
		if err != nil {
			return s, u, fmt.Errorf("parse since: %w", err)
		}
		s = t
	}
	if until != "" {
		t, err := time.Parse(time.RFC3339, until)
		if err != nil {
			return s, u, fmt.Errorf("parse until: %w", err)
		}
		u = t
	}
	return s, u, nil
}
