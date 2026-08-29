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

// Discover re-scans the user's own dev.keytrace.claim collection for a
// verified, cryptographically-checked Steam claim and upserts/clears
// steam_claims accordingly. See spec's "Claim indexing".
func Discover(ctx context.Context, client lexutil.LexClient, verifier *keytrace.Verifier, conn *sql.DB, did string) error {
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
			if claim.Type != "steam" || claim.Status != "verified" {
				continue
			}
			ok, err := verifier.VerifyAttestation(ctx, did, claim)
			if err != nil {
				return fmt.Errorf("verify claim %s: %w", rec.Uri, err)
			}
			if !ok {
				continue
			}
			return appdb.UpsertSteamClaim(ctx, conn, appdb.SteamClaim{
				DID: did, Subject: claim.Identity.Subject, DisplayName: claim.Identity.DisplayName,
				ClaimURI: claim.ClaimURI, RecordURI: rec.Uri, LastVerifiedAt: time.Now(),
			})
		}

		if resp.Cursor == nil || *resp.Cursor == "" {
			break
		}
		cursor = *resp.Cursor
	}

	// No verified Steam claim found this pass — whatever we had before is stale.
	return appdb.InvalidateSteamClaim(ctx, conn, did)
}
