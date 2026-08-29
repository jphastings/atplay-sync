// internal/keytrace/canonicalize.go
package keytrace

import (
	"encoding/json"
	"sort"
	"strings"
)

// canonicalizeStringMap reproduces keytrace's RFC 8785 canonicalization for
// the one shape a SignedClaimData payload ever takes: a flat map of string
// keys to string values. Keys are sorted by UTF-16 code unit order; every key
// this package uses is plain ASCII, where Go's default string ordering,
// UTF-16 code unit order, and Unicode code point order all agree.
func canonicalizeStringMap(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		vb, _ := json.Marshal(m[k])
		b.Write(kb)
		b.WriteByte(':')
		b.Write(vb)
	}
	b.WriteByte('}')
	return b.String()
}
