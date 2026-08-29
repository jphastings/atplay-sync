// internal/keytrace/types.go
package keytrace

import "strings"

const ClaimCollection = "dev.keytrace.claim"
const ServerKeyCollection = "dev.keytrace.serverPublicKey"

// DefaultTrustedSignerHandles mirrors keytrace's own reference library default.
var DefaultTrustedSignerHandles = []string{"keytrace.dev"}

type ClaimSignature struct {
	Kid          string   `json:"kid"`
	Src          string   `json:"src"`
	SignedAt     string   `json:"signedAt"`
	Attestation  string   `json:"attestation"`
	SignedFields []string `json:"signedFields"`
}

type ClaimIdentity struct {
	Subject     string `json:"subject"`
	DisplayName string `json:"displayName,omitempty"`
	ProfileURL  string `json:"profileUrl,omitempty"`
	AvatarURL   string `json:"avatarUrl,omitempty"`
	Account     string `json:"account,omitempty"`
}

type Claim struct {
	Type           string           `json:"type"`
	Status         string           `json:"status"`
	ClaimURI       string           `json:"claimUri"`
	Identity       ClaimIdentity    `json:"identity"`
	Sigs           []ClaimSignature `json:"sigs"`
	CreatedAt      string           `json:"createdAt"`
	LastVerifiedAt string           `json:"lastVerifiedAt"`
}

// primarySig mirrors keytrace's getPrimarySig: prefer the identity-attestation
// signature, fall back to the first signature present.
func primarySig(c Claim) (ClaimSignature, bool) {
	for _, s := range c.Sigs {
		if strings.HasPrefix(s.Kid, "attest:") {
			return s, true
		}
	}
	if len(c.Sigs) > 0 {
		return c.Sigs[0], true
	}
	return ClaimSignature{}, false
}
