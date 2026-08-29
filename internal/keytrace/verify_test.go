// internal/keytrace/verify_test.go
package keytrace

import (
	"context"
	"testing"
)

const realClaimDID = "did:plc:ephkzpinhaqcabtkugtbzrwu"
const realSignerDID = "did:plc:hcwfdlmprcc335oixyfsw7u3"
const realKeyJWK = `{"kty":"EC","x":"pretZ8lN1snAV4dNoyet54BTTs1-Mxv4-jNuVGazf8g","y":"wUT9JvxuvkRtPrufb6c4BPXoA60LhmvfaE_aH5d6A-o","crv":"P-256"}`

var realClaim = Claim{
	Type:     "steam",
	Status:   "verified",
	ClaimURI: "https://steamcommunity.com/profiles/76561197994000231",
	Identity: ClaimIdentity{Subject: "76561197994000231"},
	Sigs: []ClaimSignature{{
		Kid:          "attest:steam",
		Src:          "at://" + realSignerDID + "/dev.keytrace.serverPublicKey/2026-05-03",
		SignedAt:     "2026-05-03T07:53:39.639Z",
		Attestation:  "eyJhbGciOiJFUzI1NiIsInR5cCI6IkpXVCJ9.eyJjbGFpbVVyaSI6Imh0dHBzOi8vc3RlYW1jb21tdW5pdHkuY29tL3Byb2ZpbGVzLzc2NTYxMTk3OTk0MDAwMjMxIiwiZGlkIjoiZGlkOnBsYzplcGhrenBpbmhhcWNhYnRrdWd0Ynpyd3UiLCJpZGVudGl0eS5zdWJqZWN0IjoiNzY1NjExOTc5OTQwMDAyMzEiLCJ0eXBlIjoic3RlYW0ifQ.69NGYohaBKTQFtWoRPrqeIOIZN72Q7eEhESF2EPaQLRUfnFioQ3vtGWHsmSSEO5m8_7vd6UU347AlwcafaBBGA",
		SignedFields: []string{"claimUri", "did", "identity.subject", "type"},
	}},
}

type fakeKeyFetcher struct{ jwk string }

func (f fakeKeyFetcher) FetchPublicJWK(ctx context.Context, keyURI string) (string, error) {
	return f.jwk, nil
}

func TestVerifyAttestation_RealKeytraceClaim(t *testing.T) {
	v := &Verifier{Keys: fakeKeyFetcher{jwk: realKeyJWK}, TrustedDIDs: map[string]bool{realSignerDID: true}}
	ok, err := v.VerifyAttestation(context.Background(), realClaimDID, realClaim)
	if err != nil {
		t.Fatalf("VerifyAttestation: %v", err)
	}
	if !ok {
		t.Fatal("expected the real captured signature to verify, it did not")
	}
}

func TestVerifyAttestation_RejectsUntrustedSigner(t *testing.T) {
	v := &Verifier{Keys: fakeKeyFetcher{jwk: realKeyJWK}, TrustedDIDs: map[string]bool{"did:plc:someone-else": true}}
	ok, err := v.VerifyAttestation(context.Background(), realClaimDID, realClaim)
	if err != nil {
		t.Fatalf("VerifyAttestation: %v", err)
	}
	if ok {
		t.Fatal("expected verification to fail for an untrusted signer, it passed")
	}
}

// The claimant controls the attestation blob, so mangling it must reject the
// claim outright. If it read as an error instead, every caller would treat it
// as "uncertain, retry" and a revoked claim could be kept alive indefinitely
// by corrupting it.
func TestVerifyAttestation_MalformedAttestationRejectsRatherThanErrors(t *testing.T) {
	for name, attestation := range map[string]string{
		"wrong part count":     "onlyonepart",
		"non-base64 payload":   "aGVhZGVy.!!!not-base64!!!.c2ln",
		"short signature":      "aGVhZGVy.eyJjbGFpbVVyaSI6Imh0dHBzOi8vc3RlYW1jb21tdW5pdHkuY29tL3Byb2ZpbGVzLzc2NTYxMTk3OTk0MDAwMjMxIiwiZGlkIjoiZGlkOnBsYzplcGhrenBpbmhhcWNhYnRrdWd0Ynpyd3UiLCJpZGVudGl0eS5zdWJqZWN0IjoiNzY1NjExOTc5OTQwMDAyMzEiLCJ0eXBlIjoic3RlYW0ifQ.c2hvcnQ",
		"non-base64 signature": "aGVhZGVy.eyJjbGFpbVVyaSI6Imh0dHBzOi8vc3RlYW1jb21tdW5pdHkuY29tL3Byb2ZpbGVzLzc2NTYxMTk3OTk0MDAwMjMxIiwiZGlkIjoiZGlkOnBsYzplcGhrenBpbmhhcWNhYnRrdWd0Ynpyd3UiLCJpZGVudGl0eS5zdWJqZWN0IjoiNzY1NjExOTc5OTQwMDAyMzEiLCJ0eXBlIjoic3RlYW0ifQ.!!!",
	} {
		t.Run(name, func(t *testing.T) {
			corrupted := realClaim
			corrupted.Sigs = []ClaimSignature{realClaim.Sigs[0]}
			corrupted.Sigs[0].Attestation = attestation

			v := &Verifier{Keys: fakeKeyFetcher{jwk: realKeyJWK}, TrustedDIDs: map[string]bool{realSignerDID: true}}
			ok, err := v.VerifyAttestation(context.Background(), realClaimDID, corrupted)
			if err != nil {
				t.Fatalf("err = %v, want nil (claimant-controlled data must reject, not read as uncertain)", err)
			}
			if ok {
				t.Fatal("expected a corrupted attestation to fail verification")
			}
		})
	}
}

// The signing key comes from keytrace's own repo. A problem there is not
// evidence against the user's claim, so it must read as uncertain.
func TestVerifyAttestation_UnusableSigningKeyIsAnError(t *testing.T) {
	v := &Verifier{Keys: fakeKeyFetcher{jwk: `{"kty":"oops"}`}, TrustedDIDs: map[string]bool{realSignerDID: true}}
	ok, err := v.VerifyAttestation(context.Background(), realClaimDID, realClaim)
	if err == nil {
		t.Fatal("expected an unusable keytrace signing key to be reported as an error, got nil")
	}
	if ok {
		t.Fatal("expected ok = false alongside the error")
	}
}

func TestVerifyAttestation_RejectsSubstitutedSubject(t *testing.T) {
	// The spoofing scenario this task exists to close: an attacker with a
	// genuinely different, unrelated claim can't just paste in someone else's
	// SteamID — the signature covers identity.subject, so tampering breaks it.
	tampered := realClaim
	tampered.Identity = ClaimIdentity{Subject: "1"}

	v := &Verifier{Keys: fakeKeyFetcher{jwk: realKeyJWK}, TrustedDIDs: map[string]bool{realSignerDID: true}}
	ok, err := v.VerifyAttestation(context.Background(), realClaimDID, tampered)
	if err != nil {
		t.Fatalf("VerifyAttestation: %v", err)
	}
	if ok {
		t.Fatal("expected verification to fail for a substituted identity.subject, it passed")
	}
}
