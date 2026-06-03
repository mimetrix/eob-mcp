// Package audit provides an in-memory pub-sub broker for eob-mcp's own
// audit events. Each mutating RPC (ResourceApply, ResourceDelete, etc.)
// publishes one Event on success or failure; EventStream subscribes
// and forwards the events to the federated client.
//
// Scope: in-process only. Cross-site audit aggregation is the
// aggregator's job (it subscribes to each site's EventStream and
// merges). No persistence here — audit events are ephemeral; consumers
// who need history use stream_read against the streams they care about.
package audit

import (
	"sync"
	"time"
)

// Event is one audit record. Field names mirror EventStreamResponse on
// the proto side; the service-layer wrapper handles translation.
type Event struct {
	Timestamp         time.Time
	Reason            string
	Message           string
	InvolvedKind      string
	InvolvedName      string
	InvolvedNamespace string
	Actor             string
}

// Broker is a fan-out for audit events. Safe for concurrent use.
type Broker struct {
	mu   sync.RWMutex
	subs map[*subscription]struct{}
}

type subscription struct {
	ch chan *Event
}

// New constructs an empty broker.
func New() *Broker {
	return &Broker{subs: make(map[*subscription]struct{})}
}

// Publish broadcasts an event to every current subscriber. Non-blocking:
// a slow subscriber whose channel is full drops the event for that
// subscriber only — every other subscriber still gets it.
func (b *Broker) Publish(e *Event) {
	if b == nil {
		return
	}
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for s := range b.subs {
		select {
		case s.ch <- e:
		default:
			// Subscriber too slow — drop. The alternative
			// (blocking) would let one bad consumer back-pressure
			// the apply RPC, which is worse.
		}
	}
}

// Subscribe returns a channel that receives every subsequent published
// event, plus a cancel function the caller must invoke when finished
// (typically on stream close). bufSize controls per-subscriber
// buffering; values <=0 use a sensible default.
func (b *Broker) Subscribe(bufSize int) (<-chan *Event, func()) {
	if bufSize <= 0 {
		bufSize = 64
	}
	s := &subscription{ch: make(chan *Event, bufSize)}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	cancel := func() {
		b.mu.Lock()
		if _, ok := b.subs[s]; ok {
			delete(b.subs, s)
			close(s.ch)
		}
		b.mu.Unlock()
	}
	return s.ch, cancel
}
