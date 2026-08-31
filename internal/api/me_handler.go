package api

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/jphastings/game-status/internal/db"
)

type MeHandler struct {
	Conn             *sql.DB
	DiscordInviteURL string
}

type meResponse struct {
	DID              string   `json:"did"`
	SteamSubject     *string  `json:"steamSubject,omitempty"`
	SteamDisplay     *string  `json:"steamDisplayName,omitempty"`
	SteamEnabled     bool     `json:"steamEnabled"`
	DiscordSubject   *string  `json:"discordSubject,omitempty"`
	DiscordDisplay   *string  `json:"discordDisplayName,omitempty"`
	DiscordEnabled   bool     `json:"discordEnabled"`
	SourceOrder      []string `json:"sourceOrder"` // enabled AND disabled sources, priority order — frontend appends disabled ones after enabled
	DiscordInviteURL string   `json:"discordInviteUrl"`
}

// Live status isn't included here — the frontend reads every live
// games.atmosphere.status record straight from the user's own PDS (one per
// game currently being played, not a single fixed rkey), which is the
// authoritative source and is publicly readable, so there's no reason to
// proxy it through an authenticated session on our side.
func (h *MeHandler) Get(w http.ResponseWriter, r *http.Request) {
	did, ok := DIDFromContext(r.Context())
	if !ok {
		http.Error(w, "not signed in", http.StatusUnauthorized)
		return
	}

	resp := meResponse{DID: did}

	steamClaim, err := db.GetClaim(r.Context(), h.Conn, did, db.SteamSource)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if steamClaim != nil {
		resp.SteamSubject = &steamClaim.Subject
		resp.SteamDisplay = &steamClaim.DisplayName
	}

	steamEnabled, err := db.IsEnabled(r.Context(), h.Conn, did, db.SteamSource)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	resp.SteamEnabled = steamEnabled

	discordClaim, err := db.GetClaim(r.Context(), h.Conn, did, db.DiscordSource)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if discordClaim != nil {
		resp.DiscordSubject = &discordClaim.Subject
		resp.DiscordDisplay = &discordClaim.DisplayName
	}

	discordEnabled, err := db.IsEnabled(r.Context(), h.Conn, did, db.DiscordSource)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	resp.DiscordEnabled = discordEnabled

	sourceOrder, err := db.ListAllSourcesOrdered(r.Context(), h.Conn, did)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	resp.SourceOrder = sourceOrder
	resp.DiscordInviteURL = h.DiscordInviteURL

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
