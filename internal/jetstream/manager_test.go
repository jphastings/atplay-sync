// internal/jetstream/manager_test.go
package jetstream

import (
	"context"
	"errors"
	"testing"
)

func closedChan() chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

func staticDIDs(dids ...string) DIDLister {
	return func(context.Context) ([]string, error) { return dids, nil }
}

func testManager(connect func(ctx context.Context, host string, collections, dids []string, handler EventHandler) (*Listener, error)) *Manager {
	return &Manager{host: "test", handler: func(ctx context.Context, ev Event) error { return nil }, connect: connect}
}

func TestManager_Restart_OpensNewBeforeClosingOld(t *testing.T) {
	var events []string
	var lastDIDs []string

	fakeConnect := func(ctx context.Context, host string, collections, dids []string, handler EventHandler) (*Listener, error) {
		events = append(events, "connect")
		lastDIDs = dids
		return &Listener{cancel: func() { events = append(events, "close") }, done: closedChan()}, nil
	}

	m := testManager(fakeConnect)

	if err := m.Restart(context.Background(), staticDIDs("did:plc:a")); err != nil {
		t.Fatalf("first Restart: %v", err)
	}
	if err := m.Restart(context.Background(), staticDIDs("did:plc:a", "did:plc:b")); err != nil {
		t.Fatalf("second Restart: %v", err)
	}

	want := []string{"connect", "connect", "close"} // the 2nd connect must precede closing the 1st listener
	if !equalStrings(events, want) {
		t.Fatalf("got %v, want %v", events, want)
	}
	if !equalStrings(lastDIDs, []string{"did:plc:a", "did:plc:b"}) {
		t.Fatalf("subscribed to %v, want the list the provider returned", lastDIDs)
	}
}

func TestManager_Restart_KeepsOldListenerWhenConnectFails(t *testing.T) {
	var events []string
	connectErr := errors.New("dial refused")
	calls := 0

	fakeConnect := func(ctx context.Context, host string, collections, dids []string, handler EventHandler) (*Listener, error) {
		events = append(events, "connect")
		calls++
		if calls > 1 {
			return nil, connectErr
		}
		return &Listener{cancel: func() { events = append(events, "close") }, done: closedChan()}, nil
	}

	m := testManager(fakeConnect)

	if err := m.Restart(context.Background(), staticDIDs("did:plc:a")); err != nil {
		t.Fatalf("first Restart: %v", err)
	}
	if err := m.Restart(context.Background(), staticDIDs("did:plc:b")); !errors.Is(err, connectErr) {
		t.Fatalf("second Restart err = %v, want %v", err, connectErr)
	}

	want := []string{"connect", "connect"} // no "close": the live listener must survive a failed reconnect
	if !equalStrings(events, want) {
		t.Fatalf("got %v, want %v", events, want)
	}
}

func TestManager_Restart_ReturnsDIDListerError(t *testing.T) {
	listErr := errors.New("db down")
	m := testManager(func(ctx context.Context, host string, collections, dids []string, handler EventHandler) (*Listener, error) {
		t.Fatal("connect must not be attempted when the DID list is unavailable")
		return nil, nil
	})

	err := m.Restart(context.Background(), func(context.Context) ([]string, error) { return nil, listErr })
	if !errors.Is(err, listErr) {
		t.Fatalf("Restart err = %v, want %v", err, listErr)
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
