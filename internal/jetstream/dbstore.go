// internal/jetstream/dbstore.go
package jetstream

import (
	"context"
	"database/sql"
	"time"

	appdb "github.com/jphastings/game-status/internal/db"
)

type DBStore struct {
	Conn       *sql.DB
	Reconciler appdb.Reconciler
}

var _ Store = DBStore{}

func (s DBStore) GetClaim(ctx context.Context, did, claimType string) (*appdb.Claim, error) {
	return appdb.GetClaim(ctx, s.Conn, did, claimType)
}
func (s DBStore) UpsertClaim(ctx context.Context, c appdb.Claim) error {
	return appdb.UpsertClaim(ctx, s.Conn, c)
}
func (s DBStore) InvalidateClaim(ctx context.Context, did string) error {
	return appdb.InvalidateClaim(ctx, s.Conn, s.Reconciler, did, appdb.SteamSource, time.Now())
}
