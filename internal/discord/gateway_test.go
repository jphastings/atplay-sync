// internal/discord/gateway_test.go
package discord

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

const guildID = "123456789012345678"

func TestHandleGuildCreate_SeedsMemberCacheForTrackedGuildOnly(t *testing.T) {
	gw := &Gateway{GuildID: guildID, Members: NewMemberCache()}

	gw.handleGuildCreate(nil, &discordgo.GuildCreate{Guild: &discordgo.Guild{
		ID: guildID,
		Members: []*discordgo.Member{
			{User: &discordgo.User{ID: "1", Username: "alice"}},
			{User: &discordgo.User{ID: "2", Username: "bob"}},
		},
	}})
	gw.handleGuildCreate(nil, &discordgo.GuildCreate{Guild: &discordgo.Guild{
		ID:      "some-other-guild",
		Members: []*discordgo.Member{{User: &discordgo.User{ID: "9", Username: "ignored"}}},
	}})

	if u, ok := gw.Members.Username("1"); !ok || u != "alice" {
		t.Fatalf("Username(1) = %q, %v, want alice, true", u, ok)
	}
	if _, ok := gw.Members.Username("9"); ok {
		t.Fatal("member from an untracked guild was cached")
	}
}

func TestHandleGuildCreate_FiresOnGuildPresencesForTrackedGuildOnly(t *testing.T) {
	gw := &Gateway{GuildID: guildID, Members: NewMemberCache()}
	var gotGuildID string
	var gotPresences []*discordgo.Presence
	gw.OnGuildPresences = func(guildID string, presences []*discordgo.Presence) {
		gotGuildID = guildID
		gotPresences = presences
	}

	presences := []*discordgo.Presence{
		{User: &discordgo.User{ID: "1"}, Status: discordgo.StatusOnline},
	}
	gw.handleGuildCreate(nil, &discordgo.GuildCreate{Guild: &discordgo.Guild{
		ID:        guildID,
		Members:   []*discordgo.Member{{User: &discordgo.User{ID: "1", Username: "alice"}}},
		Presences: presences,
	}})

	if gotGuildID != guildID {
		t.Fatalf("OnGuildPresences guildID = %q, want %q", gotGuildID, guildID)
	}
	if len(gotPresences) != 1 || gotPresences[0] != presences[0] {
		t.Fatalf("OnGuildPresences presences = %v, want %v", gotPresences, presences)
	}

	gotGuildID = ""
	gotPresences = nil
	gw.handleGuildCreate(nil, &discordgo.GuildCreate{Guild: &discordgo.Guild{
		ID:        "some-other-guild",
		Presences: []*discordgo.Presence{{User: &discordgo.User{ID: "9"}}},
	}})
	if gotGuildID != "" || gotPresences != nil {
		t.Fatal("OnGuildPresences fired for an untracked guild")
	}
}

func TestHandleMemberAdd_CachesAndFiresOnJoin(t *testing.T) {
	gw := &Gateway{GuildID: guildID, Members: NewMemberCache()}
	var joined string
	gw.OnJoin = func(discordID string) { joined = discordID }

	gw.handleMemberAdd(nil, &discordgo.GuildMemberAdd{Member: &discordgo.Member{
		GuildID: guildID, User: &discordgo.User{ID: "42", Username: "newperson"},
	}})

	if u, ok := gw.Members.Username("42"); !ok || u != "newperson" {
		t.Fatalf("Username(42) = %q, %v, want newperson, true", u, ok)
	}
	if joined != "42" {
		t.Fatalf("OnJoin fired with %q, want 42", joined)
	}
}

func TestHandleMemberRemove_UncachesAndFiresOnLeave(t *testing.T) {
	gw := &Gateway{GuildID: guildID, Members: NewMemberCache()}
	gw.Members.Set("42", "newperson")
	var left string
	gw.OnLeave = func(discordID string) { left = discordID }

	gw.handleMemberRemove(nil, &discordgo.GuildMemberRemove{Member: &discordgo.Member{
		GuildID: guildID, User: &discordgo.User{ID: "42"},
	}})

	if _, ok := gw.Members.Username("42"); ok {
		t.Fatal("member still cached after handleMemberRemove")
	}
	if left != "42" {
		t.Fatalf("OnLeave fired with %q, want 42", left)
	}
}

func TestHandleEvents_IgnoreOtherGuilds(t *testing.T) {
	gw := &Gateway{GuildID: guildID, Members: NewMemberCache()}
	gw.OnJoin = func(string) { t.Fatal("OnJoin fired for a different guild") }

	gw.handleMemberAdd(nil, &discordgo.GuildMemberAdd{Member: &discordgo.Member{
		GuildID: "different-guild", User: &discordgo.User{ID: "1", Username: "x"},
	}})
	if _, ok := gw.Members.Username("1"); ok {
		t.Fatal("member from a different guild was cached")
	}
}
