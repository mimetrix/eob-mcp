package streams

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// JSReader is a Reader backed by a NATS JetStream connection.
type JSReader struct {
	nc *nats.Conn
	js jetstream.JetStream
}

// DialJetStream opens a NATS connection and returns a JetStream-backed
// Reader. The connection is closed when Close is called.
func DialJetStream(url string) (*JSReader, error) {
	if url == "" {
		return nil, errors.New("streams: empty NATS URL")
	}
	nc, err := nats.Connect(url,
		nats.Name("eob-mcp"),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("streams: connect NATS: %w", err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("streams: jetstream context: %w", err)
	}
	return &JSReader{nc: nc, js: js}, nil
}

// Close drains and closes the underlying NATS connection.
func (r *JSReader) Close() error {
	if r.nc == nil {
		return nil
	}
	r.nc.Close()
	r.nc = nil
	return nil
}

// List enumerates every stream the JetStream server knows about.
func (r *JSReader) List(ctx context.Context) ([]StreamInfo, error) {
	out := []StreamInfo{}
	lister := r.js.ListStreams(ctx)
	for info := range lister.Info() {
		out = append(out, StreamInfo{
			Name:     info.Config.Name,
			Messages: info.State.Msgs,
			Bytes:    info.State.Bytes,
			FirstTS:  info.State.FirstTime,
			LastTS:   info.State.LastTime,
		})
	}
	if err := lister.Err(); err != nil {
		return nil, fmt.Errorf("streams: list: %w", err)
	}
	return out, nil
}

// Stats reports lifetime counts for one stream. The since/until window
// is echoed back unchanged; time-windowed iteration can be added if it
// turns out to be a frequent ask (today the cheap-and-coarse stream
// state is enough for "is anything flowing").
func (r *JSReader) Stats(ctx context.Context, name string, since, until time.Time) (*StreamStats, error) {
	stream, err := r.js.Stream(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("streams: stream %q: %w", name, err)
	}
	info, err := stream.Info(ctx)
	if err != nil {
		return nil, fmt.Errorf("streams: info %q: %w", name, err)
	}
	return &StreamStats{
		Messages: info.State.Msgs,
		Bytes:    info.State.Bytes,
		Since:    since,
		Until:    until,
	}, nil
}

// defaultReadLimit caps a Read call when the caller doesn't set Limit.
const defaultReadLimit = 100

// maxReadLimit is the safety ceiling — past this we return a partial
// result. Keeps a single Read call from pulling unbounded data.
const maxReadLimit = 10000

// Read pulls up to opts.Limit messages from the named stream using an
// ordered consumer. Since (when set) maps to DeliverByStartTimePolicy;
// Until is enforced client-side after Fetch.
func (r *JSReader) Read(ctx context.Context, name string, opts ReadOpts) ([]*RawMessage, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = defaultReadLimit
	}
	if limit > maxReadLimit {
		limit = maxReadLimit
	}

	stream, err := r.js.Stream(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("streams: stream %q: %w", name, err)
	}

	consumerCfg := jetstream.OrderedConsumerConfig{
		DeliverPolicy: jetstream.DeliverAllPolicy,
	}
	if !opts.Since.IsZero() {
		consumerCfg.DeliverPolicy = jetstream.DeliverByStartTimePolicy
		consumerCfg.OptStartTime = &opts.Since
	}
	cons, err := stream.OrderedConsumer(ctx, consumerCfg)
	if err != nil {
		return nil, fmt.Errorf("streams: ordered consumer %q: %w", name, err)
	}

	out := make([]*RawMessage, 0, limit)
	// Fetch in one batch; ordered consumer streams messages in sequence.
	batch, err := cons.Fetch(limit, jetstream.FetchMaxWait(2*time.Second))
	if err != nil {
		return nil, fmt.Errorf("streams: fetch %q: %w", name, err)
	}
	for msg := range batch.Messages() {
		meta, err := msg.Metadata()
		if err != nil {
			continue
		}
		if !opts.Until.IsZero() && meta.Timestamp.After(opts.Until) {
			break
		}
		out = append(out, &RawMessage{
			Subject:   msg.Subject(),
			Sequence:  meta.Sequence.Stream,
			Timestamp: meta.Timestamp,
			Data:      msg.Data(),
		})
	}
	if err := batch.Error(); err != nil {
		return out, fmt.Errorf("streams: batch %q: %w", name, err)
	}
	return out, nil
}

// Compile-time assertion.
var _ Reader = (*JSReader)(nil)
