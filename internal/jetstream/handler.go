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
	GetSteamClaim(ctx context.Context, did string) (*appdb.SteamClaim, error)
	UpsertSteamClaim(ctx context.Context, c appdb.SteamClaim) error
	// InvalidateClaim is the whole undo — claim row, session bookkeeping and
	// the live status record on the user's PDS. See db.InvalidateClaim.
	InvalidateClaim(ctx context.Context, did string) error
}

func HandleEvent(ctx context.Context, store Store, verifier *keytrace.Verifier, ev Event) error {
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
	if claim.Type != "steam" {
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
		return store.UpsertSteamClaim(ctx, appdb.SteamClaim{
			DID: ev.DID, Subject: claim.Identity.Subject, DisplayName: claim.Identity.DisplayName,
			ClaimURI: claim.ClaimURI, RecordURI: atURI, LastVerifiedAt: time.Now(),
		})
	}

	// failed/retracted — only invalidate if this is the specific record we're
	// tracking, so an unrelated old claim being updated can't knock out a good one.
	return invalidateIfTracked(ctx, store, ev.DID, atURI)
}

func invalidateIfTracked(ctx context.Context, store Store, did, atURI string) error {
	current, err := store.GetSteamClaim(ctx, did)
	if err != nil {
		return err
	}
	if current == nil || current.RecordURI != atURI {
		return nil
	}
	return store.InvalidateClaim(ctx, did)
}
