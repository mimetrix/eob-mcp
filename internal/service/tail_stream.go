package service

import (
	"context"
	"encoding/json"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	eobv1 "github.com/mimetrix/eob-mcp/gen/go/eob/v1"
	"github.com/mimetrix/eob-mcp/internal/filter"
	"github.com/mimetrix/eob-mcp/internal/streams"
)

// TailStream is the live-tail companion to StreamRead. It opens an
// ordered ephemeral consumer (via streams.Reader.Tail) and pushes one
// envelope per gRPC stream Send as messages arrive. The stream closes
// when the client cancels or the underlying consumer terminates.
//
// jq filtering uses the same Compile/Match pattern as StreamRead;
// rejected messages are skipped silently (don't kill the stream).
func (s *Server) TailStream(req *eobv1.TailStreamRequest, srv eobv1.EoBService_TailStreamServer) error {
	if s.streams == nil {
		return status.Error(codes.FailedPrecondition, "streams backend not configured (set EOB_NATS_URL)")
	}
	if req.GetName() == "" {
		return status.Error(codes.InvalidArgument, "name is required")
	}

	ctx := srv.Context()

	var f *filter.Filter
	if req.GetFilter() != "" {
		compiled, err := filter.Compile(req.GetFilter())
		if err != nil {
			return status.Errorf(codes.InvalidArgument, "filter compile: %v", err)
		}
		f = compiled
	}

	opts := streams.TailOpts{StartAtSeq: req.GetStartAtSeq()}
	if ts := req.GetStartAtTs(); ts != "" {
		t, err := time.Parse(time.RFC3339, ts)
		if err != nil {
			return status.Errorf(codes.InvalidArgument, "parse start_at_ts: %v", err)
		}
		opts.StartAtTS = t
	}

	ch, err := s.streams.Tail(ctx, req.GetName(), opts)
	if err != nil {
		return status.Errorf(codes.Internal, "tail %q: %v", req.GetName(), err)
	}

	clusterRef := s.clusterRef()
	for {
		select {
		case <-ctx.Done():
			return nil
		case msg, ok := <-ch:
			if !ok {
				return nil
			}
			env := buildEnvelope(ctx, msg, f)
			if env == nil {
				continue // filter rejected or malformed
			}
			if err := srv.Send(&eobv1.TailStreamResponse{
				Cluster:  clusterRef,
				Envelope: env,
			}); err != nil {
				return err
			}
		}
	}
}

// buildEnvelope decodes one raw message into a RawEnvelope, optionally
// applying the jq filter. Returns nil when filter rejects or the
// envelope JSON is malformed — caller skips silently.
func buildEnvelope(ctx context.Context, m *streams.RawMessage, f *filter.Filter) *eobv1.RawEnvelope {
	var obj map[string]any
	if err := json.Unmarshal(m.Data, &obj); err != nil {
		return nil
	}
	if f != nil {
		match, err := f.Match(ctx, obj)
		if err != nil || !match {
			return nil
		}
	}
	st, err := structpb.NewStruct(obj)
	if err != nil {
		return nil
	}
	return &eobv1.RawEnvelope{
		Subject:   m.Subject,
		Sequence:  m.Sequence,
		Timestamp: rfc3339OrEmpty(m.Timestamp),
		Data:      st,
	}
}
