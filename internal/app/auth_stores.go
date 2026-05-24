package app

import (
	"context"
	"errors"
	"time"
)

var (
	ErrStoreNotFound = errors.New("store record not found")
	ErrStoreConflict = errors.New("store record conflict")
)

type AuthStores struct {
	Users         UserStore
	Sessions      SessionStore
	Tokens        AuthTokenStore
	LoginFailures LoginFailureStore
}

func (s AuthStores) configured() bool {
	return s.Users != nil || s.Sessions != nil || s.Tokens != nil || s.LoginFailures != nil
}

type UserStore interface {
	CreateUser(ctx context.Context, user User) error
	UserByID(ctx context.Context, id string) (User, error)
	UserByEmail(ctx context.Context, email string) (User, error)
	ListUsers(ctx context.Context) ([]User, error)
	UpdateUser(ctx context.Context, user User) error
}

type SessionStore interface {
	CreateSession(ctx context.Context, session Session) error
	SessionByID(ctx context.Context, id string) (Session, error)
	RevokeSession(ctx context.Context, id string, revokedAt time.Time) error
	RevokeUserSessions(ctx context.Context, userID string, revokedAt time.Time) (int64, error)
}

type AuthTokenStore interface {
	CreateAuthToken(ctx context.Context, kind string, token AuthToken) error
	AuthToken(ctx context.Context, kind string, hash string) (AuthToken, error)
	MarkAuthTokenUsed(ctx context.Context, kind string, hash string, usedAt time.Time) error
}

type LoginFailureStore interface {
	LoginFailure(ctx context.Context, email string) (LoginFailure, error)
	SaveLoginFailure(ctx context.Context, email string, failure LoginFailure) error
	DeleteLoginFailure(ctx context.Context, email string) error
}
