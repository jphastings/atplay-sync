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
func verifyJWS(claimData map[string]string, jws string, pub *ecdsa.PublicKey) (bool, error) {
	parts := strings.Split(jws, ".")
	if len(parts) != 3 {
		return false, fmt.Errorf("malformed JWS: expected 3 parts, got %d", len(parts))
	}
	headerB64, payloadB64, sigB64 := parts[0], parts[1], parts[2]

	expectedPayload := canonicalizeStringMap(claimData)
	actualPayload, err := base64.RawURLEncoding.DecodeString(payloadB64)
	if err != nil {
		return false, fmt.Errorf("decode payload: %w", err)
	}
	if string(actualPayload) != expectedPayload {
		return false, nil
	}

	sigBytes, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return false, fmt.Errorf("decode signature: %w", err)
	}
	if len(sigBytes) != 64 {
		return false, fmt.Errorf("unexpected signature length %d, want 64", len(sigBytes))
	}
	r := new(big.Int).SetBytes(sigBytes[:32])
	s := new(big.Int).SetBytes(sigBytes[32:])

	hash := sha256.Sum256([]byte(headerB64 + "." + payloadB64))
	return ecdsa.Verify(pub, hash[:], r, s), nil
}
