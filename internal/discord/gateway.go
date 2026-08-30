// internal/discord/gateway.go
package discord

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
)

// Gateway holds the always-on Discord connection for the tracking guild.
// Unlike internal/jetstream's Manager, there is no restart-on-watch-list-
// change pattern: Discord has no server-side subscription to update —
// once connected with the presence/members intents, the bot receives
// every relevant event for the whole guild for as long as the connection
// lives. discordgo's Session.Open handles reconnect/resume internally.
type Gateway struct {
	Session *discordgo.Session
	GuildID string
	Members *MemberCache
	OnJoin  func(discordID string)
	OnLeave func(discordID string)
}

func NewGateway(token, guildID string) (*Gateway, error) {
	session, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, fmt.Errorf("create discord session: %w", err)
	}
	session.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildPresences | discordgo.IntentsGuildMembers

	gw := &Gateway{Session: session, GuildID: guildID, Members: NewMemberCache()}
	session.AddHandler(gw.handleGuildCreate)
	session.AddHandler(gw.handleMemberAdd)
	session.AddHandler(gw.handleMemberUpdate)
	session.AddHandler(gw.handleMemberRemove)
	return gw, nil
}

func (g *Gateway) Open() error  { return g.Session.Open() }
func (g *Gateway) Close() error { return g.Session.Close() }

func (g *Gateway) handleGuildCreate(s *discordgo.Session, e *discordgo.GuildCreate) {
	if e.ID != g.GuildID {
		return
	}
	for _, m := range e.Members {
		g.Members.Set(m.User.ID, m.User.Username)
	}
}

func (g *Gateway) handleMemberAdd(s *discordgo.Session, e *discordgo.GuildMemberAdd) {
	if e.GuildID != g.GuildID {
		return
	}
	g.Members.Set(e.User.ID, e.User.Username)
	if g.OnJoin != nil {
		g.OnJoin(e.User.ID)
	}
}

func (g *Gateway) handleMemberUpdate(s *discordgo.Session, e *discordgo.GuildMemberUpdate) {
	if e.GuildID != g.GuildID {
		return
	}
	g.Members.Set(e.User.ID, e.User.Username)
}

func (g *Gateway) handleMemberRemove(s *discordgo.Session, e *discordgo.GuildMemberRemove) {
	if e.GuildID != g.GuildID {
		return
	}
	g.Members.Remove(e.User.ID)
	if g.OnLeave != nil {
		g.OnLeave(e.User.ID)
	}
}

// SendDM delivers onboarding instructions to a newly-joined member — the
// tracking guild has no shared channel to post them in, so this is the
// Discord equivalent of keytrace.dev's role in the Steam flow.
func (g *Gateway) SendDM(userID, message string) error {
	ch, err := g.Session.UserChannelCreate(userID)
	if err != nil {
		return fmt.Errorf("open DM channel: %w", err)
	}
	_, err = g.Session.ChannelMessageSend(ch.ID, message)
	return err
}
