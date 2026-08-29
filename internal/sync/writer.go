// internal/sync/writer.go
package sync

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/api/agnostic"
	"github.com/bluesky-social/indigo/atproto/atclient"
	"github.com/bluesky-social/indigo/atproto/atdata"
	"github.com/bluesky-social/indigo/atproto/auth/oauth"
	"github.com/bluesky-social/indigo/atproto/syntax"

	appdb "github.com/jphastings/game-status/internal/db"
)

type ATProtoWriter struct {
	App  *oauth.ClientApp
	Conn *sql.DB
}

var _ RecordWriter = (*ATProtoWriter)(nil)

func (w *ATProtoWriter) client(ctx context.Context, did string) (*atclient.APIClient, error) {
	user, err := appdb.GetUser(ctx, w.Conn, did)
	if err != nil {
		return nil, err
	}
	if user == nil || user.ActiveSessionID == "" {
		return nil, fmt.Errorf("no active session for %s", did)
	}
	parsedDID, err := syntax.ParseDID(did)
	if err != nil {
		return nil, err
	}
	sess, err := w.App.ResumeSession(ctx, parsedDID, user.ActiveSessionID)
	if err != nil {
		return nil, err
	}
	return sess.APIClient(), nil
}

func (w *ATProtoWriter) PutStatus(ctx context.Context, did string, status ActorStatus) error {
	client, err := w.client(ctx, did)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(status)
	if err != nil {
		return err
	}
	record, err := atdata.UnmarshalJSON(raw)
	if err != nil {
		return fmt.Errorf("validate status record: %w", err)
	}
	validate := true
	_, err = agnostic.RepoPutRecord(ctx, client, &agnostic.RepoPutRecord_Input{
		Collection: StatusCollection, Repo: did, Rkey: statusRkey, Record: record, Validate: &validate,
	})
	return err
}

func (w *ATProtoWriter) DeleteStatus(ctx context.Context, did string) error {
	client, err := w.client(ctx, did)
	if err != nil {
		return err
	}
	_, err = comatproto.RepoDeleteRecord(ctx, client, &comatproto.RepoDeleteRecord_Input{
		Collection: StatusCollection, Repo: did, Rkey: statusRkey,
	})
	if err != nil && isRecordNotFound(err) {
		return nil // idempotent — deleting an already-gone record is success (Global Constraints)
	}
	return err
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
