package db

import (
	"context"
	"testing"
)

func TestSetSteamEnabled_RoundTrip(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)

	if err := SetSteamEnabled(ctx, conn, "did:plc:test", true); err != nil {
		t.Fatalf("SetSteamEnabled(true): %v", err)
	}

	enabled, err := IsSteamEnabled(ctx, conn, "did:plc:test")
	if err != nil {
		t.Fatalf("IsSteamEnabled: %v", err)
	}
	if !enabled {
		t.Fatalf("got false, want true")
	}

	if err := SetSteamEnabled(ctx, conn, "did:plc:test", false); err != nil {
		t.Fatalf("SetSteamEnabled(false): %v", err)
	}

	enabled, err = IsSteamEnabled(ctx, conn, "did:plc:test")
	if err != nil {
		t.Fatalf("IsSteamEnabled: %v", err)
	}
	if enabled {
		t.Fatalf("got true, want false")
	}
}

func TestIsSteamEnabled_MissingReturnsFalse(t *testing.T) {
	conn := openTestDB(t)
	enabled, err := IsSteamEnabled(context.Background(), conn, "did:plc:nobody")
	if err != nil {
		t.Fatalf("IsSteamEnabled: %v", err)
	}
	if enabled {
		t.Fatalf("got true for missing row, want false")
	}
}
