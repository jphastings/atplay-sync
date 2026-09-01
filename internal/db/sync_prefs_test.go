package db

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestSetEnabled_RoundTrip(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)

	if err := SetEnabled(ctx, conn, "did:plc:test", SteamSource, true); err != nil {
		t.Fatalf("SetEnabled(true): %v", err)
	}

	enabled, err := IsEnabled(ctx, conn, "did:plc:test", SteamSource)
	if err != nil {
		t.Fatalf("IsEnabled: %v", err)
	}
	if !enabled {
		t.Fatalf("got false, want true")
	}

	if err := SetEnabled(ctx, conn, "did:plc:test", SteamSource, false); err != nil {
		t.Fatalf("SetEnabled(false): %v", err)
	}

	enabled, err = IsEnabled(ctx, conn, "did:plc:test", SteamSource)
	if err != nil {
		t.Fatalf("IsEnabled: %v", err)
	}
	if enabled {
		t.Fatalf("got true, want false")
	}
}

func TestIsEnabled_MissingReturnsFalse(t *testing.T) {
	conn := openTestDB(t)
	enabled, err := IsEnabled(context.Background(), conn, "did:plc:nobody", SteamSource)
	if err != nil {
		t.Fatalf("IsEnabled: %v", err)
	}
	if enabled {
		t.Fatalf("got true for missing row, want false")
	}
}

func TestSetEnabled_PriorityDefaultsBySourceAndSurvivesToggle(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)

	if err := SetEnabled(ctx, conn, "did:plc:test", SteamSource, true); err != nil {
		t.Fatalf("SetEnabled steam: %v", err)
	}
	if err := SetEnabled(ctx, conn, "did:plc:test", DiscordSource, true); err != nil {
		t.Fatalf("SetEnabled discord: %v", err)
	}
	if p := priorityFor(t, conn, "did:plc:test", SteamSource); p != 0 {
		t.Fatalf("fresh steam priority = %d, want 0", p)
	}
	if p := priorityFor(t, conn, "did:plc:test", DiscordSource); p != 1 {
		t.Fatalf("fresh discord priority = %d, want 1", p)
	}

	// A reorder must survive a later disable/re-enable — SetEnabled should
	// only ever set the default on first insert, never reset an existing row.
	if err := SetSourceOrder(ctx, conn, "did:plc:test", []string{"discord", "steam"}); err != nil {
		t.Fatalf("SetSourceOrder: %v", err)
	}
	if err := SetEnabled(ctx, conn, "did:plc:test", DiscordSource, false); err != nil {
		t.Fatalf("SetEnabled(false): %v", err)
	}
	if err := SetEnabled(ctx, conn, "did:plc:test", DiscordSource, true); err != nil {
		t.Fatalf("SetEnabled(true): %v", err)
	}
	if p := priorityFor(t, conn, "did:plc:test", DiscordSource); p != 0 {
		t.Fatalf("priority after toggle = %d, want 0 (reordered priority must survive)", p)
	}
}

func TestListEnabledDIDs_RequiresBothEnabledAndClaimed(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)

	for _, did := range []string{"did:plc:a", "did:plc:b", "did:plc:c"} {
		mustUpsertUser(t, conn, did)
	}
	if err := SetEnabled(ctx, conn, "did:plc:a", SteamSource, true); err != nil { // enabled, no claim
		t.Fatalf("SetEnabled: %v", err)
	}
	if err := SetEnabled(ctx, conn, "did:plc:b", SteamSource, true); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	mustUpsertClaim(t, conn, "did:plc:b") // enabled AND claimed
	mustUpsertClaim(t, conn, "did:plc:c") // claimed, not enabled

	dids, err := ListEnabledDIDs(ctx, conn, SteamSource)
	if err != nil {
		t.Fatalf("ListEnabledDIDs: %v", err)
	}
	if len(dids) != 1 || dids[0] != "did:plc:b" {
		t.Fatalf("got %v, want [did:plc:b]", dids)
	}
}

func TestListEnabledSourcesByPriority_OrdersBySetSourceOrder(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	mustUpsertUser(t, conn, "did:plc:a")
	if err := SetEnabled(ctx, conn, "did:plc:a", SteamSource, true); err != nil {
		t.Fatalf("SetEnabled steam: %v", err)
	}
	if err := SetEnabled(ctx, conn, "did:plc:a", DiscordSource, true); err != nil {
		t.Fatalf("SetEnabled discord: %v", err)
	}
	if err := SetSourceOrder(ctx, conn, "did:plc:a", []string{"discord", "steam"}); err != nil {
		t.Fatalf("SetSourceOrder: %v", err)
	}

	sources, err := ListEnabledSourcesByPriority(ctx, conn, "did:plc:a")
	if err != nil {
		t.Fatalf("ListEnabledSourcesByPriority: %v", err)
	}
	if len(sources) != 2 || sources[0] != "discord" || sources[1] != "steam" {
		t.Fatalf("got %v, want [discord steam]", sources)
	}
}

func TestSetSourceOrder_CreatesDisabledRowForSourceWithNoExistingRow(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	mustUpsertUser(t, conn, "did:plc:a")

	if err := SetSourceOrder(ctx, conn, "did:plc:a", []string{"discord", "steam"}); err != nil {
		t.Fatalf("SetSourceOrder: %v", err)
	}

	if p := priorityFor(t, conn, "did:plc:a", "discord"); p != 0 {
		t.Fatalf("discord priority = %d, want 0", p)
	}
	if p := priorityFor(t, conn, "did:plc:a", "steam"); p != 1 {
		t.Fatalf("steam priority = %d, want 1", p)
	}
	for _, source := range []string{"discord", "steam"} {
		enabled, err := IsEnabled(ctx, conn, "did:plc:a", source)
		if err != nil {
			t.Fatalf("IsEnabled(%s): %v", source, err)
		}
		if enabled {
			t.Fatalf("%s should be created disabled by a bare reorder", source)
		}
	}
}

func TestSetSourceOrder_DoesNotChangeEnabledForExistingRow(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	mustUpsertUser(t, conn, "did:plc:a")
	if err := SetEnabled(ctx, conn, "did:plc:a", SteamSource, true); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}

	if err := SetSourceOrder(ctx, conn, "did:plc:a", []string{"discord", "steam"}); err != nil {
		t.Fatalf("SetSourceOrder: %v", err)
	}

	enabled, err := IsEnabled(ctx, conn, "did:plc:a", SteamSource)
	if err != nil {
		t.Fatalf("IsEnabled: %v", err)
	}
	if !enabled {
		t.Fatal("reorder must not disable an already-enabled row (ON CONFLICT only touches priority)")
	}
	if p := priorityFor(t, conn, "did:plc:a", SteamSource); p != 1 {
		t.Fatalf("steam priority = %d, want 1", p)
	}
}

func priorityFor(t *testing.T, conn *sql.DB, did, source string) int {
	t.Helper()
	var p int
	if err := conn.QueryRow(`SELECT priority FROM sync_prefs WHERE did = ? AND source = ?`, did, source).Scan(&p); err != nil {
		t.Fatalf("query priority for %s/%s: %v", did, source, err)
	}
	return p
}

func mustUpsertClaim(t *testing.T, conn *sql.DB, did string) {
	t.Helper()
	err := UpsertClaim(context.Background(), conn, Claim{
		DID: did, Type: SteamSource, Subject: "76500000000000000", DisplayName: "Test",
		ClaimURI:  "https://steamcommunity.com/profiles/76500000000000000",
		RecordURI: "at://" + did + "/dev.keytrace.claim/abc", LastVerifiedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("UpsertClaim(%s): %v", did, err)
	}
}

func TestSetSourceOnline_RoundTrip(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)

	if err := SetEnabled(ctx, conn, "did:plc:a", SteamSource, true); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	if err := SetSourceOnline(ctx, conn, "did:plc:a", SteamSource, true); err != nil {
		t.Fatalf("SetSourceOnline: %v", err)
	}

	online, err := IsSourceOnline(ctx, conn, "did:plc:a", SteamSource)
	if err != nil || !online {
		t.Fatalf("IsSourceOnline = %v, %v, want true", online, err)
	}

	if err := SetSourceOnline(ctx, conn, "did:plc:a", SteamSource, false); err != nil {
		t.Fatalf("SetSourceOnline(false): %v", err)
	}
	online, err = IsSourceOnline(ctx, conn, "did:plc:a", SteamSource)
	if err != nil || online {
		t.Fatalf("IsSourceOnline = %v, %v, want false", online, err)
	}
}

func TestIsSourceOnline_DefaultsFalse(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)

	// Never polled, and a source with no row at all: both are "we have no
	// reason to believe they're online".
	if err := SetEnabled(ctx, conn, "did:plc:a", SteamSource, true); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	online, err := IsSourceOnline(ctx, conn, "did:plc:a", SteamSource)
	if err != nil || online {
		t.Fatalf("IsSourceOnline (never set) = %v, %v, want false", online, err)
	}
	online, err = IsSourceOnline(ctx, conn, "did:plc:nobody", DiscordSource)
	if err != nil || online {
		t.Fatalf("IsSourceOnline (no row) = %v, %v, want false", online, err)
	}
}
