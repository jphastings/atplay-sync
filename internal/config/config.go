package config

import (
	"fmt"
	"os"
)

type Config struct {
	ListenAddr string
	DBPath     string
}

func Load() (*Config, error) {
	return &Config{
		ListenAddr: envOr("LISTEN_ADDR", ":8080"),
		DBPath:     envOr("DB_PATH", "game-status.db"),
	}, nil
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
