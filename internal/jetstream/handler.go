// internal/jetstream/handler.go
package jetstream

import (
	"context"
	"encoding/json"
	"time"

	appdb "github.com/jphastings/game-status/internal/db"
	"github.com/jphastings/game-status/internal/keytrace"
)

type Operation string

const (
	OpCreate Operation = "create"
	OpUpdate Operation = "update"
	OpDelete Operation = "delete"
)

// supportedClaimTypes mirrors internal/claims's list of the same name.
// Duplicated rather than imported: it's two lines, and jetstream doesn't
// otherwise depend on the claims package.
var supportedClaimTypes = []string{appdb.SteamSource, appdb.DiscordSource}

func isSupportedType(t string) bool {
	for _, s := range supportedClaimTypes {
		if s == t {
			return true
		}
	}
	return false
}

// Event mirrors one Jetstream commit. Record is nil for deletes — Jetstream
// carries no record content on a delete, only did/collection/rkey, which is
// why delete matching below is by AT-URI, not by inspecting a `type` field.
type Event struct {
	DID        string
	Collection string
	Rkey       string
	Operation  Operation
	Record     []byte
}

type Store interface {
	GetClaim(ctx context.Context, did, claimType string) (*appdb.Claim, error)
	UpsertClaim(ctx context.Context, c appdb.Claim) error
	// InvalidateClaim is the whole undo — claim row, session bookkeeping and
	// the live status record on the user's PDS. See db.InvalidateClaim.
	InvalidateClaim(ctx context.Context, did, claimType string) error
}

// SubjectResolver turns a verified Discord claim's signed username into the
// stable snowflake ID sync matches presence events against. ok=false means
// "not resolvable right now" (e.g. they haven't joined the tracking server
// yet) — not an error, and callers must not treat it as one. Mirrors
// claims.SubjectResolver; kept as a separate, locally-defined interface for
// the same reason as the duplicated supportedClaimTypes above.
type SubjectResolver interface {
	ResolveDiscordSubject(ctx context.Context, did string, claim keytrace.Claim) (subject string, ok bool)

	// ConfirmDiscordSubject re-checks an ALREADY-RESOLVED snowflake ID
	// against a claim's signed subject on re-verification. It must never
	// return a DIFFERENT ID than the one it was asked to confirm — only
	// true (still theirs) or false (no longer theirs, invalidate) — so a
	// Discord username released and later reclaimed by someone else can't
	// silently re-point an existing verified claim at their account.
	ConfirmDiscordSubject(ctx context.Context, claim keytrace.Claim, currentSubject string) bool
}

func HandleEvent(ctx context.Context, store Store, verifier *keytrace.Verifier, resolver SubjectResolver, ev Event) error {
	if ev.Collection != keytrace.ClaimCollection {
		return nil
	}
	atURI := "at://" + ev.DID + "/" + ev.Collection + "/" + ev.Rkey

	if ev.Operation == OpDelete {
		return invalidateIfTracked(ctx, store, ev.DID, atURI)
	}

	var claim keytrace.Claim
	if err := json.Unmarshal(ev.Record, &claim); err != nil {
		return nil // malformed record — ignore rather than fail the whole listener
	}
	if !isSupportedType(claim.Type) {
		return nil
	}

	if claim.Status == "verified" {
		ok, err := verifier.VerifyAttestation(ctx, ev.DID, claim)
		if err != nil {
			return err
		}
		if !ok {
			return nil // fails crypto verification — don't trust it, but don't touch an unrelated existing claim either
		}

		subject := claim.Identity.Subject
		if claim.Type == appdb.DiscordSource {
			existing, err := store.GetClaim(ctx, ev.DID, appdb.DiscordSource)
			if err != nil {
				return err
			}
			if existing != nil {
				// already resolved once — only ever confirm that snowflake is
				// still theirs, never re-derive one from scratch (a released
				// username could since have been reclaimed by someone else).
				if !resolver.ConfirmDiscordSubject(ctx, claim, existing.Subject) {
					return invalidateIfTrackedType(ctx, store, ev.DID, appdb.DiscordSource, atURI)
				}
				subject = existing.Subject
			} else {
				resolved, ok := resolver.ResolveDiscordSubject(ctx, ev.DID, claim)
				if !ok {
					// verified, but not resolvable yet (e.g. hasn't joined the
					// tracking server) — leave any existing state alone, same as
					// claims.Discover's first-discovery handling.
					return nil
				}
				subject = resolved
			}
		}

		return store.UpsertClaim(ctx, appdb.Claim{
			DID: ev.DID, Type: claim.Type, Subject: subject, DisplayName: claim.Identity.DisplayName,
			ClaimURI: claim.ClaimURI, RecordURI: atURI, LastVerifiedAt: time.Now(),
		})
	}

	// failed/retracted — only invalidate if this is the specific record we're
	// tracking, so an unrelated old claim being updated can't knock out a good one.
	return invalidateIfTrackedType(ctx, store, ev.DID, claim.Type, atURI)
}

// invalidateIfTracked handles deletes, where Jetstream gives us no record
// content to read a claim type from — so it checks every supported type's
// current claim for a match on atURI.
func invalidateIfTracked(ctx context.Context, store Store, did, atURI string) error {
	for _, claimType := range supportedClaimTypes {
		if err := invalidateIfTrackedType(ctx, store, did, claimType, atURI); err != nil {
			return err
		}
	}
	return nil
}

func invalidateIfTrackedType(ctx context.Context, store Store, did, claimType, atURI string) error {
	current, err := store.GetClaim(ctx, did, claimType)
	if err != nil {
		return err
	}
	if current == nil || current.RecordURI != atURI {
		return nil
	}
	return store.InvalidateClaim(ctx, did, claimType)
}
