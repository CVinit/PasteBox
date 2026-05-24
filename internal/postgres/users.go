package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"pastebox/internal/app"
)

var (
	ErrUserNotFound    = errors.Join(errors.New("postgres user not found"), app.ErrStoreNotFound)
	ErrUserEmailExists = errors.Join(errors.New("postgres user email exists"), app.ErrStoreConflict)
)

type UserStore struct {
	pool *pgxpool.Pool
}

func NewUserStore(pool *pgxpool.Pool) *UserStore {
	return &UserStore{pool: pool}
}

func (s *UserStore) CreateUser(ctx context.Context, user app.User) error {
	if _, err := s.pool.Exec(ctx, `
INSERT INTO users (
	id,
	email,
	display_name,
	language,
	password_hash,
	role,
	email_verified,
	plan_id,
	plan_expires_at,
	frozen,
	created_at,
	updated_at,
	delete_requested_at,
	delete_scheduled_at,
	deleted_at
) VALUES (
	$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15
)
`, user.ID, user.Email, user.DisplayName, user.Language, user.PasswordHash, user.Role, user.EmailVerified, user.PlanID, user.PlanExpiresAt, user.Frozen, user.CreatedAt, user.UpdatedAt, user.DeleteRequestedAt, user.DeleteScheduledAt, user.DeletedAt); err != nil {
		if isUniqueViolation(err, "users_email_key") {
			return ErrUserEmailExists
		}
		return fmt.Errorf("create user: %w", err)
	}
	return nil
}

func (s *UserStore) UserByID(ctx context.Context, id string) (app.User, error) {
	return s.queryUser(ctx, `
SELECT
	id,
	email,
	display_name,
	language,
	password_hash,
	role,
	email_verified,
	plan_id,
	plan_expires_at,
	frozen,
	created_at,
	updated_at,
	delete_requested_at,
	delete_scheduled_at,
	deleted_at
FROM users
WHERE id = $1
`, id)
}

func (s *UserStore) UserByEmail(ctx context.Context, email string) (app.User, error) {
	return s.queryUser(ctx, `
SELECT
	id,
	email,
	display_name,
	language,
	password_hash,
	role,
	email_verified,
	plan_id,
	plan_expires_at,
	frozen,
	created_at,
	updated_at,
	delete_requested_at,
	delete_scheduled_at,
	deleted_at
FROM users
WHERE email = $1
`, email)
}

func (s *UserStore) UpdateUser(ctx context.Context, user app.User) error {
	tag, err := s.pool.Exec(ctx, `
UPDATE users
SET
	email = $2,
	display_name = $3,
	language = $4,
	password_hash = $5,
	role = $6,
	email_verified = $7,
	plan_id = $8,
	plan_expires_at = $9,
	frozen = $10,
	created_at = $11,
	updated_at = $12,
	delete_requested_at = $13,
	delete_scheduled_at = $14,
	deleted_at = $15
WHERE id = $1
`, user.ID, user.Email, user.DisplayName, user.Language, user.PasswordHash, user.Role, user.EmailVerified, user.PlanID, user.PlanExpiresAt, user.Frozen, user.CreatedAt, user.UpdatedAt, user.DeleteRequestedAt, user.DeleteScheduledAt, user.DeletedAt)
	if err != nil {
		if isUniqueViolation(err, "users_email_key") {
			return ErrUserEmailExists
		}
		return fmt.Errorf("update user: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
}

func (s *UserStore) queryUser(ctx context.Context, sql string, args ...any) (app.User, error) {
	row := s.pool.QueryRow(ctx, sql, args...)
	user, err := scanUser(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return app.User{}, ErrUserNotFound
		}
		return app.User{}, err
	}
	return user, nil
}

type userRow interface {
	Scan(dest ...any) error
}

func scanUser(row userRow) (app.User, error) {
	var user app.User
	var planExpiresAt pgtype.Timestamptz
	var deleteRequestedAt pgtype.Timestamptz
	var deleteScheduledAt pgtype.Timestamptz
	var deletedAt pgtype.Timestamptz
	if err := row.Scan(
		&user.ID,
		&user.Email,
		&user.DisplayName,
		&user.Language,
		&user.PasswordHash,
		&user.Role,
		&user.EmailVerified,
		&user.PlanID,
		&planExpiresAt,
		&user.Frozen,
		&user.CreatedAt,
		&user.UpdatedAt,
		&deleteRequestedAt,
		&deleteScheduledAt,
		&deletedAt,
	); err != nil {
		return app.User{}, fmt.Errorf("scan user: %w", err)
	}
	user.PlanExpiresAt = optionalTime(planExpiresAt)
	user.DeleteRequestedAt = optionalTime(deleteRequestedAt)
	user.DeleteScheduledAt = optionalTime(deleteScheduledAt)
	user.DeletedAt = optionalTime(deletedAt)
	return user, nil
}

func optionalTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	t := value.Time
	return &t
}

func isUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && (constraint == "" || pgErr.ConstraintName == constraint)
}
