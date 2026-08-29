// internal/keytrace/canonicalize.go
package keytrace

import (
	"bytes"
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
		b.WriteString(encodeJSONString(k))
		b.WriteByte(':')
		b.WriteString(encodeJSONString(m[k]))
	}
	b.WriteByte('}')
	return b.String()
}

// encodeJSONString matches JavaScript's JSON.stringify (and RFC 8785), which
// does not HTML-escape &, <, > — unlike json.Marshal's default behavior.
func encodeJSONString(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSuffix(buf.String(), "\n")
}
