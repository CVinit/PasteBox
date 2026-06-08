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
	ErrSessionNotFound       = errors.Join(errors.New("postgres session not found"), app.ErrStoreNotFound)
	ErrAuthTokenNotFound     = errors.Join(errors.New("postgres auth token not found"), app.ErrStoreNotFound)
	ErrLoginFailureNotFound  = errors.Join(errors.New("postgres login failure not found"), app.ErrStoreNotFound)
	ErrOAuthIdentityNotFound = errors.Join(errors.New("postgres oauth identity not found"), app.ErrStoreNotFound)
	ErrOAuthIdentityConflict = errors.Join(errors.New("postgres oauth identity conflict"), app.ErrStoreConflict)
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
	var userID any = token.UserID
	if token.UserID == "" {
		userID = nil
	}
	if _, err := s.pool.Exec(ctx, `
INSERT INTO auth_tokens (hash, user_id, email, kind, expires_at, used_at)
VALUES ($1, $2, $3, $4, $5, $6)
`, token.Hash, userID, token.Email, kind, token.ExpiresAt, token.UsedAt); err != nil {
		return fmt.Errorf("create auth token: %w", err)
	}
	return nil
}

func (s *AuthTokenStore) AuthToken(ctx context.Context, kind string, hash string) (app.AuthToken, error) {
	var token app.AuthToken
	var userID pgtype.Text
	var usedAt pgtype.Timestamptz
	err := s.pool.QueryRow(ctx, `
SELECT hash, user_id, email, expires_at, used_at
FROM auth_tokens
WHERE kind = $1 AND hash = $2
`, kind, hash).Scan(&token.Hash, &userID, &token.Email, &token.ExpiresAt, &usedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return app.AuthToken{}, ErrAuthTokenNotFound
		}
		return app.AuthToken{}, fmt.Errorf("read auth token: %w", err)
	}
	token.UserID = userID.String
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

type OAuthIdentityStore struct {
	pool *pgxpool.Pool
}

func NewOAuthIdentityStore(pool *pgxpool.Pool) *OAuthIdentityStore {
	return &OAuthIdentityStore{pool: pool}
}

func (s *OAuthIdentityStore) LinkOAuthIdentity(ctx context.Context, identity app.OAuthIdentity) error {
	if _, err := s.pool.Exec(ctx, `
INSERT INTO oauth_identities (user_id, provider, subject, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5)
`, identity.UserID, identity.Provider, identity.Subject, identity.CreatedAt, identity.UpdatedAt); err != nil {
		if isUniqueViolation(err, "") {
			return ErrOAuthIdentityConflict
		}
		return fmt.Errorf("link oauth identity: %w", err)
	}
	return nil
}

func (s *OAuthIdentityStore) OAuthIdentityByProviderSubject(ctx context.Context, provider string, subject string) (app.OAuthIdentity, error) {
	return s.queryOAuthIdentity(ctx, `
SELECT user_id, provider, subject, created_at, updated_at
FROM oauth_identities
WHERE provider = $1 AND subject = $2
`, provider, subject)
}

func (s *OAuthIdentityStore) OAuthIdentitiesByUser(ctx context.Context, userID string) ([]app.OAuthIdentity, error) {
	rows, err := s.pool.Query(ctx, `
SELECT user_id, provider, subject, created_at, updated_at
FROM oauth_identities
WHERE user_id = $1
ORDER BY provider ASC
`, userID)
	if err != nil {
		return nil, fmt.Errorf("query oauth identities: %w", err)
	}
	defer rows.Close()

	identities := []app.OAuthIdentity{}
	for rows.Next() {
		identity, err := scanOAuthIdentity(rows)
		if err != nil {
			return nil, err
		}
		identities = append(identities, identity)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read oauth identities: %w", err)
	}
	return identities, nil
}

func (s *OAuthIdentityStore) DeleteOAuthIdentity(ctx context.Context, userID string, provider string) error {
	tag, err := s.pool.Exec(ctx, `
DELETE FROM oauth_identities
WHERE user_id = $1 AND provider = $2
`, userID, provider)
	if err != nil {
		return fmt.Errorf("delete oauth identity: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrOAuthIdentityNotFound
	}
	return nil
}

func (s *OAuthIdentityStore) queryOAuthIdentity(ctx context.Context, sql string, args ...any) (app.OAuthIdentity, error) {
	identity, err := scanOAuthIdentity(s.pool.QueryRow(ctx, sql, args...))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return app.OAuthIdentity{}, ErrOAuthIdentityNotFound
		}
		return app.OAuthIdentity{}, err
	}
	return identity, nil
}

type oauthIdentityRow interface {
	Scan(dest ...any) error
}

func scanOAuthIdentity(row oauthIdentityRow) (app.OAuthIdentity, error) {
	var identity app.OAuthIdentity
	if err := row.Scan(
		&identity.UserID,
		&identity.Provider,
		&identity.Subject,
		&identity.CreatedAt,
		&identity.UpdatedAt,
	); err != nil {
		return app.OAuthIdentity{}, fmt.Errorf("scan oauth identity: %w", err)
	}
	return identity, nil
}
