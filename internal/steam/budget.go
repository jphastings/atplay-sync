package steam

import (
	"sync"
	"time"
)

// Budget is a self-imposed daily ceiling on Steam Web API calls, checked
// before each tick so a large enough user base backs off automatically
// rather than hammering Steam. ~100,000 calls/day per key is the commonly
// cited guideline (Steam publishes no hard official number for
// GetPlayerSummaries specifically) — this is a safety valve on top of that
// guess, not a substitute for respecting a real 429 (see Exhaust).
type Budget struct {
	mu          sync.Mutex
	max         int
	windowStart time.Time
	used        int
	now         func() time.Time
}

func NewBudget(maxPerDay int) *Budget {
	return newBudget(maxPerDay, time.Now)
}

func newBudget(maxPerDay int, now func() time.Time) *Budget {
	return &Budget{max: maxPerDay, windowStart: now(), now: now}
}

// Reserve reports whether n more calls fit in today's remaining budget, and
// counts them against it if so.
func (b *Budget) Reserve(n int) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.resetIfExpired()
	if b.used+n > b.max {
		return false
	}
	b.used += n
	return true
}

// Remaining reports how many calls are left in today's budget.
func (b *Budget) Remaining() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.resetIfExpired()
	return b.max - b.used
}

// Exhaust zeroes out whatever's left of today's budget. Call this when
// Steam itself returns 429 — its own enforcement is the ground truth, and
// may kick in well before our self-imposed ceiling.
func (b *Budget) Exhaust() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.resetIfExpired()
	b.used = b.max
}

func (b *Budget) resetIfExpired() {
	if b.now().Sub(b.windowStart) >= 24*time.Hour {
		b.windowStart = b.now()
		b.used = 0
	}
}
