// internal/keytrace/keyfetch.go
package keytrace

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bluesky-social/indigo/api/agnostic"
	"github.com/bluesky-social/indigo/atproto/atclient"
	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/atproto/syntax"

	appdb "github.com/jphastings/game-status/internal/db"
)

type CachedKeyFetcher struct {
	Dir  identity.Directory
	Conn *sql.DB
}

var _ KeyFetcher = (*CachedKeyFetcher)(nil)

type serverKeyRecord struct {
	PublicJWK string `json:"publicJwk"`
}

func (f *CachedKeyFetcher) FetchPublicJWK(ctx context.Context, keyURI string) (string, error) {
	if cached, err := appdb.GetKeytraceKey(ctx, f.Conn, keyURI); err != nil {
		return "", err
	} else if cached != nil {
		return cached.PublicJWK, nil
	}

	did, collection, rkey, ok := parseAtURI(keyURI)
	if !ok {
		return "", fmt.Errorf("invalid key at-uri: %s", keyURI)
	}

	parsedDID, err := syntax.ParseDID(did)
	if err != nil {
		return "", fmt.Errorf("parse did: %w", err)
	}
	ident, err := f.Dir.LookupDID(ctx, parsedDID)
	if err != nil {
		return "", fmt.Errorf("resolve signer did: %w", err)
	}

	client := atclient.NewAPIClient(ident.PDSEndpoint())
	resp, err := agnostic.RepoGetRecord(ctx, client, "", collection, did, rkey)
	if err != nil {
		return "", fmt.Errorf("fetch key record: %w", err)
	}

	var rec serverKeyRecord
	if err := json.Unmarshal(*resp.Value, &rec); err != nil {
		return "", fmt.Errorf("parse key record: %w", err)
	}

	if err := appdb.SetKeytraceKey(ctx, f.Conn, appdb.KeytraceKey{AtURI: keyURI, PublicJWK: rec.PublicJWK}); err != nil {
		return "", err
	}
	return rec.PublicJWK, nil
}

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
