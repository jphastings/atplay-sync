package db

import (
	"context"
	"testing"
	"time"
)

func TestSetSessionStart_RoundTrip(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)

	now := time.Now().UTC()
	if err := SetSessionStart(ctx, conn, "did:plc:test", "steam", "app-123", now); err != nil {
		t.Fatalf("SetSessionStart: %v", err)
	}

	s, err := GetSessionStart(ctx, conn, "did:plc:test", "steam")
	if err != nil {
		t.Fatalf("GetSessionStart: %v", err)
	}
	if s == nil {
		t.Fatalf("got nil, want SessionStart")
	}
	if s.GameKey != "app-123" {
		t.Fatalf("got GameKey %q, want app-123", s.GameKey)
	}
	// Check time is within a second (RFC3339 parsing may lose some precision)
	if diff := now.Sub(s.StartedAt); diff > time.Second || diff < -time.Second {
		t.Fatalf("got StartedAt %v, want %v (diff: %v)", s.StartedAt, now, diff)
	}
}

func TestSetSessionExtra_RoundTrip(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)

	if err := SetSessionStart(ctx, conn, "did:plc:test", "discord", "app-123", time.Now()); err != nil {
		t.Fatalf("SetSessionStart: %v", err)
	}
	if err := SetSessionExtra(ctx, conn, "did:plc:test", "discord", `{"state":"Ranked"}`); err != nil {
		t.Fatalf("SetSessionExtra: %v", err)
	}

	s, err := GetSessionStart(ctx, conn, "did:plc:test", "discord")
	if err != nil {
		t.Fatalf("GetSessionStart: %v", err)
	}
	if s == nil || s.Extra != `{"state":"Ranked"}` {
		t.Fatalf("got %+v, want Extra to round-trip", s)
	}
}

func TestGetSessionStart_ExtraDefaultsEmpty(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)

	if err := SetSessionStart(ctx, conn, "did:plc:test", "steam", "app-123", time.Now()); err != nil {
		t.Fatalf("SetSessionStart: %v", err)
	}
	s, err := GetSessionStart(ctx, conn, "did:plc:test", "steam")
	if err != nil {
		t.Fatalf("GetSessionStart: %v", err)
	}
	if s == nil || s.Extra != "" {
		t.Fatalf("got %+v, want empty Extra (never set)", s)
	}
}

func TestSetSessionRawName_RoundTrip(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)

	if err := SetSessionStart(ctx, conn, "did:plc:test", "steam", "app-123", time.Now()); err != nil {
		t.Fatalf("SetSessionStart: %v", err)
	}
	if err := SetSessionRawName(ctx, conn, "did:plc:test", "steam", "Half-Life 3"); err != nil {
		t.Fatalf("SetSessionRawName: %v", err)
	}

	s, err := GetSessionStart(ctx, conn, "did:plc:test", "steam")
	if err != nil {
		t.Fatalf("GetSessionStart: %v", err)
	}
	if s == nil || s.RawName != "Half-Life 3" {
		t.Fatalf("got %+v, want RawName to round-trip", s)
	}
}

func TestGetSessionStart_RawNameDefaultsEmpty(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)

	if err := SetSessionStart(ctx, conn, "did:plc:test", "steam", "app-123", time.Now()); err != nil {
		t.Fatalf("SetSessionStart: %v", err)
	}
	s, err := GetSessionStart(ctx, conn, "did:plc:test", "steam")
	if err != nil {
		t.Fatalf("GetSessionStart: %v", err)
	}
	if s == nil || s.RawName != "" {
		t.Fatalf("got %+v, want empty RawName (never set)", s)
	}
}

func TestGetSessionStart_MissingReturnsNil(t *testing.T) {
	conn := openTestDB(t)
	s, err := GetSessionStart(context.Background(), conn, "did:plc:nobody", "steam")
	if err != nil {
		t.Fatalf("GetSessionStart: %v", err)
	}
	if s != nil {
		t.Fatalf("got %+v, want nil", s)
	}
}

func TestClearSessionStart_RemovesData(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)

	if err := SetSessionStart(ctx, conn, "did:plc:test", "steam", "app-123", time.Now()); err != nil {
		t.Fatalf("SetSessionStart: %v", err)
	}

	if err := ClearSessionStart(ctx, conn, "did:plc:test", "steam"); err != nil {
		t.Fatalf("ClearSessionStart: %v", err)
	}

	s, err := GetSessionStart(ctx, conn, "did:plc:test", "steam")
	if err != nil {
		t.Fatalf("GetSessionStart: %v", err)
	}
	if s != nil {
		t.Fatalf("got %+v after clear, want nil", s)
	}
}
