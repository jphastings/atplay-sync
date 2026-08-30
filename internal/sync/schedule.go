package sync

import "time"

const minPollGap = 1 * time.Minute

// NextPollInterval spreads whatever polls today's remaining budget allows
// evenly across the rest of the UTC day, so a steady account count
// produces a steady cadence instead of bursty 1-minute polling followed by
// a long silence once the budget runs out. callsPerPoll is normally the
// call count the most recent tick actually used — a reasonable estimate of
// the next one's cost as long as the enabled-account count holds steady.
//
// Bounded to [1 minute, time left in the UTC day]: never faster than a
// minute, and never slower than waiting for tomorrow's budget reset.
func NextPollInterval(callsPerPoll, remainingBudget int, now time.Time) time.Duration {
	remainingInDay := timeUntilUTCMidnight(now)
	if remainingInDay <= 0 {
		return minPollGap // essentially midnight; tomorrow's reset will sort the schedule out
	}
	if callsPerPoll <= 0 {
		return minPollGap // nothing to poll for yet — check back soon in case that changes
	}

	remainingPolls := remainingBudget / callsPerPoll
	if remainingPolls <= 0 {
		return remainingInDay // today's budget is spent — wait for the reset
	}

	gap := remainingInDay / time.Duration(remainingPolls)
	if gap < minPollGap {
		return minPollGap
	}
	return gap
}

func timeUntilUTCMidnight(now time.Time) time.Duration {
	u := now.UTC()
	nextMidnight := time.Date(u.Year(), u.Month(), u.Day()+1, 0, 0, 0, 0, time.UTC)
	return nextMidnight.Sub(u)
}
