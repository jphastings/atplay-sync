// internal/db/claims_test.go
package db

import (
	"context"
	"testing"
	"time"
)

func TestUpsertClaim_RoundTrip(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)

	now := time.Now().UTC()
	claim := Claim{
		DID: "did:plc:test", Type: SteamSource, Subject: "76561198000000000",
		DisplayName: "TestPlayer", ClaimURI: "https://steamcommunity.com/profiles/76561198000000000",
		RecordURI: "at://did:plc:test/dev.keytrace.claim/xyz", LastVerifiedAt: now,
	}

	if err := UpsertClaim(ctx, conn, claim); err != nil {
		t.Fatalf("UpsertClaim: %v", err)
	}

	c, err := GetClaim(ctx, conn, "did:plc:test", SteamSource)
	if err != nil {
		t.Fatalf("GetClaim: %v", err)
	}
	if c == nil {
		t.Fatalf("got nil, want Claim")
	}
	if c.DID != claim.DID || c.Type != claim.Type || c.Subject != claim.Subject || c.DisplayName != claim.DisplayName ||
		c.ClaimURI != claim.ClaimURI || c.RecordURI != claim.RecordURI {
		t.Fatalf("got %+v, want %+v", c, claim)
	}
	if diff := now.Sub(c.LastVerifiedAt); diff > time.Second || diff < -time.Second {
		t.Fatalf("got LastVerifiedAt %v, want %v (diff: %v)", c.LastVerifiedAt, now, diff)
	}
}

func TestGetClaim_MissingReturnsNil(t *testing.T) {
	c, err := GetClaim(context.Background(), openTestDB(t), "did:plc:nobody", SteamSource)
	if err != nil {
		t.Fatalf("GetClaim: %v", err)
	}
	if c != nil {
		t.Fatalf("got %+v, want nil", c)
	}
}

func TestGetClaim_DifferentTypeSameDID_Independent(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	now := time.Now()
	if err := UpsertClaim(ctx, conn, Claim{DID: "did:plc:test", Type: SteamSource, Subject: "steam-id", ClaimURI: "x", RecordURI: "y", LastVerifiedAt: now}); err != nil {
		t.Fatalf("UpsertClaim steam: %v", err)
	}
	if err := UpsertClaim(ctx, conn, Claim{DID: "did:plc:test", Type: DiscordSource, Subject: "discord-id", ClaimURI: "x", RecordURI: "y", LastVerifiedAt: now}); err != nil {
		t.Fatalf("UpsertClaim discord: %v", err)
	}

	steam, err := GetClaim(ctx, conn, "did:plc:test", SteamSource)
	if err != nil || steam == nil || steam.Subject != "steam-id" {
		t.Fatalf("got steam claim %+v, %v", steam, err)
	}
	discord, err := GetClaim(ctx, conn, "did:plc:test", DiscordSource)
	if err != nil || discord == nil || discord.Subject != "discord-id" {
		t.Fatalf("got discord claim %+v, %v", discord, err)
	}
}

func TestDeleteClaim_RemovesData(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	claim := Claim{DID: "did:plc:test", Type: SteamSource, Subject: "x", ClaimURI: "x", RecordURI: "y", LastVerifiedAt: time.Now()}
	if err := UpsertClaim(ctx, conn, claim); err != nil {
		t.Fatalf("UpsertClaim: %v", err)
	}
	if err := DeleteClaim(ctx, conn, "did:plc:test", SteamSource); err != nil {
		t.Fatalf("DeleteClaim: %v", err)
	}
	c, err := GetClaim(ctx, conn, "did:plc:test", SteamSource)
	if err != nil || c != nil {
		t.Fatalf("got %+v, %v after delete, want nil, nil", c, err)
	}
}
