CREATE TABLE IF NOT EXISTS users (
  did TEXT PRIMARY KEY,
  active_session_id TEXT,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS oauth_sessions (
  did TEXT NOT NULL,
  session_id TEXT NOT NULL,
  data BLOB NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY (did, session_id)
);

CREATE TABLE IF NOT EXISTS oauth_auth_requests (
  state TEXT PRIMARY KEY,
  data BLOB NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS steam_claims (
  did TEXT PRIMARY KEY REFERENCES users(did),
  subject TEXT NOT NULL,
  display_name TEXT NOT NULL,
  claim_uri TEXT NOT NULL,
  record_uri TEXT NOT NULL,
  last_verified_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sync_prefs (
  did TEXT PRIMARY KEY REFERENCES users(did),
  steam_enabled INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS session_starts (
  did TEXT NOT NULL,
  source TEXT NOT NULL,
  game_key TEXT NOT NULL,
  started_at TEXT NOT NULL,
  PRIMARY KEY (did, source)
);

CREATE TABLE IF NOT EXISTS game_cache (
  steam_id TEXT PRIMARY KEY,
  game_uri TEXT NOT NULL,
  page_url TEXT NOT NULL,
  name TEXT NOT NULL,
  summary TEXT NOT NULL,
  cached_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS keytrace_key_cache (
  at_uri TEXT PRIMARY KEY,
  public_jwk TEXT NOT NULL,
  valid_from TEXT NOT NULL,
  valid_until TEXT NOT NULL,
  cached_at TEXT NOT NULL
);
