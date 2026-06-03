package streams

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// embeddedNATS spins up an in-process nats-server with JetStream
// enabled. Returns the client URL and a cleanup func. Used by the
// streams tests and (re-used by) the cmd-level end-to-end tests.
func embeddedNATS(t *testing.T) (string, func()) {
	t.Helper()
	opts := &server.Options{
		Host:      "127.0.0.1",
		Port:      -1, // any free port
		JetStream: true,
		StoreDir:  t.TempDir(),
		NoLog:     true,
		NoSigs:    true,
	}
	ns, err := server.NewServer(opts)
	if err != nil {
		t.Fatalf("nats-server new: %v", err)
	}
	go ns.Start()
	if !ns.ReadyForConnections(5 * time.Second) {
		ns.Shutdown()
		t.Fatal("nats-server not ready after 5s")
	}
	return ns.ClientURL(), ns.Shutdown
}

// seedStream creates a JetStream stream and publishes the given
// envelope JSON objects to it, one per Publish call. Subject == name.
func seedStream(t *testing.T, url, name string, envelopes []map[string]any) {
	t.Helper()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := js.CreateStream(ctx, jetstream.StreamConfig{
		Name:     name,
		Subjects: []string{name},
		Storage:  jetstream.FileStorage,
	}); err != nil {
		t.Fatalf("create stream: %v", err)
	}
	for i, env := range envelopes {
		body, err := json.Marshal(env)
		if err != nil {
			t.Fatalf("marshal envelope %d: %v", i, err)
		}
		if _, err := js.Publish(ctx, name, body); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}
}

func TestJSReader_ListReturnsCreatedStream(t *testing.T) {
	t.Parallel()
	url, stop := embeddedNATS(t)
	t.Cleanup(stop)

	seedStream(t, url, "test_capture", []map[string]any{
		{"timestamp": "2026-06-01T12:00:00Z", "data": []any{map[string]any{"type": "rawpacket"}}},
		{"timestamp": "2026-06-01T12:00:01Z", "data": []any{map[string]any{"type": "rawpacket"}}},
	})

	r, err := DialJetStream(url)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	infos, err := r.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("streams: got %d, want 1", len(infos))
	}
	if infos[0].Name != "test_capture" {
		t.Errorf("name: got %q, want %q", infos[0].Name, "test_capture")
	}
	if infos[0].Messages != 2 {
		t.Errorf("messages: got %d, want 2", infos[0].Messages)
	}
	if infos[0].Bytes == 0 {
		t.Error("bytes: got 0, want non-zero (envelopes were published)")
	}
}

func TestJSReader_StatsReportsCounts(t *testing.T) {
	t.Parallel()
	url, stop := embeddedNATS(t)
	t.Cleanup(stop)

	seedStream(t, url, "test_payload", []map[string]any{
		{"i": 1}, {"i": 2}, {"i": 3},
	})

	r, err := DialJetStream(url)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	stats, err := r.Stats(ctx, "test_payload", time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Messages != 3 {
		t.Errorf("messages: got %d, want 3", stats.Messages)
	}
}

func TestJSReader_ReadReturnsEnvelopes(t *testing.T) {
	t.Parallel()
	url, stop := embeddedNATS(t)
	t.Cleanup(stop)

	envelopes := make([]map[string]any, 5)
	for i := range envelopes {
		envelopes[i] = map[string]any{
			"i":         i,
			"timestamp": fmt.Sprintf("2026-06-01T12:00:%02dZ", i),
		}
	}
	seedStream(t, url, "test_read", envelopes)

	r, err := DialJetStream(url)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	msgs, err := r.Read(ctx, "test_read", ReadOpts{Limit: 3})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("messages: got %d, want 3", len(msgs))
	}
	// Sequence numbers should start at 1 (JetStream) and be in order.
	if msgs[0].Sequence != 1 || msgs[2].Sequence != 3 {
		t.Errorf("sequence: got [%d, %d, %d], want [1, 2, 3]",
			msgs[0].Sequence, msgs[1].Sequence, msgs[2].Sequence)
	}
	var first map[string]any
	if err := json.Unmarshal(msgs[0].Data, &first); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if first["i"].(float64) != 0 {
		t.Errorf("first.i: got %v, want 0", first["i"])
	}
}

func TestJSReader_TailDeliversFromStartSeq(t *testing.T) {
	t.Parallel()
	url, stop := embeddedNATS(t)
	t.Cleanup(stop)

	envelopes := make([]map[string]any, 5)
	for i := range envelopes {
		envelopes[i] = map[string]any{"i": i}
	}
	seedStream(t, url, "test_tail_seq", envelopes)

	r, err := DialJetStream(url)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ch, err := r.Tail(ctx, "test_tail_seq", TailOpts{StartAtSeq: 3, BufSize: 8})
	if err != nil {
		t.Fatalf("tail: %v", err)
	}

	// Expect messages 3, 4, 5 in sequence — then ctx times out.
	var got []uint64
	timeout := time.After(3 * time.Second)
	for len(got) < 3 {
		select {
		case msg, ok := <-ch:
			if !ok {
				t.Fatalf("channel closed early; got=%v", got)
			}
			got = append(got, msg.Sequence)
		case <-timeout:
			t.Fatalf("timeout waiting for tail; got=%v", got)
		}
	}
	if got[0] != 3 || got[1] != 4 || got[2] != 5 {
		t.Errorf("sequences: got %v, want [3 4 5]", got)
	}
}

func TestJSReader_TailDeliverNewSkipsHistory(t *testing.T) {
	t.Parallel()
	url, stop := embeddedNATS(t)
	t.Cleanup(stop)

	// Seed 3 messages BEFORE tail subscribes.
	seedStream(t, url, "test_tail_new",
		[]map[string]any{{"i": 0}, {"i": 1}, {"i": 2}})

	r, err := DialJetStream(url)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ch, err := r.Tail(ctx, "test_tail_new", TailOpts{BufSize: 4})
	if err != nil {
		t.Fatalf("tail: %v", err)
	}

	// No new messages were published after subscription; channel should
	// stay empty until either we publish new or ctx expires.
	select {
	case msg := <-ch:
		t.Fatalf("got unexpected message with deliver-new: seq=%d", msg.Sequence)
	case <-time.After(500 * time.Millisecond):
		// expected — no history delivered
	}

	// Publish one new message; tail should deliver it.
	seedStream(t, url, "test_tail_new", []map[string]any{{"i": 99}})

	select {
	case msg, ok := <-ch:
		if !ok {
			t.Fatal("channel closed before new message arrived")
		}
		if msg.Sequence != 4 {
			t.Errorf("sequence: got %d, want 4", msg.Sequence)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for new message")
	}
}

func TestJSReader_TailClosesOnCtxCancel(t *testing.T) {
	t.Parallel()
	url, stop := embeddedNATS(t)
	t.Cleanup(stop)

	seedStream(t, url, "test_tail_cancel", []map[string]any{{"i": 1}})

	r, err := DialJetStream(url)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	ctx, cancel := context.WithCancel(t.Context())
	ch, err := r.Tail(ctx, "test_tail_cancel", TailOpts{StartAtSeq: 1})
	if err != nil {
		t.Fatalf("tail: %v", err)
	}
	cancel()
	// Drain until close.
	timeout := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return // channel closed — success
			}
		case <-timeout:
			t.Fatal("channel did not close within 2s of cancel")
		}
	}
}
