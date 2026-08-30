# game-status

Non-obvious facts worth knowing before touching this repo again.

## atproto facts, confirmed empirically (not documented anywhere by atproto itself)

- The reference PDS implementation (bsky.social, and third-party PDSes built
  on `@atproto/pds`) serves open CORS (`access-control-allow-origin: *`) on
  public read XRPC endpoints — `com.atproto.repo.getRecord`,
  `com.atproto.sync.getBlob`, `com.atproto.repo.listRecords`. Confirmed live
  against 3 independent hosts. This is why the frontend reads status/game/
  cover-art records straight from PDSes client-side (`web/src/atproto.ts`)
  instead of proxying through our backend — default to that pattern for any
  new public-record read; don't add a backend pass-through reflexively.
- A DID's app-view/query host and its actual PDS host can differ. cartridge's
  `getGame` XRPC lives at `gamesgamesgamesgames.games`, but
  `did:web:gamesgamesgamesgames.games`'s real PDS is
  `pds.gamesgamesgamesgames.games`. Always resolve the DID doc's `service`
  array for the PDS endpoint — never assume the query host is the PDS.
- Local OAuth dev: this app is a confidential client (holds a private key),
  which the spec's `http://localhost` loopback exception doesn't cover at
  all, and plain `http://127.0.0.1:PORT` as `BASE_URL` also failed
  (`invalid_client_metadata`) against a real PDS. Use a real HTTPS tunnel
  (`cloudflared tunnel --url http://127.0.0.1:PORT` works) and point
  `BASE_URL` at it — confirmed working end to end.

## Design principle

PDS is authoritative. Don't duplicate into our own backend/DB anything a
client can read directly and verifiably from public atproto infrastructure
(the user's own status record, a linked game record, cover art). This has
been the default since the frontend rewrite — keep defaulting to it.

## Tooling gotchas (this machine/session, not the repo)

- The chrome-devtools MCP browser can refuse to start with "browser already
  running for .../chrome-profile" — a stale process from a prior session
  still holding the profile lock. Fix: `ps aux | grep chrome-devtools-mcp`
  (and `chrome-profile`) and kill the stale PIDs; safe, it's a dedicated
  automation profile.
- Backticks inside a `git commit -m "..."` double-quoted string trigger bash
  command substitution and silently mangle the message. Use
  `git commit -F - <<'EOF' ... EOF` (single-quoted heredoc) for any message
  that quotes a backtick-wrapped identifier.
