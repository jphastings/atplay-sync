// internal/livestate/hub_test.go
package livestate

import (
	"testing"

	appsync "github.com/jphastings/game-status/internal/sync"
)

type fakeConn struct {
	received [][]appsync.SourceOutcome
}

func (f *fakeConn) WriteJSON(v any) error {
	f.received = append(f.received, v.([]appsync.SourceOutcome))
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
