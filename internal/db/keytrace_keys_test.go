package db

import (
	"context"
	"testing"
)

func TestSetKeytraceKey_RoundTrip(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)

	key := KeytraceKey{
		AtURI:      "at://did:plc:signer/dev.keytrace.serverPublicKey/2026-05-03",
		PublicJWK:  `{"kty":"EC","crv":"P-256","x":"abc","y":"def"}`,
		ValidFrom:  "2026-05-03T00:00:00Z",
		ValidUntil: "2026-05-04T00:00:00Z",
	}

	if err := SetKeytraceKey(ctx, conn, key); err != nil {
		t.Fatalf("SetKeytraceKey: %v", err)
	}

	got, err := GetKeytraceKey(ctx, conn, key.AtURI)
	if err != nil {
		t.Fatalf("GetKeytraceKey: %v", err)
	}
	if got == nil {
		t.Fatalf("got nil, want KeytraceKey")
	}
	if got.AtURI != key.AtURI || got.PublicJWK != key.PublicJWK || got.ValidFrom != key.ValidFrom || got.ValidUntil != key.ValidUntil {
		t.Fatalf("got %+v, want %+v", got, key)
	}
}

func TestGetKeytraceKey_MissingReturnsNil(t *testing.T) {
	got, err := GetKeytraceKey(context.Background(), openTestDB(t), "at://did:plc:nobody/dev.keytrace.serverPublicKey/none")
	if err != nil {
		t.Fatalf("GetKeytraceKey: %v", err)
	}
	if got != nil {
		t.Fatalf("got %+v, want nil", got)
	}
}

func TestSetKeytraceKey_DoesNotOverwriteExistingEntry(t *testing.T) {
	// Keys are cached forever (no TTL) because a record at a given AT-URI is
	// immutable once published, so a second Set for the same URI must be a no-op.
	ctx := context.Background()
	conn := openTestDB(t)
	atURI := "at://did:plc:signer/dev.keytrace.serverPublicKey/2026-05-03"

	if err := SetKeytraceKey(ctx, conn, KeytraceKey{AtURI: atURI, PublicJWK: "original", ValidFrom: "a", ValidUntil: "b"}); err != nil {
		t.Fatalf("first SetKeytraceKey: %v", err)
	}
	if err := SetKeytraceKey(ctx, conn, KeytraceKey{AtURI: atURI, PublicJWK: "different", ValidFrom: "x", ValidUntil: "y"}); err != nil {
		t.Fatalf("second SetKeytraceKey: %v", err)
	}

	got, err := GetKeytraceKey(ctx, conn, atURI)
	if err != nil {
		t.Fatalf("GetKeytraceKey: %v", err)
	}
	if got == nil || got.PublicJWK != "original" {
		t.Fatalf("got %+v, want PublicJWK=original (first write preserved)", got)
	}
}
