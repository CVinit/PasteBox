package app

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"
)

func (s *Service) activeUserWithContext(ctx context.Context, userID string) (*User, error) {
	if s.auth.Users == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		user, err := s.activeUserLocked(ctx, userID)
		if err != nil {
			return nil, err
		}
		snapshot := *user
		return &snapshot, nil
	}
	user, err := s.auth.Users.UserByID(ctx, userID)
	if err != nil {
		if isStoreNotFound(err) {
			return nil, E(http.StatusUnauthorized, "user_not_found", "user not found")
		}
		return nil, err
	}
	if user.DeletedAt != nil {
		return nil, E(http.StatusUnauthorized, "user_not_found", "user not found")
	}
	if user.Frozen {
		return nil, E(http.StatusForbidden, "account_frozen", "account is frozen")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cacheUserLocked(user)
	return &user, nil
}

func (s *Service) createUserLocked(ctx context.Context, user *User) error {
	if s.auth.Users != nil {
		storedUser := *user
		s.mu.Unlock()
		err := s.auth.Users.CreateUser(ctx, storedUser)
		s.mu.Lock()
		if err != nil {
			return err
		}
	}
	s.cacheUserLocked(*user)
	return nil
}

func (s *Service) createUser(ctx context.Context, user User) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if s.auth.Users != nil {
		if err := s.auth.Users.CreateUser(ctx, user); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.cacheUserLocked(user)
	s.mu.Unlock()
	return nil
}

func (s *Service) createOAuthIdentityLocked(ctx context.Context, identity *OAuthIdentity) error {
	identity.Provider = normalizeProvider(identity.Provider)
	identity.Subject = strings.TrimSpace(identity.Subject)
	if identity.UserID == "" || identity.Provider == "" || identity.Subject == "" {
		return E(http.StatusBadRequest, "invalid_oauth_identity", "oauth identity is incomplete")
	}
	if identity.CreatedAt.IsZero() {
		identity.CreatedAt = s.now().UTC()
	}
	if identity.UpdatedAt.IsZero() {
		identity.UpdatedAt = identity.CreatedAt
	}
	if s.auth.OAuthIdentities != nil {
		storedIdentity := *identity
		s.mu.Unlock()
		err := s.auth.OAuthIdentities.LinkOAuthIdentity(ctx, storedIdentity)
		s.mu.Lock()
		if err != nil {
			if errors.Is(err, ErrStoreConflict) {
				return E(http.StatusConflict, "oauth_identity_conflict", "oauth identity is already linked")
			}
			return err
		}
	}
	s.cacheOAuthIdentityLocked(*identity)
	return nil
}

func (s *Service) cacheOAuthIdentityLocked(identity OAuthIdentity) {
	identity.Provider = normalizeProvider(identity.Provider)
	identity.Subject = strings.TrimSpace(identity.Subject)
	cached := identity
	s.oauthIdentities[oauthIdentityKey(cached.Provider, cached.Subject)] = &cached
}

func (s *Service) updateUserLocked(ctx context.Context, user *User) error {
	if s.auth.Users != nil {
		storedUser := *user
		s.mu.Unlock()
		err := s.auth.Users.UpdateUser(ctx, storedUser)
		s.mu.Lock()
		if err != nil {
			return err
		}
	}
	s.cacheUserLocked(*user)
	return nil
}

func (s *Service) cacheUserLocked(user User) *User {
	cached := user
	s.usersByID[cached.ID] = &cached
	s.userIDByEmail[cached.Email] = cached.ID
	return &cached
}

func (s *Service) listUsersLocked(ctx context.Context) ([]User, error) {
	if s.auth.Users != nil {
		s.mu.Unlock()
		users, err := s.auth.Users.ListUsers(ctx)
		s.mu.Lock()
		if err != nil {
			return nil, err
		}
		for _, user := range users {
			s.cacheUserLocked(user)
		}
		return users, nil
	}
	users := make([]User, 0, len(s.usersByID))
	for _, user := range s.usersByID {
		users = append(users, *user)
	}
	return users, nil
}

func (s *Service) newSession(ctx context.Context, user User) (AuthResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	now := s.now().UTC()
	session := Session{ID: newToken(), UserID: user.ID, CreatedAt: now, ExpiresAt: now.Add(30 * 24 * time.Hour)}
	view, err := s.viewUserWithContext(ctx, user)
	if err != nil {
		return AuthResult{}, err
	}
	if s.auth.Sessions != nil {
		if err := s.auth.Sessions.CreateSession(ctx, session); err != nil {
			return AuthResult{}, err
		}
	}
	s.mu.Lock()
	s.sessionsByID[session.ID] = &session
	s.mu.Unlock()
	return AuthResult{User: view, SessionID: session.ID, ExpiresAt: session.ExpiresAt}, nil
}

func (s *Service) userForSessionLocked(ctx context.Context, sessionID string) (*User, error) {
	if sessionID == "" {
		return nil, E(http.StatusUnauthorized, "unauthenticated", "login required")
	}
	var session *Session
	if s.auth.Sessions != nil {
		s.mu.Unlock()
		loaded, err := s.auth.Sessions.SessionByID(ctx, sessionID)
		s.mu.Lock()
		if err != nil {
			if isStoreNotFound(err) {
				return nil, E(http.StatusUnauthorized, "unauthenticated", "login required")
			}
			return nil, err
		}
		session = &loaded
		s.sessionsByID[session.ID] = session
	} else {
		session = s.sessionsByID[sessionID]
	}
	if session == nil || session.RevokedAt != nil || !session.ExpiresAt.After(s.now().UTC()) {
		return nil, E(http.StatusUnauthorized, "unauthenticated", "login required")
	}
	return s.activeUserLocked(ctx, session.UserID)
}

func (s *Service) activeUserLocked(ctx context.Context, userID string) (*User, error) {
	user, err := s.userByIDLocked(ctx, userID)
	if err != nil {
		if !isStoreNotFound(err) && !isAppStatus(err, http.StatusNotFound) {
			return nil, err
		}
		return nil, E(http.StatusUnauthorized, "user_not_found", "user not found")
	}
	if user.DeletedAt != nil {
		return nil, E(http.StatusUnauthorized, "user_not_found", "user not found")
	}
	if user.Frozen {
		return nil, E(http.StatusForbidden, "account_frozen", "account is frozen")
	}
	return user, nil
}

func (s *Service) userByIDLocked(ctx context.Context, userID string) (*User, error) {
	if s.auth.Users != nil {
		s.mu.Unlock()
		user, err := s.auth.Users.UserByID(ctx, userID)
		s.mu.Lock()
		if err != nil {
			if isStoreNotFound(err) {
				return nil, E(http.StatusNotFound, "user_not_found", "user not found")
			}
			return nil, err
		}
		return s.cacheUserLocked(user), nil
	}
	user := s.usersByID[userID]
	if user == nil {
		return nil, E(http.StatusNotFound, "user_not_found", "user not found")
	}
	return user, nil
}

func (s *Service) userByEmailLocked(ctx context.Context, email string) (*User, error) {
	email = normalizeEmail(email)
	if s.auth.Users != nil {
		s.mu.Unlock()
		user, err := s.auth.Users.UserByEmail(ctx, email)
		s.mu.Lock()
		if err != nil {
			if isStoreNotFound(err) {
				return nil, E(http.StatusNotFound, "user_not_found", "user not found")
			}
			return nil, err
		}
		return s.cacheUserLocked(user), nil
	}
	userID := s.userIDByEmail[email]
	if userID == "" {
		return nil, E(http.StatusNotFound, "user_not_found", "user not found")
	}
	return s.usersByID[userID], nil
}

func (s *Service) oauthIdentityByProviderSubjectLocked(ctx context.Context, provider string, subject string) (OAuthIdentity, bool, error) {
	provider = normalizeProvider(provider)
	subject = strings.TrimSpace(subject)
	if s.auth.OAuthIdentities != nil {
		s.mu.Unlock()
		identity, err := s.auth.OAuthIdentities.OAuthIdentityByProviderSubject(ctx, provider, subject)
		s.mu.Lock()
		if err != nil {
			if isStoreNotFound(err) {
				return OAuthIdentity{}, false, nil
			}
			return OAuthIdentity{}, false, err
		}
		s.cacheOAuthIdentityLocked(identity)
		return identity, true, nil
	}
	identity := s.oauthIdentities[oauthIdentityKey(provider, subject)]
	if identity == nil {
		return OAuthIdentity{}, false, nil
	}
	return *identity, true, nil
}

func (s *Service) oauthIdentitiesByUserLocked(ctx context.Context, userID string) ([]OAuthIdentity, error) {
	if s.auth.OAuthIdentities != nil {
		s.mu.Unlock()
		identities, err := s.auth.OAuthIdentities.OAuthIdentitiesByUser(ctx, userID)
		s.mu.Lock()
		if err != nil {
			return nil, err
		}
		for _, identity := range identities {
			s.cacheOAuthIdentityLocked(identity)
		}
		return identities, nil
	}
	identities := []OAuthIdentity{}
	for _, identity := range s.oauthIdentities {
		if identity.UserID == userID {
			identities = append(identities, *identity)
		}
	}
	sort.Slice(identities, func(i, j int) bool { return identities[i].Provider < identities[j].Provider })
	return identities, nil
}

func (s *Service) deleteOAuthIdentityLocked(ctx context.Context, userID string, provider string) error {
	provider = normalizeProvider(provider)
	if s.auth.OAuthIdentities != nil {
		s.mu.Unlock()
		err := s.auth.OAuthIdentities.DeleteOAuthIdentity(ctx, userID, provider)
		s.mu.Lock()
		if err != nil {
			if isStoreNotFound(err) {
				return E(http.StatusNotFound, "oauth_identity_not_linked", "oauth provider is not linked")
			}
			return err
		}
	}
	for key, identity := range s.oauthIdentities {
		if identity.UserID == userID && identity.Provider == provider {
			delete(s.oauthIdentities, key)
		}
	}
	return nil
}

func (s *Service) checkLoginRateLimitLocked(ctx context.Context, email string) error {
	failure, ok, err := s.loginFailureLocked(ctx, email)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	now := s.now().UTC()
	if failure.LockedUntil.After(now) {
		return E(http.StatusTooManyRequests, "login_rate_limited", "too many failed login attempts")
	}
	if now.Sub(failure.WindowStart) > 15*time.Minute {
		return s.deleteLoginFailureLocked(ctx, email)
	}
	return nil
}

func (s *Service) loginFailureLocked(ctx context.Context, email string) (LoginFailure, bool, error) {
	if s.auth.LoginFailures != nil {
		s.mu.Unlock()
		failure, err := s.auth.LoginFailures.LoginFailure(ctx, email)
		s.mu.Lock()
		if err != nil {
			if isStoreNotFound(err) {
				return LoginFailure{}, false, nil
			}
			return LoginFailure{}, false, err
		}
		s.loginFailures[email] = &failure
		return failure, true, nil
	}
	failure := s.loginFailures[email]
	if failure == nil {
		return LoginFailure{}, false, nil
	}
	return *failure, true, nil
}

func (s *Service) recordLoginFailureLocked(ctx context.Context, email string) error {
	now := s.now().UTC()
	if store, ok := s.auth.LoginFailures.(AtomicLoginFailureStore); ok {
		s.mu.Unlock()
		failure, err := store.RecordLoginFailure(ctx, email, now)
		s.mu.Lock()
		if err != nil {
			return err
		}
		s.loginFailures[email] = &failure
		return nil
	}
	failure, ok, err := s.loginFailureLocked(ctx, email)
	if err != nil {
		return err
	}
	if !ok || now.Sub(failure.WindowStart) > 15*time.Minute {
		failure = LoginFailure{Count: 1, WindowStart: now}
	} else {
		failure.Count++
	}
	if failure.Count >= 5 {
		failure.LockedUntil = now.Add(15 * time.Minute)
	}
	if s.auth.LoginFailures != nil {
		s.mu.Unlock()
		err := s.auth.LoginFailures.SaveLoginFailure(ctx, email, failure)
		s.mu.Lock()
		if err != nil {
			return err
		}
	}
	s.loginFailures[email] = &failure
	return nil
}

func (s *Service) deleteLoginFailureLocked(ctx context.Context, email string) error {
	if s.auth.LoginFailures != nil {
		s.mu.Unlock()
		err := s.auth.LoginFailures.DeleteLoginFailure(ctx, email)
		s.mu.Lock()
		if err != nil {
			return err
		}
	}
	delete(s.loginFailures, email)
	return nil
}

func (s *Service) createAuthTokenLocked(ctx context.Context, kind string, token AuthToken) error {
	s.mu.Unlock()
	defer s.mu.Lock()
	return s.createAuthToken(ctx, kind, token)
}

func (s *Service) createAuthToken(ctx context.Context, kind string, token AuthToken) error {
	if s.auth.Tokens == nil {
		return nil
	}
	return s.auth.Tokens.CreateAuthToken(ctx, kind, token)
}

func (s *Service) consumeTokenLocked(ctx context.Context, kind string, tokens map[string]*AuthToken, token string) (*AuthToken, error) {
	hash := tokenHash(token)
	if s.auth.Tokens != nil {
		s.mu.Unlock()
		loaded, err := s.auth.Tokens.ConsumeAuthToken(ctx, kind, hash, s.now().UTC())
		s.mu.Lock()
		if err != nil {
			if isStoreNotFound(err) {
				return nil, E(http.StatusUnauthorized, "invalid_token", "token is invalid or expired")
			}
			return nil, err
		}
		tokens[hash] = &loaded
		return &loaded, nil
	}
	authToken := tokens[hash]
	if authToken == nil || authToken.UsedAt != nil || !authToken.ExpiresAt.After(s.now().UTC()) {
		return nil, E(http.StatusUnauthorized, "invalid_token", "token is invalid or expired")
	}
	now := s.now().UTC()
	authToken.UsedAt = &now
	return authToken, nil
}

func (s *Service) ensureAllowedRegistrationEmailLocked(email string) error {
	domain := emailDomain(email)
	if domain == "" {
		return E(http.StatusBadRequest, "invalid_email", "valid email is required")
	}
	allowed := s.runtimeConfig.Registration.AllowedDomains
	if len(allowed) == 0 {
		return nil
	}
	for _, candidate := range allowed {
		if strings.EqualFold(domain, candidate) {
			return nil
		}
	}
	return E(http.StatusForbidden, "email_domain_not_allowed", "email domain is not allowed for registration")
}

func (s *Service) consumeRegistrationEmailVerificationLocked(ctx context.Context, email string, code string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	hash := registrationVerificationHash(email, code)
	authToken := s.emailVerifies[hash]
	persisted := s.auth.Tokens != nil
	if s.auth.Tokens != nil {
		s.mu.Unlock()
		loaded, err := s.auth.Tokens.ConsumeAuthToken(ctx, "registration_email_verification", hash, s.now().UTC())
		s.mu.Lock()
		if err != nil {
			if isStoreNotFound(err) {
				return E(http.StatusUnauthorized, "invalid_token", "token is invalid or expired")
			}
			return err
		}
		authToken = &loaded
		s.emailVerifies[hash] = authToken
	}
	if authToken == nil || (!persisted && authToken.UsedAt != nil) || !authToken.ExpiresAt.After(s.now().UTC()) {
		return E(http.StatusUnauthorized, "invalid_token", "token is invalid or expired")
	}
	if subtle.ConstantTimeCompare([]byte(normalizeEmail(authToken.Email)), []byte(normalizeEmail(email))) != 1 {
		return E(http.StatusUnauthorized, "invalid_token", "token is invalid or expired")
	}
	if !persisted {
		now := s.now().UTC()
		authToken.UsedAt = &now
	}
	return nil
}

func (s *Service) verifyRegistrationTurnstile(ctx context.Context, token string, remoteIP string) error {
	s.mu.Lock()
	required := s.runtimeConfig.Registration.RequireTurnstile
	s.mu.Unlock()
	if !required {
		return nil
	}
	return s.VerifyTurnstile(ctx, token, remoteIP)
}

func (s *Service) VerifyTurnstile(ctx context.Context, token string, remoteIP string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.verifyTurnstileLocked(ctx, token, remoteIP)
}

func (s *Service) issueEmailVerificationLocked(ctx context.Context, user *User) (string, error) {
	token := newToken()
	hash := tokenHash(token)
	authToken := AuthToken{Hash: hash, UserID: user.ID, Email: user.Email, ExpiresAt: s.now().UTC().Add(24 * time.Hour)}
	if err := s.createAuthTokenLocked(ctx, "email_verification", authToken); err != nil {
		return "", err
	}
	s.emailVerifies[hash] = &authToken
	if err := s.mail(ctx, user.Email, "Verify your PasteBox email", s.authLinkBody("Verify your PasteBox email", "/email-verification", token, 24*time.Hour)); err != nil {
		return "", err
	}
	return token, nil
}

func (s *Service) revokeUserSessionsLocked(ctx context.Context, userID string) error {
	now := s.now().UTC()
	if s.auth.Sessions != nil {
		s.mu.Unlock()
		_, err := s.auth.Sessions.RevokeUserSessions(ctx, userID, now)
		s.mu.Lock()
		if err != nil {
			return err
		}
	}
	for _, session := range s.sessionsByID {
		if session.UserID == userID && session.RevokedAt == nil {
			session.RevokedAt = &now
		}
	}
	return nil
}
