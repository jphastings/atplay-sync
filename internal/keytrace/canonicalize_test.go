// internal/keytrace/canonicalize_test.go
package keytrace

import "testing"

func TestCanonicalizeStringMap_SortsKeysAndEscapes(t *testing.T) {
	got := canonicalizeStringMap(map[string]string{
		"type":     "steam",
		"claimUri": "https://example.com/a\"b",
		"did":      "did:plc:abc",
	})
	want := `{"claimUri":"https://example.com/a\"b","did":"did:plc:abc","type":"steam"}`
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}
