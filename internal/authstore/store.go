package authstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/bluesky-social/indigo/atproto/auth/oauth"
	"github.com/bluesky-social/indigo/atproto/syntax"
)

type SQLiteStore struct {
	Conn *sql.DB
}

var _ oauth.ClientAuthStore = (*SQLiteStore)(nil)

func (s *SQLiteStore) GetSession(ctx context.Context, did syntax.DID, sessionID string) (*oauth.ClientSessionData, error) {
	var raw []byte
	err := s.Conn.QueryRowContext(ctx, `SELECT data FROM oauth_sessions WHERE did = ? AND session_id = ?`, did.String(), sessionID).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("no session for %s/%s", did, sessionID)
	}
	if err != nil {
		return nil, err
	}
	var sess oauth.ClientSessionData
	if err := json.Unmarshal(raw, &sess); err != nil {
		return nil, err
	}
	return &sess, nil
}

func (s *SQLiteStore) SaveSession(ctx context.Context, sess oauth.ClientSessionData) error {
	raw, err := json.Marshal(sess)
	if err != nil {
		return err
	}
	_, err = s.Conn.ExecContext(ctx, `
		INSERT INTO oauth_sessions (did, session_id, data, updated_at) VALUES (?, ?, ?, datetime('now'))
		ON CONFLICT(did, session_id) DO UPDATE SET data = excluded.data, updated_at = excluded.updated_at
	`, sess.AccountDID.String(), sess.SessionID, raw)
	return err
}

func (s *SQLiteStore) DeleteSession(ctx context.Context, did syntax.DID, sessionID string) error {
	_, err := s.Conn.ExecContext(ctx, `DELETE FROM oauth_sessions WHERE did = ? AND session_id = ?`, did.String(), sessionID)
	return err
}

func (s *SQLiteStore) GetAuthRequestInfo(ctx context.Context, state string) (*oauth.AuthRequestData, error) {
	var raw []byte
	err := s.Conn.QueryRowContext(ctx, `SELECT data FROM oauth_auth_requests WHERE state = ?`, state).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("no auth request for state %s", state)
	}
	if err != nil {
		return nil, err
	}
	var info oauth.AuthRequestData
	if err := json.Unmarshal(raw, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

func (s *SQLiteStore) SaveAuthRequestInfo(ctx context.Context, info oauth.AuthRequestData) error {
	raw, err := json.Marshal(info)
	if err != nil {
		return err
	}
	_, err = s.Conn.ExecContext(ctx, `INSERT INTO oauth_auth_requests (state, data, created_at) VALUES (?, ?, datetime('now'))`, info.State, raw)
	return err
}

func (s *SQLiteStore) DeleteAuthRequestInfo(ctx context.Context, state string) error {
	_, err := s.Conn.ExecContext(ctx, `DELETE FROM oauth_auth_requests WHERE state = ?`, state)
	return err
}
