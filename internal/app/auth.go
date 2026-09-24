package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func (s *Service) StartRegistrationEmailVerification(ctx context.Context, email string) (map[string]string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	email = normalizeEmail(email)
	s.mu.Lock()
	token, mail, err := func() (string, Mail, error) {
		if email == "" || !strings.Contains(email, "@") {
			return "", Mail{}, E(http.StatusBadRequest, "invalid_email", "valid email is required")
		}
		if err := s.ensureAllowedRegistrationEmailLocked(email); err != nil {
			return "", Mail{}, err
		}
		if _, err := s.userByEmailLocked(ctx, email); err == nil {
			return "", Mail{}, E(http.StatusConflict, "email_exists", "email is already registered")
		} else if !isStoreNotFound(err) && !isAppStatus(err, http.StatusNotFound) {
			return "", Mail{}, err
		}
		token := verificationCode()
		authToken := AuthToken{Hash: registrationVerificationHash(email, token), Email: email, ExpiresAt: s.now().UTC().Add(15 * time.Minute)}
		if err := s.createAuthToken(ctx, "registration_email_verification", authToken); err != nil {
			return "", Mail{}, err
		}
		s.emailVerifies[authToken.Hash] = &authToken
		return token, Mail{ID: s.newID("mail"), To: email, Subject: "Your PasteBox registration code", Body: fmt.Sprintf("Your PasteBox registration code is %s.\n\nThis code expires in 15 minutes.", token), CreatedAt: s.now().UTC()}, nil
	}()
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if err := s.queueMail(ctx, mail); err != nil {
		return nil, err
	}
	s.mu.Lock()
	response := s.authTokenResponse(token, "registration verification sent")
	s.mu.Unlock()
	return response, nil
}

func (s *Service) Register(ctx context.Context, input RegisterInput) (AuthResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	email := normalizeEmail(input.Email)
	if email == "" || !strings.Contains(email, "@") {
		return AuthResult{}, E(http.StatusBadRequest, "invalid_email", "valid email is required")
	}
	if len(input.Password) < 8 {
		return AuthResult{}, E(http.StatusBadRequest, "weak_password", "password must be at least 8 characters")
	}
	if err := s.verifyRegistrationTurnstile(ctx, input.TurnstileToken, input.RemoteIP); err != nil {
		return AuthResult{}, err
	}
	passwordHash, err := hashPassword(input.Password)
	if err != nil {
		return AuthResult{}, err
	}

	s.mu.Lock()
	var verificationHash string
	var verificationUsedAt time.Time
	user, mail, err := func() (User, Mail, error) {
		if err := s.ensureAllowedRegistrationEmailLocked(email); err != nil {
			return User{}, Mail{}, err
		}
		if _, err := s.userByEmailLocked(ctx, email); err == nil {
			return User{}, Mail{}, E(http.StatusConflict, "email_exists", "email is already registered")
		} else if !isStoreNotFound(err) && !isAppStatus(err, http.StatusNotFound) {
			return User{}, Mail{}, err
		}
		if s.runtimeConfig.Registration.RequireEmailVerification {
			verificationHash = registrationVerificationHash(email, input.EmailVerificationCode)
			verificationUsedAt = s.now().UTC()
			if transaction, ok := s.transactions.(AuthRegistrationTransactionStore); !ok || transaction == nil {
				if err := s.consumeRegistrationEmailVerificationLocked(ctx, email, input.EmailVerificationCode); err != nil {
					return User{}, Mail{}, err
				}
			}
		}
		now := s.now().UTC()
		user := &User{
			ID:            s.newID("usr"),
			Email:         email,
			DisplayName:   defaultString(strings.TrimSpace(input.DisplayName), email),
			Language:      NormalizeUserLanguage(input.Language),
			PasswordHash:  passwordHash,
			Role:          "user",
			EmailVerified: true,
			PlanID:        "free",
			CreatedAt:     now,
			UpdatedAt:     now,
		}
		return *user, Mail{ID: s.newID("mail"), To: user.Email, Subject: "Welcome to PasteBox", Body: "Your PasteBox account is ready.", CreatedAt: s.now().UTC()}, nil
	}()
	s.mu.Unlock()
	if err != nil {
		return AuthResult{}, err
	}
	if transaction, ok := s.transactions.(AuthRegistrationTransactionStore); ok {
		var err error
		if verificationHash != "" {
			err = transaction.RegisterUserWithEmailVerification(ctx, user, mail, verificationHash, email, verificationUsedAt)
		} else {
			err = transaction.RegisterUser(ctx, user, mail)
		}
		if err != nil {
			if errors.Is(err, ErrStoreConflict) {
				return AuthResult{}, E(http.StatusConflict, "email_exists", "email is already registered")
			}
			return AuthResult{}, err
		}
		s.mu.Lock()
		s.cacheUserLocked(user)
		s.cacheMailLocked(mail)
		if verificationHash != "" {
			if token := s.emailVerifies[verificationHash]; token != nil {
				usedAt := verificationUsedAt
				token.UsedAt = &usedAt
			}
		}
		s.mu.Unlock()
	} else {
		if err := s.createUser(ctx, user); err != nil {
			if errors.Is(err, ErrStoreConflict) {
				return AuthResult{}, E(http.StatusConflict, "email_exists", "email is already registered")
			}
			return AuthResult{}, err
		}
		if err := s.queueMail(ctx, mail); err != nil {
			return AuthResult{}, err
		}
	}
	return s.newSession(ctx, user)
}

func (s *Service) Login(ctx context.Context, email string, password string) (AuthResult, error) {
	return s.LoginFromIP(ctx, email, password, "")
}

// LoginFromIP scopes failed attempts to the account and the trusted client IP.
// One client's failed attempts must not lock the account for every other client.
func (s *Service) LoginFromIP(ctx context.Context, email string, password string, remoteIP string) (AuthResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	email = normalizeEmail(email)
	failureKey := email
	if remoteIP = strings.TrimSpace(remoteIP); remoteIP != "" {
		failureKey = "login:" + tokenHash(email+"\x00"+remoteIP)
	}
	if err := s.checkLoginRateLimitLocked(ctx, failureKey); err != nil {
		s.mu.Unlock()
		return AuthResult{}, err
	}
	user, err := s.userByEmailLocked(ctx, email)
	if err != nil {
		if !isStoreNotFound(err) && !isAppStatus(err, http.StatusNotFound) {
			s.mu.Unlock()
			return AuthResult{}, err
		}
		if recordErr := s.recordLoginFailureLocked(ctx, failureKey); recordErr != nil {
			s.mu.Unlock()
			return AuthResult{}, recordErr
		}
		s.mu.Unlock()
		return AuthResult{}, E(http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
	}
	if user.DeletedAt != nil || user.Frozen {
		s.mu.Unlock()
		return AuthResult{}, E(http.StatusForbidden, "account_unavailable", "account is unavailable")
	}
	userSnapshot := *user
	s.mu.Unlock()

	if err := verifyPassword(userSnapshot.PasswordHash, password); err != nil {
		s.mu.Lock()
		if recordErr := s.recordLoginFailureLocked(ctx, failureKey); recordErr != nil {
			s.mu.Unlock()
			return AuthResult{}, recordErr
		}
		s.mu.Unlock()
		return AuthResult{}, E(http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
	}

	s.mu.Lock()
	user = s.usersByID[userSnapshot.ID]
	if user == nil {
		user = &userSnapshot
	}
	if user.PasswordHash != userSnapshot.PasswordHash {
		s.mu.Unlock()
		return AuthResult{}, E(http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
	}
	if user.DeletedAt != nil || user.Frozen {
		s.mu.Unlock()
		return AuthResult{}, E(http.StatusForbidden, "account_unavailable", "account is unavailable")
	}
	if !user.EmailVerified {
		s.mu.Unlock()
		return AuthResult{}, E(http.StatusForbidden, "email_not_verified", "email verification is required before password login")
	}
	if err := s.deleteLoginFailureLocked(ctx, failureKey); err != nil {
		s.mu.Unlock()
		return AuthResult{}, err
	}
	mail := Mail{ID: s.newID("mail"), To: user.Email, Subject: "New PasteBox login", Body: "A new device logged in to your PasteBox account.", CreatedAt: s.now().UTC()}
	userSnapshot = *user
	s.mu.Unlock()
	if err := s.queueMail(ctx, mail); err != nil {
		return AuthResult{}, err
	}
	return s.newSession(ctx, userSnapshot)
}

func (s *Service) GoogleOAuth(ctx context.Context, email string, displayName string, googleSubject string, language string) (AuthResult, error) {
	return s.OAuthLogin(ctx, "google", email, displayName, googleSubject, language)
}

func (s *Service) GitHubOAuth(ctx context.Context, email string, displayName string, githubSubject string, language string) (AuthResult, error) {
	return s.OAuthLogin(ctx, "github", email, displayName, githubSubject, language)
}

func (s *Service) OAuthLogin(ctx context.Context, provider string, email string, displayName string, subject string, language string) (AuthResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return AuthResult{}, E(http.StatusBadRequest, "invalid_oauth_provider", "oauth provider is required")
	}
	email = normalizeEmail(email)
	if email == "" || !strings.Contains(email, "@") {
		return AuthResult{}, E(http.StatusBadRequest, "invalid_email", "valid email is required")
	}
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return AuthResult{}, E(http.StatusBadRequest, "missing_oauth_subject", "oauth subject is required")
	}
	passwordHash, err := hashPassword(newToken())
	if err != nil {
		return AuthResult{}, err
	}

	s.mu.Lock()
	locked := true
	unlock := func() {
		if locked {
			s.mu.Unlock()
			locked = false
		}
	}
	defer unlock()

	if identity, ok, err := s.oauthIdentityByProviderSubjectLocked(ctx, provider, subject); err != nil {
		return AuthResult{}, err
	} else if ok {
		user, err := s.activeUserLocked(ctx, identity.UserID)
		if err != nil {
			return AuthResult{}, err
		}

		snapshot := *user
		user = &snapshot
		if strings.TrimSpace(displayName) != "" {
			user.DisplayName = strings.TrimSpace(displayName)
		}
		user.EmailVerified = true
		user.UpdatedAt = s.now().UTC()
		if err := s.saveOAuthAccountLocked(ctx, *user, nil, provider); err != nil {
			return AuthResult{}, err
		}
		userSnapshot := *user
		unlock()
		return s.newSession(ctx, userSnapshot)
	}

	if user, err := s.userByEmailLocked(ctx, email); err == nil {
		if user.DeletedAt != nil || user.Frozen {
			return AuthResult{}, E(http.StatusForbidden, "account_unavailable", "account is unavailable")
		}
		if identities, err := s.oauthIdentitiesByUserLocked(ctx, user.ID); err != nil {
			return AuthResult{}, err
		} else if hasOAuthProvider(identities, provider) {
			return AuthResult{}, E(http.StatusConflict, "oauth_identity_conflict", "oauth account is already linked to a different identity")
		}

		snapshot := *user
		user = &snapshot
		user.EmailVerified = true
		if strings.TrimSpace(displayName) != "" {
			user.DisplayName = strings.TrimSpace(displayName)
		}
		now := s.now().UTC()
		user.UpdatedAt = now
		identity := OAuthIdentity{UserID: user.ID, Provider: provider, Subject: subject, CreatedAt: now, UpdatedAt: now}
		if err := s.saveOAuthAccountLocked(ctx, *user, &identity, provider); err != nil {
			return AuthResult{}, err
		}
		userSnapshot := *user
		unlock()
		return s.newSession(ctx, userSnapshot)
	} else if !isStoreNotFound(err) && !isAppStatus(err, http.StatusNotFound) {
		return AuthResult{}, err
	}
	if err := s.ensureAllowedRegistrationEmailLocked(email); err != nil {
		return AuthResult{}, err
	}

	now := s.now().UTC()
	user := &User{
		ID:            s.newID("usr"),
		Email:         email,
		DisplayName:   defaultString(strings.TrimSpace(displayName), email),
		Language:      NormalizeUserLanguage(language),
		PasswordHash:  passwordHash,
		Role:          "user",
		EmailVerified: true,
		PlanID:        "free",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	userSnapshot := *user
	identity := OAuthIdentity{UserID: user.ID, Provider: provider, Subject: subject, CreatedAt: now, UpdatedAt: now}
	audits := []AuditLog{
		{ID: s.newID("aud"), ActorID: user.ID, Action: "auth.oauth_linked", Target: user.ID, Metadata: map[string]any{"provider": provider}, CreatedAt: now},
		{ID: s.newID("aud"), ActorID: user.ID, Action: "auth." + provider + "_oauth", Target: user.ID, Metadata: map[string]any{"provider": provider}, CreatedAt: now},
	}
	mail := Mail{
		ID: s.newID("mail"), To: user.Email, Subject: "Welcome to PasteBox",
		Body: "Your " + provider + "-authenticated PasteBox account is ready.", CreatedAt: s.now().UTC(),
	}
	unlock()
	if transaction, ok := s.transactions.(AuthRegistrationTransactionStore); ok {
		if err := transaction.RegisterOAuthUser(ctx, OAuthRegistrationTransactionInput{User: userSnapshot, Identity: identity, Audits: audits, Mail: mail}); err != nil {
			if errors.Is(err, ErrOAuthIdentityConflict) {
				return AuthResult{}, E(http.StatusConflict, "oauth_identity_conflict", "oauth account is already linked to a different identity")
			}
			if errors.Is(err, ErrStoreConflict) {
				return AuthResult{}, E(http.StatusConflict, "email_exists", "email is already registered")
			}
			return AuthResult{}, err
		}
		s.mu.Lock()
		s.cacheUserLocked(userSnapshot)
		s.cacheOAuthIdentityLocked(identity)
		for _, audit := range audits {
			s.cacheAuditLogLocked(audit)
		}
		s.cacheMailLocked(mail)
		s.mu.Unlock()
	} else {
		if err := s.createUser(ctx, userSnapshot); err != nil {
			if errors.Is(err, ErrStoreConflict) {
				return AuthResult{}, E(http.StatusConflict, "email_exists", "email is already registered")
			}
			return AuthResult{}, err
		}
		s.mu.Lock()
		locked = true
		user = s.usersByID[userSnapshot.ID]
		if user == nil {
			user = s.cacheUserLocked(userSnapshot)
		}
		if err := s.createOAuthIdentityLocked(ctx, &identity); err != nil {
			return AuthResult{}, err
		}
		if err := s.auditLocked(ctx, user.ID, "auth.oauth_linked", user.ID, map[string]any{"provider": provider}); err != nil {
			return AuthResult{}, err
		}
		if err := s.auditLocked(ctx, user.ID, "auth."+provider+"_oauth", user.ID, map[string]any{"provider": provider}); err != nil {
			return AuthResult{}, err
		}
		unlock()
		if err := s.queueMail(ctx, mail); err != nil {
			return AuthResult{}, err
		}
	}
	return s.newSession(ctx, userSnapshot)
}

func (s *Service) StartEmailVerificationWithContext(ctx context.Context, userID string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	user, err := s.activeUserLocked(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user.EmailVerified {
		return map[string]string{"message": "email already verified"}, nil
	}
	token, err := s.issueEmailVerificationLocked(ctx, user)
	if err != nil {
		return nil, err
	}
	return s.authTokenResponse(token, "verification sent"), nil
}

func (s *Service) FinishEmailVerification(token string) (UserView, error) {
	return s.FinishEmailVerificationWithContext(context.Background(), token)
}

func (s *Service) FinishEmailVerificationWithContext(ctx context.Context, token string) (UserView, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	authToken, err := s.consumeTokenLocked(ctx, "email_verification", s.emailVerifies, token)
	if err != nil {
		return UserView{}, err
	}
	user, err := s.activeUserLocked(ctx, authToken.UserID)
	if err != nil {
		return UserView{}, E(http.StatusUnauthorized, "user_not_found", "user not found")
	}
	user.EmailVerified = true
	user.UpdatedAt = s.now().UTC()
	if err := s.updateUserLocked(ctx, user); err != nil {
		return UserView{}, err
	}
	return s.viewUserLocked(ctx, user)
}

func (s *Service) StartPasswordReset(ctx context.Context, email string) (map[string]string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	email = normalizeEmail(email)
	if email == "" || !strings.Contains(email, "@") {
		return nil, E(http.StatusBadRequest, "invalid_email", "valid email is required")
	}
	token := newToken()
	response := s.authTokenResponse(token, "password reset sent")

	var user User
	var err error
	if s.auth.Users != nil {
		user, err = s.auth.Users.UserByEmail(ctx, email)
		if err != nil {
			if isStoreNotFound(err) {
				return response, nil
			}
			return nil, err
		}
	} else {
		s.mu.Lock()
		loaded, lookupErr := s.userByEmailLocked(ctx, email)
		if lookupErr != nil {
			s.mu.Unlock()
			if isStoreNotFound(lookupErr) || isAppStatus(lookupErr, http.StatusNotFound) {
				return response, nil
			}
			return nil, lookupErr
		}
		user = *loaded
		s.mu.Unlock()
	}
	if !user.EmailVerified {
		return response, nil
	}
	hash := tokenHash(token)
	now := s.now().UTC()
	authToken := AuthToken{Hash: hash, UserID: user.ID, Email: user.Email, ExpiresAt: now.Add(30 * time.Minute)}
	if err := s.createAuthToken(ctx, "password_reset", authToken); err != nil {
		return nil, err
	}
	mail := Mail{ID: s.newID("mail"), To: user.Email, Subject: "Reset your PasteBox password", Body: s.authLinkBody("Reset your PasteBox password", "/password-reset", token, 30*time.Minute), CreatedAt: now}
	if s.ops.Mails != nil {
		if err := s.ops.Mails.QueueMail(ctx, mail); err != nil {
			return nil, err
		}
	}
	s.mu.Lock()
	s.passwordResets[hash] = &authToken
	s.cacheMailLocked(mail)
	s.mu.Unlock()
	return response, nil
}

func (s *Service) FinishPasswordReset(ctx context.Context, token string, password string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(password) < 8 {
		return E(http.StatusBadRequest, "weak_password", "password must be at least 8 characters")
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	if transaction, ok := s.transactions.(PasswordResetTransactionStore); ok {
		usedAt := s.now().UTC()
		result, err := transaction.FinishPasswordReset(ctx, PasswordResetTransactionInput{
			TokenHash:     tokenHash(token),
			PasswordHash:  hash,
			UsedAt:        usedAt,
			MailID:        s.newID("mail"),
			MailCreatedAt: usedAt,
		})
		if err != nil {
			if isStoreNotFound(err) {
				return E(http.StatusUnauthorized, "invalid_token", "token is invalid or expired")
			}
			return err
		}
		s.mu.Lock()
		s.cacheUserLocked(result.User)
		if reset := s.passwordResets[tokenHash(token)]; reset != nil {
			reset.UsedAt = &usedAt
		}
		for _, session := range s.sessionsByID {
			if session.UserID == result.User.ID && session.RevokedAt == nil {
				session.RevokedAt = &usedAt
			}
		}
		s.cacheMailLocked(result.Mail)
		s.mu.Unlock()
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	authToken, err := s.consumeTokenLocked(ctx, "password_reset", s.passwordResets, token)
	if err != nil {
		return err
	}
	user, err := s.activeUserLocked(ctx, authToken.UserID)
	if err != nil {
		return err
	}
	user.PasswordHash = hash
	user.UpdatedAt = s.now().UTC()
	if err := s.updateUserLocked(ctx, user); err != nil {
		return err
	}
	if err := s.revokeUserSessionsLocked(ctx, user.ID); err != nil {
		return err
	}
	if err := s.mail(ctx, user.Email, "PasteBox password changed", "Your password was changed."); err != nil {
		return err
	}
	return nil
}

func (s *Service) UserForSessionWithContext(ctx context.Context, sessionID string) (UserView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	user, err := s.userForSessionLocked(ctx, sessionID)
	if err != nil {
		return UserView{}, err
	}
	return s.viewUserLocked(ctx, user)
}

func (s *Service) Logout(ctx context.Context, sessionID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if sessionID == "" {
		return nil
	}
	now := s.now().UTC()
	if s.auth.Sessions != nil {
		if err := s.auth.Sessions.RevokeSession(ctx, sessionID, now); err != nil && !isStoreNotFound(err) {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if session := s.sessionsByID[sessionID]; session != nil {
		session.RevokedAt = &now
	}
	return nil
}

func (s *Service) LogoutAll(ctx context.Context, userID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	now := s.now().UTC()
	if s.auth.Sessions != nil {
		if _, err := s.auth.Sessions.RevokeUserSessions(ctx, userID, now); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, session := range s.sessionsByID {
		if session.UserID == userID && session.RevokedAt == nil {
			session.RevokedAt = &now
		}
	}
	return nil
}

func (s *Service) UserForSession(sessionID string) (UserView, error) {
	return s.UserForSessionWithContext(context.Background(), sessionID)
}

func (s *Service) StartEmailVerification(userID string) (map[string]string, error) {
	return s.StartEmailVerificationWithContext(context.Background(), userID)
}

func (s *Service) saveOAuthAccountLocked(ctx context.Context, user User, identity *OAuthIdentity, provider string) error {
	actions := []string{"auth." + provider + "_oauth"}
	if identity != nil {
		actions = append([]string{"auth.oauth_linked"}, actions...)
	}
	audits := make([]AuditLog, 0, len(actions))
	for _, action := range actions {
		audits = append(audits, AuditLog{ID: s.newID("aud"), ActorID: user.ID, Action: action, Target: user.ID, Metadata: map[string]any{"provider": provider}, CreatedAt: s.now().UTC()})
	}
	if store, ok := s.transactions.(OAuthAccountTransactionStore); ok {
		s.mu.Unlock()
		err := store.SaveOAuthAccount(ctx, user, identity, audits)
		s.mu.Lock()
		if err != nil {
			return err
		}
		s.cacheUserLocked(user)
		if identity != nil {
			s.cacheOAuthIdentityLocked(*identity)
		}
		for _, audit := range audits {
			s.cacheAuditLogLocked(audit)
		}
		return nil
	}
	if err := s.updateUserLocked(ctx, &user); err != nil {
		return err
	}
	if identity != nil {
		if err := s.createOAuthIdentityLocked(ctx, identity); err != nil {
			return err
		}
	}
	for _, audit := range audits {
		if err := s.auditLocked(ctx, audit.ActorID, audit.Action, audit.Target, audit.Metadata); err != nil {
			return err
		}
	}
	return nil
}
