package api

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"encoding/xml"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"exa/internal/auth"
	"exa/internal/db"
	"exa/internal/model"
)

type mockMonitorController struct {
	mu       sync.Mutex
	reloaded []string
	removed  []string
	pushHits []string
	store    *db.Store
}

func (m *mockMonitorController) ReloadMonitor(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reloaded = append(m.reloaded, id)
}

func (m *mockMonitorController) RemoveMonitor(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removed = append(m.removed, id)
}

func (m *mockMonitorController) ReloadAll() {}

func (m *mockMonitorController) TestMonitorNow(ctx context.Context, mon model.Monitor) model.ProbeResult {
	return model.ProbeResult{
		Status:     model.HeartbeatStatusUp,
		LatencyMs:  18,
		StatusCode: 200,
		Message:    "ok",
		CheckedAt:  time.Now().UTC(),
	}
}

func (m *mockMonitorController) HandlePush(ctx context.Context, token string, status model.HeartbeatStatus, latencyMs int, msg string) (*model.Monitor, error) {
	m.mu.Lock()
	m.pushHits = append(m.pushHits, token)
	m.mu.Unlock()
	mon, err := m.store.GetMonitorByPushToken(token)
	if err != nil {
		return nil, err
	}
	_ = m.store.RecordHeartbeat(ctx, mon.ID, model.ProbeResult{
		Status:    status,
		LatencyMs: latencyMs,
		Message:   msg,
		CheckedAt: time.Now().UTC(),
	}, model.ComponentStatusOperational, mon.IntervalSec)
	return m.store.GetMonitorByID(mon.ID)
}

type mockNotifier struct {
	mu            sync.Mutex
	incidentCalls int
	maintCalls    int
	testCalls     int
	doneCh        chan struct{}
}

func (n *mockNotifier) NotifyMonitorStateChange(ctx context.Context, m model.Monitor, prev, next model.ComponentStatus, errMsg string) {
}

func (n *mockNotifier) NotifyIncidentUpdate(ctx context.Context, page model.StatusPage, inc model.Incident, upd model.IncidentUpdate) {
	n.mu.Lock()
	n.incidentCalls++
	n.mu.Unlock()
	if n.doneCh != nil {
		select {
		case n.doneCh <- struct{}{}:
		default:
		}
	}
}

func (n *mockNotifier) NotifyMaintenanceUpdate(ctx context.Context, page model.StatusPage, maint model.Maintenance) {
	n.mu.Lock()
	n.maintCalls++
	n.mu.Unlock()
}

func (n *mockNotifier) TestChannel(ctx context.Context, ch model.NotificationChannel) error {
	n.mu.Lock()
	n.testCalls++
	n.mu.Unlock()
	return nil
}

func setupTestAPI(t *testing.T) (http.Handler, *db.Store, *SSEHub, *mockMonitorController, *mockNotifier) {
	t.Helper()
	store, err := db.OpenPath(filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	authSvc := auth.NewService(store)
	sse := NewSSEHub()
	mc := &mockMonitorController{store: store}
	notifier := &mockNotifier{doneCh: make(chan struct{}, 8)}
	staticFS := fstest.MapFS{
		"index.html":    {Data: []byte("<!doctype html><html><body>exa-spa</body></html>")},
		"assets/app.js": {Data: []byte("console.log('exa');")},
	}

	h := NewRouter(RouterConfig{
		Store:     store,
		Auth:      authSvc,
		Monitors:  mc,
		Notifier:  notifier,
		SSE:       sse,
		StaticFS:  staticFS,
		Version:   "0.1.0-test",
		StartedAt: time.Now().UTC().Add(-10 * time.Second),
	})
	return h, store, sse, mc, notifier
}

func doReq(t *testing.T, h http.Handler, method, path string, body any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestEndToEndAPIWorkflow(t *testing.T) {
	h, store, _, mc, notifier := setupTestAPI(t)

	// 1. Setup wizard.
	rec := doReq(t, h, http.MethodGet, "/api/v1/setup/status", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"setup_completed":false`) {
		t.Fatalf("setup status before: %d %s", rec.Code, rec.Body.String())
	}

	rec = doReq(t, h, http.MethodPost, "/api/v1/setup", map[string]string{
		"username":   "admin",
		"email":      "admin@exa.local",
		"password":   "password123",
		"site_title": "Exa Core Status",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup failed: %d %s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatalf("expected session cookie on setup")
	}
	sessionCookie := cookies[0]

	// Second setup rejected.
	rec = doReq(t, h, http.MethodPost, "/api/v1/setup", map[string]string{
		"username": "other",
		"password": "password123",
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 on repeat setup, got %d", rec.Code)
	}

	// Auth /me.
	rec = doReq(t, h, http.MethodGet, "/api/v1/auth/me", nil, sessionCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("/auth/me failed: %d %s", rec.Code, rec.Body.String())
	}

	// 2. Create group & monitor (verifies attach_default_page defaults to true).
	rec = doReq(t, h, http.MethodPost, "/api/v1/groups", map[string]any{
		"name":       "Edge",
		"sort_order": 1,
	}, sessionCookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create group: %d %s", rec.Code, rec.Body.String())
	}
	var grp model.ComponentGroup
	_ = json.Unmarshal(rec.Body.Bytes(), &grp)

	rec = doReq(t, h, http.MethodPost, "/api/v1/monitors", map[string]any{
		"name":         "Public API",
		"slug":         "public-api",
		"type":         "http",
		"target":       "https://api.example.com/health",
		"group_id":     grp.ID,
		"interval_sec": 30,
	}, sessionCookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create monitor: %d %s", rec.Code, rec.Body.String())
	}
	var mon model.Monitor
	_ = json.Unmarshal(rec.Body.Bytes(), &mon)
	if len(mc.reloaded) != 1 || mc.reloaded[0] != mon.ID {
		t.Fatalf("expected ReloadMonitor called, got %+v", mc.reloaded)
	}

	// Public status before any heartbeats: 90 days, all no_data, uptime_pct null.
	rec = doReq(t, h, http.MethodGet, "/api/v1/status", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("public status: %d %s", rec.Code, rec.Body.String())
	}
	var pub model.PublicStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &pub); err != nil {
		t.Fatalf("unmarshal public status: %v", err)
	}
	if len(pub.Groups) != 1 || len(pub.Groups[0].Monitors) != 1 {
		t.Fatalf("expected 1 group with 1 monitor on default page, got %+v", pub.Groups)
	}
	pm := pub.Groups[0].Monitors[0]
	if len(pm.Days) != 90 {
		t.Fatalf("expected 90 days, got %d", len(pm.Days))
	}
	if pm.Days[89].Status != "no_data" || pm.Days[89].UptimePct != nil || pm.Uptime90d != nil {
		t.Fatalf("expected no_data and nil uptime_pct, got %+v", pm.Days[89])
	}

	// Record a heartbeat and verify status + badges + metrics.
	if err := store.RecordHeartbeat(context.Background(), mon.ID, model.ProbeResult{
		Status:     model.HeartbeatStatusUp,
		LatencyMs:  24,
		StatusCode: 200,
		Message:    "ok",
		CheckedAt:  time.Now().UTC(),
	}, model.ComponentStatusOperational, 30); err != nil {
		t.Fatalf("RecordHeartbeat: %v", err)
	}

	rec = doReq(t, h, http.MethodGet, "/api/v1/status", nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &pub)
	pm = pub.Groups[0].Monitors[0]
	if pm.Days[89].Status != "up" || pm.Days[89].UptimePct == nil || *pm.Days[89].UptimePct != 100 {
		t.Fatalf("expected today up 100%%, got %+v", pm.Days[89])
	}

	// Badges.
	rec = doReq(t, h, http.MethodGet, "/api/v1/badge/public-api/status", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Operational") {
		t.Fatalf("status badge: %d %s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodGet, "/api/v1/badge/public-api/uptime", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "100.00%") {
		t.Fatalf("uptime badge: %d %s", rec.Code, rec.Body.String())
	}

	// 3. Incidents + Feeds + Notifier hook.
	rec = doReq(t, h, http.MethodPost, "/api/v1/incidents", map[string]any{
		"title":       "Elevated errors",
		"impact":      "minor",
		"status":      "investigating",
		"message":     "Looking into 502 spikes.",
		"monitor_ids": []string{mon.ID},
	}, sessionCookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create incident: %d %s", rec.Code, rec.Body.String())
	}
	select {
	case <-notifier.doneCh:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for async NotifyIncidentUpdate")
	}

	rec = doReq(t, h, http.MethodGet, "/api/v1/status/default/feed.rss", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Elevated errors") {
		t.Fatalf("rss feed: %d %s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodGet, "/api/v1/status/default/feed.atom", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Elevated errors") {
		t.Fatalf("atom feed: %d %s", rec.Code, rec.Body.String())
	}

	// 4. Subscribe disabled by default (403), then enabled (201).
	rec = doReq(t, h, http.MethodPost, "/api/v1/status/default/subscribe", map[string]string{
		"type":     "email",
		"endpoint": "alerts@example.com",
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when subscriptions disabled, got %d %s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodPut, "/api/v1/settings", map[string]string{
		"allow_subscriptions": "true",
	}, sessionCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("enable allow_subscriptions: %d %s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodPost, "/api/v1/status/default/subscribe", map[string]string{
		"type":     "email",
		"endpoint": "alerts@example.com",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("subscribe: %d %s", rec.Code, rec.Body.String())
	}

	// 5. Metrics requires metrics_token to be configured and matched.
	rec = doReq(t, h, http.MethodGet, "/metrics", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 when metrics_token unset, got %d %s", rec.Code, rec.Body.String())
	}

	rec = doReq(t, h, http.MethodPut, "/api/v1/settings", map[string]string{
		"metrics_token": "prom-secret",
	}, sessionCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("put settings: %d %s", rec.Code, rec.Body.String())
	}

	rec = doReq(t, h, http.MethodGet, "/metrics", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 when metrics_token set, got %d", rec.Code)
	}
	reqMetrics := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	reqMetrics.Header.Set("Authorization", "Bearer prom-secret")
	recMetrics := httptest.NewRecorder()
	h.ServeHTTP(recMetrics, reqMetrics)
	if recMetrics.Code != http.StatusOK {
		t.Fatalf("expected 200 with valid metrics token, got %d", recMetrics.Code)
	}

	// 6. SPA fallback and static assets.
	rec = doReq(t, h, http.MethodGet, "/admin/monitors", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "exa-spa") {
		t.Fatalf("SPA fallback: %d %s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodGet, "/assets/app.js", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "console.log") {
		t.Fatalf("static asset: %d %s", rec.Code, rec.Body.String())
	}
}

type sseRecorder struct {
	mu     sync.Mutex
	header http.Header
	buf    bytes.Buffer
	code   int
	notify chan struct{}
}

func newSSERecorder() *sseRecorder {
	return &sseRecorder{
		header: make(http.Header),
		notify: make(chan struct{}, 16),
	}
}

func (s *sseRecorder) Header() http.Header { return s.header }
func (s *sseRecorder) WriteHeader(code int) {
	s.mu.Lock()
	s.code = code
	s.mu.Unlock()
}
func (s *sseRecorder) Write(p []byte) (int, error) {
	s.mu.Lock()
	n, err := s.buf.Write(p)
	s.mu.Unlock()
	select {
	case s.notify <- struct{}{}:
	default:
	}
	return n, err
}
func (s *sseRecorder) Flush() {
	select {
	case s.notify <- struct{}{}:
	default:
	}
}
func (s *sseRecorder) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func TestSSEStream(t *testing.T) {
	h, store, sse, _, _ := setupTestAPI(t)

	// Unauthenticated SSE requires an existing public status page.
	rec404 := doReq(t, h, http.MethodGet, "/api/v1/stream", nil)
	if rec404.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when no public page exists, got %d", rec404.Code)
	}

	page, err := store.CreateStatusPage(model.StatusPage{
		Slug:        "default",
		Title:       "Public",
		IsDefault:   true,
		IsPublic:    true,
		ShowLatency: false,
	})
	if err != nil {
		t.Fatalf("CreateStatusPage: %v", err)
	}
	monPub, err := store.CreateMonitor(model.Monitor{
		Name:   "PubMon",
		Slug:   "pubmon",
		Type:   model.MonitorTypeHTTP,
		Target: "https://example.com",
		Active: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	if err := store.AttachMonitorToPages(monPub.ID, []string{page.ID}, nil, 0); err != nil {
		t.Fatalf("AttachMonitorToPages: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/stream", nil).WithContext(ctx)
	rec := newSSERecorder()

	done := make(chan struct{})
	go func() {
		h.ServeHTTP(rec, req)
		close(done)
	}()

	// Wait for initial ": connected" flush.
	deadline := time.After(2 * time.Second)
	for !strings.Contains(rec.String(), ": connected") {
		select {
		case <-rec.notify:
		case <-deadline:
			t.Fatalf("timed out waiting for SSE connect")
		}
	}

	// Broadcast unattached monitor event (must be filtered out).
	sse.Broadcast(model.SSEEvent{
		Type: "heartbeat",
		Data: map[string]any{"monitor_id": "private-mon", "message": "secret error", "latency_ms": 99},
	})
	// Broadcast attached monitor event (must clear message and omit latency_ms because ShowLatency=false).
	sse.Broadcast(model.SSEEvent{
		Type: "heartbeat",
		Data: map[string]any{"monitor_id": monPub.ID, "message": "internal detail", "latency_ms": 42},
	})

	for !strings.Contains(rec.String(), monPub.ID) {
		select {
		case <-rec.notify:
		case <-deadline:
			t.Fatalf("timed out waiting for SSE broadcast, got %q", rec.String())
		}
	}

	out := rec.String()
	if strings.Contains(out, "private-mon") || strings.Contains(out, "internal detail") || strings.Contains(out, "latency_ms") {
		t.Fatalf("SSE leaked unattached or sensitive fields: %q", out)
	}
	if !strings.Contains(out, "event: heartbeat") {
		t.Fatalf("expected event: heartbeat in %q", out)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("unexpected Content-Type: %q", ct)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("SSE handler did not exit on context cancel")
	}

	sc := bufio.NewScanner(strings.NewReader(out))
	var lines []string
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 SSE lines, got %v", lines)
	}
}

func TestOverviewAndMonitorDetailRanges(t *testing.T) {
	h, store, _, _, _ := setupTestAPI(t)

	rec := doReq(t, h, http.MethodPost, "/api/v1/setup", map[string]string{
		"username":   "admin",
		"email":      "admin@exa.local",
		"password":   "password123",
		"site_title": "Exa Ops",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup: %d %s", rec.Code, rec.Body.String())
	}
	cookie := rec.Result().Cookies()[0]

	// 1. Empty overview: nullable metrics must be nil/null, buckets=60, points=48.
	rec = doReq(t, h, http.MethodGet, "/api/v1/overview", nil, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("empty overview: %d %s", rec.Code, rec.Body.String())
	}
	var emptyOV model.AdminOverviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &emptyOV); err != nil {
		t.Fatalf("unmarshal empty overview: %v", err)
	}
	if emptyOV.TotalMonitors != 0 || emptyOV.Uptime30d != nil || emptyOV.MedianResponse24hMs != nil || emptyOV.Availability24hPct != nil {
		t.Fatalf("expected nil metrics on empty overview, got %+v", emptyOV)
	}
	if len(emptyOV.Availability24h) != 60 || len(emptyOV.ResponseSeries24h) != 48 {
		t.Fatalf("unexpected bucket lengths: avail=%d resp=%d", len(emptyOV.Availability24h), len(emptyOV.ResponseSeries24h))
	}
	if emptyOV.Availability24h[0].Status != "no_data" || emptyOV.Availability24h[0].UptimePct != nil {
		t.Fatalf("expected no_data bucket, got %+v", emptyOV.Availability24h[0])
	}
	if emptyOV.DefaultPageSlug != "default" || emptyOV.DefaultPageTitle != "Exa Ops" {
		t.Fatalf("unexpected default page info: %+v", emptyOV)
	}

	// 2. Populate group, channel, monitors, heartbeats across windows, TLS, and incident.
	grp, err := store.CreateGroup(model.ComponentGroup{Name: "API Tier", SortOrder: 1})
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	ch, err := store.CreateNotificationChannel(model.NotificationChannel{
		Name:      "Ops Webhook",
		Type:      model.NotificationTypeWebhook,
		Config:    json.RawMessage(`{"url":"https://hooks.local/1"}`),
		IsDefault: true,
		Enabled:   true,
	})
	if err != nil {
		t.Fatalf("CreateNotificationChannel: %v", err)
	}
	_ = ch

	monUp, err := store.CreateMonitor(model.Monitor{
		Name:        "Gateway",
		Slug:        "gateway",
		Type:        model.MonitorTypeHTTP,
		Target:      "https://gw.local/health",
		GroupID:     &grp.ID,
		IntervalSec: 60,
		TimeoutSec:  5,
		Active:      true,
		Config:      json.RawMessage(`{"tls_expiry_warn_days":20}`),
	})
	if err != nil {
		t.Fatalf("CreateMonitor up: %v", err)
	}
	_ = store.AttachMonitorToDefaultPage(monUp.ID, monUp.GroupID, 0)

	monDown, err := store.CreateMonitor(model.Monitor{
		Name:        "Billing Worker",
		Slug:        "billing",
		Type:        model.MonitorTypeTCP,
		Target:      "billing.local:443",
		IntervalSec: 60,
		TimeoutSec:  5,
		Active:      true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor down: %v", err)
	}
	_ = store.AttachMonitorToDefaultPage(monDown.ID, nil, 1)

	now := time.Now().UTC()
	ctx := context.Background()
	daysLeft := 12
	exp := now.Add(12 * 24 * time.Hour)

	// Record historical check 30h ago (prev 24h) and recent checks (last 30m).
	_ = store.RecordHeartbeat(ctx, monUp.ID, model.ProbeResult{
		Status:     model.HeartbeatStatusUp,
		LatencyMs:  50,
		StatusCode: 200,
		CheckedAt:  now.Add(-30 * time.Hour),
	}, model.ComponentStatusOperational, 60)
	_ = store.RecordHeartbeat(ctx, monUp.ID, model.ProbeResult{
		Status:       model.HeartbeatStatusUp,
		LatencyMs:    30,
		StatusCode:   200,
		TLSExpiresAt: &exp,
		TLSDaysLeft:  &daysLeft,
		CheckedAt:    now.Add(-20 * time.Minute),
	}, model.ComponentStatusOperational, 60)
	_ = store.RecordHeartbeat(ctx, monUp.ID, model.ProbeResult{
		Status:       model.HeartbeatStatusUp,
		LatencyMs:    40,
		StatusCode:   200,
		TLSExpiresAt: &exp,
		TLSDaysLeft:  &daysLeft,
		CheckedAt:    now.Add(-5 * time.Minute),
	}, model.ComponentStatusOperational, 60)

	_ = store.RecordHeartbeat(ctx, monDown.ID, model.ProbeResult{
		Status:    model.HeartbeatStatusDown,
		LatencyMs: 200,
		Message:   "connection refused",
		CheckedAt: now.Add(-10 * time.Minute),
	}, model.ComponentStatusMajorOutage, 60)

	defPage, _ := store.GetDefaultStatusPage()
	inc, err := store.CreateIncident(model.Incident{
		StatusPageID: defPage.ID,
		Title:        "Billing outage",
		Impact:       model.IncidentImpactMajor,
		Status:       model.IncidentStatusInvestigating,
		MonitorIDs:   []string{monDown.ID},
	}, "Investigating TCP failure.")
	if err != nil {
		t.Fatalf("CreateIncident: %v", err)
	}

	rec = doReq(t, h, http.MethodGet, "/api/v1/overview", nil, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("populated overview: %d %s", rec.Code, rec.Body.String())
	}
	var ov model.AdminOverviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &ov); err != nil {
		t.Fatalf("unmarshal populated overview: %v", err)
	}
	if ov.TotalMonitors != 2 || ov.OperationalMonitors != 1 || ov.DownMonitors != 1 {
		t.Fatalf("unexpected monitor counts: %+v", ov)
	}
	if ov.OpenIncidentsCount != 1 || ov.Downtime30dSec != 60 {
		t.Fatalf("unexpected incident/downtime counts: open=%d downtime=%d", ov.OpenIncidentsCount, ov.Downtime30dSec)
	}
	if ov.MedianResponse24hMs == nil || *ov.MedianResponse24hMs != 40 {
		t.Fatalf("expected median_response_24h_ms=40, got %v", ov.MedianResponse24hMs)
	}
	if ov.MedianResponsePrev24hMs == nil || *ov.MedianResponsePrev24hMs != 50 {
		t.Fatalf("expected median_response_prev_24h_ms=50, got %v", ov.MedianResponsePrev24hMs)
	}
	if len(ov.ActiveAlerts) != 1 || ov.ActiveAlerts[0].IncidentID != inc.ID || ov.ActiveAlerts[0].MonitorID != monDown.ID {
		t.Fatalf("unexpected active_alerts: %+v", ov.ActiveAlerts)
	}
	if len(ov.TLSCertificates) != 1 || ov.TLSCertificates[0].TLSDaysLeft != 12 || ov.TLSCertificates[0].WarnDays != 20 {
		t.Fatalf("unexpected tls_certificates: %+v", ov.TLSCertificates)
	}
	if len(ov.Activity) == 0 {
		t.Fatalf("expected activity entries")
	}

	// 3. Test MonitorDetailResponse across ranges (1h, 24h, 7d, 30d).
	rec = doReq(t, h, http.MethodGet, "/api/v1/monitors/"+monUp.ID+"?range=1h", nil, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("monitor detail 1h: %d %s", rec.Code, rec.Body.String())
	}
	var det1h model.MonitorDetailResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &det1h); err != nil {
		t.Fatalf("unmarshal detail 1h: %v", err)
	}
	if det1h.Range != "1h" || len(det1h.Heartbeats) != 2 {
		t.Fatalf("expected range=1h and 2 heartbeats, got range=%s len=%d", det1h.Range, len(det1h.Heartbeats))
	}
	if len(det1h.RecentChecks60) != 3 || det1h.RecentChecks60[0].LatencyMs != 50 || det1h.RecentChecks60[2].LatencyMs != 40 {
		t.Fatalf("expected recent_checks_60 oldest-to-newest, got %+v", det1h.RecentChecks60)
	}
	if det1h.AvgLatency24h == nil || *det1h.AvgLatency24h != 35 {
		t.Fatalf("expected avg_latency_24h=35, got %v", det1h.AvgLatency24h)
	}
	if len(det1h.ChannelNames) != 1 || det1h.ChannelNames[0] != "Ops Webhook" {
		t.Fatalf("expected channel_names=[Ops Webhook], got %+v", det1h.ChannelNames)
	}
	if len(det1h.StatusPageNames) != 1 || det1h.StatusPageNames[0] != "Exa Ops" {
		t.Fatalf("expected status_page_names=[Exa Ops], got %+v", det1h.StatusPageNames)
	}
	if det1h.StatusSince == nil {
		t.Fatalf("expected non-nil status_since")
	}

	rec = doReq(t, h, http.MethodGet, "/api/v1/monitors/"+monUp.ID+"?range=7d", nil, cookie)
	var det7d model.MonitorDetailResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &det7d)
	if det7d.Range != "7d" || len(det7d.Heartbeats) != 3 {
		t.Fatalf("expected range=7d and 3 heartbeats, got range=%s len=%d", det7d.Range, len(det7d.Heartbeats))
	}

	rec = doReq(t, h, http.MethodGet, "/api/v1/monitors/"+monDown.ID+"?range=30d", nil, cookie)
	var detDown model.MonitorDetailResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &detDown)
	if detDown.Range != "30d" || len(detDown.Incidents) != 1 || detDown.Incidents[0].ID != inc.ID {
		t.Fatalf("expected linked incident on down monitor, got %+v", detDown.Incidents)
	}
}

func TestSecurityHardening(t *testing.T) {
	store, err := db.OpenPath(filepath.Join(t.TempDir(), "sec.db"))
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	defer func() { _ = store.Close() }()

	authSvc := auth.NewService(store)
	sse := NewSSEHub()
	mc := &mockMonitorController{store: store}
	notifier := &mockNotifier{doneCh: make(chan struct{}, 4)}

	rt := &router{
		cfg: RouterConfig{
			Store:     store,
			Auth:      authSvc,
			Monitors:  mc,
			Notifier:  notifier,
			SSE:       sse,
			Version:   "0.1.0-sec",
			StartedAt: time.Now().UTC(),
		},
		loginIPLimiter:   newRateLimiter(10, 5*time.Minute),
		loginUserLimiter: newRateLimiter(10, 5*time.Minute),
		setupLimiter:     newRateLimiter(5, 10*time.Minute),
		subscribeLimiter: newRateLimiter(100, 10*time.Minute),
		pushLimiter:      newRateLimiter(120, 1*time.Minute),
		lookupIP: func(ctx context.Context, network, host string) ([]net.IP, error) {
			if host == "hooks.example.com" {
				return []net.IP{net.ParseIP("93.184.216.34")}, nil
			}
			return []net.IP{net.ParseIP("127.0.0.1")}, nil
		},
	}

	// Build full handler via NewRouter for middleware/headers test, and use rt.handleSubscribe for DNS-mocked test.
	h := NewRouter(rt.cfg)

	// 1. Security headers + CSP.
	rec := doReq(t, h, http.MethodGet, "/api/v1/health", nil)
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" ||
		rec.Header().Get("X-Frame-Options") != "DENY" ||
		!strings.Contains(rec.Header().Get("Content-Security-Policy"), "default-src 'self'") {
		t.Fatalf("missing security headers: %+v", rec.Header())
	}

	// 2. Setup enforces 10-char minimum password.
	rec = doReq(t, h, http.MethodPost, "/api/v1/setup", map[string]string{
		"username": "admin",
		"password": "short",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for short setup password, got %d", rec.Code)
	}

	rec = doReq(t, h, http.MethodPost, "/api/v1/setup", map[string]string{
		"username":   "admin",
		"email":      "admin@exa.local",
		"password":   "strong-pass-123",
		"site_title": "Sec Status",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup: %d %s", rec.Code, rec.Body.String())
	}
	adminCookie := rec.Result().Cookies()[0]

	// 3. Create viewer user + push monitor (not attached to any page).
	rec = doReq(t, h, http.MethodPost, "/api/v1/users", map[string]string{
		"username": "viewer1",
		"password": "viewer-pass-123",
		"role":     "viewer",
	}, adminCookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create viewer: %d %s", rec.Code, rec.Body.String())
	}

	rec = doReq(t, h, http.MethodPost, "/api/v1/monitors", map[string]any{
		"name":                "Secret Push",
		"slug":                "secret-push",
		"type":                "push",
		"attach_default_page": false,
	}, adminCookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create push monitor: %d %s", rec.Code, rec.Body.String())
	}
	var pushMon model.Monitor
	_ = json.Unmarshal(rec.Body.Bytes(), &pushMon)
	if pushMon.PushToken == "" {
		t.Fatalf("expected admin create response to include push_token")
	}

	// Private monitor badge returns 404 unauthenticated, 200 authenticated.
	rec = doReq(t, h, http.MethodGet, "/api/v1/badge/secret-push/status", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unattached badge, got %d", rec.Code)
	}
	rec = doReq(t, h, http.MethodGet, "/api/v1/badge/secret-push/status", nil, adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for authenticated badge, got %d", rec.Code)
	}

	// Login as viewer and verify RBAC + push_token stripping.
	rec = doReq(t, h, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"username": "viewer1",
		"password": "viewer-pass-123",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("viewer login: %d %s", rec.Code, rec.Body.String())
	}
	viewerCookie := rec.Result().Cookies()[0]

	if rec := doReq(t, h, http.MethodGet, "/api/v1/notifications", nil, viewerCookie); rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for viewer on notifications, got %d", rec.Code)
	}
	if rec := doReq(t, h, http.MethodGet, "/api/v1/subscribers", nil, viewerCookie); rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for viewer on subscribers, got %d", rec.Code)
	}

	rec = doReq(t, h, http.MethodGet, "/api/v1/monitors", nil, viewerCookie)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), pushMon.PushToken) {
		t.Fatalf("viewer leaked push_token on list monitors: %s", rec.Body.String())
	}
	rec = doReq(t, h, http.MethodGet, "/api/v1/monitors/"+pushMon.ID, nil, viewerCookie)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), pushMon.PushToken) {
		t.Fatalf("viewer leaked push_token on monitor detail: %s", rec.Body.String())
	}
	rec = doReq(t, h, http.MethodGet, "/api/v1/overview", nil, viewerCookie)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), pushMon.PushToken) {
		t.Fatalf("viewer leaked push_token on overview: %s", rec.Body.String())
	}

	// 4. Notification secret redaction & preservation on update.
	rec = doReq(t, h, http.MethodPost, "/api/v1/notifications", map[string]any{
		"name": "TG",
		"type": "telegram",
		"config": map[string]string{
			"bot_token": "super-secret-bot-token",
			"chat_id":   "-100123",
		},
	}, adminCookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create notification: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "super-secret-bot-token") || !strings.Contains(rec.Body.String(), "********") {
		t.Fatalf("expected masked bot_token, got %s", rec.Body.String())
	}
	var createdCh model.NotificationChannel
	_ = json.Unmarshal(rec.Body.Bytes(), &createdCh)

	rec = doReq(t, h, http.MethodPut, "/api/v1/notifications/"+createdCh.ID, map[string]any{
		"name": "TG Renamed",
		"type": "telegram",
		"config": map[string]string{
			"bot_token": "********",
			"chat_id":   "-100999",
		},
	}, adminCookie)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "super-secret-bot-token") {
		t.Fatalf("update notification: %d %s", rec.Code, rec.Body.String())
	}
	storedCh, _ := store.GetNotificationChannel(createdCh.ID)
	if !strings.Contains(string(storedCh.Config), "super-secret-bot-token") || !strings.Contains(string(storedCh.Config), "-100999") {
		t.Fatalf("expected stored secret preserved and chat_id updated, got %s", string(storedCh.Config))
	}

	// 5. Public status subscriptions_enabled + SSRF validation.
	rec = doReq(t, h, http.MethodGet, "/api/v1/status", nil)
	if !strings.Contains(rec.Body.String(), `"subscriptions_enabled":false`) {
		t.Fatalf("expected subscriptions_enabled:false, got %s", rec.Body.String())
	}

	_ = store.SetSetting("allow_subscriptions", "true")
	rec = doReq(t, h, http.MethodGet, "/api/v1/status", nil)
	if !strings.Contains(rec.Body.String(), `"subscriptions_enabled":true`) {
		t.Fatalf("expected subscriptions_enabled:true, got %s", rec.Body.String())
	}

	badWebhooks := []string{
		"http://hooks.example.com/cb",
		"https://127.0.0.1/hook",
		"https://169.254.169.254/latest/meta-data",
		"https://10.0.0.1/hook",
		"https://[::1]/hook",
		"https://[fc00::1]/hook",
		"https://localhost/hook",
	}
	for _, u := range badWebhooks {
		b, _ := json.Marshal(map[string]string{"type": "webhook", "endpoint": u})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/status/default/subscribe", bytes.NewReader(b))
		req.SetPathValue("slug", "default")
		subRec := httptest.NewRecorder()
		rt.handleSubscribe(subRec, req)
		if subRec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for SSRF webhook %q, got %d %s", u, subRec.Code, subRec.Body.String())
		}
	}

	// Valid public HTTPS webhook passes.
	bGood, _ := json.Marshal(map[string]string{"type": "webhook", "endpoint": "https://hooks.example.com/cb"})
	reqGood := httptest.NewRequest(http.MethodPost, "/api/v1/status/default/subscribe", bytes.NewReader(bGood))
	reqGood.SetPathValue("slug", "default")
	recGood := httptest.NewRecorder()
	rt.handleSubscribe(recGood, reqGood)
	if recGood.Code != http.StatusCreated {
		t.Fatalf("expected 201 for public HTTPS webhook, got %d %s", recGood.Code, recGood.Body.String())
	}

	// 6. Password change invalidates old session and issues a new cookie.
	rec = doReq(t, h, http.MethodPut, "/api/v1/auth/password", map[string]string{
		"current_password": "strong-pass-123",
		"new_password":     "newer-strong-pass-456",
	}, adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("change password: %d %s", rec.Code, rec.Body.String())
	}
	newAdminCookie := rec.Result().Cookies()[0]

	if rec := doReq(t, h, http.MethodGet, "/api/v1/auth/me", nil, adminCookie); rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected old session cookie invalidated after password change, got %d", rec.Code)
	}
	if rec := doReq(t, h, http.MethodGet, "/api/v1/auth/me", nil, newAdminCookie); rec.Code != http.StatusOK {
		t.Fatalf("expected new session cookie valid after password change, got %d", rec.Code)
	}

	// 7. Login rate limit returns 429 on 11th attempt.
	for i := 0; i < 10; i++ {
		_ = doReq(t, h, http.MethodPost, "/api/v1/auth/login", map[string]string{
			"username": "admin",
			"password": "wrong-password-123",
		})
	}
	rec = doReq(t, h, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"username": "admin",
		"password": "wrong-password-123",
	})
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on 11th login attempt, got %d", rec.Code)
	}
}

func TestBackupGzipAndS3Endpoints(t *testing.T) {
	h, store, _, _, _ := setupTestAPI(t)

	// Setup admin + viewer.
	rec := doReq(t, h, http.MethodPost, "/api/v1/setup", map[string]string{
		"username":   "admin",
		"email":      "admin@exa.local",
		"password":   "password123",
		"site_title": "Exa S3",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup: %d %s", rec.Code, rec.Body.String())
	}
	adminCookie := rec.Result().Cookies()[0]

	rec = doReq(t, h, http.MethodPost, "/api/v1/users", map[string]string{
		"username": "viewer",
		"password": "viewer-pass-123",
		"role":     "viewer",
	}, adminCookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create viewer: %d %s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"username": "viewer",
		"password": "viewer-pass-123",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("login viewer: %d %s", rec.Code, rec.Body.String())
	}
	viewerCookie := rec.Result().Cookies()[0]

	// Create monitor + heartbeats + rollups.
	rec = doReq(t, h, http.MethodPost, "/api/v1/monitors", map[string]any{
		"name":         "Edge API",
		"slug":         "edge-api",
		"type":         "http",
		"target":       "https://edge.example.com/health",
		"interval_sec": 60,
	}, adminCookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create monitor: %d %s", rec.Code, rec.Body.String())
	}
	var mon model.Monitor
	_ = json.Unmarshal(rec.Body.Bytes(), &mon)

	ctx := context.Background()
	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		if err := store.RecordHeartbeat(ctx, mon.ID, model.ProbeResult{
			Status:     model.HeartbeatStatusUp,
			LatencyMs:  20 + i*5,
			StatusCode: 200,
			Message:    "ok",
			CheckedAt:  now.Add(-time.Duration(3-i) * time.Minute),
		}, model.ComponentStatusOperational, 60); err != nil {
			t.Fatalf("RecordHeartbeat %d: %v", i, err)
		}
	}

	// 1. Gzip export + import preserving history and admin session.
	rec = doReq(t, h, http.MethodGet, "/api/v1/backup/export?format=gzip", nil, adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("gzip export: %d %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/gzip" {
		t.Fatalf("expected application/gzip, got %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, ".json.gz") {
		t.Fatalf("expected .json.gz attachment header, got %q", cd)
	}

	gzBytes := append([]byte(nil), rec.Body.Bytes()...)
	gzReader, err := gzip.NewReader(bytes.NewReader(gzBytes))
	if err != nil {
		t.Fatalf("open gzip export: %v", err)
	}
	var exported model.BackupData
	if err := json.NewDecoder(gzReader).Decode(&exported); err != nil {
		t.Fatalf("decode gzip export: %v", err)
	}
	_ = gzReader.Close()
	if len(exported.Heartbeats) != 3 || len(exported.DailyRollups) != 1 {
		t.Fatalf("expected 3 heartbeats and 1 rollup in gzip backup, got hb=%d ru=%d", len(exported.Heartbeats), len(exported.DailyRollups))
	}

	// Wipe monitor + history, then restore from .json.gz payload.
	if err := store.DeleteMonitor(mon.ID); err != nil {
		t.Fatalf("DeleteMonitor: %v", err)
	}
	reqImport := httptest.NewRequest(http.MethodPost, "/api/v1/backup/import", bytes.NewReader(gzBytes))
	reqImport.Header.Set("Content-Type", "application/gzip")
	reqImport.AddCookie(adminCookie)
	recImport := httptest.NewRecorder()
	h.ServeHTTP(recImport, reqImport)
	if recImport.Code != http.StatusOK {
		t.Fatalf("gzip import: %d %s", recImport.Code, recImport.Body.String())
	}
	if !strings.Contains(recImport.Body.String(), `"heartbeats":3`) || !strings.Contains(recImport.Body.String(), `"daily_rollups":1`) {
		t.Fatalf("unexpected import counts: %s", recImport.Body.String())
	}

	// Admin session still valid and history restored.
	if recMe := doReq(t, h, http.MethodGet, "/api/v1/auth/me", nil, adminCookie); recMe.Code != http.StatusOK {
		t.Fatalf("admin session lost after import: %d", recMe.Code)
	}
	restoredHBs, _ := store.ListHeartbeats(mon.ID, 10)
	if len(restoredHBs) != 3 {
		t.Fatalf("expected 3 restored heartbeats, got %d", len(restoredHBs))
	}

	// 2. Settings validation + s3_secret_access_key masking and preservation.
	rec = doReq(t, h, http.MethodPut, "/api/v1/settings", map[string]string{
		"s3_schedule": "every-minute",
	}, adminCookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on invalid s3_schedule, got %d", rec.Code)
	}
	rec = doReq(t, h, http.MethodPut, "/api/v1/settings", map[string]string{
		"s3_retention_count": "-5",
	}, adminCookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on negative s3_retention_count, got %d", rec.Code)
	}

	rec = doReq(t, h, http.MethodPut, "/api/v1/settings", map[string]string{
		"s3_secret_access_key": "real-s3-secret-xyz",
		"s3_schedule":          "daily",
		"s3_retention_count":   "5",
		"s3_last_error":        "should-be-ignored",
	}, adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("save s3 settings: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "real-s3-secret-xyz") || !strings.Contains(rec.Body.String(), `"s3_secret_access_key":"********"`) {
		t.Fatalf("expected masked s3 secret in PUT settings response, got %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "should-be-ignored") {
		t.Fatalf("client should not overwrite s3_last_error: %s", rec.Body.String())
	}

	rec = doReq(t, h, http.MethodPut, "/api/v1/settings", map[string]string{
		"s3_secret_access_key": "********",
		"s3_retention_count":   "7",
	}, adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("preserve s3 secret: %d %s", rec.Code, rec.Body.String())
	}
	storedSecret, _ := store.GetSetting("s3_secret_access_key")
	if storedSecret != "real-s3-secret-xyz" {
		t.Fatalf("expected stored secret preserved, got %q", storedSecret)
	}
	rec = doReq(t, h, http.MethodGet, "/api/v1/settings", nil, adminCookie)
	if strings.Contains(rec.Body.String(), "real-s3-secret-xyz") || !strings.Contains(rec.Body.String(), `"s3_secret_access_key":"********"`) {
		t.Fatalf("expected masked s3 secret in GET settings, got %s", rec.Body.String())
	}

	// Missing S3 endpoint/bucket returns 400 Bad Request on S3 run/restore.
	if rec := doReq(t, h, http.MethodPost, "/api/v1/backup/s3/run", nil, adminCookie); rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on unconfigured s3 run, got %d", rec.Code)
	}
	if rec := doReq(t, h, http.MethodPost, "/api/v1/backup/s3/restore", map[string]string{"key": ""}, adminCookie); rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on empty restore key, got %d", rec.Code)
	}

	// 3. RBAC 403 for viewer on all 5 S3 endpoints.
	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/backup/s3/test"},
		{http.MethodPost, "/api/v1/backup/s3/run"},
		{http.MethodGet, "/api/v1/backup/s3/list"},
		{http.MethodPost, "/api/v1/backup/s3/restore"},
		{http.MethodDelete, "/api/v1/backup/s3/object?key=prod/a.json.gz"},
	} {
		vRec := doReq(t, h, tc.method, tc.path, nil, viewerCookie)
		if vRec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for viewer on %s %s, got %d", tc.method, tc.path, vRec.Code)
		}
	}
}

func TestS3EndpointsWithMockServer(t *testing.T) {
	h, store, _, _, _ := setupTestAPI(t)

	rec := doReq(t, h, http.MethodPost, "/api/v1/setup", map[string]string{
		"username":   "admin",
		"email":      "admin@exa.local",
		"password":   "password123",
		"site_title": "Exa S3",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup: %d %s", rec.Code, rec.Body.String())
	}
	adminCookie := rec.Result().Cookies()[0]

	rec = doReq(t, h, http.MethodPost, "/api/v1/monitors", map[string]any{
		"name":         "Edge API",
		"slug":         "edge-api",
		"type":         "http",
		"target":       "https://edge.example.com/health",
		"interval_sec": 60,
	}, adminCookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create monitor: %d %s", rec.Code, rec.Body.String())
	}
	var mon model.Monitor
	_ = json.Unmarshal(rec.Body.Bytes(), &mon)

	ctx := context.Background()
	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		if err := store.RecordHeartbeat(ctx, mon.ID, model.ProbeResult{
			Status:     model.HeartbeatStatusUp,
			LatencyMs:  20 + i*5,
			StatusCode: 200,
			Message:    "ok",
			CheckedAt:  now.Add(-time.Duration(3-i) * time.Minute),
		}, model.ComponentStatusOperational, 60); err != nil {
			t.Fatalf("RecordHeartbeat %d: %v", i, err)
		}
	}

	type mockObj struct {
		data []byte
		mod  time.Time
	}
	var s3Mu sync.Mutex
	s3Objs := make(map[string]mockObj)
	s3Srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		trimmed := strings.TrimPrefix(r.URL.Path, "/exa-bucket")
		key := strings.TrimLeft(trimmed, "/")

		s3Mu.Lock()
		defer s3Mu.Unlock()

		if r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2" {
			prefix := r.URL.Query().Get("prefix")
			type itemXML struct {
				Key          string `xml:"Key"`
				LastModified string `xml:"LastModified"`
				Size         int64  `xml:"Size"`
			}
			type listXML struct {
				XMLName     xml.Name  `xml:"ListBucketResult"`
				IsTruncated bool      `xml:"IsTruncated"`
				Contents    []itemXML `xml:"Contents"`
			}
			var keys []string
			for k := range s3Objs {
				if prefix == "" || strings.HasPrefix(k, prefix) {
					keys = append(keys, k)
				}
			}
			sort.Strings(keys)
			out := listXML{}
			for _, k := range keys {
				obj := s3Objs[k]
				out.Contents = append(out.Contents, itemXML{
					Key:          k,
					LastModified: obj.mod.UTC().Format(time.RFC3339),
					Size:         int64(len(obj.data)),
				})
			}
			w.Header().Set("Content-Type", "application/xml")
			_ = xml.NewEncoder(w).Encode(out)
			return
		}

		switch r.Method {
		case http.MethodPut:
			b, _ := io.ReadAll(r.Body)
			s3Objs[key] = mockObj{data: b, mod: time.Now().UTC()}
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			obj, ok := s3Objs[key]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(obj.data)
		case http.MethodDelete:
			delete(s3Objs, key)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer s3Srv.Close()

	rec = doReq(t, h, http.MethodPut, "/api/v1/settings", map[string]string{
		"s3_enabled":           "true",
		"s3_endpoint":          s3Srv.URL,
		"s3_region":            "us-east-1",
		"s3_bucket":            "exa-bucket",
		"s3_prefix":            "prod/",
		"s3_access_key_id":     "AKIAEXATEST",
		"s3_secret_access_key": "real-s3-secret-xyz",
		"s3_force_path_style":  "true",
		"s3_schedule":          "daily",
		"s3_retention_count":   "5",
	}, adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("save s3 settings: %d %s", rec.Code, rec.Body.String())
	}

	// 4. Test all 5 S3 endpoints as admin.
	rec = doReq(t, h, http.MethodPost, "/api/v1/backup/s3/test", map[string]any{
		"s3_secret_access_key": "********",
	}, adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("s3 test: %d %s", rec.Code, rec.Body.String())
	}

	rec = doReq(t, h, http.MethodPost, "/api/v1/backup/s3/run", nil, adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("s3 run: %d %s", rec.Code, rec.Body.String())
	}
	var runRes model.S3BackupResult
	if err := json.Unmarshal(rec.Body.Bytes(), &runRes); err != nil {
		t.Fatalf("unmarshal s3 run: %v", err)
	}
	if !strings.HasPrefix(runRes.Key, "prod/") || runRes.Heartbeats != 3 || runRes.DailyRollups != 1 {
		t.Fatalf("unexpected s3 run result: %+v", runRes)
	}

	rec = doReq(t, h, http.MethodGet, "/api/v1/backup/s3/list", nil, adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("s3 list: %d %s", rec.Code, rec.Body.String())
	}
	var objs []model.S3BackupObject
	_ = json.Unmarshal(rec.Body.Bytes(), &objs)
	if len(objs) != 1 || objs[0].Key != runRes.Key {
		t.Fatalf("unexpected s3 list: %+v", objs)
	}

	// Delete monitor locally, then restore from S3.
	_ = store.DeleteMonitor(mon.ID)
	rec = doReq(t, h, http.MethodPost, "/api/v1/backup/s3/restore", map[string]string{
		"key": runRes.Key,
	}, adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("s3 restore: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"heartbeats":3`) {
		t.Fatalf("expected 3 restored heartbeats from S3, got %s", rec.Body.String())
	}
	if _, err := store.GetMonitorByID(mon.ID); err != nil {
		t.Fatalf("expected monitor restored from S3: %v", err)
	}

	// Delete S3 object.
	rec = doReq(t, h, http.MethodDelete, "/api/v1/backup/s3/object?key="+runRes.Key, nil, adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("s3 delete: %d %s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodGet, "/api/v1/backup/s3/list", nil, adminCookie)
	_ = json.Unmarshal(rec.Body.Bytes(), &objs)
	if len(objs) != 0 {
		t.Fatalf("expected empty s3 list after delete, got %+v", objs)
	}
}
