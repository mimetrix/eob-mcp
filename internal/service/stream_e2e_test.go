package service

import (
	"context"
	"testing"
	"time"

	"github.com/mimetrix/eob-mcp/internal/config"
	"github.com/mimetrix/eob-mcp/internal/streams"

	eobv1 "github.com/mimetrix/eob-mcp/gen/go/eob/v1"
)

// fakeStreams is an in-memory Reader for service-level tests. Keeps
// the service test free of nats-server boot cost; the streams package
// has its own NATS-backed coverage.
type fakeStreams struct {
	list   []streams.StreamInfo
	stats  map[string]*streams.StreamStats
	msgs   map[string][]*streams.RawMessage
	closed bool
}

func (f *fakeStreams) List(_ context.Context) ([]streams.StreamInfo, error) {
	return f.list, nil
}
func (f *fakeStreams) Stats(_ context.Context, name string, since, until time.Time) (*streams.StreamStats, error) {
	st := f.stats[name]
	if st == nil {
		return &streams.StreamStats{Since: since, Until: until}, nil
	}
	copy := *st
	copy.Since = since
	copy.Until = until
	return &copy, nil
}
func (f *fakeStreams) Read(_ context.Context, name string, opts streams.ReadOpts) ([]*streams.RawMessage, error) {
	all := f.msgs[name]
	limit := opts.Limit
	if limit <= 0 || limit > len(all) {
		limit = len(all)
	}
	return all[:limit], nil
}
func (f *fakeStreams) Tail(ctx context.Context, name string, _ streams.TailOpts) (<-chan *streams.RawMessage, error) {
	// Synchronously push the canned messages, then close on ctx cancel.
	// Sufficient for service-layer tests that don't need real live-tail
	// timing semantics — TailStream's runtime behavior is covered by
	// the NATS-backed tests in internal/streams.
	ch := make(chan *streams.RawMessage, len(f.msgs[name]))
	for _, m := range f.msgs[name] {
		ch <- m
	}
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return ch, nil
}
func (f *fakeStreams) Close() error { f.closed = true; return nil }

func newServerWithStreams(streams streams.Reader) *Server {
	return New(&config.Config{
		SiteID: "site-x", Tenant: "tenant-y", Region: "us",
		CRDAPIGroup: "tawon.mantisnet.com",
	}, nil, nil, streams)
}

func TestStreamList_PassesThroughBackend(t *testing.T) {
	t.Parallel()
	t1 := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 6, 1, 12, 5, 0, 0, time.UTC)
	svc := newServerWithStreams(&fakeStreams{
		list: []streams.StreamInfo{
			{Name: "capture_master_0", Messages: 100, Bytes: 4096, FirstTS: t1, LastTS: t2},
			{Name: "payload_master_0", Messages: 50, Bytes: 2048, FirstTS: t1, LastTS: t2},
		},
	})
	resp, err := svc.StreamList(t.Context(), &eobv1.StreamListRequest{})
	if err != nil {
		t.Fatalf("StreamList: %v", err)
	}
	if len(resp.Streams) != 2 {
		t.Fatalf("streams: got %d, want 2", len(resp.Streams))
	}
	if resp.Streams[0].Name != "capture_master_0" || resp.Streams[0].Messages != 100 {
		t.Errorf("stream[0]: got %+v", resp.Streams[0])
	}
	if resp.Cluster.GetSiteId() != "site-x" {
		t.Errorf("cluster envelope: site_id=%q, want %q", resp.Cluster.GetSiteId(), "site-x")
	}
}

func TestStreamList_NoBackendReturnsError(t *testing.T) {
	t.Parallel()
	svc := New(&config.Config{}, nil, nil, nil)
	if _, err := svc.StreamList(t.Context(), &eobv1.StreamListRequest{}); err == nil {
		t.Fatal("expected error when no streams backend configured")
	}
}

func TestStreamStats_RequiresName(t *testing.T) {
	t.Parallel()
	svc := newServerWithStreams(&fakeStreams{})
	if _, err := svc.StreamStats(t.Context(), &eobv1.StreamStatsRequest{}); err == nil {
		t.Fatal("expected error when name is empty")
	}
}

func TestStreamStats_RejectsBadTime(t *testing.T) {
	t.Parallel()
	svc := newServerWithStreams(&fakeStreams{})
	if _, err := svc.StreamStats(t.Context(), &eobv1.StreamStatsRequest{
		Name: "foo", Since: "not-a-time",
	}); err == nil {
		t.Fatal("expected parse error on bad since")
	}
}

func TestStreamRead_NoFilterReturnsAllUpToLimit(t *testing.T) {
	t.Parallel()
	msgs := []*streams.RawMessage{
		{Subject: "s", Sequence: 1, Timestamp: time.Now(),
			Data: []byte(`{"i":1,"data":[{"data":{"net":{"peerPort":80}}}]}`)},
		{Subject: "s", Sequence: 2, Timestamp: time.Now(),
			Data: []byte(`{"i":2,"data":[{"data":{"net":{"peerPort":53}}}]}`)},
		{Subject: "s", Sequence: 3, Timestamp: time.Now(),
			Data: []byte(`{"i":3,"data":[{"data":{"net":{"peerPort":443}}}]}`)},
	}
	svc := newServerWithStreams(&fakeStreams{msgs: map[string][]*streams.RawMessage{"s": msgs}})
	resp, err := svc.StreamRead(t.Context(), &eobv1.StreamReadRequest{Name: "s", Limit: 10})
	if err != nil {
		t.Fatalf("StreamRead: %v", err)
	}
	if resp.Count != 3 {
		t.Errorf("count: got %d, want 3", resp.Count)
	}
}

func TestStreamRead_FilterNarrowsResult(t *testing.T) {
	t.Parallel()
	msgs := []*streams.RawMessage{
		{Subject: "s", Sequence: 1, Timestamp: time.Now(),
			Data: []byte(`{"i":1,"data":[{"data":{"net":{"peerPort":80}}}]}`)},
		{Subject: "s", Sequence: 2, Timestamp: time.Now(),
			Data: []byte(`{"i":2,"data":[{"data":{"net":{"peerPort":53}}}]}`)},
		{Subject: "s", Sequence: 3, Timestamp: time.Now(),
			Data: []byte(`{"i":3,"data":[{"data":{"net":{"peerPort":443}}}]}`)},
		{Subject: "s", Sequence: 4, Timestamp: time.Now(),
			Data: []byte(`{"i":4,"data":[{"data":{"net":{"peerPort":53}}}]}`)},
	}
	svc := newServerWithStreams(&fakeStreams{msgs: map[string][]*streams.RawMessage{"s": msgs}})
	resp, err := svc.StreamRead(t.Context(), &eobv1.StreamReadRequest{
		Name:   "s",
		Limit:  10,
		Filter: `.data[].data.net.peerPort == 53`,
	})
	if err != nil {
		t.Fatalf("StreamRead: %v", err)
	}
	if resp.Count != 2 {
		t.Errorf("count: got %d, want 2 (two DNS records)", resp.Count)
	}
	for _, env := range resp.Messages {
		// Each surviving envelope must have peerPort == 53. Drill into
		// the structpb to confirm.
		data := env.Data.AsMap()
		dataArr := data["data"].([]any)
		inner := dataArr[0].(map[string]any)
		net := inner["data"].(map[string]any)["net"].(map[string]any)
		if int(net["peerPort"].(float64)) != 53 {
			t.Errorf("post-filter envelope: peerPort=%v, want 53", net["peerPort"])
		}
	}
}

func TestStreamRead_InvalidFilterIsError(t *testing.T) {
	t.Parallel()
	svc := newServerWithStreams(&fakeStreams{})
	if _, err := svc.StreamRead(t.Context(), &eobv1.StreamReadRequest{
		Name: "s", Filter: ".foo |",
	}); err == nil {
		t.Fatal("expected jq parse error to surface")
	}
}

func TestStreamRead_MalformedEnvelopeSkipped(t *testing.T) {
	t.Parallel()
	msgs := []*streams.RawMessage{
		{Subject: "s", Sequence: 1, Timestamp: time.Now(), Data: []byte(`not json`)},
		{Subject: "s", Sequence: 2, Timestamp: time.Now(), Data: []byte(`{"ok":true}`)},
	}
	svc := newServerWithStreams(&fakeStreams{msgs: map[string][]*streams.RawMessage{"s": msgs}})
	resp, err := svc.StreamRead(t.Context(), &eobv1.StreamReadRequest{Name: "s", Limit: 10})
	if err != nil {
		t.Fatalf("StreamRead: %v", err)
	}
	if resp.Count != 1 {
		t.Errorf("count: got %d, want 1 (one malformed envelope must be skipped)", resp.Count)
	}
}
