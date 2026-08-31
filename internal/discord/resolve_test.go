// internal/discord/resolve_test.go
package discord

import (
	"context"
	"testing"

	"github.com/jphastings/game-status/internal/keytrace"
)

func realDiscordClaim() keytrace.Claim {
	return keytrace.Claim{
		Type: "discord",
		Identity: keytrace.ClaimIdentity{
			Subject:    "jphastings",
			ProfileURL: "https://discord.com/users/690973862245957683",
		},
	}
}

func TestResolveDiscordSubject_TrustsSignedSubjectViaProfileURLHint(t *testing.T) {
	members := NewMemberCache()
	members.Set("690973862245957683", "jphastings")
	r := &ClaimResolver{Members: members}

	id, ok := r.ResolveDiscordSubject(context.Background(), "did:plc:test", realDiscordClaim())
	if !ok || id != "690973862245957683" {
		t.Fatalf("ResolveDiscordSubject = %q, %v, want the hinted ID confirmed by cache", id, ok)
	}
}

func TestResolveDiscordSubject_RejectsProfileURLHintWhenUsernameMismatches(t *testing.T) {
	members := NewMemberCache()
	members.Set("690973862245957683", "someone-else-now") // profileUrl is unsigned — this ID may not actually be them
	r := &ClaimResolver{Members: members}

	if _, ok := r.ResolveDiscordSubject(context.Background(), "did:plc:test", realDiscordClaim()); ok {
		t.Fatal("ResolveDiscordSubject trusted an unsigned profileUrl hint whose username doesn't match the signed subject")
	}
}

func TestResolveDiscordSubject_FallsBackToUsernameScanWhenProfileURLMissing(t *testing.T) {
	members := NewMemberCache()
	members.Set("690973862245957683", "jphastings")
	claim := realDiscordClaim()
	claim.Identity.ProfileURL = ""
	r := &ClaimResolver{Members: members}

	id, ok := r.ResolveDiscordSubject(context.Background(), "did:plc:test", claim)
	if !ok || id != "690973862245957683" {
		t.Fatalf("ResolveDiscordSubject (no hint) = %q, %v, want a match via username scan", id, ok)
	}
}

func TestSnowflakeFromProfileURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{"well-formed URL", "https://discord.com/users/690973862245957683", "690973862245957683"},
		{"empty ID", "https://discord.com/users/", ""},
		{"non-digit ID", "https://discord.com/users/../admin", ""},
		{"non-matching prefix", "https://example.com/users/690973862245957683", ""},
		{"trailing slash", "https://discord.com/users/690973862245957683/", ""},
		{"extra path segment", "https://discord.com/users/690973862245957683/profile", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := snowflakeFromProfileURL(tt.url); got != tt.want {
				t.Fatalf("snowflakeFromProfileURL(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestResolveDiscordSubject_NotInGuildYet_NotOK(t *testing.T) {
	r := &ClaimResolver{Members: NewMemberCache()}
	if _, ok := r.ResolveDiscordSubject(context.Background(), "did:plc:test", realDiscordClaim()); ok {
		t.Fatal("ResolveDiscordSubject = true, want false — cache is empty, they haven't joined")
	}
}

func TestConfirmDiscordSubject_StillMatches_True(t *testing.T) {
	members := NewMemberCache()
	members.Set("690973862245957683", "jphastings")
	r := &ClaimResolver{Members: members}

	if !r.ConfirmDiscordSubject(context.Background(), realDiscordClaim(), "690973862245957683") {
		t.Fatal("ConfirmDiscordSubject = false, want true — username still matches the signed subject")
	}
}

func TestConfirmDiscordSubject_UsernameChanged_False(t *testing.T) {
	members := NewMemberCache()
	members.Set("690973862245957683", "someone-else-now") // snowflake was reclaimed or owner renamed
	r := &ClaimResolver{Members: members}

	if r.ConfirmDiscordSubject(context.Background(), realDiscordClaim(), "690973862245957683") {
		t.Fatal("ConfirmDiscordSubject = true, want false — snowflake maps to a different username now, preventing hijack")
	}
}

func TestConfirmDiscordSubject_SnowflakeNotInCache_False(t *testing.T) {
	members := NewMemberCache()
	r := &ClaimResolver{Members: members}

	if r.ConfirmDiscordSubject(context.Background(), realDiscordClaim(), "690973862245957683") {
		t.Fatal("ConfirmDiscordSubject = true, want false — snowflake not in cache (e.g. user left guild)")
	}
}
