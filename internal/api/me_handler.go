package api

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/jphastings/game-status/internal/db"
)

type MeHandler struct {
	Conn *sql.DB
}

type meResponse struct {
	DID          string  `json:"did"`
	SteamSubject *string `json:"steamSubject,omitempty"`
	SteamDisplay *string `json:"steamDisplayName,omitempty"`
	SteamEnabled bool    `json:"steamEnabled"`
}

func (h *MeHandler) Get(w http.ResponseWriter, r *http.Request) {
	did, ok := DIDFromContext(r.Context())
	if !ok {
		http.Error(w, "not signed in", http.StatusUnauthorized)
		return
	}

	resp := meResponse{DID: did}

	claim, err := db.GetSteamClaim(r.Context(), h.Conn, did)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if claim != nil {
		resp.SteamSubject = &claim.Subject
		resp.SteamDisplay = &claim.DisplayName
	}

	enabled, err := db.IsSteamEnabled(r.Context(), h.Conn, did)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	resp.SteamEnabled = enabled

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
