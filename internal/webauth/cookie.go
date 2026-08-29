package webauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const sessionTTL = 30 * 24 * time.Hour

type SignedCookies struct {
	Secret []byte
}

func (s SignedCookies) Encode(did string) string {
	return s.EncodeWithTTL(did, sessionTTL)
}

func (s SignedCookies) EncodeWithTTL(value string, ttl time.Duration) string {
	exp := time.Now().Add(ttl).Unix()
	payload := base64.RawURLEncoding.EncodeToString([]byte(value)) + "." + strconv.FormatInt(exp, 10)
	sig := s.sign(payload)
	return payload + "." + sig
}

func (s SignedCookies) Decode(value string) (string, error) {
	parts := strings.SplitN(value, ".", 3)
	if len(parts) != 3 {
		return "", fmt.Errorf("malformed session cookie")
	}
	didB64, expStr, sig := parts[0], parts[1], parts[2]
	payload := didB64 + "." + expStr

	if !hmac.Equal([]byte(sig), []byte(s.sign(payload))) {
		return "", fmt.Errorf("invalid session signature")
	}

	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return "", fmt.Errorf("malformed expiry")
	}
	if time.Now().Unix() > exp {
		return "", fmt.Errorf("session expired")
	}

	didBytes, err := base64.RawURLEncoding.DecodeString(didB64)
	if err != nil {
		return "", fmt.Errorf("malformed did")
	}
	return string(didBytes), nil
}

func (s SignedCookies) sign(payload string) string {
	mac := hmac.New(sha256.New, s.Secret)
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}
