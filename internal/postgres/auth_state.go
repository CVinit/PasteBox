package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"pastebox/internal/app"
)

var (
	ErrSessionNotFound      = errors.New("postgres session not found")
	ErrAuthTokenNotFound    = errors.New("postgres auth token not found")
	ErrLoginFailureNotFound = errors.New("postgres login failure not found")
)

type SessionStore struct {
	pool *pgxpool.Pool
}

func NewSessionStore(pool *pgxpool.Pool) *SessionStore {
	return &SessionStore{pool: pool}
}

func (s *SessionStore) CreateSession(ctx context.Context, session app.Session) error {
	if _, err := s.pool.Exec(ctx, `
INSERT INTO sessions (id, user_id, created_at, expires_at, revoked_at)
VALUES ($1, $2, $3, $4, $5)
`, session.ID, session.UserID, session.CreatedAt, session.ExpiresAt, session.RevokedAt); err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

func (s *SessionStore) SessionByID(ctx context.Context, id string) (app.Session, error) {
	var session app.Session
	var revokedAt pgtype.Timestamptz
	err := s.pool.QueryRow(ctx, `
SELECT id, user_id, created_at, expires_at, revoked_at
FROM sessions
WHERE id = $1
`, id).Scan(&session.ID, &session.UserID, &session.CreatedAt, &session.ExpiresAt, &revokedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return app.Session{}, ErrSessionNotFound
		}
		return app.Session{}, fmt.Errorf("read session: %w", err)
	}
	session.RevokedAt = optionalTime(revokedAt)
	return session, nil
}

func (s *SessionStore) RevokeSession(ctx context.Context, id string, revokedAt time.Time) error {
	tag, err := s.pool.Exec(ctx, `
UPDATE sessions
SET revoked_at = $2
WHERE id = $1
`, id, revokedAt)
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrSessionNotFound
	}
	return nil
}

func (s *SessionStore) RevokeUserSessions(ctx context.Context, userID string, revokedAt time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
UPDATE sessions
SET revoked_at = $2
WHERE user_id = $1 AND revoked_at IS NULL
`, userID, revokedAt)
	if err != nil {
		return 0, fmt.Errorf("revoke user sessions: %w", err)
	}
	return tag.RowsAffected(), nil
}

type AuthTokenStore struct {
	pool *pgxpool.Pool
}

func NewAuthTokenStore(pool *pgxpool.Pool) *AuthTokenStore {
	return &AuthTokenStore{pool: pool}
}

func (s *AuthTokenStore) CreateAuthToken(ctx context.Context, kind string, token app.AuthToken) error {
	if _, err := s.pool.Exec(ctx, `
INSERT INTO auth_tokens (hash, user_id, email, kind, expires_at, used_at)
VALUES ($1, $2, $3, $4, $5, $6)
`, token.Hash, token.UserID, token.Email, kind, token.ExpiresAt, token.UsedAt); err != nil {
		return fmt.Errorf("create auth token: %w", err)
	}
	return nil
}

func (s *AuthTokenStore) AuthToken(ctx context.Context, kind string, hash string) (app.AuthToken, error) {
	var token app.AuthToken
	var usedAt pgtype.Timestamptz
	err := s.pool.QueryRow(ctx, `
SELECT hash, user_id, email, expires_at, used_at
FROM auth_tokens
WHERE kind = $1 AND hash = $2
`, kind, hash).Scan(&token.Hash, &token.UserID, &token.Email, &token.ExpiresAt, &usedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return app.AuthToken{}, ErrAuthTokenNotFound
		}
		return app.AuthToken{}, fmt.Errorf("read auth token: %w", err)
	}
	token.UsedAt = optionalTime(usedAt)
	return token, nil
}

func (s *AuthTokenStore) MarkAuthTokenUsed(ctx context.Context, kind string, hash string, usedAt time.Time) error {
	tag, err := s.pool.Exec(ctx, `
UPDATE auth_tokens
SET used_at = $3
WHERE kind = $1 AND hash = $2
`, kind, hash, usedAt)
	if err != nil {
		return fmt.Errorf("mark auth token used: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrAuthTokenNotFound
	}
	return nil
}

type LoginFailureStore struct {
	pool *pgxpool.Pool
}

func NewLoginFailureStore(pool *pgxpool.Pool) *LoginFailureStore {
	return &LoginFailureStore{pool: pool}
}

func (s *LoginFailureStore) LoginFailure(ctx context.Context, email string) (app.LoginFailure, error) {
	var failure app.LoginFailure
	var lockedUntil pgtype.Timestamptz
	err := s.pool.QueryRow(ctx, `
SELECT count, window_start, locked_until
FROM login_failures
WHERE email = $1
`, email).Scan(&failure.Count, &failure.WindowStart, &lockedUntil)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return app.LoginFailure{}, ErrLoginFailureNotFound
		}
		return app.LoginFailure{}, fmt.Errorf("read login failure: %w", err)
	}
	if value := optionalTime(lockedUntil); value != nil {
		failure.LockedUntil = *value
	}
	return failure, nil
}

func (s *LoginFailureStore) SaveLoginFailure(ctx context.Context, email string, failure app.LoginFailure) error {
	var lockedUntil *time.Time
	if !failure.LockedUntil.IsZero() {
		lockedUntil = &failure.LockedUntil
	}
	if _, err := s.pool.Exec(ctx, `
INSERT INTO login_failures (email, count, window_start, locked_until)
VALUES ($1, $2, $3, $4)
ON CONFLICT (email) DO UPDATE SET
	count = EXCLUDED.count,
	window_start = EXCLUDED.window_start,
	locked_until = EXCLUDED.locked_until
`, email, failure.Count, failure.WindowStart, lockedUntil); err != nil {
		return fmt.Errorf("save login failure: %w", err)
	}
	return nil
}

func (s *LoginFailureStore) DeleteLoginFailure(ctx context.Context, email string) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM login_failures WHERE email = $1`, email); err != nil {
		return fmt.Errorf("delete login failure: %w", err)
	}
	return nil
}
