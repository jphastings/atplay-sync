// internal/keytrace/verify.go
package keytrace

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
)

type es256PublicJWK struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

func parsePublicJWK(raw string) (*ecdsa.PublicKey, error) {
	var jwk es256PublicJWK
	if err := json.Unmarshal([]byte(raw), &jwk); err != nil {
		return nil, fmt.Errorf("parse jwk: %w", err)
	}
	if jwk.Kty != "EC" || jwk.Crv != "P-256" {
		return nil, fmt.Errorf("unsupported key type %s/%s", jwk.Kty, jwk.Crv)
	}
	x, err := base64.RawURLEncoding.DecodeString(jwk.X)
	if err != nil {
		return nil, fmt.Errorf("decode x: %w", err)
	}
	y, err := base64.RawURLEncoding.DecodeString(jwk.Y)
	if err != nil {
		return nil, fmt.Errorf("decode y: %w", err)
	}
	return &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}, nil
}

// verifyJWS checks a compact JWS (header.payload.signature) was signed by pub
// over exactly the canonical form of claimData — mirrors keytrace's
// crypto/signature.ts verifyES256Signature.
//
// It is purely computational over an attestation blob the claimant fully
// controls, so every way it can go wrong — wrong part count, undecodable
// parts, a wrong-length signature — means "this claim does not verify", never
// "we couldn't tell". Reporting those as errors would let a claimant keep a
// revoked claim alive forever by corrupting their own attestation, since every
// caller treats an error as "uncertain, retry later".
func verifyJWS(claimData map[string]string, jws string, pub *ecdsa.PublicKey) bool {
	parts := strings.Split(jws, ".")
	if len(parts) != 3 {
		return false
	}
	headerB64, payloadB64, sigB64 := parts[0], parts[1], parts[2]

	actualPayload, err := base64.RawURLEncoding.DecodeString(payloadB64)
	if err != nil || string(actualPayload) != canonicalizeStringMap(claimData) {
		return false
	}

	sigBytes, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil || len(sigBytes) != 64 {
		return false
	}
	r := new(big.Int).SetBytes(sigBytes[:32])
	s := new(big.Int).SetBytes(sigBytes[32:])

	hash := sha256.Sum256([]byte(headerB64 + "." + payloadB64))
	return ecdsa.Verify(pub, hash[:], r, s)
}
