// internal/keytrace/verifier_test.go
package keytrace

import (
	"reflect"
	"testing"
)

func TestReconstructSignedData_CreatedAtUsesSignedAt(t *testing.T) {
	claim := Claim{
		Type:      "steam",
		CreatedAt: "2020-01-01T00:00:00.000Z",
		Identity:  ClaimIdentity{Subject: "76561197994000231"},
	}
	sig := ClaimSignature{
		SignedAt:     "2026-05-03T07:53:39.639Z",
		SignedFields: []string{"identity.subject", "createdAt"},
	}

	got, ok := reconstructSignedData("did:plc:abc", claim, sig)
	if !ok {
		t.Fatal("expected reconstructSignedData to succeed")
	}
	if got["createdAt"] != sig.SignedAt {
		t.Fatalf("createdAt = %q, want sig.SignedAt %q", got["createdAt"], sig.SignedAt)
	}
	if got["createdAt"] == claim.CreatedAt {
		t.Fatalf("createdAt must not equal claim.CreatedAt (%q)", claim.CreatedAt)
	}
}

func TestReconstructSignedData_LegacyFormat(t *testing.T) {
	claim := Claim{
		Type:     "steam",
		Identity: ClaimIdentity{Subject: "76561197994000231"},
	}
	sig := ClaimSignature{
		SignedAt:     "2026-05-03T07:53:39.639Z",
		SignedFields: []string{"did", "subject", "type", "verifiedAt"},
	}

	got, ok := reconstructSignedData("did:plc:abc", claim, sig)
	if !ok {
		t.Fatal("expected reconstructSignedData to succeed")
	}
	want := map[string]string{
		"did":        "did:plc:abc",
		"subject":    claim.Identity.Subject,
		"type":       claim.Type,
		"verifiedAt": sig.SignedAt,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestReconstructSignedData_IdentityAccount(t *testing.T) {
	claim := Claim{
		Type:     "steam",
		Identity: ClaimIdentity{Subject: "76561197994000231", Account: "somehandle"},
	}
	sig := ClaimSignature{
		SignedAt:     "2026-05-03T07:53:39.639Z",
		SignedFields: []string{"identity.subject", "identity.account"},
	}

	got, ok := reconstructSignedData("did:plc:abc", claim, sig)
	if !ok {
		t.Fatal("expected reconstructSignedData to succeed")
	}
	if got["identity.account"] != claim.Identity.Account {
		t.Fatalf("identity.account = %q, want %q", got["identity.account"], claim.Identity.Account)
	}

	sigWithoutAccount := ClaimSignature{
		SignedAt:     "2026-05-03T07:53:39.639Z",
		SignedFields: []string{"identity.subject"},
	}
	got, ok = reconstructSignedData("did:plc:abc", claim, sigWithoutAccount)
	if !ok {
		t.Fatal("expected reconstructSignedData to succeed")
	}
	if _, present := got["identity.account"]; present {
		t.Fatalf("identity.account should be absent when not in SignedFields, got %v", got)
	}
}
