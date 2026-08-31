// internal/claims/discover.go
package claims

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/bluesky-social/indigo/api/agnostic"
	lexutil "github.com/bluesky-social/indigo/lex/util"

	appdb "github.com/jphastings/game-status/internal/db"
	"github.com/jphastings/game-status/internal/keytrace"
)

var supportedClaimTypes = []string{appdb.SteamSource, appdb.DiscordSource}

// SubjectResolver turns a verified Discord claim's signed username into the
// stable snowflake ID sync matches presence events against. ok=false means
// "not resolvable right now" (e.g. they haven't joined the tracking server
// yet) — not an error, and callers must not treat it as one.
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

type foundClaim struct {
	claim keytrace.Claim
	uri   string
}

// Discover re-scans the user's own dev.keytrace.claim collection for
// verified, cryptographically-checked claims of every supported type, and
// upserts/invalidates each type's row in claims accordingly. See spec's
// "Claim indexing" and the design doc's "Linking" section.
func Discover(ctx context.Context, client lexutil.LexClient, verifier *keytrace.Verifier, resolver SubjectResolver, conn *sql.DB, reconciler appdb.Reconciler, did string) error {
	found := map[string]foundClaim{}

	var cursor string
	for {
		resp, err := agnostic.RepoListRecords(ctx, client, keytrace.ClaimCollection, cursor, 100, did, false)
		if err != nil {
			return fmt.Errorf("list claim records: %w", err)
		}
		for _, rec := range resp.Records {
			var claim keytrace.Claim
			if err := json.Unmarshal(*rec.Value, &claim); err != nil {
				continue // skip malformed records rather than fail the whole scan
			}
			if claim.Status != "verified" || !isSupportedType(claim.Type) {
				continue
			}
			if _, already := found[claim.Type]; already {
				continue // first verified claim of each type wins
			}
			ok, err := verifier.VerifyAttestation(ctx, did, claim)
			if err != nil {
				return fmt.Errorf("verify claim %s: %w", rec.Uri, err)
			}
			if ok {
				found[claim.Type] = foundClaim{claim: claim, uri: rec.Uri}
			}
		}
		if resp.Cursor == nil || *resp.Cursor == "" {
			break
		}
		cursor = *resp.Cursor
	}

	for _, claimType := range supportedClaimTypes {
		fc, ok := found[claimType]
		if !ok {
			if err := appdb.InvalidateClaim(ctx, conn, reconciler, did, claimType, time.Now()); err != nil {
				return err
			}
			continue
		}

		subject := fc.claim.Identity.Subject
		if claimType == appdb.DiscordSource {
			existing, err := appdb.GetClaim(ctx, conn, did, appdb.DiscordSource)
			if err != nil {
				return err
			}
			if existing != nil {
				// already resolved once — only ever confirm that snowflake is
				// still theirs, never re-derive one from scratch (a released
				// username could since have been reclaimed by someone else).
				if !resolver.ConfirmDiscordSubject(ctx, fc.claim, existing.Subject) {
					if err := appdb.InvalidateClaim(ctx, conn, reconciler, did, claimType, time.Now()); err != nil {
						return err
					}
					continue
				}
				subject = existing.Subject
			} else {
				resolved, ok := resolver.ResolveDiscordSubject(ctx, did, fc.claim)
				if !ok {
					continue // verified, but not resolvable yet — leave any prior state alone
				}
				subject = resolved
			}
		}

		if err := appdb.UpsertClaim(ctx, conn, appdb.Claim{
			DID: did, Type: claimType, Subject: subject, DisplayName: fc.claim.Identity.DisplayName,
			ClaimURI: fc.claim.ClaimURI, RecordURI: fc.uri, LastVerifiedAt: time.Now(),
		}); err != nil {
			return err
		}
	}
	return nil
}

func isSupportedType(t string) bool {
	for _, s := range supportedClaimTypes {
		if s == t {
			return true
		}
	}
	return false
}
