package service

import (
	"context"
	"encoding/json"
	"fmt"

	"google.golang.org/protobuf/types/known/structpb"

	eobv1 "github.com/mimetrix/eob-mcp/gen/go/eob/v1"
	"github.com/mimetrix/eob-mcp/internal/filter"
	"github.com/mimetrix/eob-mcp/internal/streams"
)

// streamReadDefaultLimit is the per-call cap when the caller doesn't
// set limit. Chosen to be cheap (a few hundred KB at typical envelope
// sizes) while still useful for a "show me the latest" call.
const streamReadDefaultLimit = 100

// streamReadMaxLimit is the hard ceiling. A single RPC should never
// pull more than this; aggregation across many requests is the
// consumer's job, not ours.
const streamReadMaxLimit = 1000

// StreamRead returns raw Tawon JSON envelopes from one stream,
// optionally narrowed by a jq filter expression. Bodies are returned
// unchanged — no decoding, no aggregation.
func (s *Server) StreamRead(ctx context.Context, req *eobv1.StreamReadRequest) (*eobv1.StreamReadResponse, error) {
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
	limit := int(req.Limit)
	if limit <= 0 {
		limit = streamReadDefaultLimit
	}
	if limit > streamReadMaxLimit {
		limit = streamReadMaxLimit
	}

	var f *filter.Filter
	if req.Filter != "" {
		f, err = filter.Compile(req.Filter)
		if err != nil {
			return nil, err
		}
	}

	// Overshoot the backend fetch when a filter is set — many messages
	// may be rejected before we reach `limit` keepers. Cap so we don't
	// stream the world.
	backendLimit := limit
	if f != nil {
		backendLimit = limit * 4
		if backendLimit > streamReadMaxLimit {
			backendLimit = streamReadMaxLimit
		}
	}

	raw, err := s.streams.Read(ctx, req.Name, streams.ReadOpts{
		Since: since,
		Until: until,
		Limit: backendLimit,
	})
	if err != nil {
		return nil, err
	}

	out := make([]*eobv1.RawEnvelope, 0, limit)
	for _, m := range raw {
		var obj map[string]any
		if jerr := json.Unmarshal(m.Data, &obj); jerr != nil {
			continue // skip malformed; Tawon envelopes are JSON
		}
		if f != nil {
			match, ferr := f.Match(ctx, obj)
			if ferr != nil || !match {
				continue
			}
		}
		st, perr := structpb.NewStruct(obj)
		if perr != nil {
			continue
		}
		out = append(out, &eobv1.RawEnvelope{
			Subject:   m.Subject,
			Sequence:  m.Sequence,
			Timestamp: rfc3339OrEmpty(m.Timestamp),
			Data:      st,
		})
		if len(out) >= limit {
			break
		}
	}

	return &eobv1.StreamReadResponse{
		Cluster:  s.clusterRef(),
		Name:     req.Name,
		Count:    int32(len(out)),
		Messages: out,
	}, nil
}
