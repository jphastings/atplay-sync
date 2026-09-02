// Package livestate pushes each did's current sync.SourceOutcome list to any
// browser tab that's live-watching it (the settings page's sync-state
// indicator), so it updates the moment a Steam tick or Discord presence
// event changes what's playing — no polling.
package livestate

import (
	"context"
	"sync"
	"time"

	appsync "github.com/jphastings/game-status/internal/sync"
)

// Conn is the minimal surface Hub needs from a live connection — satisfied
// by *websocket.Conn as-is, and trivially faked in tests without a real
// socket.
type Conn interface {
	WriteJSON(v any) error
	Close() error
}

// Hub is a per-did registry of live subscribers and the sync.Broadcaster
// Reconcile pushes into. Single-process, in-memory — this app runs as one
// instance.
// ponytail: a multi-instance deployment would need a shared broker instead
// of an in-process map; not needed at this app's scale.
type Hub struct {
	mu    sync.Mutex
	conns map[string]map[Conn]struct{}
	// last is each did's most recently sent state, so a heartbeat can
	// re-assert it without recomputing (which would mean fresh DB reads,
	// and cartridge lookups for anything unresolved, every tick).
	last map[string][]appsync.SourceOutcome
}

func NewHub() *Hub {
	return &Hub{
		conns: map[string]map[Conn]struct{}{},
		last:  map[string][]appsync.SourceOutcome{},
	}
}

// HeartbeatInterval is how often every open connection is re-sent its
// current state. It serves two purposes beyond redundancy: traffic stops
// proxies culling an idle connection, and a write failure is the only
// timely way we learn a connection is half-open (Read blocks forever on
// one). The client treats a long silence as a dead socket, so this also
// has to comfortably outpace that threshold.
const HeartbeatInterval = 30 * time.Second

// Heartbeat runs until ctx is cancelled, re-asserting every connected did's
// state on each tick. Beyond keeping connections alive, it makes the whole
// channel self-correcting: a push that never lands can only leave the UI
// stale until the next beat, rather than indefinitely.
func (h *Hub) Heartbeat(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.beat()
		}
	}
}

func (h *Hub) beat() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for did := range h.conns {
		h.sendLocked(did, h.last[did])
	}
}

var _ appsync.Broadcaster = (*Hub)(nil)

func (h *Hub) Register(did string, c Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.conns[did] == nil {
		h.conns[did] = map[Conn]struct{}{}
	}
	h.conns[did][c] = struct{}{}
}

func (h *Hub) Deregister(did string, c Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.conns[did], c)
	if len(h.conns[did]) == 0 {
		delete(h.conns, did)
	}
}

// Publish sends outcomes to every connection currently registered for did. A
// did with none is a no-op — Reconcile calls this on every run regardless of
// whether anyone's listening.
//
// Holds the hub lock for the duration of the writes: at this app's scale (a
// personal deployment, one connection per open settings tab) that's simpler
// than per-connection locking and still satisfies gorilla/websocket's "one
// concurrent writer per connection" rule, just more strictly than needed —
// a slow/dead client would stall other dids' Register/Deregister/Publish
// calls too, not just its own.
// ponytail: switch to a per-connection write lock if that stall ever
// matters.
func (h *Hub) Publish(did string, outcomes []appsync.SourceOutcome) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.last[did] = outcomes
	h.sendLocked(did, outcomes)
}

// sendLocked writes to every connection registered for did, dropping any
// whose write fails. Callers hold h.mu.
//
// That failure is worth acting on rather than ignoring: a half-open socket
// leaves the read pump blocked in Read indefinitely (no data means no
// error), so a write error is the first and often only sign the peer is
// gone. Closing here also makes the read pump return, which deregisters it.
func (h *Hub) sendLocked(did string, outcomes []appsync.SourceOutcome) {
	if outcomes == nil {
		outcomes = []appsync.SourceOutcome{} // a nil slice marshals to null, which the browser can't iterate
	}
	for c := range h.conns[did] {
		if err := c.WriteJSON(outcomes); err != nil {
			c.Close()
			delete(h.conns[did], c)
		}
	}
	if len(h.conns[did]) == 0 {
		delete(h.conns, did)
		delete(h.last, did)
	}
}
