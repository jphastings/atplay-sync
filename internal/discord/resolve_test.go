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

func TestResolveDiscordSubject_NotInGuildYet_NotOK(t *testing.T) {
	r := &ClaimResolver{Members: NewMemberCache()}
	if _, ok := r.ResolveDiscordSubject(context.Background(), "did:plc:test", realDiscordClaim()); ok {
		t.Fatal("ResolveDiscordSubject = true, want false — cache is empty, they haven't joined")
	}
}
