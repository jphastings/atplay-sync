# game-status

A small Go service that keeps your `games.gamesgamesgamesgames.actor.status`
record live on your own atproto PDS while you play. Sign in with atproto OAuth,
prove your Steam identity with a cryptographically-verified
[keytrace](https://keytrace.dev) claim, and the service polls Steam — no
more than once a minute, and evenly spread across the rest of the day
against a self-imposed daily call budget (or a real `429`) — to write
(and clear) your play status. A growing user base slows the cadence
gracefully instead of bursting through the budget early and going quiet
for the rest of the day. A Jetstream subscription watches
`dev.keytrace.claim` so a revoked claim stops the sync in real time, with
a daily sweep as the backstop.

## Build

The binary embeds the built frontend from `cmd/server/web/dist`, which is a
build artifact and is not in git — so `go build ./...` cannot work on a fresh
clone until the frontend is built. Use the Makefile, which does both:

```sh
make build   # pnpm install + vite build, then go build ./cmd/server
make test    # same, then go test ./...
```

Requires Go and [pnpm](https://pnpm.io).

## Configuration

All via environment variables. Required:

| Variable | What it is |
| --- | --- |
| `STEAM_API_KEY` | A [Steam Web API key](https://steamcommunity.com/dev/apikey). See [docs/steam.md](docs/steam.md). |
| `BASE_URL` | The public HTTPS origin this service is reachable on, e.g. `https://sync.atplay.games`. The OAuth client metadata, callback and JWKS URLs are derived from it. |
| `OAUTH_PRIVATE_KEY` | A P-256 confidential-client key in multibase form. Generate with [`goat`](https://github.com/bluesky-social/goat): `goat key generate -t P-256`. |
| `SESSION_SECRET` | 32 random bytes, hex-encoded, for signing session cookies: `openssl rand -hex 32`. |
| `DISCORD_BOT_TOKEN` | A bot token for the tracking guild's application, with the guild presences and guild members privileged intents enabled. |
| `DISCORD_GUILD_ID` | The snowflake ID of the tracking guild the bot watches for presence. |
| `DISCORD_INVITE_URL` | A permanent invite link to the tracking guild, shown in the "join the server" UI prompt. |

Optional: `LISTEN_ADDR` (default `:8080`), `DB_PATH` (default
`game-status.db`), `OAUTH_KEY_ID` (default `1`), `CARTRIDGE_HOST`,
`CARTRIDGE_CLIENT_KEY` (cartridge.dev's game lookup API is open access
without one — set this only if cartridge/HappyView give you a key of your
own for better rate limits or attribution; don't reuse the key baked into
their public frontend bundle, it identifies their app, not yours),
`STEAM_DAILY_CALL_BUDGET` (default `100000` — the commonly cited, unofficial
ceiling for a Steam Web API key; the poll interval widens as this gets
spent down over the day, and the sync loop stops calling Steam entirely
for the rest of the day if it runs out, whether from hitting this or from
Steam itself returning `429`).

### The Discord tracking guild

Discord only delivers presence over a bot's Gateway connection, and only for
members of a guild the bot is in — there's no polling API like Steam's. The
tracking guild this points at exists purely to satisfy that scoping rule:
`@everyone` has no channel visibility, so there's nothing to browse or read,
and onboarding happens by DM on join rather than in a shared channel. See
[docs/discord.md](docs/discord.md) for how to set one up, and
[the design doc](docs/superpowers/specs/2026-08-30-discord-game-status-sync-design.md)
for the full privacy rationale and how a claim's signed Discord username
resolves to the guild member it belongs to.
