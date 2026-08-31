// internal/sync/status.go
package sync

const StatusCollection = "games.atmosphere.status"
const statusRkey = "self"

// ViaClientName identifies this app in the record's `via` field, per the
// lexicon's own description: "the (unique) name of the client ... which
// wrote this record." Set from BASE_URL's host at startup (see main.go).
var ViaClientName string

type ActorStatus struct {
	Type string `json:"$type"`
	Game string `json:"game"`
	// Platform now references a games.gamesgamesgamesgames.platform record
	// (an at-uri) rather than a bare token — left unset until there's a
	// confirmed Steam platform record to link to (none exists yet upstream).
	Platform  string         `json:"platform,omitempty"`
	Playing   map[string]any `json:"playing"` // always {} in v1 — see Global Constraints, no party info yet
	Embed     *Embed         `json:"embed,omitempty"`
	CreatedAt string         `json:"createdAt"`
	StaleAt   string         `json:"staleAt"`
	Via       string         `json:"via,omitempty"`
}

type Embed struct {
	Type     string        `json:"$type"`
	External EmbedExternal `json:"external"`
}

type EmbedExternal struct {
	URI         string `json:"uri"`
	Title       string `json:"title"`
	Description string `json:"description"`
}
