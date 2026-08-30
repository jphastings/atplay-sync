// internal/discord/resolve.go
package discord

import (
	"context"
	"strings"

	"github.com/jphastings/game-status/internal/keytrace"
)

// ClaimResolver resolves a verified Discord claim's signed username to a
// stable snowflake ID. Security property: only claim.Identity.Subject (the
// SIGNED field) is ever load-bearing. claim.Identity.ProfileURL is unsigned
// client-filled metadata — usable only as a fast-path hint that still has
// to be confirmed against the signed subject before being trusted.
type ClaimResolver struct {
	Members *MemberCache
}

func (r *ClaimResolver) ResolveDiscordSubject(ctx context.Context, did string, claim keytrace.Claim) (string, bool) {
	if id := snowflakeFromProfileURL(claim.Identity.ProfileURL); id != "" {
		if username, ok := r.Members.Username(id); ok && strings.EqualFold(username, claim.Identity.Subject) {
			return id, true
		}
	}
	return r.Members.FindByUsername(claim.Identity.Subject)
}

func snowflakeFromProfileURL(url string) string {
	const prefix = "https://discord.com/users/"
	if !strings.HasPrefix(url, prefix) {
		return ""
	}
	id := strings.TrimPrefix(url, prefix)
	if id == "" {
		return ""
	}
	for _, r := range id {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return id
}
