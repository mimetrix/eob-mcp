// Package streams is the StreamReader abstraction over NATS JetStream.
//
// The concrete `*JSReader` wraps `nats.go`'s jetstream client. The
// `Reader` interface keeps the service layer decoupled from NATS: tests
// substitute an in-memory implementation; a future swap to a different
// stream backend (Kafka, Redpanda, an in-cluster replay service)
// touches only this package.
//
// Scope is deliberately narrow — list, stats, read. Production of
// streams (publishing) is not our concern; consumption only.
package streams

import (
	"context"
	"time"
)

// Reader is the minimum surface the data-plane RPCs need from a stream
// backend. Implementations must be safe for concurrent use.
type Reader interface {
	// List returns metadata for every stream the backend knows about.
	List(ctx context.Context) ([]StreamInfo, error)

	// Stats returns counts for one stream. If both since and until are
	// the zero Time, returns lifetime stats; implementations are free
	// to ignore the time window for now (we'll iterate later if it
	// becomes important).
	Stats(ctx context.Context, name string, since, until time.Time) (*StreamStats, error)

	// Read returns up to opts.Limit messages from one stream, in
	// sequence order. Implementations should respect opts.Since /
	// opts.Until when possible. Returned messages carry the raw
	// envelope bytes — no decoding here.
	Read(ctx context.Context, name string, opts ReadOpts) ([]*RawMessage, error)

	// Tail streams messages from one stream as they arrive. The returned
	// channel is closed when ctx is canceled or the consumer terminates;
	// callers detect end via channel close. Errors during setup are
	// returned synchronously; runtime errors are silent (the channel
	// just closes). Implementations should honor opts.StartAtSeq or
	// opts.StartAtTS when set (StartAtSeq wins if both are non-zero).
	Tail(ctx context.Context, name string, opts TailOpts) (<-chan *RawMessage, error)

	// Close releases the backend connection.
	Close() error
}

// StreamInfo is the per-stream summary returned by List.
type StreamInfo struct {
	Name     string
	Messages uint64
	Bytes    uint64
	FirstTS  time.Time // zero if stream is empty
	LastTS   time.Time // zero if stream is empty
}

// StreamStats is the per-stream snapshot returned by Stats. For now
// these are lifetime counts; time-window stats can be added later by
// iterating messages.
type StreamStats struct {
	Messages uint64
	Bytes    uint64
	Since    time.Time // echoed from request
	Until    time.Time // echoed from request
}

// ReadOpts narrows a Read call.
type ReadOpts struct {
	Since time.Time // zero means deliver from the start
	Until time.Time // zero means no upper bound
	Limit int       // 0 means implementation default
}

// TailOpts narrows a Tail call. At most one of StartAtSeq / StartAtTS
// should be set; StartAtSeq wins if both are. Both zero/empty means
// "deliver new messages only" (DeliverNewPolicy).
type TailOpts struct {
	StartAtSeq uint64
	StartAtTS  time.Time
	// BufSize sets the channel buffer for backpressure tuning.
	// 0 → implementation default (small but >1).
	BufSize int
}

// RawMessage is one envelope as published by Tawon to JetStream. Data
// is the raw payload bytes (a JSON envelope in Tawon's case); callers
// decode as they see fit.
type RawMessage struct {
	Subject   string
	Sequence  uint64
	Timestamp time.Time
	Data      []byte
}
