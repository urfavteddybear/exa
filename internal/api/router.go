package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"exa/internal/auth"
	"exa/internal/db"
	"exa/internal/model"
)

const (
	minPasswordLength = 10
	maxPasswordBytes  = 72 // bcrypt rejects longer input
)

type RouterConfig struct {
	Store      *db.Store
	Auth       *auth.Service
	Monitors   model.MonitorController
	Notifier   model.Notifier
	SSE        *SSEHub
	StaticFS   fs.FS
	Version    string
	StartedAt  time.Time
	TrustProxy bool
}

type router struct {
	cfg              RouterConfig
	loginIPLimiter   *rateLimiter
	setupLimiter     *rateLimiter
	subscribeLimiter *rateLimiter
	pushLimiter      *rateLimiter
	lookupIP         func(ctx context.Context, network, host string) ([]net.IP, error)
}

// NewRouter constructs the HTTP handler for all API, feed, metrics, and SPA routes.
func NewRouter(cfg RouterConfig) http.Handler {
	if cfg.Version == "" {
		cfg.Version = "dev"
	}
	if cfg.StartedAt.IsZero() {
		cfg.StartedAt = time.Now().UTC()
	}
	if cfg.SSE == nil {
		cfg.SSE = NewSSEHub()
	}

	rt := &router{
		cfg:              cfg,
		loginIPLimiter:   newRateLimiter(10, 5*time.Minute),
		setupLimiter:     newRateLimiter(5, 10*time.Minute),
		subscribeLimiter: newRateLimiter(5, 10*time.Minute),
		pushLimiter:      newRateLimiter(120, 1*time.Minute),
		lookupIP:         net.DefaultResolver.LookupIP,
	}
	mux := http.NewServeMux()

	// Health, push, feeds, badges, metrics, and SSE.
	mux.HandleFunc("GET /api/v1/health", rt.handleHealth)
	mux.HandleFunc("GET /api/v1/push/{token}", rt.handlePush)
	mux.HandleFunc("POST /api/v1/push/{token}", rt.handlePush)
	mux.HandleFunc("GET /api/v1/stream", rt.handleSSE)
	mux.HandleFunc("GET /metrics", rt.handleMetrics)

	mux.HandleFunc("GET /api/v1/badge/{slug}/status", rt.handleBadgeStatus)
	mux.HandleFunc("GET /api/v1/badge/{slug}/status.svg", rt.handleBadgeStatus)
	mux.HandleFunc("GET /api/v1/badge/{slug}/uptime", rt.handleBadgeUptime)
	mux.HandleFunc("GET /api/v1/badge/{slug}/uptime.svg", rt.handleBadgeUptime)

	mux.HandleFunc("GET /api/v1/status/feed.rss", rt.handleRSSDefault)
	mux.HandleFunc("GET /api/v1/status/feed.atom", rt.handleAtomDefault)
	mux.HandleFunc("GET /api/v1/status/{slug}/feed.rss", rt.handleRSSBySlug)
	mux.HandleFunc("GET /api/v1/status/{slug}/feed.atom", rt.handleAtomBySlug)

	// Setup and authentication.
	mux.HandleFunc("GET /api/v1/setup/status", rt.handleSetupStatus)
	mux.HandleFunc("POST /api/v1/setup", rt.handleSetup)
	mux.HandleFunc("POST /api/v1/auth/login", rt.handleLogin)
	mux.HandleFunc("POST /api/v1/auth/logout", rt.handleLogout)
	mux.HandleFunc("GET /api/v1/auth/me", cfg.Auth.RequireAuth(model.UserRoleViewer, rt.handleMe))
	mux.HandleFunc("PUT /api/v1/auth/password", cfg.Auth.RequireAuth(model.UserRoleViewer, rt.handleChangePassword))

	// Public status and subscriptions.
	mux.HandleFunc("GET /api/v1/status", rt.handlePublicStatusDefault)
	mux.HandleFunc("GET /api/v1/status/{slug}", rt.handlePublicStatusBySlug)
	mux.HandleFunc("GET /api/v1/status/{slug}/incidents", rt.handlePublicIncidents)
	mux.HandleFunc("POST /api/v1/status/{slug}/subscribe", rt.handleSubscribe)
	mux.HandleFunc("GET /api/v1/unsubscribe/{token}", rt.handleUnsubscribe)
	mux.HandleFunc("GET /api/v1/subscribe/confirm/{token}", rt.handleConfirmSubscription)

	// Admin overview, monitors, and component groups.
	mux.HandleFunc("GET /api/v1/overview", cfg.Auth.RequireAuth(model.UserRoleViewer, rt.handleOverview))
	mux.HandleFunc("GET /api/v1/monitors", cfg.Auth.RequireAuth(model.UserRoleViewer, rt.handleListMonitors))
	mux.HandleFunc("POST /api/v1/monitors", cfg.Auth.RequireAuth(model.UserRoleAdmin, rt.handleCreateMonitor))
	mux.HandleFunc("GET /api/v1/monitors/{id}", cfg.Auth.RequireAuth(model.UserRoleViewer, rt.handleGetMonitor))
	mux.HandleFunc("PUT /api/v1/monitors/{id}", cfg.Auth.RequireAuth(model.UserRoleAdmin, rt.handleUpdateMonitor))
	mux.HandleFunc("DELETE /api/v1/monitors/{id}", cfg.Auth.RequireAuth(model.UserRoleAdmin, rt.handleDeleteMonitor))
	mux.HandleFunc("POST /api/v1/monitors/{id}/pause", cfg.Auth.RequireAuth(model.UserRoleAdmin, rt.handlePauseMonitor))
	mux.HandleFunc("POST /api/v1/monitors/{id}/resume", cfg.Auth.RequireAuth(model.UserRoleAdmin, rt.handleResumeMonitor))
	mux.HandleFunc("POST /api/v1/monitors/{id}/test", cfg.Auth.RequireAuth(model.UserRoleAdmin, rt.handleTestMonitor))

	mux.HandleFunc("GET /api/v1/groups", cfg.Auth.RequireAuth(model.UserRoleViewer, rt.handleListGroups))
	mux.HandleFunc("POST /api/v1/groups", cfg.Auth.RequireAuth(model.UserRoleAdmin, rt.handleCreateGroup))
	mux.HandleFunc("PUT /api/v1/groups/{id}", cfg.Auth.RequireAuth(model.UserRoleAdmin, rt.handleUpdateGroup))
	mux.HandleFunc("DELETE /api/v1/groups/{id}", cfg.Auth.RequireAuth(model.UserRoleAdmin, rt.handleDeleteGroup))

	// Incidents, maintenances, and status pages.
	mux.HandleFunc("GET /api/v1/incidents", cfg.Auth.RequireAuth(model.UserRoleViewer, rt.handleListIncidents))
	mux.HandleFunc("POST /api/v1/incidents", cfg.Auth.RequireAuth(model.UserRoleAdmin, rt.handleCreateIncident))
	mux.HandleFunc("POST /api/v1/incidents/{id}/updates", cfg.Auth.RequireAuth(model.UserRoleAdmin, rt.handleAddIncidentUpdate))
	mux.HandleFunc("DELETE /api/v1/incidents/{id}", cfg.Auth.RequireAuth(model.UserRoleAdmin, rt.handleDeleteIncident))

	mux.HandleFunc("GET /api/v1/maintenances", cfg.Auth.RequireAuth(model.UserRoleViewer, rt.handleListMaintenances))
	mux.HandleFunc("POST /api/v1/maintenances", cfg.Auth.RequireAuth(model.UserRoleAdmin, rt.handleCreateMaintenance))
	mux.HandleFunc("PUT /api/v1/maintenances/{id}", cfg.Auth.RequireAuth(model.UserRoleAdmin, rt.handleUpdateMaintenance))
	mux.HandleFunc("DELETE /api/v1/maintenances/{id}", cfg.Auth.RequireAuth(model.UserRoleAdmin, rt.handleDeleteMaintenance))

	mux.HandleFunc("GET /api/v1/status-pages", cfg.Auth.RequireAuth(model.UserRoleViewer, rt.handleListStatusPages))
	mux.HandleFunc("POST /api/v1/status-pages", cfg.Auth.RequireAuth(model.UserRoleAdmin, rt.handleCreateStatusPage))
	mux.HandleFunc("PUT /api/v1/status-pages/{id}", cfg.Auth.RequireAuth(model.UserRoleAdmin, rt.handleUpdateStatusPage))
	mux.HandleFunc("DELETE /api/v1/status-pages/{id}", cfg.Auth.RequireAuth(model.UserRoleAdmin, rt.handleDeleteStatusPage))
	mux.HandleFunc("GET /api/v1/status-pages/{id}/monitors", cfg.Auth.RequireAuth(model.UserRoleViewer, rt.handleListPageMonitors))
	mux.HandleFunc("PUT /api/v1/status-pages/{id}/monitors", cfg.Auth.RequireAuth(model.UserRoleAdmin, rt.handleSetPageMonitors))

	// Notifications and subscribers (admin only because channel configs and subscriber endpoints are sensitive).
	mux.HandleFunc("GET /api/v1/notifications", cfg.Auth.RequireAuth(model.UserRoleAdmin, rt.handleListNotifications))
	mux.HandleFunc("POST /api/v1/notifications", cfg.Auth.RequireAuth(model.UserRoleAdmin, rt.handleCreateNotification))
	mux.HandleFunc("PUT /api/v1/notifications/{id}", cfg.Auth.RequireAuth(model.UserRoleAdmin, rt.handleUpdateNotification))
	mux.HandleFunc("DELETE /api/v1/notifications/{id}", cfg.Auth.RequireAuth(model.UserRoleAdmin, rt.handleDeleteNotification))
	mux.HandleFunc("POST /api/v1/notifications/{id}/test", cfg.Auth.RequireAuth(model.UserRoleAdmin, rt.handleTestNotification))

	mux.HandleFunc("GET /api/v1/subscribers", cfg.Auth.RequireAuth(model.UserRoleAdmin, rt.handleListSubscribers))
	mux.HandleFunc("DELETE /api/v1/subscribers/{id}", cfg.Auth.RequireAuth(model.UserRoleAdmin, rt.handleDeleteSubscriber))

	// API keys, users, settings, and backups.
	mux.HandleFunc("GET /api/v1/api-keys", cfg.Auth.RequireAuth(model.UserRoleAdmin, rt.handleListAPIKeys))
	mux.HandleFunc("POST /api/v1/api-keys", cfg.Auth.RequireAuth(model.UserRoleAdmin, rt.handleCreateAPIKey))
	mux.HandleFunc("DELETE /api/v1/api-keys/{id}", cfg.Auth.RequireAuth(model.UserRoleAdmin, rt.handleDeleteAPIKey))

	mux.HandleFunc("GET /api/v1/users", cfg.Auth.RequireAuth(model.UserRoleAdmin, rt.handleListUsers))
	mux.HandleFunc("POST /api/v1/users", cfg.Auth.RequireAuth(model.UserRoleAdmin, rt.handleCreateUser))
	mux.HandleFunc("DELETE /api/v1/users/{id}", cfg.Auth.RequireAuth(model.UserRoleAdmin, rt.handleDeleteUser))

	mux.HandleFunc("GET /api/v1/settings", cfg.Auth.RequireAuth(model.UserRoleAdmin, rt.handleGetSettings))
	mux.HandleFunc("PUT /api/v1/settings", cfg.Auth.RequireAuth(model.UserRoleAdmin, rt.handlePutSettings))
	mux.HandleFunc("GET /api/v1/backup/export", cfg.Auth.RequireAuth(model.UserRoleAdmin, rt.handleExportBackup))
	mux.HandleFunc("POST /api/v1/backup/import", cfg.Auth.RequireAuth(model.UserRoleAdmin, rt.handleImportBackup))
	mux.HandleFunc("POST /api/v1/backup/s3/test", cfg.Auth.RequireAuth(model.UserRoleAdmin, rt.handleS3Test))
	mux.HandleFunc("POST /api/v1/backup/s3/run", cfg.Auth.RequireAuth(model.UserRoleAdmin, rt.handleS3Run))
	mux.HandleFunc("GET /api/v1/backup/s3/list", cfg.Auth.RequireAuth(model.UserRoleAdmin, rt.handleS3List))
	mux.HandleFunc("POST /api/v1/backup/s3/restore", cfg.Auth.RequireAuth(model.UserRoleAdmin, rt.handleS3Restore))
	mux.HandleFunc("DELETE /api/v1/backup/s3/object", cfg.Auth.RequireAuth(model.UserRoleAdmin, rt.handleS3Delete))

	// API 404 fallback and SPA static asset fallback.
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "endpoint not found")
	})
	mux.HandleFunc("/", rt.handleSPA)

	return securityHeadersMiddleware(mux)
}

func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func (rt *router) handleHealth(w http.ResponseWriter, r *http.Request) {
	uptime := int64(time.Since(rt.cfg.StartedAt).Seconds())
	writeJSON(w, http.StatusOK, map[string]any{
		"status":     "ok",
		"version":    rt.cfg.Version,
		"uptime_sec": uptime,
	})
}

func (rt *router) handlePush(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r, rt.cfg.TrustProxy)
	if !rt.pushLimiter.Allow(ip) {
		writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
		return
	}

	token := r.PathValue("token")
	if token == "" {
		writeError(w, http.StatusBadRequest, "missing push token")
		return
	}

	status := model.HeartbeatStatusUp
	latencyMs := 0
	msg := "ok"

	q := r.URL.Query()
	if s := strings.ToLower(strings.TrimSpace(q.Get("status"))); s != "" {
		switch model.HeartbeatStatus(s) {
		case model.HeartbeatStatusUp, model.HeartbeatStatusDegraded, model.HeartbeatStatusDown:
			status = model.HeartbeatStatus(s)
		}
	}
	if p := q.Get("ping"); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n >= 0 {
			latencyMs = n
		}
	} else if p := q.Get("latency_ms"); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n >= 0 {
			latencyMs = n
		}
	}
	if m := q.Get("msg"); m != "" {
		msg = m
	} else if m := q.Get("message"); m != "" {
		msg = m
	}

	if r.Method == http.MethodPost && r.Body != nil {
		var body struct {
			Status    string `json:"status"`
			LatencyMs *int   `json:"latency_ms"`
			Ping      *int   `json:"ping"`
			Message   string `json:"message"`
			Msg       string `json:"msg"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err == nil {
			if body.Status != "" {
				switch model.HeartbeatStatus(strings.ToLower(body.Status)) {
				case model.HeartbeatStatusUp, model.HeartbeatStatusDegraded, model.HeartbeatStatusDown:
					status = model.HeartbeatStatus(strings.ToLower(body.Status))
				}
			}
			if body.LatencyMs != nil && *body.LatencyMs >= 0 {
				latencyMs = *body.LatencyMs
			} else if body.Ping != nil && *body.Ping >= 0 {
				latencyMs = *body.Ping
			}
			if body.Message != "" {
				msg = body.Message
			} else if body.Msg != "" {
				msg = body.Msg
			}
		}
	}

	if rt.cfg.Monitors == nil {
		writeError(w, http.StatusServiceUnavailable, "monitor controller unavailable")
		return
	}
	mon, err := rt.cfg.Monitors.HandlePush(r.Context(), token, status, latencyMs, msg)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "invalid push token")
			return
		}
		writeInternalError(w, r, "handle push failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"monitor_id":     mon.ID,
		"current_status": mon.CurrentStatus,
	})
}

func (rt *router) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	done, err := rt.cfg.Store.IsSetupCompleted()
	if err != nil {
		writeInternalError(w, r, "setup status check failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"setup_completed": done})
}

func (rt *router) handleSetup(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r, rt.cfg.TrustProxy)
	if !rt.setupLimiter.Allow(ip) {
		writeError(w, http.StatusTooManyRequests, "too many setup attempts, try again later")
		return
	}

	var req model.SetupRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.Email = strings.TrimSpace(req.Email)
	req.SiteTitle = strings.TrimSpace(req.SiteTitle)
	if req.Username == "" || len(req.Password) < minPasswordLength || len(req.Password) > maxPasswordBytes {
		writeError(w, http.StatusBadRequest, "username is required and password must be 10 to 72 bytes")
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeInternalError(w, r, "hash password failed", err)
		return
	}

	user, page, err := rt.cfg.Store.SetupInitialAdmin(req.Username, req.Email, hash, req.SiteTitle)
	if err != nil {
		if errors.Is(err, db.ErrSetupCompleted) {
			writeError(w, http.StatusConflict, "setup already completed")
			return
		}
		writeInternalError(w, r, "setup initial admin failed", err)
		return
	}

	if err := rt.cfg.Auth.CreateSessionCookie(w, r, user.ID); err != nil {
		writeInternalError(w, r, "create session failed", err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"user":        user,
		"status_page": page,
	})
}

func (rt *router) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r, rt.cfg.TrustProxy)
	if !rt.loginIPLimiter.Allow(ip) {
		writeError(w, http.StatusTooManyRequests, "too many login attempts, try again later")
		return
	}

	var req model.LoginRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// No per-username limiter: anyone could use it to lock the real admin out. The per-IP limit plus
	// bcrypt and a 10-character minimum keep online guessing impractical.
	username := strings.TrimSpace(req.Username)

	user, err := rt.cfg.Store.GetUserByUsername(username)
	if err != nil {
		auth.VerifyDummyPassword(req.Password)
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	if !auth.VerifyPassword(user.PasswordHash, req.Password) {
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	if err := rt.cfg.Auth.CreateSessionCookie(w, r, user.ID); err != nil {
		writeInternalError(w, r, "create session failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (rt *router) handleLogout(w http.ResponseWriter, r *http.Request) {
	rt.cfg.Auth.ClearSessionCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (rt *router) handleMe(w http.ResponseWriter, r *http.Request) {
	p, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if p.User != nil {
		writeJSON(w, http.StatusOK, map[string]any{"user": p.User})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user": model.User{
			ID:       p.APIKeyID,
			Username: p.Username,
			Role:     p.Role,
		},
	})
}

func (rt *router) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	p, ok := auth.PrincipalFromContext(r.Context())
	if !ok || p.User == nil {
		writeError(w, http.StatusForbidden, "session user required")
		return
	}
	var req model.ChangePasswordRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.NewPassword) < minPasswordLength || len(req.NewPassword) > maxPasswordBytes {
		writeError(w, http.StatusBadRequest, "new password must be 10 to 72 bytes")
		return
	}
	if !auth.VerifyPassword(p.User.PasswordHash, req.CurrentPassword) {
		writeError(w, http.StatusUnauthorized, "current password is incorrect")
		return
	}
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		writeInternalError(w, r, "hash password failed", err)
		return
	}
	if err := rt.cfg.Store.UpdateUserPassword(p.User.ID, hash); err != nil {
		writeInternalError(w, r, "update password failed", err)
		return
	}
	if err := rt.cfg.Store.DeleteUserSessions(p.User.ID); err != nil {
		writeInternalError(w, r, "revoke sessions failed", err)
		return
	}
	if err := rt.cfg.Auth.CreateSessionCookie(w, r, p.User.ID); err != nil {
		writeInternalError(w, r, "reissue session failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (rt *router) handleListAPIKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := rt.cfg.Store.ListAPIKeys()
	if err != nil {
		writeInternalError(w, r, "list api keys failed", err)
		return
	}
	writeJSON(w, http.StatusOK, keys)
}

func (rt *router) handleCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	var req model.APIKeyCreateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	role := req.Role
	if role != model.UserRoleAdmin && role != model.UserRoleViewer {
		role = model.UserRoleAdmin
	}

	raw, prefix, hash, err := auth.GenerateAPIKey()
	if err != nil {
		writeInternalError(w, r, "generate api key failed", err)
		return
	}
	created, err := rt.cfg.Store.CreateAPIKey(model.APIKey{
		Name:      req.Name,
		KeyPrefix: prefix,
		KeyHash:   hash,
		Role:      role,
	})
	if err != nil {
		writeInternalError(w, r, "store api key failed", err)
		return
	}
	writeJSON(w, http.StatusCreated, model.APIKeyCreateResponse{
		APIKey: *created,
		Token:  raw,
	})
}

func (rt *router) handleDeleteAPIKey(w http.ResponseWriter, r *http.Request) {
	if err := rt.cfg.Store.DeleteAPIKey(r.PathValue("id")); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "api key not found")
			return
		}
		writeInternalError(w, r, "delete api key failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (rt *router) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := rt.cfg.Store.ListUsers()
	if err != nil {
		writeInternalError(w, r, "list users failed", err)
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func (rt *router) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req model.UserCreateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.Email = strings.TrimSpace(req.Email)
	if req.Username == "" || len(req.Password) < minPasswordLength || len(req.Password) > maxPasswordBytes {
		writeError(w, http.StatusBadRequest, "username is required and password must be 10 to 72 bytes")
		return
	}
	role := req.Role
	if role != model.UserRoleAdmin && role != model.UserRoleViewer {
		role = model.UserRoleViewer
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeInternalError(w, r, "hash user password failed", err)
		return
	}
	u, err := rt.cfg.Store.CreateUser(model.User{
		Username:     req.Username,
		Email:        req.Email,
		PasswordHash: hash,
		Role:         role,
	})
	if err != nil {
		if errors.Is(err, db.ErrConflict) {
			writeError(w, http.StatusConflict, "username already exists")
			return
		}
		writeInternalError(w, r, "create user failed", err)
		return
	}
	writeJSON(w, http.StatusCreated, u)
}

func (rt *router) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if p, ok := auth.PrincipalFromContext(r.Context()); ok && p.UserID == id {
		writeError(w, http.StatusBadRequest, "cannot delete your own user account")
		return
	}
	if err := rt.cfg.Store.DeleteUser(id); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeInternalError(w, r, "delete user failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (rt *router) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := rt.cfg.Store.GetAllSettings()
	if err != nil {
		writeInternalError(w, r, "get settings failed", err)
		return
	}
	maskSensitiveSettings(settings)
	writeJSON(w, http.StatusOK, settings)
}

func (rt *router) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var body map[string]string
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if rawSched, ok := body["s3_schedule"]; ok {
		sched := strings.ToLower(strings.TrimSpace(rawSched))
		switch sched {
		case "", "off", "hourly", "daily", "weekly":
		default:
			writeError(w, http.StatusBadRequest, "s3_schedule must be off, hourly, daily, or weekly")
			return
		}
		if sched == "" {
			sched = "off"
		}
		body["s3_schedule"] = sched
	}

	if rawRet, ok := body["s3_retention_count"]; ok {
		trimmed := strings.TrimSpace(rawRet)
		if trimmed != "" {
			n, err := strconv.Atoi(trimmed)
			if err != nil || n < 0 {
				writeError(w, http.StatusBadRequest, "s3_retention_count must be a non-negative integer")
				return
			}
			body["s3_retention_count"] = strconv.Itoa(n)
		}
	}

	for k, v := range body {
		switch k {
		case "setup_completed", "s3_last_backup_at", "s3_last_backup_key", "s3_last_error":
			continue
		case "s3_secret_access_key":
			if strings.TrimSpace(v) == "" || v == "********" {
				continue
			}
		}
		if err := rt.cfg.Store.SetSetting(k, v); err != nil {
			writeInternalError(w, r, "save setting failed", err)
			return
		}
	}
	settings, err := rt.cfg.Store.GetAllSettings()
	if err != nil {
		writeInternalError(w, r, "reload settings failed", err)
		return
	}
	maskSensitiveSettings(settings)
	writeJSON(w, http.StatusOK, settings)
}

func maskSensitiveSettings(settings map[string]string) {
	if settings["s3_secret_access_key"] != "" {
		settings["s3_secret_access_key"] = "********"
	}
}

func (rt *router) handleSPA(w http.ResponseWriter, r *http.Request) {
	if rt.cfg.StaticFS == nil {
		http.NotFound(w, r)
		return
	}
	cleaned := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if cleaned == "" {
		cleaned = "index.html"
	}

	if info, err := fs.Stat(rt.cfg.StaticFS, cleaned); err == nil && !info.IsDir() {
		http.FileServer(http.FS(rt.cfg.StaticFS)).ServeHTTP(w, r)
		return
	}

	// Serve index.html directly for client-side SPA routes without triggering FileServer redirects.
	indexBytes, err := fs.ReadFile(rt.cfg.StaticFS, "index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(indexBytes)
}

func isAdminCaller(r *http.Request) bool {
	p, ok := auth.PrincipalFromContext(r.Context())
	return ok && p.Role == model.UserRoleAdmin
}

func decodeJSON(r *http.Request, dst any) error {
	if r.Body == nil {
		return errors.New("empty request body")
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if err := dec.Decode(dst); err != nil {
		return errors.New("invalid JSON payload")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func writeInternalError(w http.ResponseWriter, r *http.Request, msg string, err error) {
	slog.Error(msg, "method", r.Method, "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "internal server error")
}
