// internal/jetstream/dbstore.go
package jetstream

import (
	"context"
	"database/sql"

	appdb "github.com/jphastings/game-status/internal/db"
)

type DBStore struct{ Conn *sql.DB }

var _ Store = DBStore{}

func (s DBStore) GetSteamClaim(ctx context.Context, did string) (*appdb.SteamClaim, error) {
	return appdb.GetSteamClaim(ctx, s.Conn, did)
}
func (s DBStore) UpsertSteamClaim(ctx context.Context, c appdb.SteamClaim) error {
	return appdb.UpsertSteamClaim(ctx, s.Conn, c)
}
func (s DBStore) InvalidateSteamClaim(ctx context.Context, did string) error {
	return appdb.InvalidateSteamClaim(ctx, s.Conn, did)
}
