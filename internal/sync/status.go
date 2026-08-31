// internal/sync/status.go
package sync

const StatusCollection = "games.atmosphere.status"

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
	Platform  string  `json:"platform,omitempty"`
	State     string  `json:"state,omitempty"`
	Details   string  `json:"details,omitempty"`
	Playing   Playing `json:"playing"` // presence of this object (even empty) signals "playing" per the lexicon
	Embed     *Embed  `json:"embed,omitempty"`
	CreatedAt string  `json:"createdAt"`
	StaleAt   string  `json:"staleAt"`
	Via       string  `json:"via,omitempty"`
}

// Playing mirrors the lexicon's `playing` object. Only Discord populates it
// today (via SessionExtra) — every other source leaves it at its zero value.
type Playing struct {
	ID    string `json:"id,omitempty"`
	Party *Party `json:"party,omitempty"`
}

type Party struct {
	Current int      `json:"current"`
	Max     int      `json:"max,omitempty"`
	DIDs    []string `json:"dids,omitempty"`
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

// SessionExtra is opaque per-source metadata threaded through
// session_starts.extra (JSON-encoded) between UpdateSession and Reconcile,
// since Reconcile re-reads every enabled source's row fresh rather than
// receiving live event data directly. Only Discord ever populates it.
type SessionExtra struct {
	State        string   `json:"state,omitempty"`
	Details      string   `json:"details,omitempty"`
	PartyID      string   `json:"partyId,omitempty"`
	PartyCurrent int      `json:"partyCurrent,omitempty"`
	PartyMax     int      `json:"partyMax,omitempty"`
	PartyDIDs    []string `json:"partyDids,omitempty"`
}
