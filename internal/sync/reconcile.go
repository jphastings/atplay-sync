// internal/sync/reconcile.go
package sync

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"sync"
	"time"

	appdb "github.com/jphastings/game-status/internal/db"
)

type Reconciler struct {
	Conn     *sql.DB
	Resolver GameResolver
	Writer   RecordWriter
	// Broadcaster, if set, is pushed a did's freshly computed per-source
	// outcomes at the end of every Reconcile — e.g. a live WS hub feeding
	// the settings page's sync-state indicator. Optional: nil is fine
	// (every existing caller/test that doesn't need live-push leaves it
	// unset), and Publish on a did with no subscribers is expected to be a
	// cheap no-op.
	Broadcaster Broadcaster
	// mu serializes Reconcile end-to-end (read session_starts, compute,
	// publish, diff/write the PDS) across every caller — Steam's tick and
	// Discord's presence handler run on independent goroutines and can
	// both reconcile the same did around the same moment. Without this,
	// two concurrent calls race with no guarantee the one with fresher
	// data finishes (and publishes) last, so a stale snapshot can
	// overwrite a fresh one both on the live-push socket and in what gets
	// written to the PDS.
	// ponytail: one global lock, not per-did — simplest correct fix at
	// this app's scale. It fully serializes reconciles across every user,
	// including their PDS network calls; switch to a per-did lock if that
	// throughput ever matters.
	mu sync.Mutex
}

var _ appdb.Reconciler = (*Reconciler)(nil)

// reconcileTimeout bounds one Reconcile's work — chiefly its PDS calls,
// which have no timeout of their own and are reached with a context that
// carries no deadline (every caller passes context.Background()). Since
// Reconcile holds mu for its whole body, an unanswered PDS connection would
// otherwise hold that lock forever and wedge every other account's sync
// behind it. Overridable for tests, like steam.Client.BaseURL.
var reconcileTimeout = 30 * time.Second

// SourceOutcome is what one enabled+connected source is currently reporting,
// from the settings page's point of view — distinct from ActorStatus, which
// is what actually gets published. A source absent from Reconcile/
// ComputeDesired's outcome slice has nothing to show (hidden): no session,
// or the source is disabled/disconnected.
type SourceOutcome struct {
	Source   string `json:"source"`             // appdb.SteamSource / appdb.DiscordSource
	Status   string `json:"status"`             // OutcomeSynced / OutcomeDuplicate / OutcomeUnknown
	GameName string `json:"gameName,omitempty"` // resolved game name (synced/duplicate), or the source's own raw reported name (unknown)
}

const (
	// OutcomeSynced: this source's session resolved to a game, and it was
	// the highest-priority source to claim that game's rkey — its report
	// is what created/maintains the live games.atmosphere.status record.
	OutcomeSynced = "synced"
	// OutcomeDuplicate: this source's session resolved to the same game as
	// a higher-priority source, so it's a valid match but isn't the one
	// driving the published record.
	OutcomeDuplicate = "duplicate"
	// OutcomeUnknown: this source has a session, but its reported game
	// never resolved to a cartridge game — nothing is or can be published
	// for it, but the raw reported name is still worth showing.
	OutcomeUnknown = "unknown"
)

// Broadcaster pushes a did's just-computed sync outcomes out to whatever's
// listening live (see internal/livestate.Hub). Defined here, not in the
// consumer package, so Reconciler can depend on it without internal/sync
// needing to know anything about WebSockets or HTTP.
type Broadcaster interface {
	Publish(did string, outcomes []SourceOutcome)
}

// ComputeDesired walks did's enabled sources in priority order — same walk
// Reconcile has always done — and returns both what it always returned
// (desired, for the PDS diff/write below) and, alongside it, a per-source
// classification (outcomes) of what each source is doing with what it's
// reporting: driving the published record (synced), a valid match superseded
// by a higher-priority source (duplicate), or unresolvable (unknown, with
// the source's own raw reported name since there's no cartridge game name to
// use instead). A source with no active session is simply absent from both.
func ComputeDesired(ctx context.Context, conn *sql.DB, resolver GameResolver, did string, now time.Time) (map[string]ActorStatus, []SourceOutcome, error) {
	sources, err := appdb.ListEnabledSourcesByPriority(ctx, conn, did)
	if err != nil {
		return nil, nil, err
	}

	desired := map[string]ActorStatus{}
	var outcomes []SourceOutcome
	for _, source := range sources {
		row, err := appdb.GetSessionStart(ctx, conn, did, source)
		if err != nil {
			return nil, nil, err
		}
		if row == nil {
			continue
		}
		game, err := resolver.GetGameBySteamID(ctx, row.GameKey)
		if err != nil {
			return nil, nil, err
		}
		if game == nil {
			outcomes = append(outcomes, SourceOutcome{Source: source, Status: OutcomeUnknown, GameName: row.RawName})
			continue
		}
		_, _, rkey, ok := parseAtURI(game.URI)
		if !ok {
			continue // malformed game URI — shouldn't happen from cartridge, but never publish garbage
		}
		if _, claimed := desired[rkey]; claimed {
			outcomes = append(outcomes, SourceOutcome{Source: source, Status: OutcomeDuplicate, GameName: game.Name})
			continue // a higher-priority source already claimed this game
		}
		status := ActorStatus{
			Type: "games.atmosphere.status", Game: game.URI,
			Embed:     &Embed{Type: "app.bsky.embed.external", External: EmbedExternal{URI: game.PageURL, Title: game.Name, Description: game.Summary}},
			CreatedAt: row.StartedAt.UTC().Format(time.RFC3339),
			StaleAt:   now.Add(staleBuffer).UTC().Format(time.RFC3339),
			Via:       ViaClientName,
		}
		if row.Extra != "" {
			var extra SessionExtra
			if err := json.Unmarshal([]byte(row.Extra), &extra); err == nil {
				status.State = extra.State
				if extra.Details != "" || extra.DetailsStartedAt != "" || extra.DetailsEndsAt != "" {
					status.Details = &Details{Event: extra.Details, StartedAt: extra.DetailsStartedAt, EndsAt: extra.DetailsEndsAt}
				}
				if extra.PartyID != "" || extra.PartyCurrent > 0 {
					status.Playing.ID = extra.PartyID
					status.Playing.Party = &Party{Current: extra.PartyCurrent, Max: extra.PartyMax, DIDs: extra.PartyDIDs}
				}
			}
		}
		desired[rkey] = status
		outcomes = append(outcomes, SourceOutcome{Source: source, Status: OutcomeSynced, GameName: game.Name})
	}
	return desired, outcomes, nil
}

// Reconcile computes what should be live for did — one status record per
// distinct game currently being played across all enabled sources, keyed by
// that game's own rkey — and diffs it against what's actually live on the
// PDS, writing new/changed entries and deleting anything no longer desired.
//
// Two sources reporting the same game resolve to the same rkey: the
// priority walk below lets only the first (highest-priority) source to
// claim a given rkey populate it, so same-game conflicts resolve exactly
// like the old single-record design did, just scoped per game instead of
// per account. A game switch (rkey A -> rkey B) needs no special case: A
// simply stops appearing in `desired` (so it's deleted) while B appears (so
// it's created).
//
// Call this after any change to any source's session_starts row, after
// enable/disable, after a priority reorder, and after a claim
// invalidation — nothing else should call Writer.PutStatus/DeleteStatus.
func (r *Reconciler) Reconcile(ctx context.Context, did string, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Started after the lock, not before: a call that waited its turn gets
	// its own full budget to work in, rather than being timed out by the
	// queue ahead of it.
	ctx, cancel := context.WithTimeout(ctx, reconcileTimeout)
	defer cancel()

	desired, outcomes, err := ComputeDesired(ctx, r.Conn, r.Resolver, did, now)
	if err != nil {
		return err
	}
	if r.Broadcaster != nil {
		r.Broadcaster.Publish(did, outcomes)
	}

	live, err := r.Writer.ListStatuses(ctx, did)
	if err != nil {
		return err
	}
	for _, entry := range live {
		if entry.Via != "" && entry.Via != ViaClientName {
			continue // written by a different client/instance sharing this collection — not ours to delete
		}
		if _, ok := desired[entry.Rkey]; !ok {
			if err := r.Writer.DeleteStatus(ctx, did, entry.Rkey); err != nil {
				return err
			}
		}
	}
	for rkey, status := range desired {
		if err := r.Writer.PutStatus(ctx, did, rkey, status); err != nil {
			return err
		}
	}
	return nil
}

// UpdateSession is the source-agnostic half of what tickOne used to do
// alone: given whether a source is currently playing something (and
// what), update that source's session_starts row via the existing pure
// Decide, then let the reconciler decide what's publicly shown. Steam's
// tick and Discord's presence handler both call this — it's the only
// place either one touches session_starts.
//
// rawName is the human-readable name the source itself reported (Steam's
// GameExtraInfo, Discord's Activity.Name) — persisted even when gameKey
// never resolves to a cartridge game, so ComputeDesired can still show an
// unmatched session's name instead of just its opaque key.
//
// extra is opaque per-source metadata (state/details/party — only Discord
// ever populates it; Steam passes the zero value) persisted alongside the
// session so Reconcile can read it back later, since Reconcile re-reads
// every enabled source's row fresh rather than receiving this call's data
// directly.
func UpdateSession(ctx context.Context, conn *sql.DB, reconciler *Reconciler, did, source string, playing bool, gameKey, rawName string, extra SessionExtra, now time.Time) error {
	var prev *SessionStart
	row, err := appdb.GetSessionStart(ctx, conn, did, source)
	if err != nil {
		return err
	}
	if row != nil {
		prev = &SessionStart{GameKey: row.GameKey, StartedAt: row.StartedAt}
	}

	decision := Decide(playing, gameKey, prev, now)
	switch decision.Action {
	case ActionDelete:
		if err := appdb.ClearSessionStart(ctx, conn, did, source); err != nil {
			return err
		}
	case ActionWrite:
		if err := appdb.SetSessionStart(ctx, conn, did, source, decision.GameKey, decision.CreatedAt); err != nil {
			return err
		}
		if err := appdb.SetSessionRawName(ctx, conn, did, source, rawName); err != nil {
			return err
		}
		extraJSON, err := json.Marshal(extra)
		if err != nil {
			return err
		}
		if err := appdb.SetSessionExtra(ctx, conn, did, source, string(extraJSON)); err != nil {
			return err
		}
	}
	return reconciler.Reconcile(ctx, did, now)
}

// parseAtURI splits an at:// URI into its did/collection/rkey parts. Used
// both to derive a status record's rkey from the game record it links to,
// and by ATProtoWriter.ListStatuses to read the rkey back off a listed
// record's own uri.
func parseAtURI(atURI string) (did, collection, rkey string, ok bool) {
	const prefix = "at://"
	if !strings.HasPrefix(atURI, prefix) {
		return "", "", "", false
	}
	parts := strings.SplitN(strings.TrimPrefix(atURI, prefix), "/", 3)
	if len(parts) != 3 {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}
