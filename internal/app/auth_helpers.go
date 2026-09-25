package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"golang.org/x/crypto/argon2"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (s *Service) mail(ctx context.Context, to string, subject string, body string) error {
	return s.createMailLocked(ctx, &Mail{ID: s.newID("mail"), To: to, Subject: subject, Body: body, CreatedAt: s.now().UTC()})
}

func (s *Service) authTokenResponse(token string, message string) map[string]string {
	response := map[string]string{"message": message}
	if s.cfg.ExposeDevAuthTokens() {
		response["devToken"] = token
	}
	return response
}

func (s *Service) authLinkBody(action string, routePath string, token string, ttl time.Duration) string {
	link := s.publicURLWithToken(routePath, token)
	return fmt.Sprintf("%s:\n\n%s\n\nThis link expires in %s.", action, link, authTokenTTL(ttl))
}

func (s *Service) publicURLWithToken(routePath string, token string) string {
	base := strings.TrimRight(s.cfg.PublicURL, "/")
	if base == "" {
		base = "http://localhost:5173"
	}
	routePath = "/" + strings.Trim(strings.TrimSpace(routePath), "/")
	values := url.Values{}
	values.Set("token", token)
	return base + routePath + "?" + values.Encode()
}

func authTokenTTL(ttl time.Duration) string {
	if ttl%time.Hour == 0 {
		hours := int(ttl / time.Hour)
		if hours == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", hours)
	}
	if ttl%time.Minute == 0 {
		minutes := int(ttl / time.Minute)
		if minutes == 1 {
			return "1 minute"
		}
		return fmt.Sprintf("%d minutes", minutes)
	}
	return ttl.String()
}

func (s *Service) newID(prefix string) string {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(err)
	}
	return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(raw[:]))
}

func (s *Service) viewUserLocked(ctx context.Context, user *User) (UserView, error) {
	snapshot := *user
	user = &snapshot
	identities, err := s.oauthIdentitiesByUserLocked(ctx, user.ID)
	if err != nil {
		return UserView{}, err
	}
	view := viewUser(user)
	view.OAuthProviders = oauthProviderNames(identities)
	return view, nil
}

func (s *Service) viewUserWithContext(ctx context.Context, user User) (UserView, error) {
	view := viewUser(&user)
	if s.auth.OAuthIdentities == nil {
		s.mu.Lock()
		identities := make([]OAuthIdentity, 0)
		for _, identity := range s.oauthIdentities {
			if identity.UserID == user.ID {
				identities = append(identities, *identity)
			}
		}
		s.mu.Unlock()
		view.OAuthProviders = oauthProviderNames(identities)
		return view, nil
	}
	identities, err := s.auth.OAuthIdentities.OAuthIdentitiesByUser(ctx, user.ID)
	if err != nil {
		return UserView{}, err
	}
	view.OAuthProviders = oauthProviderNames(identities)
	return view, nil
}

func viewUser(user *User) UserView {
	return UserView{
		ID:                user.ID,
		Email:             user.Email,
		DisplayName:       user.DisplayName,
		Language:          user.Language,
		Role:              user.Role,
		EmailVerified:     user.EmailVerified,
		PlanID:            user.PlanID,
		PlanExpiresAt:     user.PlanExpiresAt,
		OAuthProviders:    []string{},
		Frozen:            user.Frozen,
		CreatedAt:         user.CreatedAt,
		DeleteRequestedAt: user.DeleteRequestedAt,
		DeleteScheduledAt: user.DeleteScheduledAt,
	}
}

func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, 2, 19*1024, 1, 32)
	return "argon2id$" + base64.RawURLEncoding.EncodeToString(salt) + "$" + base64.RawURLEncoding.EncodeToString(key), nil
}

func hashSharePassword(password string) (string, error) {
	if password == "" {
		return "", nil
	}
	return hashPassword(password)
}

func verifySharePassword(encoded string, password string) (valid bool, needsUpgrade bool, err error) {
	if encoded == "" {
		return true, false, nil
	}
	if strings.HasPrefix(encoded, "argon2id$") {
		if err := verifyPassword(encoded, password); err != nil {
			if isAppStatus(err, http.StatusUnauthorized) {
				return false, false, nil
			}
			return false, false, err
		}
		return true, false, nil
	}
	legacy, err := hex.DecodeString(encoded)
	if err != nil || len(legacy) != sha256.Size {
		return false, false, E(http.StatusInternalServerError, "bad_share_password_hash", "stored share password hash is invalid")
	}
	got := sha256.Sum256([]byte(password))
	return subtle.ConstantTimeCompare(got[:], legacy) == 1, true, nil
}

func verifyPassword(encoded string, password string) error {
	parts := strings.Split(encoded, "$")
	if len(parts) != 3 || parts[0] != "argon2id" {
		return E(http.StatusInternalServerError, "bad_password_hash", "stored password hash is invalid")
	}
	salt, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return err
	}
	want, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return err
	}
	got := argon2.IDKey([]byte(password), salt, 2, 19*1024, 1, 32)
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return E(http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
	}
	return nil
}

func newToken() string {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func verificationCode() string {
	raw := make([]byte, 4)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	return fmt.Sprintf("%06d", binary.BigEndian.Uint32(raw)%1000000)
}

func registrationVerificationHash(email string, code string) string {
	return tokenHash(normalizeEmail(email) + "\x00" + strings.TrimSpace(code))
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
