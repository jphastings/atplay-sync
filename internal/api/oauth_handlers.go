package api

import (
	"crypto/hmac"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/bluesky-social/indigo/atproto/auth/oauth"

	"github.com/jphastings/game-status/internal/authstore"
	"github.com/jphastings/game-status/internal/db"
	"github.com/jphastings/game-status/internal/jetstream"
	"github.com/jphastings/game-status/internal/webauth"
)

const (
	sessionCookieName    = "gs_session"
	oauthStateCookieName = "gs_oauth_state"
	oauthStateTTL        = 10 * time.Minute
)

type OAuthHandlers struct {
	App       *oauth.ClientApp
	Conn      *sql.DB
	Cookies   webauth.SignedCookies
	BaseURL   string
	Jetstream *jetstream.Manager
}

func (h *OAuthHandlers) ClientMetadata(w http.ResponseWriter, r *http.Request) {
	meta := h.App.Config.ClientMetadata()
	if h.App.Config.IsConfidential() {
		jwksURI := h.BaseURL + "/oauth/jwks.json"
		meta.JWKSURI = &jwksURI
	}
	name := "At Play Sync"
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
	ctx, state := authstore.WithStateCapture(r.Context())
	redirectURL, err := h.App.StartAuthFlow(ctx, identifier)
	if err != nil {
		http.Error(w, "could not start sign-in: "+err.Error(), http.StatusBadGateway)
		return
	}
	if *state == "" {
		http.Error(w, "could not start sign-in", http.StatusInternalServerError)
		return
	}

	// Binds the flow to this browser: without it, an attacker can start their own flow,
	// capture the callback URL instead of following it, and get a victim's browser to
	// complete it — signing the victim in as the attacker.
	http.SetCookie(w, &http.Cookie{
		Name: oauthStateCookieName, Value: h.Cookies.EncodeWithTTL(*state, oauthStateTTL), Path: "/",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: int(oauthStateTTL.Seconds()),
	})
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func (h *OAuthHandlers) Logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: "", Path: "/",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
	http.Redirect(w, r, "/", http.StatusFound)
}

func (h *OAuthHandlers) stateCookieMatches(r *http.Request) bool {
	cookie, err := r.Cookie(oauthStateCookieName)
	if err != nil {
		return false
	}
	expected, err := h.Cookies.Decode(cookie.Value)
	if err != nil || expected == "" {
		return false
	}
	return hmac.Equal([]byte(expected), []byte(r.URL.Query().Get("state")))
}

func (h *OAuthHandlers) Callback(w http.ResponseWriter, r *http.Request) {
	if !h.stateCookieMatches(r) {
		http.Error(w, "sign-in request did not start in this browser", http.StatusBadRequest)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: oauthStateCookieName, Value: "", Path: "/",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})

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
	// A brand-new sign-in needs to land on the Jetstream watch list right
	// away — otherwise their first claim link (or an unlink before they
	// ever toggle sync on) wouldn't be caught live.
	restartJetstreamWatch(h.Jetstream, h.Conn)

	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: h.Cookies.Encode(did), Path: "/",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: 30 * 24 * 60 * 60,
	})
	http.Redirect(w, r, "/", http.StatusFound)
}
