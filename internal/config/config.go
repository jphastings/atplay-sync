package config

import (
	"encoding/hex"
	"fmt"
	"os"
)

type Config struct {
	ListenAddr string
	DBPath     string

	BaseURL                  string
	SessionSecret            []byte
	OAuthPrivateKeyMultibase string
	OAuthKeyID               string

	CartridgeHost      string
	CartridgeClientKey string

	SteamAPIKey string
}

func Load() (*Config, error) {
	cfg := &Config{
		ListenAddr: envOr("LISTEN_ADDR", ":8080"),
		DBPath:     envOr("DB_PATH", "game-status.db"),
	}

	baseURL, err := requireEnv("BASE_URL")
	if err != nil {
		return nil, err
	}
	cfg.BaseURL = baseURL

	secretHex, err := requireEnv("SESSION_SECRET")
	if err != nil {
		return nil, err
	}
	secret, err := hex.DecodeString(secretHex)
	if err != nil {
		return nil, fmt.Errorf("SESSION_SECRET must be hex-encoded: %w", err)
	}
	cfg.SessionSecret = secret

	oauthKey, err := requireEnv("OAUTH_PRIVATE_KEY")
	if err != nil {
		return nil, err
	}
	cfg.OAuthPrivateKeyMultibase = oauthKey
	cfg.OAuthKeyID = envOr("OAUTH_KEY_ID", "1")

	cfg.CartridgeHost = envOr("CARTRIDGE_HOST", "https://gamesgamesgamesgames.games")
	// CARTRIDGE_CLIENT_KEY is optional — cartridge.dev's getGame endpoint is
	// open access (confirmed live: unauthenticated requests return 200).
	// Sent when set, in case it affects rate limits or attribution.
	cfg.CartridgeClientKey = envOr("CARTRIDGE_CLIENT_KEY", "")

	steamKey, err := requireEnv("STEAM_API_KEY")
	if err != nil {
		return nil, err
	}
	cfg.SteamAPIKey = steamKey

	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func requireEnv(key string) (string, error) {
	v := os.Getenv(key)
	if v == "" {
		return "", fmt.Errorf("required environment variable %s is not set", key)
	}
	return v, nil
}
