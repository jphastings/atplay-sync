// internal/jetstream/manager.go
package jetstream

import (
	"context"
	"sync"

	"github.com/jphastings/game-status/internal/keytrace"
)

// DIDLister reports the DIDs that should currently be watched. Manager calls it
// while holding its own lock so a read can't be overtaken by a concurrent
// restart applying a staler list.
type DIDLister func(ctx context.Context) ([]string, error)

type Manager struct {
	mu      sync.Mutex
	host    string
	handler EventHandler
	current *Listener
	connect func(ctx context.Context, host string, collections, dids []string, handler EventHandler) (*Listener, error)
}

func NewManager(host string, handler EventHandler) *Manager {
	return &Manager{host: host, handler: handler, connect: connect}
}

// Restart opens a new connection with the given DIDs BEFORE closing the old
// one. A gap here means a claim revocation could go unnoticed until the next
// daily sweep (spec: "make-before-break restart"). Duplicate events during
// the brief overlap are harmless — HandleEvent's upsert/invalidate are idempotent.
func (m *Manager) Restart(ctx context.Context, listDIDs DIDLister) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	dids, err := listDIDs(ctx)
	if err != nil {
		return err
	}

	next, err := m.connect(ctx, m.host, []string{keytrace.ClaimCollection}, dids, m.handler)
	if err != nil {
		return err
	}

	old := m.current
	m.current = next
	if old != nil {
		old.Close()
	}
	return nil
}

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current != nil {
		m.current.Close()
		m.current = nil
	}
}
