package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"pastebox/internal/app"
)

func TestAuthStateStoresPersistSessionTokenAndLoginFailure(t *testing.T) {
	databaseURL := os.Getenv("PASTEBOX_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set PASTEBOX_TEST_DATABASE_URL to run PostgreSQL auth-state integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := ApplyMigrations(ctx, databaseURL); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	defer pool.Close()

	userID := "usr_auth_state_test"
	sessionID := "sess_auth_state_test"
	secondSessionID := "sess_auth_state_test_second"
	tokenHash := "auth_state_token_hash"
	email := "auth-state-test@example.com"
	cleanupAuthStateTestRows(ctx, t, pool, userID, email)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		cleanupAuthStateTestRows(cleanupCtx, t, pool, userID, email)
	})

	user := app.User{
		ID:            userID,
		Email:         email,
		DisplayName:   "Auth State Test",
		Language:      "en",
		PasswordHash:  "argon2-test-hash",
		Role:          "user",
		EmailVerified: true,
		PlanID:        "free",
		CreatedAt:     time.Date(2026, 5, 24, 9, 0, 0, 0, time.UTC),
		UpdatedAt:     time.Date(2026, 5, 24, 9, 0, 0, 0, time.UTC),
	}
	if err := NewUserStore(pool).CreateUser(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	sessionStore := NewSessionStore(pool)
	createdAt := time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC)
	expiresAt := createdAt.Add(30 * 24 * time.Hour)
	session := app.Session{ID: sessionID, UserID: userID, CreatedAt: createdAt, ExpiresAt: expiresAt}
	if err := sessionStore.CreateSession(ctx, session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	secondSession := app.Session{ID: secondSessionID, UserID: userID, CreatedAt: createdAt.Add(time.Minute), ExpiresAt: expiresAt}
	if err := sessionStore.CreateSession(ctx, secondSession); err != nil {
		t.Fatalf("create second session: %v", err)
	}
	loadedSession, err := sessionStore.SessionByID(ctx, sessionID)
	if err != nil {
		t.Fatalf("read session: %v", err)
	}
	if loadedSession.ID != sessionID || loadedSession.UserID != userID || loadedSession.RevokedAt != nil {
		t.Fatalf("unexpected session: %#v", loadedSession)
	}
	revokedAt := createdAt.Add(time.Hour)
	if err := sessionStore.RevokeSession(ctx, sessionID, revokedAt); err != nil {
		t.Fatalf("revoke session: %v", err)
	}
	revokedSession, err := sessionStore.SessionByID(ctx, sessionID)
	if err != nil {
		t.Fatalf("read revoked session: %v", err)
	}
	if revokedSession.RevokedAt == nil || !revokedSession.RevokedAt.Equal(revokedAt) {
		t.Fatalf("expected session revoked at %s, got %#v", revokedAt, revokedSession.RevokedAt)
	}
	if count, err := sessionStore.RevokeUserSessions(ctx, userID, revokedAt.Add(time.Minute)); err != nil || count != 1 {
		t.Fatalf("expected one remaining user session revoked, count=%d err=%v", count, err)
	}
	if _, err := sessionStore.SessionByID(ctx, "sess_auth_state_missing"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected missing session error, got %v", err)
	}

	authTokenStore := NewAuthTokenStore(pool)
	tokenExpiresAt := createdAt.Add(24 * time.Hour)
	token := app.AuthToken{Hash: tokenHash, UserID: userID, Email: email, ExpiresAt: tokenExpiresAt}
	if err := authTokenStore.CreateAuthToken(ctx, "email_verification", token); err != nil {
		t.Fatalf("create auth token: %v", err)
	}
	loadedToken, err := authTokenStore.AuthToken(ctx, "email_verification", tokenHash)
	if err != nil {
		t.Fatalf("read auth token: %v", err)
	}
	if loadedToken.Hash != tokenHash || loadedToken.UserID != userID || loadedToken.UsedAt != nil {
		t.Fatalf("unexpected auth token: %#v", loadedToken)
	}
	usedAt := createdAt.Add(2 * time.Hour)
	if err := authTokenStore.MarkAuthTokenUsed(ctx, "email_verification", tokenHash, usedAt); err != nil {
		t.Fatalf("mark auth token used: %v", err)
	}
	usedToken, err := authTokenStore.AuthToken(ctx, "email_verification", tokenHash)
	if err != nil {
		t.Fatalf("read used auth token: %v", err)
	}
	if usedToken.UsedAt == nil || !usedToken.UsedAt.Equal(usedAt) {
		t.Fatalf("expected auth token used at %s, got %#v", usedAt, usedToken.UsedAt)
	}
	if _, err := authTokenStore.AuthToken(ctx, "wrong_kind", tokenHash); !errors.Is(err, ErrAuthTokenNotFound) {
		t.Fatalf("expected wrong-kind token miss, got %v", err)
	}

	loginFailureStore := NewLoginFailureStore(pool)
	failure := app.LoginFailure{Count: 4, WindowStart: createdAt, LockedUntil: createdAt.Add(15 * time.Minute)}
	if err := loginFailureStore.SaveLoginFailure(ctx, email, failure); err != nil {
		t.Fatalf("save login failure: %v", err)
	}
	loadedFailure, err := loginFailureStore.LoginFailure(ctx, email)
	if err != nil {
		t.Fatalf("read login failure: %v", err)
	}
	if loadedFailure.Count != 4 || !loadedFailure.WindowStart.Equal(failure.WindowStart) || !loadedFailure.LockedUntil.Equal(failure.LockedUntil) {
		t.Fatalf("unexpected login failure: %#v", loadedFailure)
	}
	if err := loginFailureStore.DeleteLoginFailure(ctx, email); err != nil {
		t.Fatalf("delete login failure: %v", err)
	}
	if _, err := loginFailureStore.LoginFailure(ctx, email); !errors.Is(err, ErrLoginFailureNotFound) {
		t.Fatalf("expected deleted login failure miss, got %v", err)
	}
}

func cleanupAuthStateTestRows(ctx context.Context, t *testing.T, pool *pgxpool.Pool, userID string, email string) {
	t.Helper()
	_, _ = pool.Exec(ctx, `DELETE FROM auth_tokens WHERE user_id = $1`, userID)
	_, _ = pool.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID)
	_, _ = pool.Exec(ctx, `DELETE FROM login_failures WHERE email = $1`, email)
	_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
}
