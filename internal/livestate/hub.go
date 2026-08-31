// Package livestate pushes each did's current sync.SourceOutcome list to any
// browser tab that's live-watching it (the settings page's sync-state
// indicator), so it updates the moment a Steam tick or Discord presence
// event changes what's playing — no polling.
package livestate

import (
	"sync"

	appsync "github.com/jphastings/game-status/internal/sync"
)

// Conn is the minimal surface Hub needs from a live connection — satisfied
// by *websocket.Conn as-is, and trivially faked in tests without a real
// socket.
type Conn interface {
	WriteJSON(v any) error
}

// Hub is a per-did registry of live subscribers and the sync.Broadcaster
// Reconcile pushes into. Single-process, in-memory — this app runs as one
// instance.
// ponytail: a multi-instance deployment would need a shared broker instead
// of an in-process map; not needed at this app's scale.
type Hub struct {
	mu    sync.Mutex
	conns map[string]map[Conn]struct{}
}

func NewHub() *Hub {
	return &Hub{conns: map[string]map[Conn]struct{}{}}
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
	for c := range h.conns[did] {
		_ = c.WriteJSON(outcomes) // best-effort — a broken conn's read pump notices and deregisters
	}
}
