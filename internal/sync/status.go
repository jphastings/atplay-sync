// internal/sync/status.go
package sync

const StatusCollection = "games.gamesgamesgamesgames.actor.status"
const statusRkey = "self"

type ActorStatus struct {
	Type      string         `json:"$type"`
	Game      string         `json:"game"`
	Platform  string         `json:"platform"`
	Playing   map[string]any `json:"playing"` // always {} in v1 — see Global Constraints, no party info yet
	Embed     *Embed         `json:"embed,omitempty"`
	CreatedAt string         `json:"createdAt"`
	StaleAt   string         `json:"staleAt"`
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
