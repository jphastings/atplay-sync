// internal/sync/decide.go
package sync

import "time"

type SessionStart struct {
	GameKey   string
	StartedAt time.Time
}

type Action int

const (
	ActionNone Action = iota
	ActionDelete
	ActionWrite
)

type Decision struct {
	Action    Action
	GameKey   string    // only meaningful for ActionWrite
	CreatedAt time.Time // only meaningful for ActionWrite
}

// Decide is pure and needs no read of the previous PDS record (Global
// Constraints): not playing always deletes, regardless of prior state — an
// idempotent no-op if nothing existed. Playing always writes; CreatedAt is
// reused only when the game hasn't changed, which is the one thing that
// can't be derived from "what's playing right now" alone.
func Decide(playing bool, appID string, prev *SessionStart, now time.Time) Decision {
	if !playing {
		return Decision{Action: ActionDelete}
	}
	if prev != nil && prev.GameKey == appID {
		return Decision{Action: ActionWrite, GameKey: appID, CreatedAt: prev.StartedAt}
	}
	return Decision{Action: ActionWrite, GameKey: appID, CreatedAt: now}
}
