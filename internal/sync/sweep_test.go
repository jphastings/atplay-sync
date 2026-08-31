// internal/sync/sweep_test.go
package sync

import (
	"context"
	"testing"
	"time"

	appdb "github.com/jphastings/game-status/internal/db"
)

func TestRunStatusSweep_DeletesOnlyStaleEntries(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	appdb.UpsertUser(ctx, conn, "did:plc:a")
	appdb.UpsertUser(ctx, conn, "did:plc:b")

	now := time.Now()
	writer := &fakeWriter{live: map[string][]StatusEntry{
		"did:plc:a": {
			{Rkey: "stale-game", StaleAt: now.Add(-time.Hour)},
			{Rkey: "live-game", StaleAt: now.Add(time.Hour)},
		},
		"did:plc:b": {
			{Rkey: "also-stale", StaleAt: now.Add(-time.Minute)},
		},
	}}

	if err := RunStatusSweep(ctx, conn, writer, now); err != nil {
		t.Fatalf("RunStatusSweep: %v", err)
	}

	want := map[recordedDelete]bool{
		{did: "did:plc:a", rkey: "stale-game"}: true,
		{did: "did:plc:b", rkey: "also-stale"}: true,
	}
	if len(writer.deletes) != len(want) {
		t.Fatalf("got deletes=%+v, want exactly %v", writer.deletes, want)
	}
	for _, d := range writer.deletes {
		if !want[d] {
			t.Fatalf("got unexpected delete %+v", d)
		}
	}
	if len(writer.puts) != 0 {
		t.Fatalf("got puts=%+v, want none — the sweep never writes", writer.puts)
	}
}

func TestRunStatusSweep_NoStaleEntries_NoDeletes(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	appdb.UpsertUser(ctx, conn, "did:plc:a")

	now := time.Now()
	writer := &fakeWriter{live: map[string][]StatusEntry{
		"did:plc:a": {{Rkey: "live-game", StaleAt: now.Add(time.Hour)}},
	}}

	if err := RunStatusSweep(ctx, conn, writer, now); err != nil {
		t.Fatalf("RunStatusSweep: %v", err)
	}
	if len(writer.deletes) != 0 {
		t.Fatalf("got deletes=%+v, want none", writer.deletes)
	}
}
