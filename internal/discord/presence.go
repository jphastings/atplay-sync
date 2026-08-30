// internal/discord/presence.go
package discord

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/bwmarrin/discordgo"

	appdb "github.com/jphastings/game-status/internal/db"
	appsync "github.com/jphastings/game-status/internal/sync"
)

type PresenceHandler struct {
	Conn       *sql.DB
	GuildID    string
	Games      *GameIndex
	Reconciler *appsync.Reconciler
}

// HandlePresenceUpdate is the Discord equivalent of sync.tickOne: given
// whatever's currently playing (or not), update this user's session_starts
// row and reconcile. Unlike Steam's tick, this is push-driven — one event
// is one user, no batching or budget.
func (h *PresenceHandler) HandlePresenceUpdate(s *discordgo.Session, e *discordgo.PresenceUpdate) {
	if e.GuildID != h.GuildID {
		return // Global Constraints: every Gateway event handler filters by the tracking guild first
	}
	ctx := context.Background()

	claim, err := findClaimByDiscordID(ctx, h.Conn, e.User.ID)
	if err != nil || claim == nil {
		return // not a linked+claimed user, or a lookup error — nothing to do
	}
	enabled, err := appdb.IsEnabled(ctx, h.Conn, claim.DID, appdb.DiscordSource)
	if err != nil || !enabled {
		return
	}

	var gameKey string
	playing := false
	for _, activity := range e.Activities {
		if activity.Type != discordgo.ActivityTypeGame || activity.ApplicationID == "" {
			continue
		}
		if appID, ok := h.Games.SteamAppID(activity.ApplicationID); ok {
			gameKey = appID
			playing = true
			break
		}
	}

	if err := appsync.UpdateSession(ctx, h.Conn, h.Reconciler, claim.DID, appdb.DiscordSource, playing, gameKey, time.Now()); err != nil {
		slog.Error("discord presence update failed", "discord_id", e.User.ID, "err", err)
	}
}

// HandleGuildMemberRemove treats leaving the tracking guild like a revoked
// claim: we can no longer receive this person's presence at all, so
// continuing to show a Discord-sourced status for them would be stale by
// construction.
func (h *PresenceHandler) HandleGuildMemberRemove(ctx context.Context, discordID string) error {
	claim, err := findClaimByDiscordID(ctx, h.Conn, discordID)
	if err != nil || claim == nil {
		return err
	}
	return appdb.InvalidateClaim(ctx, h.Conn, h.Reconciler, claim.DID, appdb.DiscordSource, time.Now())
}

func findClaimByDiscordID(ctx context.Context, conn *sql.DB, discordID string) (*appdb.Claim, error) {
	var did string
	err := conn.QueryRowContext(ctx, `SELECT did FROM claims WHERE claim_type = ? AND subject = ?`, appdb.DiscordSource, discordID).Scan(&did)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return appdb.GetClaim(ctx, conn, did, appdb.DiscordSource)
}
