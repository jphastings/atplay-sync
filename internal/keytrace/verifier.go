// internal/keytrace/verifier.go
package keytrace

import (
	"context"
	"fmt"
)

// KeyFetcher resolves a keytrace signing-key AT-URI (sig.src) to the raw
// publicJwk JSON string it contains.
type KeyFetcher interface {
	FetchPublicJWK(ctx context.Context, keyURI string) (string, error)
}

type Verifier struct {
	Keys        KeyFetcher
	TrustedDIDs map[string]bool
}

// VerifyAttestation checks that the identity binding on a claim (did, type,
// identity.subject, claimUri) was genuinely attested by a trusted keytrace
// signer. See this task's header note for why only the attest:* signature is
// checked, not `status`.
func (v *Verifier) VerifyAttestation(ctx context.Context, did string, claim Claim) (bool, error) {
	sig, ok := primarySig(claim)
	if !ok || sig.Src == "" || sig.Attestation == "" || sig.SignedAt == "" {
		return false, nil
	}

	signerDID, ok := didFromAtURI(sig.Src)
	if !ok || !v.TrustedDIDs[signerDID] {
		return false, nil
	}

	rawJWK, err := v.Keys.FetchPublicJWK(ctx, sig.Src)
	if err != nil {
		return false, fmt.Errorf("fetch signing key: %w", err)
	}
	pub, err := parsePublicJWK(rawJWK)
	if err != nil {
		return false, nil // malformed key -> bad claim, not a transient error
	}

	signedData, ok := reconstructSignedData(did, claim, sig)
	if !ok {
		return false, nil
	}
	return verifyJWS(signedData, sig.Attestation, pub)
}

func reconstructSignedData(did string, claim Claim, sig ClaimSignature) (map[string]string, bool) {
	isNewFormat := containsString(sig.SignedFields, "identity.subject")
	if !isNewFormat {
		// Legacy format: { did, subject, type, verifiedAt }
		return map[string]string{"did": did, "subject": claim.Identity.Subject, "type": claim.Type, "verifiedAt": sig.SignedAt}, true
	}

	values := map[string]string{
		"claimUri":         claim.ClaimURI,
		"createdAt":        sig.SignedAt, // signed at attestation time, NOT claim.CreatedAt
		"did":              did,
		"identity.subject": claim.Identity.Subject,
		"type":             claim.Type,
	}
	if claim.Identity.Account != "" {
		values["identity.account"] = claim.Identity.Account
	}

	signed := make(map[string]string, len(sig.SignedFields))
	for _, field := range sig.SignedFields {
		val, ok := values[field]
		if !ok {
			return nil, false // signer covered a field we can't reconstruct -> fail closed
		}
		signed[field] = val
	}
	return signed, true
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func didFromAtURI(atURI string) (string, bool) {
	const prefix = "at://"
	if len(atURI) <= len(prefix) || atURI[:len(prefix)] != prefix {
		return "", false
	}
	rest := atURI[len(prefix):]
	for i := 0; i < len(rest); i++ {
		if rest[i] == '/' {
			return rest[:i], true
		}
	}
	return "", false
}
