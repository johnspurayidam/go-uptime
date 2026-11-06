package events

import (
	"testing"
	"time"
)

func TestPublishDeliversToSubscriber(t *testing.T) {
	t.Parallel()
	b := New()
	ch, cancel := b.Subscribe()
	defer cancel()

	b.Publish(Event{Type: CheckResult, Data: "ok"})

	select {
	case got := <-ch:
		if got.Type != CheckResult || got.Data != "ok" {
			t.Fatalf("event = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestPublishDoesNotBlockWhenBufferIsFull(t *testing.T) {
	t.Parallel()
	b := New()
	_, cancel := b.Subscribe()
	defer cancel()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 32; i++ {
			b.Publish(Event{Type: CheckResult, Data: i})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("publish blocked on a full subscriber")
	}
}

func TestUnsubscribeStopsDelivery(t *testing.T) {
	t.Parallel()
	b := New()
	keep, cancelKeep := b.Subscribe()
	defer cancelKeep()
	drop, cancelDrop := b.Subscribe()
	cancelDrop()

	b.Publish(Event{Type: IncidentUpdated, Data: 1})

	select {
	case got := <-keep:
		if got.Type != IncidentUpdated {
			t.Fatalf("event = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("active subscriber received nothing")
	}
	select {
	case got, ok := <-drop:
		if ok {
			t.Fatalf("unsubscribed channel received %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("unsubscribed channel was not closed")
	}
}

func TestCloseUnblocksSubscribers(t *testing.T) {
	t.Parallel()
	b := New()
	ch, cancel := b.Subscribe()
	defer cancel()
	b.Close()
	b.Close()

	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("closed broker delivered an event")
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber was not closed")
	}

	b.Publish(Event{Type: CheckResult})
}
