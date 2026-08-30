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
| `STEAM_API_KEY` | A [Steam Web API key](https://steamcommunity.com/dev/apikey). |
| `BASE_URL` | The public HTTPS origin this service is reachable on, e.g. `https://game-status.example.com`. The OAuth client metadata, callback and JWKS URLs are derived from it. |
| `OAUTH_PRIVATE_KEY` | A P-256 confidential-client key in multibase form. Generate with [`goat`](https://github.com/bluesky-social/goat): `goat key generate -t P-256`. |
| `SESSION_SECRET` | 32 random bytes, hex-encoded, for signing session cookies: `openssl rand -hex 32`. |

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
