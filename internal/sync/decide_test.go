// internal/sync/decide_test.go
package sync

import (
	"testing"
	"time"
)

func TestDecide_NotPlaying_AlwaysDeletes(t *testing.T) {
	now := time.Now()
	if d := Decide(false, "", nil, now); d.Action != ActionDelete {
		t.Fatalf("got %+v, want ActionDelete", d)
	}
	// Idempotent by design (Global Constraints) — deletes even with no prior session.
	if d := Decide(false, "", &SessionStart{GameKey: "271590", StartedAt: now.Add(-time.Hour)}, now); d.Action != ActionDelete {
		t.Fatalf("got %+v, want ActionDelete even with a prior session", d)
	}
}

func TestDecide_PlayingSameGame_ReusesStartedAt(t *testing.T) {
	now := time.Now()
	started := now.Add(-2 * time.Hour)
	d := Decide(true, "271590", &SessionStart{GameKey: "271590", StartedAt: started}, now)
	if d.Action != ActionWrite || !d.CreatedAt.Equal(started) {
		t.Fatalf("got %+v, want ActionWrite with CreatedAt=%v", d, started)
	}
}

func TestDecide_PlayingDifferentGame_ResetsStartedAt(t *testing.T) {
	now := time.Now()
	d := Decide(true, "570", &SessionStart{GameKey: "271590", StartedAt: now.Add(-2 * time.Hour)}, now)
	if d.Action != ActionWrite || !d.CreatedAt.Equal(now) {
		t.Fatalf("got %+v, want ActionWrite with CreatedAt=now (%v)", d, now)
	}
}

func TestDecide_PlayingWithNoPriorSession_StartsNow(t *testing.T) {
	now := time.Now()
	d := Decide(true, "271590", nil, now)
	if d.Action != ActionWrite || !d.CreatedAt.Equal(now) {
		t.Fatalf("got %+v, want ActionWrite with CreatedAt=now", d)
	}
}
