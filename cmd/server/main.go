package main

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"log/slog"
	"net/http"
	"time"

	"github.com/bluesky-social/indigo/atproto/atcrypto"
	"github.com/bluesky-social/indigo/atproto/auth/oauth"
	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/atproto/syntax"

	"github.com/jphastings/game-status/internal/api"
	"github.com/jphastings/game-status/internal/authstore"
	"github.com/jphastings/game-status/internal/cartridge"
	"github.com/jphastings/game-status/internal/claims"
	"github.com/jphastings/game-status/internal/config"
	"github.com/jphastings/game-status/internal/db"
	"github.com/jphastings/game-status/internal/jetstream"
	"github.com/jphastings/game-status/internal/keytrace"
	"github.com/jphastings/game-status/internal/steam"
	"github.com/jphastings/game-status/internal/sync"
	"github.com/jphastings/game-status/internal/webauth"
)

//go:embed web/dist
var frontendFS embed.FS

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

	oauthHandlers := &api.OAuthHandlers{App: oauthApp, Conn: conn, Cookies: webauth.SignedCookies{Secret: cfg.SessionSecret}, BaseURL: cfg.BaseURL}

	mux.HandleFunc("GET /oauth/client-metadata.json", oauthHandlers.ClientMetadata)
	mux.HandleFunc("GET /oauth/jwks.json", oauthHandlers.JWKS)
	mux.HandleFunc("GET /login", oauthHandlers.Login)
	mux.HandleFunc("GET /oauth/callback", oauthHandlers.Callback)

	dir := identity.DefaultDirectory()
	trustedDIDs, err := resolveTrustedDIDs(context.Background(), dir, keytrace.DefaultTrustedSignerHandles)
	if err != nil {
		log.Fatalf("resolve trusted keytrace signers: %v", err)
	}
	verifier := &keytrace.Verifier{
		Keys:        &keytrace.CachedKeyFetcher{Dir: dir, Conn: conn},
		TrustedDIDs: trustedDIDs,
	}

	writer := &sync.ATProtoWriter{App: oauthApp, Conn: conn}

	steamHandlers := &api.SteamHandlers{App: oauthApp, Conn: conn, Verifier: verifier, Deleter: writer}
	mux.HandleFunc("POST /api/steam/recheck", oauthHandlers.RequireAuth(steamHandlers.Recheck))
	mux.HandleFunc("POST /api/steam/enabled", oauthHandlers.RequireAuth(steamHandlers.SetEnabled))

	meHandler := &api.MeHandler{Conn: conn, App: oauthApp}
	mux.HandleFunc("GET /api/me", oauthHandlers.RequireAuth(meHandler.Get))

	cartridgeClient := cartridge.New(cfg.CartridgeHost, cfg.CartridgeClientKey, conn)
	steamClient := steam.New(cfg.SteamAPIKey)

	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			if err := sync.RunTick(context.Background(), conn, steamClient, cartridgeClient, writer, time.Now()); err != nil {
				slog.Error("sync tick", "err", err)
			}
		}
	}()

	recordFetcher := claims.IndigoRecordFetcher{Dir: dir}
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			if err := claims.RunSweep(context.Background(), conn, recordFetcher, verifier, writer); err != nil {
				slog.Error("daily claim sweep", "err", err)
			}
		}
	}()

	jetHandler := func(ctx context.Context, ev jetstream.Event) error {
		return jetstream.HandleEvent(ctx, jetstream.DBStore{Conn: conn, Deleter: writer}, verifier, ev)
	}
	jetManager := jetstream.NewManager("jetstream2.us-east.bsky.network", jetHandler)

	listWatchedDIDs := func(ctx context.Context) ([]string, error) {
		return db.ListSteamEnabledDIDs(ctx, conn)
	}
	if err := jetManager.Restart(context.Background(), listWatchedDIDs); err != nil {
		log.Fatalf("start jetstream: %v", err)
	}
	steamHandlers.Jetstream = jetManager

	distFS, err := fs.Sub(frontendFS, "web/dist")
	if err != nil {
		log.Fatalf("frontend embed: %v", err)
	}
	mux.Handle("GET /", http.FileServerFS(distFS))

	slog.Info("listening", "addr", cfg.ListenAddr)
	if err := http.ListenAndServe(cfg.ListenAddr, mux); err != nil {
		log.Fatalf("serve: %v", err)
	}
}

func resolveTrustedDIDs(ctx context.Context, dir identity.Directory, handles []string) (map[string]bool, error) {
	dids := map[string]bool{}
	for _, h := range handles {
		handle, err := syntax.ParseHandle(h)
		if err != nil {
			return nil, err
		}
		ident, err := dir.LookupHandle(ctx, handle)
		if err != nil {
			return nil, err
		}
		dids[ident.DID.String()] = true
	}
	return dids, nil
}
