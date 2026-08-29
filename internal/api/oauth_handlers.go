package api

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/bluesky-social/indigo/atproto/auth/oauth"

	"github.com/jphastings/game-status/internal/db"
	"github.com/jphastings/game-status/internal/webauth"
)

const sessionCookieName = "gs_session"

type OAuthHandlers struct {
	App     *oauth.ClientApp
	Conn    *sql.DB
	Cookies webauth.SignedCookies
}

func (h *OAuthHandlers) ClientMetadata(w http.ResponseWriter, r *http.Request) {
	meta := h.App.Config.ClientMetadata()
	if h.App.Config.IsConfidential() {
		jwksURI := "https://" + r.Host + "/oauth/jwks.json"
		meta.JWKSURI = &jwksURI
	}
	name := "Game Status Sync"
	meta.ClientName = &name
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(meta)
}

func (h *OAuthHandlers) JWKS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(h.App.Config.PublicJWKS())
}

func (h *OAuthHandlers) Login(w http.ResponseWriter, r *http.Request) {
	identifier := r.URL.Query().Get("handle")
	if identifier == "" {
		http.Error(w, "missing handle", http.StatusBadRequest)
		return
	}
	redirectURL, err := h.App.StartAuthFlow(r.Context(), identifier)
	if err != nil {
		http.Error(w, "could not start sign-in: "+err.Error(), http.StatusBadGateway)
		return
	}
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func (h *OAuthHandlers) Callback(w http.ResponseWriter, r *http.Request) {
	sess, err := h.App.ProcessCallback(r.Context(), r.URL.Query())
	if err != nil {
		http.Error(w, "sign-in failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	did := sess.AccountDID.String()
	if err := db.UpsertUser(r.Context(), h.Conn, did); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := db.SetActiveSession(r.Context(), h.Conn, did, sess.SessionID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: h.Cookies.Encode(did), Path: "/",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: 30 * 24 * 60 * 60,
	})
	http.Redirect(w, r, "/", http.StatusFound)
}
