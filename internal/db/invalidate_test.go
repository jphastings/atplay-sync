package db

import (
	"context"
	"path/filepath"
	"testing"
)

type fakeDeleter struct{ calls []string }

func (f *fakeDeleter) DeleteStatus(ctx context.Context, did string) error {
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
	SetSteamEnabled(ctx, conn, "did:plc:a", true)

	deleter := &fakeDeleter{}
	if err := InvalidateClaim(ctx, conn, deleter, "did:plc:a", SteamSource); err != nil {
		t.Fatalf("InvalidateClaim: %v", err)
	}

	if enabled, err := IsSteamEnabled(ctx, conn, "did:plc:a"); err != nil || enabled {
		t.Fatalf("IsSteamEnabled = %v, %v, want false, nil — a lost claim must turn sync off, not leave it silently waiting to resume", enabled, err)
	}
	if len(deleter.calls) != 1 || deleter.calls[0] != "did:plc:a" {
		t.Fatalf("DeleteStatus calls = %v, want one call for did:plc:a", deleter.calls)
	}
}
