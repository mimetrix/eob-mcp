package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"google.golang.org/protobuf/proto"

	eobv1 "github.com/mimetrix/eob-mcp/gen/go/eob/v1"
	"github.com/mimetrix/eob-mcp/internal/mcp"
	"github.com/mimetrix/eob-mcp/internal/service"
)

// emitProto is the shared MCP-body builder. Same pattern as the other
// migrated tools, factored once to keep stream.go small.
func emitProto(m proto.Message) (mcp.CallToolResult, error) {
	body, err := protoMarshal.Marshal(m)
	if err != nil {
		return mcp.CallToolResult{}, err
	}
	return mcp.CallToolResult{
		Content: []mcp.Content{{Type: "text", Text: string(body)}},
	}, nil
}

// ---------------- stream_list ----------------

type StreamList struct{ svc *service.Server }

func NewStreamList(svc *service.Server) *StreamList { return &StreamList{svc: svc} }

func (t *StreamList) Name() string { return "stream_list" }
func (t *StreamList) Description() string {
	return "List the NATS JetStream streams this EoB site exposes. Returns name, message count, byte count, and first/last timestamp per stream. No arguments. Pair with stream_read to inspect contents."
}
func (t *StreamList) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
}
func (t *StreamList) Call(ctx context.Context, _ json.RawMessage) (mcp.CallToolResult, error) {
	resp, err := t.svc.StreamList(ctx, &eobv1.StreamListRequest{})
	if err != nil {
		return mcp.CallToolResult{}, err
	}
	return emitProto(resp)
}

var _ mcp.ToolHandler = (*StreamList)(nil)

// ---------------- stream_stats ----------------

type StreamStats struct{ svc *service.Server }

func NewStreamStats(svc *service.Server) *StreamStats { return &StreamStats{svc: svc} }

func (t *StreamStats) Name() string { return "stream_stats" }
func (t *StreamStats) Description() string {
	return "Return message + byte counts for one NATS JetStream stream. Cheap; no message bodies returned. Optional 'since' and 'until' (RFC3339) — the backend may report lifetime counts and echo the window unchanged."
}
func (t *StreamStats) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","description":"Stream name (see stream_list)"},"since":{"type":"string","description":"Optional RFC3339 lower bound"},"until":{"type":"string","description":"Optional RFC3339 upper bound"}},"required":["name"],"additionalProperties":false}`)
}

type streamStatsArgs struct {
	Name  string `json:"name"`
	Since string `json:"since"`
	Until string `json:"until"`
}

func (t *StreamStats) Call(ctx context.Context, raw json.RawMessage) (mcp.CallToolResult, error) {
	var a streamStatsArgs
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &a); err != nil {
			return mcp.CallToolResult{}, fmt.Errorf("parse args: %w", err)
		}
	}
	resp, err := t.svc.StreamStats(ctx, &eobv1.StreamStatsRequest{
		Name:  a.Name,
		Since: a.Since,
		Until: a.Until,
	})
	if err != nil {
		return mcp.CallToolResult{}, err
	}
	return emitProto(resp)
}

var _ mcp.ToolHandler = (*StreamStats)(nil)

// ---------------- stream_read ----------------

type StreamRead struct{ svc *service.Server }

func NewStreamRead(svc *service.Server) *StreamRead { return &StreamRead{svc: svc} }

func (t *StreamRead) Name() string { return "stream_read" }
func (t *StreamRead) Description() string {
	return "Read raw Tawon JSON envelopes from one NATS JetStream stream. Optional time window (since/until RFC3339), limit (default 100, max 1000), and jq filter expression evaluated server-side against each envelope. Bodies are returned unchanged — base64 'payload' bytes stay base64; decode externally with tshark or equivalent."
}
func (t *StreamRead) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","description":"Stream name (see stream_list)"},"since":{"type":"string","description":"Optional RFC3339 lower bound"},"until":{"type":"string","description":"Optional RFC3339 upper bound"},"limit":{"type":"integer","description":"Max messages to return (default 100, capped at 1000)"},"filter":{"type":"string","description":"jq expression; only envelopes for which it yields truthy are returned"}},"required":["name"],"additionalProperties":false}`)
}

type streamReadArgs struct {
	Name   string `json:"name"`
	Since  string `json:"since"`
	Until  string `json:"until"`
	Limit  int32  `json:"limit"`
	Filter string `json:"filter"`
}

func (t *StreamRead) Call(ctx context.Context, raw json.RawMessage) (mcp.CallToolResult, error) {
	var a streamReadArgs
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &a); err != nil {
			return mcp.CallToolResult{}, fmt.Errorf("parse args: %w", err)
		}
	}
	resp, err := t.svc.StreamRead(ctx, &eobv1.StreamReadRequest{
		Name:   a.Name,
		Since:  a.Since,
		Until:  a.Until,
		Limit:  a.Limit,
		Filter: a.Filter,
	})
	if err != nil {
		return mcp.CallToolResult{}, err
	}
	return emitProto(resp)
}

var _ mcp.ToolHandler = (*StreamRead)(nil)
