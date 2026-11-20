package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"exa/internal/db"
	"exa/internal/model"
)

const (
	SessionCookieName = "exa_session"
	DefaultSessionTTL = 7 * 24 * time.Hour
)

type contextKey string

const principalContextKey contextKey = "exa_principal"

var dummyBcryptHash []byte

func init() {
	h, err := bcrypt.GenerateFromPassword([]byte("exa-dummy-timing-guard"), bcrypt.DefaultCost)
	if err == nil {
		dummyBcryptHash = h
	}
}

// Principal represents an authenticated user or API key caller.
type Principal struct {
	UserID     string
	Username   string
	Role       model.UserRole
	APIKeyID   string
	AuthMethod string
	User       *model.User
}

// Service handles password hashing, session cookies, and API key verification.
type Service struct {
	store        *db.Store
	sessionTTL   time.Duration
	cookieSecure bool
	trustProxy   bool
}

func NewService(store *db.Store) *Service {
	return &Service{
		store:      store,
		sessionTTL: DefaultSessionTTL,
	}
}

func (s *Service) SetCookieSecure(secure bool) {
	s.cookieSecure = secure
}

func (s *Service) SetTrustProxy(trust bool) {
	s.trustProxy = trust
}

func (s *Service) TrustProxy() bool {
	return s.trustProxy
}

func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func VerifyPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// VerifyDummyPassword performs a constant-cost bcrypt comparison to mask missing usernames.
func VerifyDummyPassword(password string) {
	if len(dummyBcryptHash) > 0 {
		_ = bcrypt.CompareHashAndPassword(dummyBcryptHash, []byte(password))
	}
}

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func GenerateAPIKey() (rawToken, prefix, hash string, err error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", "", "", err
	}
	rawToken = "exa_" + hex.EncodeToString(b)
	prefix = rawToken[:12]
	hash = HashToken(rawToken)
	return rawToken, prefix, hash, nil
}

func (s *Service) CreateSessionCookie(w http.ResponseWriter, r *http.Request, userID string) error {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	rawToken := hex.EncodeToString(b)
	expiresAt := time.Now().UTC().Add(s.sessionTTL)
	if _, err := s.store.CreateSession(HashToken(rawToken), userID, expiresAt); err != nil {
		return err
	}

	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    rawToken,
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   int(s.sessionTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cookieSecure || isSecureRequest(r, s.trustProxy),
	})
	return nil
}

func (s *Service) ClearSessionCookie(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(SessionCookieName); err == nil && c.Value != "" {
		_ = s.store.DeleteSession(HashToken(c.Value))
	}
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cookieSecure || isSecureRequest(r, s.trustProxy),
	})
}

func (s *Service) AuthenticateRequest(r *http.Request) (*Principal, bool) {
	if authHeader := strings.TrimSpace(r.Header.Get("Authorization")); authHeader != "" {
		if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
			raw := strings.TrimSpace(authHeader[7:])
			if raw != "" {
				key, err := s.store.GetAPIKeyByHash(HashToken(raw))
				if err == nil {
					_ = s.store.TouchAPIKey(key.ID)
					return &Principal{
						Username:   key.Name,
						Role:       key.Role,
						APIKeyID:   key.ID,
						AuthMethod: "apikey",
					}, true
				}
			}
		}
		return nil, false
	}

	cookie, err := r.Cookie(SessionCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return nil, false
	}
	user, err := s.store.GetSessionUser(HashToken(cookie.Value))
	if err != nil {
		return nil, false
	}
	return &Principal{
		UserID:     user.ID,
		Username:   user.Username,
		Role:       user.Role,
		AuthMethod: "session",
		User:       user,
	}, true
}

// RequireAuth enforces authentication, CSRF origin checks on cookie mutations, and minimum role.
func (s *Service) RequireAuth(role model.UserRole, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := s.AuthenticateRequest(r)
		if !ok {
			writeAuthError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		if p.AuthMethod == "session" && isStateChangingMethod(r.Method) && !checkCSRFHeaders(r) {
			writeAuthError(w, http.StatusForbidden, "csrf validation failed")
			return
		}
		if role == model.UserRoleAdmin && p.Role != model.UserRoleAdmin {
			writeAuthError(w, http.StatusForbidden, "admin role required")
			return
		}
		ctx := context.WithValue(r.Context(), principalContextKey, p)
		next(w, r.WithContext(ctx))
	}
}

func PrincipalFromContext(ctx context.Context) (*Principal, bool) {
	p, ok := ctx.Value(principalContextKey).(*Principal)
	return p, ok && p != nil
}

func isStateChangingMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func checkCSRFHeaders(r *http.Request) bool {
	if sfs := strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")); sfs != "" {
		if !strings.EqualFold(sfs, "same-origin") && !strings.EqualFold(sfs, "none") {
			return false
		}
	}
	if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || u.Host == "" {
			return false
		}
		if r.Host != "" && !strings.EqualFold(u.Host, r.Host) {
			return false
		}
	}
	return true
}

func isSecureRequest(r *http.Request, trustProxy bool) bool {
	if r.TLS != nil {
		return true
	}
	if trustProxy {
		return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	}
	return false
}

func writeAuthError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
