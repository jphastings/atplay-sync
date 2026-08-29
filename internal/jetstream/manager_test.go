// internal/jetstream/manager_test.go
package jetstream

import (
	"context"
	"testing"
)

func closedChan() chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

func TestManager_Restart_OpensNewBeforeClosingOld(t *testing.T) {
	var events []string

	fakeConnect := func(ctx context.Context, host string, collections, dids []string, handler EventHandler) (*Listener, error) {
		events = append(events, "connect")
		return &Listener{cancel: func() { events = append(events, "close") }, done: closedChan()}, nil
	}

	m := &Manager{host: "test", handler: func(ctx context.Context, ev Event) error { return nil }, connect: fakeConnect}

	if err := m.Restart(context.Background(), []string{"did:plc:a"}); err != nil {
		t.Fatalf("first Restart: %v", err)
	}
	if err := m.Restart(context.Background(), []string{"did:plc:a", "did:plc:b"}); err != nil {
		t.Fatalf("second Restart: %v", err)
	}

	want := []string{"connect", "connect", "close"} // the 2nd connect must precede closing the 1st listener
	if !equalStrings(events, want) {
		t.Fatalf("got %v, want %v", events, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
