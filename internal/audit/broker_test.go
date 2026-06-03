package audit

import (
	"sync"
	"testing"
	"time"
)

func TestBroker_PublishReachesAllSubscribers(t *testing.T) {
	b := New()
	ch1, cancel1 := b.Subscribe(4)
	defer cancel1()
	ch2, cancel2 := b.Subscribe(4)
	defer cancel2()

	b.Publish(&Event{Reason: "X"})

	for i, ch := range []<-chan *Event{ch1, ch2} {
		select {
		case e := <-ch:
			if e.Reason != "X" {
				t.Errorf("sub %d: reason=%q, want X", i, e.Reason)
			}
		case <-time.After(time.Second):
			t.Errorf("sub %d: no event delivered", i)
		}
	}
}

func TestBroker_PublishStampsTimestampWhenZero(t *testing.T) {
	b := New()
	ch, cancel := b.Subscribe(1)
	defer cancel()
	b.Publish(&Event{Reason: "X"}) // Timestamp zero
	e := <-ch
	if e.Timestamp.IsZero() {
		t.Error("Publish should auto-stamp Timestamp when zero")
	}
	if time.Since(e.Timestamp) > time.Second {
		t.Errorf("Timestamp not recent: %v", e.Timestamp)
	}
}

func TestBroker_SlowSubscriberDoesNotBlockOthers(t *testing.T) {
	b := New()
	// sub1 has tiny buffer; sub2 ample.
	_, cancel1 := b.Subscribe(1) // intentionally don't drain
	defer cancel1()
	ch2, cancel2 := b.Subscribe(100)
	defer cancel2()

	// Publish enough events to overflow sub1's buffer but not sub2's.
	for i := 0; i < 50; i++ {
		b.Publish(&Event{Reason: "burst"})
	}

	// sub2 should have received all 50; sub1 dropped some, but Publish
	// must not have blocked (we'd hang otherwise).
	got := 0
	timeout := time.After(2 * time.Second)
	for got < 50 {
		select {
		case <-ch2:
			got++
		case <-timeout:
			t.Fatalf("sub2 got %d, want 50 (slow sub1 should not have blocked)", got)
		}
	}
}

func TestBroker_CancelClosesChannel(t *testing.T) {
	b := New()
	ch, cancel := b.Subscribe(1)
	cancel()
	// Channel should be closed; second receive returns ok=false.
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("expected channel closed after cancel")
		}
	case <-time.After(time.Second):
		t.Error("cancel did not close channel")
	}
}

func TestBroker_NilBrokerSafe(t *testing.T) {
	var b *Broker // nil
	// Should not panic.
	done := make(chan struct{})
	go func() {
		b.Publish(&Event{Reason: "x"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("nil-broker Publish hung")
	}
}

func TestBroker_ConcurrentPublishSubscribe(t *testing.T) {
	b := New()
	var wg sync.WaitGroup
	// 5 publishers, 5 subscribers, 100 events each.
	const N = 100
	subs := make([]<-chan *Event, 5)
	for i := range subs {
		ch, cancel := b.Subscribe(N * 5)
		subs[i] = ch
		defer cancel()
	}
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < N; j++ {
				b.Publish(&Event{Reason: "x"})
			}
		}()
	}
	wg.Wait()
	for i, ch := range subs {
		got := 0
		timeout := time.After(2 * time.Second)
	drain:
		for {
			select {
			case <-ch:
				got++
				if got == 5*N {
					break drain
				}
			case <-timeout:
				t.Errorf("sub %d: got %d, want %d", i, got, 5*N)
				break drain
			}
		}
	}
}
