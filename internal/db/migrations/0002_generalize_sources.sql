ALTER TABLE steam_claims RENAME TO steam_claims_old;
ALTER TABLE sync_prefs RENAME TO sync_prefs_old;

CREATE TABLE claims (
  did              TEXT NOT NULL REFERENCES users(did),
  claim_type       TEXT NOT NULL,   -- "steam" | "discord"
  subject          TEXT NOT NULL,   -- SteamID64, or a resolved Discord snowflake
  display_name     TEXT NOT NULL,
  claim_uri        TEXT NOT NULL,
  record_uri       TEXT NOT NULL,
  last_verified_at TEXT NOT NULL,
  PRIMARY KEY (did, claim_type)
);

CREATE TABLE sync_prefs (
  did      TEXT NOT NULL REFERENCES users(did),
  source   TEXT NOT NULL,    -- "steam" | "discord"
  enabled  INTEGER NOT NULL DEFAULT 0,
  priority INTEGER NOT NULL, -- lower = higher priority among ENABLED rows
  PRIMARY KEY (did, source)
);

INSERT INTO claims (did, claim_type, subject, display_name, claim_uri, record_uri, last_verified_at)
  SELECT did, 'steam', subject, display_name, claim_uri, record_uri, last_verified_at FROM steam_claims_old;

INSERT INTO sync_prefs (did, source, enabled, priority)
  SELECT did, 'steam', steam_enabled, 0 FROM sync_prefs_old;

DROP TABLE steam_claims_old;
DROP TABLE sync_prefs_old;
