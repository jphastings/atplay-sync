// internal/keytrace/keyfetch_test.go
package keytrace

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/atproto/syntax"

	appdb "github.com/jphastings/game-status/internal/db"
)

// fakeDirectory resolves every DID to one PDS: the test server.
type fakeDirectory struct{ pds string }

func (d fakeDirectory) LookupDID(ctx context.Context, did syntax.DID) (*identity.Identity, error) {
	return &identity.Identity{
		DID:      did,
		Services: map[string]identity.ServiceEndpoint{"atproto_pds": {Type: "AtprotoPersonalDataServer", URL: d.pds}},
	}, nil
}
func (d fakeDirectory) LookupHandle(context.Context, syntax.Handle) (*identity.Identity, error) {
	panic("not used")
}
func (d fakeDirectory) Lookup(context.Context, syntax.AtIdentifier) (*identity.Identity, error) {
	panic("not used")
}
func (d fakeDirectory) Purge(context.Context, syntax.AtIdentifier) error { return nil }

const testKeyURI = "at://" + realSignerDID + "/" + ServerKeyCollection + "/2026-05-03"

func testFetcher(t *testing.T, recordJSON string) (*CachedKeyFetcher, *sql.DB) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"uri":"` + testKeyURI + `","cid":"bafy","value":` + recordJSON + `}`))
	}))
	t.Cleanup(srv.Close)

	conn, err := appdb.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	return &CachedKeyFetcher{Dir: fakeDirectory{pds: srv.URL}, Conn: conn}, conn
}

func TestFetchPublicJWK_ParsesAndCachesTheRecord(t *testing.T) {
	f, conn := testFetcher(t, `{"publicJwk":`+quoteJSON(realKeyJWK)+`}`)

	got, err := f.FetchPublicJWK(context.Background(), testKeyURI)
	if err != nil {
		t.Fatalf("FetchPublicJWK: %v", err)
	}
	if got != realKeyJWK {
		t.Fatalf("got %q, want %q", got, realKeyJWK)
	}

	cached, err := appdb.GetKeytraceKey(context.Background(), conn, testKeyURI)
	if err != nil || cached == nil || cached.PublicJWK != realKeyJWK {
		t.Fatalf("cached = %+v, %v; want the fetched key", cached, err)
	}
}

// The cache has no TTL and never overwrites, so a key that came back unusable
// must not be written: one bad fetch would otherwise break this URI forever.
func TestFetchPublicJWK_DoesNotCacheAnUnusableRecord(t *testing.T) {
	for name, record := range map[string]string{
		"wrong field name": `{"public_jwk":"whatever"}`,
		"empty value":      `{"publicJwk":""}`,
	} {
		t.Run(name, func(t *testing.T) {
			f, conn := testFetcher(t, record)

			if _, err := f.FetchPublicJWK(context.Background(), testKeyURI); err == nil {
				t.Fatal("expected an error for a record with no usable publicJwk")
			}
			cached, err := appdb.GetKeytraceKey(context.Background(), conn, testKeyURI)
			if err != nil {
				t.Fatalf("GetKeytraceKey: %v", err)
			}
			if cached != nil {
				t.Fatalf("cached %+v, want nothing written", cached)
			}
		})
	}
}

func TestFetchPublicJWK_RejectsNonKeyCollection(t *testing.T) {
	f, _ := testFetcher(t, `{"publicJwk":"irrelevant"}`)

	_, err := f.FetchPublicJWK(context.Background(), "at://"+realSignerDID+"/dev.keytrace.claim/abc")
	if err == nil {
		t.Fatal("expected a record outside the signing-key collection to be refused")
	}
}

func quoteJSON(s string) string {
	raw, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(raw)
}
