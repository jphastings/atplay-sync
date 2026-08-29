package webauth

import "testing"

func TestEncodeDecode_RoundTrips(t *testing.T) {
	c := SignedCookies{Secret: []byte("test-secret")}
	token := c.Encode("did:plc:abc")
	did, err := c.Decode(token)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if did != "did:plc:abc" {
		t.Fatalf("got %s, want did:plc:abc", did)
	}
}

func TestDecode_RejectsTamperedSignature(t *testing.T) {
	c := SignedCookies{Secret: []byte("test-secret")}
	token := c.Encode("did:plc:abc")
	tampered := token[:len(token)-1] + "0"
	if _, err := c.Decode(tampered); err == nil {
		t.Fatal("expected error for tampered cookie, got nil")
	}
}

func TestDecode_RejectsWrongSecret(t *testing.T) {
	a := SignedCookies{Secret: []byte("secret-a")}
	b := SignedCookies{Secret: []byte("secret-b")}
	token := a.Encode("did:plc:abc")
	if _, err := b.Decode(token); err == nil {
		t.Fatal("expected error for wrong secret, got nil")
	}
}
