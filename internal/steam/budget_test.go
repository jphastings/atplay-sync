package steam

import (
	"testing"
	"time"
)

func TestBudget_ReserveWithinLimit(t *testing.T) {
	b := NewBudget(100)
	if !b.Reserve(60) {
		t.Fatal("Reserve(60) of 100 = false, want true")
	}
	if !b.Reserve(40) {
		t.Fatal("Reserve(40) after 60 used of 100 = false, want true")
	}
	if b.Reserve(1) {
		t.Fatal("Reserve(1) after budget exhausted = true, want false")
	}
}

func TestBudget_ResetsAfter24Hours(t *testing.T) {
	now := time.Now()
	b := newBudget(10, func() time.Time { return now })
	if !b.Reserve(10) {
		t.Fatal("Reserve(10) of 10 = false, want true")
	}
	if b.Reserve(1) {
		t.Fatal("Reserve(1) after exhausted = true, want false")
	}

	now = now.Add(24 * time.Hour)
	if !b.Reserve(10) {
		t.Fatal("Reserve(10) after 24h window rolled over = false, want true")
	}
}

func TestBudget_ExhaustZeroesRemainingBudgetForToday(t *testing.T) {
	now := time.Now()
	b := newBudget(1000, func() time.Time { return now })
	b.Reserve(1) // barely touched today's budget

	b.Exhaust()

	if b.Reserve(1) {
		t.Fatal("Reserve(1) right after Exhaust = true, want false")
	}

	now = now.Add(24 * time.Hour)
	if !b.Reserve(1000) {
		t.Fatal("Reserve(1000) after 24h following Exhaust = false, want true (new window)")
	}
}
