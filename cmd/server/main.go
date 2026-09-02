package main

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/bluesky-social/indigo/atproto/atcrypto"
	"github.com/bluesky-social/indigo/atproto/auth/oauth"
	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/bwmarrin/discordgo"

	"github.com/jphastings/game-status/internal/api"
	"github.com/jphastings/game-status/internal/atsession"
	"github.com/jphastings/game-status/internal/authstore"
	"github.com/jphastings/game-status/internal/cartridge"
	"github.com/jphastings/game-status/internal/claims"
	"github.com/jphastings/game-status/internal/config"
	"github.com/jphastings/game-status/internal/db"
	"github.com/jphastings/game-status/internal/discord"
	"github.com/jphastings/game-status/internal/jetstream"
	"github.com/jphastings/game-status/internal/keytrace"
	"github.com/jphastings/game-status/internal/livestate"
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

	baseURL, err := url.Parse(cfg.BaseURL)
	if err != nil {
		log.Fatalf("BASE_URL: %v", err)
	}
	sync.ViaClientName = baseURL.Hostname()

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
			"repo:games.atmosphere.status?action=create",
			"repo:games.atmosphere.status?action=update",
			"repo:games.atmosphere.status?action=delete",
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
	store := &authstore.SQLiteStore{Conn: conn}
	oauthApp := oauth.NewClientApp(&oauthConfig, store)
	resumer := atsession.NewResumer(oauthApp)

	oauthHandlers := &api.OAuthHandlers{App: oauthApp, Conn: conn, Cookies: webauth.SignedCookies{Secret: cfg.SessionSecret}, BaseURL: cfg.BaseURL}

	mux.HandleFunc("GET /oauth/client-metadata.json", oauthHandlers.ClientMetadata)
	mux.HandleFunc("GET /oauth/jwks.json", oauthHandlers.JWKS)
	mux.HandleFunc("GET /login", oauthHandlers.Login)
	mux.HandleFunc("GET /oauth/callback", oauthHandlers.Callback)
	mux.HandleFunc("GET /logout", oauthHandlers.Logout)

	dir := identity.DefaultDirectory()
	trustedDIDs, err := resolveTrustedDIDs(context.Background(), dir, keytrace.DefaultTrustedSignerHandles)
	if err != nil {
		log.Fatalf("resolve trusted keytrace signers: %v", err)
	}
	verifier := &keytrace.Verifier{
		Keys:        &keytrace.CachedKeyFetcher{Dir: dir, Conn: conn},
		TrustedDIDs: trustedDIDs,
	}

	cartridgeClient := cartridge.New(cfg.CartridgeHost, cfg.CartridgeClientKey, conn)
	steamClient := steam.New(cfg.SteamAPIKey)
	steamBudget := steam.NewBudget(cfg.SteamDailyCallBudget)

	discordGateway, err := discord.NewGateway(cfg.DiscordBotToken, cfg.DiscordGuildID)
	if err != nil {
		log.Fatalf("discord gateway: %v", err)
	}
	discordResolver := &discord.ClaimResolver{Members: discordGateway.Members}
	gameIndex := discord.NewGameIndex()
	if err := gameIndex.Refresh(context.Background()); err != nil {
		log.Fatalf("discord detectable games: %v", err)
	}
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			if err := gameIndex.Refresh(context.Background()); err != nil {
				slog.Error("discord detectable games refresh", "err", err)
			}
		}
	}()

	writer := &sync.ATProtoWriter{Resumer: resumer, Conn: conn, Dir: dir}
	liveHub := livestate.NewHub()
	// Keeps idle sockets from being culled by anything in between, and
	// re-asserts state so a missed push can't leave a browser stale.
	go liveHub.Heartbeat(context.Background(), livestate.HeartbeatInterval)
	reconciler := &sync.Reconciler{Conn: conn, Resolver: cartridgeClient, Writer: writer, Broadcaster: liveHub}

	presenceHandler := &discord.PresenceHandler{Conn: conn, GuildID: cfg.DiscordGuildID, Games: gameIndex, Reconciler: reconciler}
	discordGateway.Session.AddHandler(presenceHandler.HandlePresenceUpdate)
	discordGateway.OnJoin = func(discordID string) {
		if err := discordGateway.SendDM(discordID, "Link your atmosphere account: https://keytrace.dev/add/discord, then check your sync settings at "+cfg.BaseURL); err != nil {
			slog.Error("discord onboarding dm", "discord_id", discordID, "err", err)
		}
	}
	discordGateway.OnLeave = func(discordID string) {
		if err := presenceHandler.HandleGuildMemberRemove(context.Background(), discordID); err != nil {
			slog.Error("discord member remove", "discord_id", discordID, "err", err)
		}
	}
	discordGateway.OnGuildPresences = func(guildID string, presences []*discordgo.Presence) {
		for _, p := range presences {
			presenceHandler.HandlePresenceUpdate(nil, &discordgo.PresenceUpdate{Presence: *p, GuildID: guildID})
		}
	}
	if err := discordGateway.Open(); err != nil {
		log.Fatalf("discord gateway open: %v", err)
	}
	defer discordGateway.Close()

	steamHandlers := &api.SteamHandlers{Resumer: resumer, Conn: conn, Verifier: verifier, Resolver: discordResolver, Reconciler: reconciler}
	mux.HandleFunc("POST /api/steam/recheck", oauthHandlers.RequireAuth(steamHandlers.Recheck))
	mux.HandleFunc("POST /api/steam/enabled", oauthHandlers.RequireAuth(steamHandlers.SetEnabled))

	meHandler := &api.MeHandler{Conn: conn, DiscordInviteURL: cfg.DiscordInviteURL}
	mux.HandleFunc("GET /api/me", oauthHandlers.RequireAuth(meHandler.Get))

	go func() {
		callsPerPoll := 0 // unknown before the first tick — NextPollInterval treats that as "check back in a minute"
		for {
			time.Sleep(sync.NextPollInterval(callsPerPoll, steamBudget.Remaining(), time.Now()))

			dids, err := db.ListEnabledDIDs(context.Background(), conn, db.SteamSource)
			if err != nil {
				slog.Error("sync tick", "err", err)
				continue
			}
			callsPerPoll = (len(dids) + steam.BatchSize - 1) / steam.BatchSize

			if err := sync.RunTick(context.Background(), conn, steamClient, cartridgeClient, writer, steamBudget, time.Now()); err != nil {
				slog.Error("sync tick", "err", err)
			}
		}
	}()

	recordFetcher := claims.IndigoRecordFetcher{Dir: dir}
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			if err := claims.RunSweep(context.Background(), conn, recordFetcher, verifier, discordResolver, reconciler); err != nil {
				slog.Error("daily claim sweep", "err", err)
			}
			if err := store.DeleteStaleAuthRequests(context.Background()); err != nil {
				slog.Error("stale auth request cleanup", "err", err)
			}
		}
	}()

	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			if err := sync.RunStatusSweep(context.Background(), conn, writer, time.Now()); err != nil {
				slog.Error("daily status sweep", "err", err)
			}
		}
	}()

	jetHandler := func(ctx context.Context, ev jetstream.Event) error {
		return jetstream.HandleEvent(ctx, jetstream.DBStore{Conn: conn, Reconciler: reconciler}, verifier, discordResolver, ev)
	}
	jetManager := jetstream.NewManager("jetstream2.us-east.bsky.network", jetHandler)

	// Every signed-in user is watched, not just those currently syncing —
	// a first-time claim link or an unlink needs to be caught live.
	listWatchedDIDs := func(ctx context.Context) ([]string, error) {
		return db.ListAllDIDs(ctx, conn)
	}
	if err := jetManager.Restart(context.Background(), listWatchedDIDs); err != nil {
		log.Fatalf("start jetstream: %v", err)
	}
	steamHandlers.Jetstream = jetManager
	oauthHandlers.Jetstream = jetManager

	discordHandlers := &api.DiscordHandlers{Resumer: resumer, Conn: conn, Verifier: verifier, Resolver: discordResolver, Reconciler: reconciler, Jetstream: jetManager}
	mux.HandleFunc("POST /api/discord/recheck", oauthHandlers.RequireAuth(discordHandlers.Recheck))
	mux.HandleFunc("POST /api/discord/enabled", oauthHandlers.RequireAuth(discordHandlers.SetEnabled))

	syncHandlers := &api.SyncHandlers{Conn: conn, Reconciler: reconciler}
	mux.HandleFunc("POST /api/sync/order", oauthHandlers.RequireAuth(syncHandlers.SetOrder))

	liveHandlers := &api.LiveHandlers{Conn: conn, Resolver: cartridgeClient, Hub: liveHub}
	mux.HandleFunc("GET /api/sync/live", oauthHandlers.RequireAuth(liveHandlers.Serve))

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
