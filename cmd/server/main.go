package main

import (
	"log"
	"log/slog"
	"net/http"

	"github.com/bluesky-social/indigo/atproto/atcrypto"
	"github.com/bluesky-social/indigo/atproto/auth/oauth"

	"github.com/jphastings/game-status/internal/api"
	"github.com/jphastings/game-status/internal/authstore"
	"github.com/jphastings/game-status/internal/config"
	"github.com/jphastings/game-status/internal/db"
	"github.com/jphastings/game-status/internal/webauth"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	conn, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer conn.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	priv, err := atcrypto.ParsePrivateMultibase(cfg.OAuthPrivateKeyMultibase)
	if err != nil {
		log.Fatalf("oauth key: %v", err)
	}
	oauthConfig := oauth.NewPublicConfig(cfg.BaseURL+"/oauth/client-metadata.json", cfg.BaseURL+"/oauth/callback",
		[]string{
			"atproto",
			"repo:games.gamesgamesgamesgames.actor.status?action=create",
			"repo:games.gamesgamesgamesgames.actor.status?action=update",
			"repo:games.gamesgamesgamesgames.actor.status?action=delete",
			// This granular repo-scope-per-action pattern is confirmed from indigo's
			// own oauth-web-demo for a single "?action=create" case; it's not confirmed
			// for "read" or for arbitrary third-party collections like keytrace's. If the
			// PDS rejects this scope list at authorization time, fall back to the single
			// broader "transition:generic" scope instead.
			"repo:dev.keytrace.claim?action=read",
		})
	if err := oauthConfig.SetClientSecret(priv, cfg.OAuthKeyID); err != nil {
		log.Fatalf("oauth client secret: %v", err)
	}
	oauthApp := oauth.NewClientApp(&oauthConfig, &authstore.SQLiteStore{Conn: conn})

	oauthHandlers := &api.OAuthHandlers{App: oauthApp, Conn: conn, Cookies: webauth.SignedCookies{Secret: cfg.SessionSecret}}

	mux.HandleFunc("GET /oauth/client-metadata.json", oauthHandlers.ClientMetadata)
	mux.HandleFunc("GET /oauth/jwks.json", oauthHandlers.JWKS)
	mux.HandleFunc("GET /login", oauthHandlers.Login)
	mux.HandleFunc("GET /oauth/callback", oauthHandlers.Callback)

	slog.Info("listening", "addr", cfg.ListenAddr)
	if err := http.ListenAndServe(cfg.ListenAddr, mux); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
