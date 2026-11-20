package auth

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"exa/internal/db"
	"exa/internal/model"
)

func TestPasswordSessionAndAPIKeyAuth(t *testing.T) {
	store, err := db.OpenPath(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	defer func() { _ = store.Close() }()

	hash, err := HashPassword("correct-horse-battery")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !VerifyPassword(hash, "correct-horse-battery") {
		t.Fatalf("expected password verification to succeed")
	}
	if VerifyPassword(hash, "wrong-password") {
		t.Fatalf("expected wrong password to fail")
	}

	admin, _, err := store.SetupInitialAdmin("admin", "admin@exa.local", hash, "Exa")
	if err != nil {
		t.Fatalf("SetupInitialAdmin: %v", err)
	}
	viewerHash, _ := HashPassword("viewer-pass-123")
	viewer, err := store.CreateUser(model.User{
		Username:     "viewer",
		Email:        "viewer@exa.local",
		PasswordHash: viewerHash,
		Role:         model.UserRoleViewer,
	})
	if err != nil {
		t.Fatalf("CreateUser viewer: %v", err)
	}

	svc := NewService(store)

	// Test session cookie auth.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	if err := svc.CreateSessionCookie(rec, req, viewer.ID); err != nil {
		t.Fatalf("CreateSessionCookie: %v", err)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != SessionCookieName || !cookies[0].HttpOnly {
		t.Fatalf("unexpected cookies: %+v", cookies)
	}

	adminHandler := svc.RequireAuth(model.UserRoleAdmin, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	viewerHandler := svc.RequireAuth(model.UserRoleViewer, func(w http.ResponseWriter, r *http.Request) {
		p, ok := PrincipalFromContext(r.Context())
		if !ok || p.UserID != viewer.ID {
			t.Fatalf("missing viewer principal")
		}
		w.WriteHeader(http.StatusOK)
	})

	// Unauthenticated -> 401.
	rUnauth := httptest.NewRequest(http.MethodGet, "/test", nil)
	wUnauth := httptest.NewRecorder()
	viewerHandler(wUnauth, rUnauth)
	if wUnauth.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", wUnauth.Code)
	}

	// Viewer on viewer route -> 200.
	rViewer := httptest.NewRequest(http.MethodGet, "/test", nil)
	rViewer.AddCookie(cookies[0])
	wViewer := httptest.NewRecorder()
	viewerHandler(wViewer, rViewer)
	if wViewer.Code != http.StatusOK {
		t.Fatalf("expected 200 for viewer, got %d", wViewer.Code)
	}

	// Viewer on admin route -> 403.
	wForbidden := httptest.NewRecorder()
	adminHandler(wForbidden, rViewer)
	if wForbidden.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for viewer on admin route, got %d", wForbidden.Code)
	}

	// Clear cookie -> 401.
	recClear := httptest.NewRecorder()
	svc.ClearSessionCookie(recClear, rViewer)
	wAfterClear := httptest.NewRecorder()
	viewerHandler(wAfterClear, rViewer)
	if wAfterClear.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 after logout, got %d", wAfterClear.Code)
	}

	// API Key Bearer auth.
	rawTok, prefix, keyHash, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	if _, err := store.CreateAPIKey(model.APIKey{
		Name:      "deploy-bot",
		KeyPrefix: prefix,
		KeyHash:   keyHash,
		Role:      admin.Role,
	}); err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}

	rBearer := httptest.NewRequest(http.MethodGet, "/admin", nil)
	rBearer.Header.Set("Authorization", "Bearer "+rawTok)
	wBearer := httptest.NewRecorder()
	adminHandler(wBearer, rBearer)
	if wBearer.Code != http.StatusOK {
		t.Fatalf("expected 200 with bearer api key, got %d", wBearer.Code)
	}

	// Verify session tokens are hashed in DB (raw cookie value cannot match DB id directly).
	recSec := httptest.NewRecorder()
	reqSec := httptest.NewRequest(http.MethodPost, "/login", nil)
	svc.SetCookieSecure(true)
	if err := svc.CreateSessionCookie(recSec, reqSec, admin.ID); err != nil {
		t.Fatalf("CreateSessionCookie admin: %v", err)
	}
	adminCookie := recSec.Result().Cookies()[0]
	if !adminCookie.Secure {
		t.Fatalf("expected Secure cookie when SetCookieSecure(true)")
	}
	if _, err := store.GetSessionUser(adminCookie.Value); err != db.ErrNotFound {
		t.Fatalf("expected raw session token NOT stored unhashed in DB, got err=%v", err)
	}
	if u, err := store.GetSessionUser(HashToken(adminCookie.Value)); err != nil || u.ID != admin.ID {
		t.Fatalf("expected hashed session token in DB, got u=%+v err=%v", u, err)
	}

	// Verify CSRF protection on cookie-authenticated mutations.
	postAdmin := svc.RequireAuth(model.UserRoleAdmin, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	rBadOrigin := httptest.NewRequest(http.MethodPost, "https://exa.local/api/v1/monitors", nil)
	rBadOrigin.Host = "exa.local"
	rBadOrigin.Header.Set("Origin", "https://evil.example")
	rBadOrigin.AddCookie(adminCookie)
	wBadOrigin := httptest.NewRecorder()
	postAdmin(wBadOrigin, rBadOrigin)
	if wBadOrigin.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on CSRF Origin mismatch, got %d", wBadOrigin.Code)
	}

	rBadFetch := httptest.NewRequest(http.MethodDelete, "https://exa.local/api/v1/monitors/1", nil)
	rBadFetch.Host = "exa.local"
	rBadFetch.Header.Set("Sec-Fetch-Site", "cross-site")
	rBadFetch.AddCookie(adminCookie)
	wBadFetch := httptest.NewRecorder()
	postAdmin(wBadFetch, rBadFetch)
	if wBadFetch.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on Sec-Fetch-Site: cross-site, got %d", wBadFetch.Code)
	}

	rSameOrigin := httptest.NewRequest(http.MethodPost, "https://exa.local/api/v1/monitors", nil)
	rSameOrigin.Host = "exa.local"
	rSameOrigin.Header.Set("Origin", "https://exa.local")
	rSameOrigin.Header.Set("Sec-Fetch-Site", "same-origin")
	rSameOrigin.AddCookie(adminCookie)
	wSameOrigin := httptest.NewRecorder()
	postAdmin(wSameOrigin, rSameOrigin)
	if wSameOrigin.Code != http.StatusOK {
		t.Fatalf("expected 200 on matching Origin + same-origin, got %d", wSameOrigin.Code)
	}

	// Verify TrustProxy gating on X-Forwarded-Proto.
	rXFP := httptest.NewRequest(http.MethodPost, "/login", nil)
	rXFP.Header.Set("X-Forwarded-Proto", "https")
	svc.SetCookieSecure(false)
	svc.SetTrustProxy(false)
	recNoTrust := httptest.NewRecorder()
	_ = svc.CreateSessionCookie(recNoTrust, rXFP, admin.ID)
	if recNoTrust.Result().Cookies()[0].Secure {
		t.Fatalf("expected Secure=false when TrustProxy=false")
	}
	svc.SetTrustProxy(true)
	recTrust := httptest.NewRecorder()
	_ = svc.CreateSessionCookie(recTrust, rXFP, admin.ID)
	if !recTrust.Result().Cookies()[0].Secure {
		t.Fatalf("expected Secure=true when TrustProxy=true and X-Forwarded-Proto=https")
	}

	// VerifyDummyPassword executes cleanly.
	VerifyDummyPassword("anything")
}
