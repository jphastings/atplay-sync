// internal/sync/writer.go
package sync

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/bluesky-social/indigo/api/agnostic"
	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/atclient"
	"github.com/bluesky-social/indigo/atproto/atdata"
	"github.com/bluesky-social/indigo/atproto/auth/oauth"
	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/atproto/syntax"

	"github.com/jphastings/game-status/internal/atsession"
	appdb "github.com/jphastings/game-status/internal/db"
)

type ATProtoWriter struct {
	Resumer *atsession.Resumer
	Conn    *sql.DB
	Dir     identity.Directory
}

var _ RecordWriter = (*ATProtoWriter)(nil)

func (w *ATProtoWriter) withClient(ctx context.Context, did string, fn func(*atclient.APIClient) error) error {
	user, err := appdb.GetUser(ctx, w.Conn, did)
	if err != nil {
		return err
	}
	if user == nil || user.ActiveSessionID == "" {
		return fmt.Errorf("no active session for %s", did)
	}
	parsedDID, err := syntax.ParseDID(did)
	if err != nil {
		return err
	}
	return w.Resumer.WithSession(ctx, parsedDID, user.ActiveSessionID, func(sess *oauth.ClientSession) error {
		return fn(sess.APIClient())
	})
}

func (w *ATProtoWriter) PutStatus(ctx context.Context, did, rkey string, status ActorStatus) error {
	raw, err := json.Marshal(status)
	if err != nil {
		return err
	}
	record, err := atdata.UnmarshalJSON(raw)
	if err != nil {
		return fmt.Errorf("validate status record: %w", err)
	}
	return w.withClient(ctx, did, func(client *atclient.APIClient) error {
		_, err := agnostic.RepoPutRecord(ctx, client, &agnostic.RepoPutRecord_Input{
			Collection: StatusCollection, Repo: did, Rkey: rkey, Record: record,
		})
		return err
	})
}

func (w *ATProtoWriter) DeleteStatus(ctx context.Context, did, rkey string) error {
	return w.withClient(ctx, did, func(client *atclient.APIClient) error {
		_, err := comatproto.RepoDeleteRecord(ctx, client, &comatproto.RepoDeleteRecord_Input{
			Collection: StatusCollection, Repo: did, Rkey: rkey,
		})
		if err != nil && isRecordNotFound(err) {
			return nil // idempotent — deleting an already-gone record is success (Global Constraints)
		}
		return err
	})
}

// statusListLimit is comfortably above any plausible number of live status
// records for one account — far more than this app's own desired set could
// ever produce (bounded by enabled source count), with headroom left for
// another self-hosted instance's records to sit alongside them too. No
// cursor pagination; anything beyond this page simply wouldn't be diffed or
// swept.
// ponytail: fixed page, no pagination loop; revisit if that bound changes.
const statusListLimit = 100

// ListStatuses reads every games.atmosphere.status record currently live
// for did straight off their PDS — the source of truth Reconcile diffs
// against, and RunStatusSweep checks for expiry.
//
// Deliberately unauthenticated, like internal/keytrace/keyfetch.go and
// internal/claims/sweep.go's own reads: com.atproto.repo.listRecords is a
// public, open-CORS endpoint on the reference PDS implementation (see
// CLAUDE.md's atproto facts), and this app's OAuth scopes only ever request
// create/update/delete on this collection, not read — going unauthenticated
// sidesteps depending on an unconfirmed "action=read" scope entirely, and
// avoids a session resume on every reconcile.
func (w *ATProtoWriter) ListStatuses(ctx context.Context, did string) ([]StatusEntry, error) {
	parsedDID, err := syntax.ParseDID(did)
	if err != nil {
		return nil, err
	}
	ident, err := w.Dir.LookupDID(ctx, parsedDID)
	if err != nil {
		return nil, err
	}
	client := atclient.NewAPIClient(ident.PDSEndpoint())

	out, err := agnostic.RepoListRecords(ctx, client, StatusCollection, "", statusListLimit, did, false)
	if err != nil {
		return nil, err
	}

	var entries []StatusEntry
	for _, rec := range out.Records {
		_, _, rkey, ok := parseAtURI(rec.Uri)
		if !ok {
			continue // malformed uri — not something we can diff against or clean up
		}
		var v struct {
			StaleAt string `json:"staleAt"`
			Via     string `json:"via"`
		}
		if err := json.Unmarshal(*rec.Value, &v); err != nil {
			slog.Warn("skipping unparseable status record", "uri", rec.Uri, "err", err)
			continue // one bad record shouldn't block every other status this account has
		}
		staleAt, err := time.Parse(time.RFC3339, v.StaleAt)
		if err != nil {
			slog.Warn("skipping status record with unparseable staleAt", "uri", rec.Uri, "err", err)
			continue
		}
		entries = append(entries, StatusEntry{Rkey: rkey, StaleAt: staleAt, Via: v.Via})
	}
	return entries, nil
}

// isRecordNotFound checks indigo's actual XRPC error type. Confirmed by
// reading atclient.APIClient.LexDo (atproto/atclient/lexclient.go): a
// non-2xx response body is decoded as {"error": <name>, "message": <msg>}
// and returned as *atclient.APIError{Name: <name>}, so we match on that
// typed field rather than substring-matching the formatted error text.
//
// Empirically, though, this is a defensive fallback rather than the primary
// idempotency mechanism: com.atproto.repo.deleteRecord's own lexicon says
// "Delete a repository record, or ensure it doesn't exist" and declares no
// RecordNotFound error (only InvalidSwap), and the reference PDS
// implementation (bluesky-social/atproto packages/pds
// src/api/com/atproto/repo/deleteRecord.ts) returns a plain 200 success
// no-op when the record is already gone — it never emits this error at all.
// This check exists in case a non-reference PDS behaves differently.
func isRecordNotFound(err error) bool {
	var apiErr *atclient.APIError
	return errors.As(err, &apiErr) && apiErr.Name == "RecordNotFound"
}
