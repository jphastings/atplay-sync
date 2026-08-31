// internal/sync/writer.go
package sync

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/bluesky-social/indigo/api/agnostic"
	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/atclient"
	"github.com/bluesky-social/indigo/atproto/atdata"
	"github.com/bluesky-social/indigo/atproto/auth/oauth"
	"github.com/bluesky-social/indigo/atproto/syntax"

	"github.com/jphastings/game-status/internal/atsession"
	appdb "github.com/jphastings/game-status/internal/db"
)

type ATProtoWriter struct {
	Resumer *atsession.Resumer
	Conn    *sql.DB
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

// statusListLimit is comfortably above how many status records one account
// could ever have live at once (bounded by enabled source count).
// ponytail: fixed page, no cursor pagination loop; revisit if that bound changes.
const statusListLimit = 100

// ListStatuses reads every games.atmosphere.status record currently live
// for did straight off their PDS — the source of truth Reconcile diffs
// against, and RunStatusSweep checks for expiry.
func (w *ATProtoWriter) ListStatuses(ctx context.Context, did string) ([]StatusEntry, error) {
	var entries []StatusEntry
	err := w.withClient(ctx, did, func(client *atclient.APIClient) error {
		out, err := agnostic.RepoListRecords(ctx, client, StatusCollection, "", statusListLimit, did, false)
		if err != nil {
			return err
		}
		for _, rec := range out.Records {
			_, _, rkey, ok := parseAtURI(rec.Uri)
			if !ok {
				continue
			}
			var v struct {
				StaleAt string `json:"staleAt"`
			}
			if err := json.Unmarshal(*rec.Value, &v); err != nil {
				return fmt.Errorf("parse status record %s: %w", rec.Uri, err)
			}
			staleAt, err := time.Parse(time.RFC3339, v.StaleAt)
			if err != nil {
				return fmt.Errorf("parse staleAt for %s: %w", rec.Uri, err)
			}
			entries = append(entries, StatusEntry{Rkey: rkey, StaleAt: staleAt})
		}
		return nil
	})
	return entries, err
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
