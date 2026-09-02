// internal/livestate/hub_test.go
package livestate

import (
	"errors"
	"testing"

	appsync "github.com/jphastings/game-status/internal/sync"
)

type fakeConn struct {
	received [][]appsync.SourceOutcome
	writeErr error
	closed   bool
}

func (f *fakeConn) WriteJSON(v any) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	f.received = append(f.received, v.([]appsync.SourceOutcome))
	return nil
}

func (f *fakeConn) Close() error {
	f.closed = true
	return nil
}

func TestHub_PublishReachesRegisteredConn(t *testing.T) {
	h := NewHub()
	conn := &fakeConn{}
	h.Register("did:plc:a", conn)

	outcomes := []appsync.SourceOutcome{{Source: "steam", Status: appsync.OutcomeSynced, GameName: "Dota 2"}}
	h.Publish("did:plc:a", outcomes)

	if len(conn.received) != 1 || len(conn.received[0]) != 1 || conn.received[0][0] != outcomes[0] {
		t.Fatalf("got %+v, want the outcomes delivered once", conn.received)
	}
}

func TestHub_PublishToUnregisteredDID_NoOp(t *testing.T) {
	h := NewHub()
	conn := &fakeConn{}
	h.Register("did:plc:a", conn)

	h.Publish("did:plc:someone-else", []appsync.SourceOutcome{{Source: "steam", Status: appsync.OutcomeSynced}})

	if len(conn.received) != 0 {
		t.Fatalf("got %+v, want no delivery — different did", conn.received)
	}
}

func TestHub_Deregister_StopsDelivery(t *testing.T) {
	h := NewHub()
	conn := &fakeConn{}
	h.Register("did:plc:a", conn)
	h.Deregister("did:plc:a", conn)

	h.Publish("did:plc:a", []appsync.SourceOutcome{{Source: "steam", Status: appsync.OutcomeSynced}})

	if len(conn.received) != 0 {
		t.Fatalf("got %+v, want no delivery after deregister", conn.received)
	}
}

func TestHub_MultipleConnsSameDID_BothReceive(t *testing.T) {
	h := NewHub()
	connA, connB := &fakeConn{}, &fakeConn{}
	h.Register("did:plc:a", connA)
	h.Register("did:plc:a", connB)

	h.Publish("did:plc:a", []appsync.SourceOutcome{{Source: "steam", Status: appsync.OutcomeSynced}})

	if len(connA.received) != 1 || len(connB.received) != 1 {
		t.Fatalf("got a=%+v b=%+v, want both to receive", connA.received, connB.received)
	}
}

func TestPublish_DropsConnectionWhoseWriteFails(t *testing.T) {
	h := NewHub()
	dead := &fakeConn{writeErr: errors.New("broken pipe")}
	alive := &fakeConn{}
	h.Register("did:plc:a", dead)
	h.Register("did:plc:a", alive)

	h.Publish("did:plc:a", []appsync.SourceOutcome{{Source: "steam", Status: appsync.OutcomeIdle}})

	// A half-open socket blocks in Read forever, so the read pump can't be
	// relied on to notice — the failed write is the only signal we get.
	if !dead.closed {
		t.Fatal("dead connection was not closed")
	}
	h.Publish("did:plc:a", []appsync.SourceOutcome{{Source: "steam", Status: appsync.OutcomeSynced}})
	if len(alive.received) != 2 {
		t.Fatalf("alive conn got %d messages, want 2 — a dead sibling must not affect it", len(alive.received))
	}
}

func TestHeartbeat_ResendsLastStateToOpenConnections(t *testing.T) {
	h := NewHub()
	c := &fakeConn{}
	h.Register("did:plc:a", c)
	h.Publish("did:plc:a", []appsync.SourceOutcome{{Source: "steam", Status: appsync.OutcomeSynced, GameName: "Dota 2"}})

	h.beat()

	if len(c.received) != 2 {
		t.Fatalf("got %d messages, want 2 (publish + heartbeat)", len(c.received))
	}
	if got := c.received[1]; len(got) != 1 || got[0].Status != appsync.OutcomeSynced {
		t.Fatalf("heartbeat sent %+v, want the last published state", got)
	}
}

func TestHeartbeat_NeverSendsNull(t *testing.T) {
	h := NewHub()
	c := &fakeConn{}
	h.Register("did:plc:a", c) // connected, nothing ever published

	h.beat()

	// A nil slice marshals to JSON null, which the browser would hand
	// straight to a for..of and throw on.
	if len(c.received) != 1 || c.received[0] == nil {
		t.Fatalf("got %+v, want a non-nil empty outcome list", c.received)
	}
}
