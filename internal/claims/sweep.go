// internal/claims/sweep.go
package claims

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/bluesky-social/indigo/api/agnostic"
	"github.com/bluesky-social/indigo/atproto/atclient"
	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/atproto/syntax"

	appdb "github.com/jphastings/game-status/internal/db"
	"github.com/jphastings/game-status/internal/keytrace"
)

type RecordFetcher interface {
	// FetchClaimRecord returns (nil, true, nil) if the record no longer
	// exists, (claim, false, nil) if it does, or a non-nil error for an
	// uncertain outcome (e.g. a network blip) that the caller should NOT
	// treat as deletion.
	FetchClaimRecord(ctx context.Context, atURI string) (claim *keytrace.Claim, deleted bool, err error)
}

type IndigoRecordFetcher struct{ Dir identity.Directory }

var _ RecordFetcher = IndigoRecordFetcher{}

func (f IndigoRecordFetcher) FetchClaimRecord(ctx context.Context, atURI string) (*keytrace.Claim, bool, error) {
	did, collection, rkey, ok := parseSweepAtURI(atURI)
	if !ok {
		return nil, false, fmt.Errorf("invalid claim at-uri: %s", atURI)
	}
	parsedDID, err := syntax.ParseDID(did)
	if err != nil {
		return nil, false, err
	}
	ident, err := f.Dir.LookupDID(ctx, parsedDID)
	if err != nil {
		return nil, false, err
	}

	client := atclient.NewAPIClient(ident.PDSEndpoint())
	resp, err := agnostic.RepoGetRecord(ctx, client, "", collection, did, rkey)
	if err != nil {
		if isRecordNotFoundSweep(err) {
			return nil, true, nil
		}
		return nil, false, err // uncertain — caller skips this pass rather than invalidating on a network blip
	}

	var c keytrace.Claim
	if err := json.Unmarshal(*resp.Value, &c); err != nil {
		return nil, false, err
	}
	return &c, false, nil
}

// isRecordNotFoundSweep — same caveat as internal/sync/writer.go's
// isRecordNotFound: indigo's exact error shape for this case wasn't
// confirmed while writing this plan; verify during implementation.
func isRecordNotFoundSweep(err error) bool {
	return err != nil && strings.Contains(err.Error(), "RecordNotFound")
}

func parseSweepAtURI(atURI string) (did, collection, rkey string, ok bool) {
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

// RunSweep re-verifies every enabled user's claim of every supported type
// once a day — pure reconciliation for whatever the Jetstream listener
// missed during a disconnect (spec: "daily sweep"), not the primary
// revocation mechanism.
func RunSweep(ctx context.Context, conn *sql.DB, fetcher RecordFetcher, verifier *keytrace.Verifier, resolver SubjectResolver, reconciler appdb.Reconciler) error {
	for _, claimType := range supportedClaimTypes {
		dids, err := appdb.ListEnabledDIDs(ctx, conn, claimType)
		if err != nil {
			return err
		}

		for _, did := range dids {
			claim, err := appdb.GetClaim(ctx, conn, did, claimType)
			if err != nil {
				return err
			}
			if claim == nil {
				continue
			}

			c, deleted, err := fetcher.FetchClaimRecord(ctx, claim.RecordURI)
			if err != nil {
				continue // uncertain outcome — try again on tomorrow's sweep
			}
			if deleted || c.Status != "verified" {
				if err := appdb.InvalidateClaim(ctx, conn, reconciler, did, claimType, time.Now()); err != nil {
					return err
				}
				continue
			}

			ok, err := verifier.VerifyAttestation(ctx, did, *c)
			if err != nil {
				continue
			}
			if !ok {
				if err := appdb.InvalidateClaim(ctx, conn, reconciler, did, claimType, time.Now()); err != nil {
					return err
				}
				continue
			}

			subject := c.Identity.Subject
			if claimType == appdb.DiscordSource {
				resolved, ok := resolver.ResolveDiscordSubject(ctx, did, *c)
				if !ok {
					// unlike first discovery, this is a regression from an
					// already-resolved state — invalidate rather than leave stale.
					if err := appdb.InvalidateClaim(ctx, conn, reconciler, did, claimType, time.Now()); err != nil {
						return err
					}
					continue
				}
				subject = resolved
			}

			// The event this sweep exists to catch may have been a missed *update*
			// — a re-verification against a different subject at the same record.
			// Confirming the signature isn't enough; without this we'd keep polling
			// the old subject and publish someone else's play state to this user.
			if err := appdb.UpsertClaim(ctx, conn, appdb.Claim{
				DID: did, Type: claimType, Subject: subject, DisplayName: c.Identity.DisplayName,
				ClaimURI: c.ClaimURI, RecordURI: claim.RecordURI, LastVerifiedAt: time.Now(),
			}); err != nil {
				return err
			}
		}
	}
	return nil
}
