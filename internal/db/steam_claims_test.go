package db

import (
	"context"
	"testing"
	"time"
)

func TestUpsertSteamClaim_RoundTrip(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)

	now := time.Now().UTC()
	claim := SteamClaim{
		DID:            "did:plc:test",
		Subject:        "76561198000000000",
		DisplayName:    "TestPlayer",
		ClaimURI:       "https://steamcommunity.com/profiles/76561198000000000",
		RecordURI:      "at://did:plc:test/dev.keytrace.claim/xyz",
		LastVerifiedAt: now,
	}

	if err := UpsertSteamClaim(ctx, conn, claim); err != nil {
		t.Fatalf("UpsertSteamClaim: %v", err)
	}

	c, err := GetSteamClaim(ctx, conn, "did:plc:test")
	if err != nil {
		t.Fatalf("GetSteamClaim: %v", err)
	}
	if c == nil {
		t.Fatalf("got nil, want SteamClaim")
	}
	if c.DID != claim.DID || c.Subject != claim.Subject || c.DisplayName != claim.DisplayName ||
		c.ClaimURI != claim.ClaimURI || c.RecordURI != claim.RecordURI {
		t.Fatalf("got %+v, want %+v", c, claim)
	}
	// Check time is within a second (RFC3339 parsing may lose some precision)
	if diff := now.Sub(c.LastVerifiedAt); diff > time.Second || diff < -time.Second {
		t.Fatalf("got LastVerifiedAt %v, want %v (diff: %v)", c.LastVerifiedAt, now, diff)
	}
}

func TestGetSteamClaim_MissingReturnsNil(t *testing.T) {
	conn := openTestDB(t)
	c, err := GetSteamClaim(context.Background(), conn, "did:plc:nobody")
	if err != nil {
		t.Fatalf("GetSteamClaim: %v", err)
	}
	if c != nil {
		t.Fatalf("got %+v, want nil", c)
	}
}

func TestInvalidateSteamClaim_RemovesData(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)

	claim := SteamClaim{
		DID:            "did:plc:test",
		Subject:        "76561198000000000",
		DisplayName:    "TestPlayer",
		ClaimURI:       "https://steamcommunity.com/profiles/76561198000000000",
		RecordURI:      "at://did:plc:test/dev.keytrace.claim/xyz",
		LastVerifiedAt: time.Now(),
	}

	if err := UpsertSteamClaim(ctx, conn, claim); err != nil {
		t.Fatalf("UpsertSteamClaim: %v", err)
	}

	if err := InvalidateSteamClaim(ctx, conn, "did:plc:test"); err != nil {
		t.Fatalf("InvalidateSteamClaim: %v", err)
	}

	c, err := GetSteamClaim(ctx, conn, "did:plc:test")
	if err != nil {
		t.Fatalf("GetSteamClaim: %v", err)
	}
	if c != nil {
		t.Fatalf("got %+v after invalidate, want nil", c)
	}
}
