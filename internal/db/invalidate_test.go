package db

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

type fakeReconciler struct{ calls []string }

func (f *fakeReconciler) Reconcile(ctx context.Context, did string, now time.Time) error {
	f.calls = append(f.calls, did)
	return nil
}

func TestInvalidateClaim_TurnsOffSync(t *testing.T) {
	conn, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()
	ctx := context.Background()

	UpsertUser(ctx, conn, "did:plc:a")
	SetEnabled(ctx, conn, "did:plc:a", SteamSource, true)

	reconciler := &fakeReconciler{}
	if err := InvalidateClaim(ctx, conn, reconciler, "did:plc:a", SteamSource, time.Now()); err != nil {
		t.Fatalf("InvalidateClaim: %v", err)
	}

	if enabled, err := IsEnabled(ctx, conn, "did:plc:a", SteamSource); err != nil || enabled {
		t.Fatalf("IsEnabled = %v, %v, want false, nil — a lost claim must turn sync off, not leave it silently waiting to resume", enabled, err)
	}
	if len(reconciler.calls) != 1 || reconciler.calls[0] != "did:plc:a" {
		t.Fatalf("Reconcile calls = %v, want one call for did:plc:a", reconciler.calls)
	}
}
