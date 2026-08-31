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

	var gameKey, rawName string
	var extra appsync.SessionExtra
	playing := false
	for _, activity := range e.Activities {
		if activity.Type != discordgo.ActivityTypeGame || activity.ApplicationID == "" {
			continue
		}
		if appID, ok := h.Games.SteamAppID(activity.ApplicationID); ok {
			gameKey = appID
		} else {
			// Not one of our known Steam-mapped games — record it anyway
			// under a namespaced key rather than silently dropping it.
			// "discord:"-prefixed keys are never valid Steam app IDs, so
			// they never resolve via GetGameBySteamID: nothing published
			// changes, but the settings page can now show this as an
			// unmatched signal instead of it just vanishing.
			gameKey = "discord:" + activity.ApplicationID
		}
		rawName = activity.Name
		playing = true
		extra.State = activity.State
		extra.Details = activity.Details
		if activity.Timestamps.StartTimestamp > 0 {
			extra.DetailsStartedAt = time.UnixMilli(activity.Timestamps.StartTimestamp).UTC().Format(time.RFC3339)
		}
		if activity.Timestamps.EndTimestamp > 0 {
			extra.DetailsEndsAt = time.UnixMilli(activity.Timestamps.EndTimestamp).UTC().Format(time.RFC3339)
		}
		if activity.Party.ID != "" {
			extra.PartyID = activity.Party.ID
			if len(activity.Party.Size) > 0 {
				extra.PartyCurrent = activity.Party.Size[0]
			}
			if len(activity.Party.Size) > 1 {
				extra.PartyMax = activity.Party.Size[1]
			}
			extra.PartyDIDs = h.partyMemberDIDs(ctx, s, activity.ApplicationID, activity.Party.ID)
		}
		break
	}

	if err := appsync.UpdateSession(ctx, h.Conn, h.Reconciler, claim.DID, appdb.DiscordSource, playing, gameKey, rawName, extra, time.Now()); err != nil {
		slog.Error("discord presence update failed", "discord_id", e.User.ID, "err", err)
	}
}

// partyMemberDIDs resolves co-players sharing this Discord activity party to
// their DIDs, reusing discordgo's own guild presence cache (Session.State,
// already populated by the same GUILD_PRESENCES intent this bot requests —
// no bespoke index needed; discordgo applies state updates before dispatching
// to handlers, so this always reflects the very event being handled).
//
// A co-player is included only if they'd pass the same claim+enabled gate
// that governs whether we'd write a status for them off their own presence
// event — that gate IS each person's individual consent to publish their
// Discord activity, so it applies symmetrically to being named in someone
// else's party. This naturally includes the primary user themself (their
// own presence is in the same slice, already past that gate), matching the
// lexicon's "including yourself".
func (h *PresenceHandler) partyMemberDIDs(ctx context.Context, s *discordgo.Session, appID, partyID string) []string {
	if s == nil || s.State == nil {
		return nil // e.g. under test — supplementary data, never blocks state/details/count
	}
	guild, err := s.State.Guild(h.GuildID)
	if err != nil {
		return nil
	}
	var dids []string
	for _, presence := range guild.Presences {
		for _, activity := range presence.Activities {
			if activity.Type != discordgo.ActivityTypeGame || activity.ApplicationID != appID || activity.Party.ID != partyID {
				continue
			}
			claim, err := findClaimByDiscordID(ctx, h.Conn, presence.User.ID)
			if err != nil || claim == nil {
				break
			}
			enabled, err := appdb.IsEnabled(ctx, h.Conn, claim.DID, appdb.DiscordSource)
			if err != nil || !enabled {
				break
			}
			dids = append(dids, claim.DID)
			break
		}
	}
	return dids
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
