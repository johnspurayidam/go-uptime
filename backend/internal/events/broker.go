// Package events is an in-memory pub/sub for server-sent events.
// The checker and the API publish. Each dashboard connection subscribes.
package events

import "sync"

const (
	// CheckResult is published after every stored probe.
	CheckResult = "check_result"
	// IncidentOpened is published when three failures open an incident.
	IncidentOpened = "incident_opened"
	// IncidentUpdated is published when an incident is acknowledged, resolved, or gets a new last error.
	IncidentUpdated = "incident_updated"
)

// Event is one message on the stream. Data is encoded as JSON by the HTTP handler.
type Event struct {
	Type string
	Data any
}

// Broker fans out events to current subscribers.
// A subscriber that does not read fast enough drops new events instead of blocking a probe.
type Broker struct {
	mu     sync.Mutex
	subs   map[int]chan Event
	next   int
	closed bool
}

// New returns a broker with no subscribers.
func New() *Broker {
	return &Broker{subs: make(map[int]chan Event)}
}

// Publish sends event to every subscriber. A full buffer drops that event for that subscriber.
func (b *Broker) Publish(event Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	for _, ch := range b.subs {
		select {
		case ch <- event:
		default:
		}
	}
}

// Subscribe returns the event channel and a function that removes this subscriber.
// The channel is closed when the subscription ends or the broker is closed.
func (b *Broker) Subscribe() (<-chan Event, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	ch := make(chan Event, 16)
	if b.closed {
		close(ch)
		return ch, func() {}
	}
	id := b.next
	b.next++
	b.subs[id] = ch
	return ch, func() { b.unsubscribe(id) }
}

// Close drops every subscriber and ignores later publishes. It is safe to call more than once.
func (b *Broker) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for id, ch := range b.subs {
		close(ch)
		delete(b.subs, id)
	}
}

// unsubscribe removes one subscriber and closes its channel so the handler returns.
// Calling it again for the same id does nothing.
func (b *Broker) unsubscribe(id int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	ch, ok := b.subs[id]
	if !ok {
		return
	}
	delete(b.subs, id)
	close(ch)
}
