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

func TestUserStoreCreatesReadsAndUpdatesUsers(t *testing.T) {
	databaseURL := os.Getenv("PASTEBOX_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set PASTEBOX_TEST_DATABASE_URL to run PostgreSQL user integration test")
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

	userID := "usr_store_test"
	duplicateID := "usr_store_test_duplicate"
	for _, id := range []string{userID, duplicateID} {
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		for _, id := range []string{userID, duplicateID} {
			_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE id = $1`, id)
		}
	})

	store := NewUserStore(pool)
	createdAt := time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC)
	user := app.User{
		ID:            userID,
		Email:         "user-store-test@example.com",
		DisplayName:   "User Store Test",
		Language:      "en",
		PasswordHash:  "argon2-test-hash",
		Role:          "user",
		EmailVerified: false,
		PlanID:        "free",
		Frozen:        false,
		CreatedAt:     createdAt,
		UpdatedAt:     createdAt,
	}
	if err := store.CreateUser(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	byID, err := store.UserByID(ctx, userID)
	if err != nil {
		t.Fatalf("read by id: %v", err)
	}
	if byID.ID != user.ID || byID.Email != user.Email || byID.PlanExpiresAt != nil || byID.DeletedAt != nil {
		t.Fatalf("unexpected user by id: %#v", byID)
	}
	byEmail, err := store.UserByEmail(ctx, user.Email)
	if err != nil {
		t.Fatalf("read by email: %v", err)
	}
	if byEmail.ID != userID {
		t.Fatalf("expected read by email to return %q, got %#v", userID, byEmail)
	}
	users, err := store.ListUsers(ctx)
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	if !hasPostgresUser(users, userID) {
		t.Fatalf("expected listed users to include %q, got %#v", userID, users)
	}

	duplicate := user
	duplicate.ID = duplicateID
	if err := store.CreateUser(ctx, duplicate); !errors.Is(err, ErrUserEmailExists) {
		t.Fatalf("expected duplicate email error, got %v", err)
	}

	planExpiresAt := createdAt.Add(30 * 24 * time.Hour)
	deleteRequestedAt := createdAt.Add(time.Hour)
	deleteScheduledAt := deleteRequestedAt.Add(7 * 24 * time.Hour)
	user.DisplayName = "Updated User"
	user.Language = "zh-CN"
	user.Role = "admin"
	user.EmailVerified = true
	user.PlanID = "plus"
	user.PlanExpiresAt = &planExpiresAt
	user.Frozen = true
	user.UpdatedAt = createdAt.Add(2 * time.Hour)
	user.DeleteRequestedAt = &deleteRequestedAt
	user.DeleteScheduledAt = &deleteScheduledAt
	if err := store.UpdateUser(ctx, user); err != nil {
		t.Fatalf("update user: %v", err)
	}
	updated, err := store.UserByID(ctx, userID)
	if err != nil {
		t.Fatalf("read updated user: %v", err)
	}
	if updated.DisplayName != "Updated User" || updated.Language != "zh-CN" || updated.Role != "admin" || !updated.EmailVerified || updated.PlanID != "plus" || !updated.Frozen {
		t.Fatalf("expected updated user fields, got %#v", updated)
	}
	if updated.PlanExpiresAt == nil || !updated.PlanExpiresAt.Equal(planExpiresAt) {
		t.Fatalf("expected plan expiry %s, got %#v", planExpiresAt, updated.PlanExpiresAt)
	}
	if updated.DeleteRequestedAt == nil || !updated.DeleteRequestedAt.Equal(deleteRequestedAt) {
		t.Fatalf("expected delete requested %s, got %#v", deleteRequestedAt, updated.DeleteRequestedAt)
	}
	if updated.DeleteScheduledAt == nil || !updated.DeleteScheduledAt.Equal(deleteScheduledAt) {
		t.Fatalf("expected delete scheduled %s, got %#v", deleteScheduledAt, updated.DeleteScheduledAt)
	}

	_, err = store.UserByID(ctx, "usr_store_test_missing")
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("expected missing user error, got %v", err)
	}
	missing := user
	missing.ID = "usr_store_test_missing"
	if err := store.UpdateUser(ctx, missing); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("expected update missing user error, got %v", err)
	}
}

func hasPostgresUser(users []app.User, id string) bool {
	for _, user := range users {
		if user.ID == id {
			return true
		}
	}
	return false
}
