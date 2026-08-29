package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/bluesky-social/indigo/api/agnostic"
	"github.com/bluesky-social/indigo/atproto/auth/oauth"
	"github.com/bluesky-social/indigo/atproto/syntax"

	"github.com/jphastings/game-status/internal/db"
	"github.com/jphastings/game-status/internal/sync"
)

type MeHandler struct {
	Conn *sql.DB
	App  *oauth.ClientApp
}

type liveStatus struct {
	Game     string `json:"game"`
	Platform string `json:"platform,omitempty"`
}

type meResponse struct {
	DID          string      `json:"did"`
	SteamSubject *string     `json:"steamSubject,omitempty"`
	SteamDisplay *string     `json:"steamDisplayName,omitempty"`
	SteamEnabled bool        `json:"steamEnabled"`
	Live         *liveStatus `json:"live,omitempty"`
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

	live, err := h.getLiveStatus(r.Context(), did)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	resp.Live = live

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *MeHandler) getLiveStatus(ctx context.Context, did string) (*liveStatus, error) {
	user, err := db.GetUser(ctx, h.Conn, did)
	if err != nil {
		return nil, err
	}
	if user == nil || user.ActiveSessionID == "" {
		return nil, nil
	}
	parsedDID, err := syntax.ParseDID(did)
	if err != nil {
		return nil, err
	}
	sess, err := h.App.ResumeSession(ctx, parsedDID, user.ActiveSessionID)
	if err != nil {
		slog.Warn("resume session failed, omitting live status", "did", did, "err", err)
		return nil, nil
	}
	resp, err := agnostic.RepoGetRecord(ctx, sess.APIClient(), "", sync.StatusCollection, did, "self")
	if err != nil {
		return nil, nil // no record — not currently playing anything, not an error
	}
	var status struct {
		Game     string `json:"game"`
		Platform string `json:"platform"`
	}
	if err := json.Unmarshal(*resp.Value, &status); err != nil {
		return nil, err
	}
	return &liveStatus{Game: status.Game, Platform: status.Platform}, nil
}
