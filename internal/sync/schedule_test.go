package sync

import (
	"testing"
	"time"
)

func TestNextPollInterval_SpreadsRemainingPollsAcrossRestOfDay(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC) // exactly 12h left in the day
	got := NextPollInterval(1, 120, now)                 // 120 calls left, 1 call/poll -> 120 polls
	want := 6 * time.Minute                              // 12h / 120
	if got != want {
		t.Fatalf("NextPollInterval = %v, want %v", got, want)
	}
}

func TestNextPollInterval_ClampsToOneMinuteFloor(t *testing.T) {
	now := time.Date(2026, 8, 30, 0, 0, 1, 0, time.UTC) // ~24h left
	got := NextPollInterval(1, 100000, now)             // plenty of budget -> ideal gap well under 1 minute
	if got != minPollGap {
		t.Fatalf("NextPollInterval = %v, want the 1-minute floor", got)
	}
}

func TestNextPollInterval_BudgetExhausted_WaitsForRestOfDay(t *testing.T) {
	now := time.Date(2026, 8, 30, 20, 0, 0, 0, time.UTC) // 4h left
	got := NextPollInterval(5, 4, now)                   // budget < one poll's worth
	want := 4 * time.Hour
	if got != want {
		t.Fatalf("NextPollInterval = %v, want %v (rest of the day)", got, want)
	}
}

func TestNextPollInterval_NoAccounts_ChecksBackInAMinute(t *testing.T) {
	now := time.Date(2026, 8, 30, 20, 0, 0, 0, time.UTC)
	got := NextPollInterval(0, 100000, now)
	if got != minPollGap {
		t.Fatalf("NextPollInterval = %v, want the 1-minute floor", got)
	}
}
