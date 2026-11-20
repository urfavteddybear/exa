package db

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"exa/internal/model"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := OpenPath(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestOpenPragmasAndTables(t *testing.T) {
	s := openTestStore(t)

	var journalMode string
	if err := s.DB().QueryRow(`PRAGMA journal_mode;`).Scan(&journalMode); err != nil {
		t.Fatalf("pragma journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Fatalf("expected wal journal_mode, got %q", journalMode)
	}

	var fk int
	if err := s.DB().QueryRow(`PRAGMA foreign_keys;`).Scan(&fk); err != nil {
		t.Fatalf("pragma foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Fatalf("expected foreign_keys=1, got %d", fk)
	}

	var tableCount int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`).Scan(&tableCount); err != nil {
		t.Fatalf("count tables: %v", err)
	}
	if tableCount != 18 {
		t.Fatalf("expected 18 tables, got %d", tableCount)
	}
}

func TestSetupAuthAndAPIKeys(t *testing.T) {
	s := openTestStore(t)

	done, err := s.IsSetupCompleted()
	if err != nil {
		t.Fatalf("IsSetupCompleted: %v", err)
	}
	if done {
		t.Fatalf("expected setup not completed initially")
	}

	user, page, err := s.SetupInitialAdmin("admin", "admin@example.com", "hashedpw", "Exa Ops")
	if err != nil {
		t.Fatalf("SetupInitialAdmin: %v", err)
	}
	if user.Role != model.UserRoleAdmin || page.Slug != "default" || !page.IsDefault {
		t.Fatalf("unexpected setup result: user=%+v page=%+v", user, page)
	}

	if _, _, err := s.SetupInitialAdmin("admin2", "", "pw", "x"); err != ErrSetupCompleted {
		t.Fatalf("expected ErrSetupCompleted, got %v", err)
	}

	done, err = s.IsSetupCompleted()
	if err != nil || !done {
		t.Fatalf("expected setup completed, got done=%v err=%v", done, err)
	}

	sess, err := s.CreateSession("tok123", user.ID, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	sessUser, err := s.GetSessionUser(sess.ID)
	if err != nil || sessUser.ID != user.ID {
		t.Fatalf("GetSessionUser: user=%+v err=%v", sessUser, err)
	}
	if err := s.DeleteSession(sess.ID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := s.GetSessionUser(sess.ID); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}

	key, err := s.CreateAPIKey(model.APIKey{
		Name:      "ci",
		KeyPrefix: "exa_1234",
		KeyHash:   "sha256hash",
		Role:      model.UserRoleAdmin,
	})
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	gotKey, err := s.GetAPIKeyByHash("sha256hash")
	if err != nil || gotKey.ID != key.ID {
		t.Fatalf("GetAPIKeyByHash: got=%+v err=%v", gotKey, err)
	}
	if err := s.TouchAPIKey(key.ID); err != nil {
		t.Fatalf("TouchAPIKey: %v", err)
	}
}

func TestMonitorsHeartbeatsAndRollups(t *testing.T) {
	s := openTestStore(t)
	_, _, err := s.SetupInitialAdmin("admin", "", "hash", "Exa")
	if err != nil {
		t.Fatalf("SetupInitialAdmin: %v", err)
	}

	grp, err := s.CreateGroup(model.ComponentGroup{Name: "Core API", SortOrder: 1})
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}

	m1, err := s.CreateMonitor(model.Monitor{
		Name:        "API Gateway",
		Type:        model.MonitorTypeHTTP,
		Target:      "https://api.internal/health",
		GroupID:     &grp.ID,
		IntervalSec: 30,
		TimeoutSec:  5,
		Retries:     1,
		Active:      true,
		Config:      json.RawMessage(`{"method":"GET"}`),
	})
	if err != nil {
		t.Fatalf("CreateMonitor http: %v", err)
	}

	m2, err := s.CreateMonitor(model.Monitor{
		Name:   "Nightly Job",
		Type:   model.MonitorTypePush,
		Active: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor push: %v", err)
	}
	if m2.PushToken == "" {
		t.Fatalf("expected push token generated")
	}

	if err := s.AttachMonitorToDefaultPage(m1.ID, m1.GroupID, 0); err != nil {
		t.Fatalf("AttachMonitorToDefaultPage: %v", err)
	}

	ctx := context.Background()
	t1 := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	exp := t1.Add(30 * 24 * time.Hour)
	daysLeft := 30
	if err := s.RecordHeartbeat(ctx, m1.ID, model.ProbeResult{
		Status:       model.HeartbeatStatusUp,
		LatencyMs:    40,
		StatusCode:   200,
		Message:      "ok",
		TLSExpiresAt: &exp,
		TLSDaysLeft:  &daysLeft,
		CheckedAt:    t1,
	}, model.ComponentStatusOperational, 30); err != nil {
		t.Fatalf("RecordHeartbeat 1: %v", err)
	}

	t2 := t1.Add(30 * time.Second)
	if err := s.RecordHeartbeat(ctx, m1.ID, model.ProbeResult{
		Status:     model.HeartbeatStatusDown,
		LatencyMs:  120,
		StatusCode: 503,
		Message:    "503 unavailable",
		CheckedAt:  t2,
	}, model.ComponentStatusMajorOutage, 30); err != nil {
		t.Fatalf("RecordHeartbeat 2: %v", err)
	}

	updated, err := s.GetMonitorByID(m1.ID)
	if err != nil {
		t.Fatalf("GetMonitorByID: %v", err)
	}
	if updated.CurrentStatus != model.ComponentStatusMajorOutage || updated.LastError != "503 unavailable" {
		t.Fatalf("unexpected monitor state: %+v", updated)
	}
	if updated.TLSDaysLeft == nil || *updated.TLSDaysLeft != 30 {
		t.Fatalf("expected preserved tls_days_left=30, got %v", updated.TLSDaysLeft)
	}

	hbs, err := s.ListHeartbeats(m1.ID, 10)
	if err != nil || len(hbs) != 2 {
		t.Fatalf("ListHeartbeats: len=%d err=%v", len(hbs), err)
	}
	if hbs[0].Status != model.HeartbeatStatusDown {
		t.Fatalf("expected newest heartbeat first, got %+v", hbs[0])
	}

	rollups, err := s.GetDailyRollups(m1.ID, "2026-11-20", "2026-11-20")
	if err != nil || len(rollups) != 1 {
		t.Fatalf("GetDailyRollups: len=%d err=%v", len(rollups), err)
	}
	r := rollups[0]
	if r.TotalChecks != 2 || r.UpChecks != 1 || r.DownChecks != 1 || r.MinLatencyMs != 40 || r.MaxLatencyMs != 120 || r.DowntimeSec != 30 {
		t.Fatalf("unexpected rollup: %+v", r)
	}

	pruned, err := s.PruneHeartbeats(t1.Add(15 * time.Second))
	if err != nil || pruned != 1 {
		t.Fatalf("PruneHeartbeats: pruned=%d err=%v", pruned, err)
	}
}

func TestIncidentsMaintenancesChannelsAndBackup(t *testing.T) {
	s := openTestStore(t)
	_, page, err := s.SetupInitialAdmin("admin", "", "hash", "Exa")
	if err != nil {
		t.Fatalf("SetupInitialAdmin: %v", err)
	}

	mon, err := s.CreateMonitor(model.Monitor{
		Name:   "DB",
		Type:   model.MonitorTypeTCP,
		Target: "localhost:5432",
		Active: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	inc, err := s.CreateIncident(model.Incident{
		StatusPageID: page.ID,
		Title:        "Elevated DB latency",
		Impact:       model.IncidentImpactMinor,
		Status:       model.IncidentStatusInvestigating,
		MonitorIDs:   []string{mon.ID},
	}, "Investigating slow queries.")
	if err != nil {
		t.Fatalf("CreateIncident: %v", err)
	}
	if len(inc.Updates) != 1 || len(inc.MonitorIDs) != 1 {
		t.Fatalf("unexpected incident details: %+v", inc)
	}

	inc, upd, err := s.AddIncidentUpdate(inc.ID, model.IncidentStatusResolved, "Query plan fixed.")
	if err != nil || upd == nil || inc.ResolvedAt == nil {
		t.Fatalf("AddIncidentUpdate: inc=%+v err=%v", inc, err)
	}

	now := time.Now().UTC()
	maint, err := s.CreateMaintenance(model.Maintenance{
		StatusPageID:       page.ID,
		Title:              "Kernel patch",
		ScheduledStart:     now.Add(-5 * time.Minute),
		ScheduledEnd:       now.Add(25 * time.Minute),
		AutoSuppressAlerts: true,
		MonitorIDs:         []string{mon.ID},
	})
	if err != nil {
		t.Fatalf("CreateMaintenance: %v", err)
	}
	if maint.Status != model.MaintenanceStatusInProgress {
		t.Fatalf("expected in_progress, got %s", maint.Status)
	}

	inMaint, suppress, err := s.IsMonitorInActiveMaintenance(mon.ID, now)
	if err != nil || !inMaint || !suppress {
		t.Fatalf("IsMonitorInActiveMaintenance: inMaint=%v suppress=%v err=%v", inMaint, suppress, err)
	}

	defCh, err := s.CreateNotificationChannel(model.NotificationChannel{
		Name:      "Default Webhook",
		Type:      model.NotificationTypeWebhook,
		Config:    json.RawMessage(`{"url":"https://hooks.internal/a"}`),
		IsDefault: true,
		Enabled:   true,
	})
	if err != nil {
		t.Fatalf("CreateNotificationChannel: %v", err)
	}

	chs, err := s.ListChannelsForMonitor(mon.ID)
	if err != nil || len(chs) != 1 || chs[0].ID != defCh.ID {
		t.Fatalf("expected default channel fallback, got %+v err=%v", chs, err)
	}

	customCh, err := s.CreateNotificationChannel(model.NotificationChannel{
		Name:      "Custom",
		Type:      model.NotificationTypeNtfy,
		Config:    json.RawMessage(`{"topic":"ops"}`),
		IsDefault: false,
		Enabled:   true,
	})
	if err != nil {
		t.Fatalf("CreateNotificationChannel custom: %v", err)
	}
	if err := s.SetMonitorNotificationChannels(mon.ID, []string{customCh.ID}); err != nil {
		t.Fatalf("SetMonitorNotificationChannels: %v", err)
	}
	chs, err = s.ListChannelsForMonitor(mon.ID)
	if err != nil || len(chs) != 1 || chs[0].ID != customCh.ID {
		t.Fatalf("expected linked channel, got %+v err=%v", chs, err)
	}

	sub, err := s.CreateSubscriber(model.Subscriber{
		StatusPageID: page.ID,
		Type:         model.SubscriberTypeEmail,
		Endpoint:     "ops@example.com",
		Verified:     true,
		Active:       true,
	})
	if err != nil {
		t.Fatalf("CreateSubscriber: %v", err)
	}
	if err := s.DeactivateSubscriberByToken(sub.Token); err != nil {
		t.Fatalf("DeactivateSubscriberByToken: %v", err)
	}

	backup, err := s.ExportBackup()
	if err != nil {
		t.Fatalf("ExportBackup: %v", err)
	}
	if err := s.ImportBackup(*backup); err != nil {
		t.Fatalf("ImportBackup: %v", err)
	}
	monitorsAfter, err := s.ListMonitors()
	if err != nil || len(monitorsAfter) != 1 {
		t.Fatalf("expected 1 monitor after import, got %d err=%v", len(monitorsAfter), err)
	}

	// Verify allow_subscriptions defaults to false.
	allowSubs, err := s.GetSetting("allow_subscriptions")
	if err != nil || allowSubs != "false" {
		t.Fatalf("expected allow_subscriptions=false, got %q err=%v", allowSubs, err)
	}

	// Verify ImportBackup validates monitor/channel types, clamps bounds, and preserves setup_completed.
	badMonBackup := *backup
	badMonBackup.Monitors = []model.Monitor{{
		ID: "bad1", Name: "Bad", Slug: "bad", Type: model.MonitorType("exec"),
	}}
	if err := s.ImportBackup(badMonBackup); err == nil {
		t.Fatalf("expected ImportBackup to reject invalid monitor type")
	}

	badChanBackup := *backup
	badChanBackup.NotificationChannels = []model.NotificationChannel{{
		ID: "badc", Name: "BadC", Type: model.NotificationType("sms"),
	}}
	if err := s.ImportBackup(badChanBackup); err == nil {
		t.Fatalf("expected ImportBackup to reject invalid channel type")
	}

	clampBackup := *backup
	clampBackup.Settings = map[string]string{"setup_completed": "false"}
	clampBackup.Monitors[0].IntervalSec = 1
	clampBackup.Monitors[0].TimeoutSec = 0
	clampBackup.Monitors[0].Retries = -3
	if err := s.ImportBackup(clampBackup); err != nil {
		t.Fatalf("ImportBackup clamp: %v", err)
	}
	clamped, _ := s.GetMonitorByID(clampBackup.Monitors[0].ID)
	if clamped.IntervalSec < 10 || clamped.TimeoutSec < 1 || clamped.Retries < 0 {
		t.Fatalf("expected clamped monitor intervals, got %+v", clamped)
	}
	scVal, _ := s.GetSetting("setup_completed")
	if scVal != "true" {
		t.Fatalf("expected setup_completed preserved as true when users exist, got %q", scVal)
	}

	// Verify public status page monitor check and DeleteUserSessions.
	onPub, err := s.IsMonitorOnPublicStatusPage(mon.ID)
	if err != nil || onPub {
		t.Fatalf("expected unattached monitor not on public page, got %v err=%v", onPub, err)
	}
	_ = s.AttachMonitorToDefaultPage(mon.ID, nil, 0)
	onPub, err = s.IsMonitorOnPublicStatusPage(mon.ID)
	if err != nil || !onPub {
		t.Fatalf("expected attached monitor on public page, got %v err=%v", onPub, err)
	}

	u, _ := s.GetUserByUsername("admin")
	_, _ = s.CreateSession("s1", u.ID, time.Now().UTC().Add(time.Hour))
	if err := s.DeleteUserSessions(u.ID); err != nil {
		t.Fatalf("DeleteUserSessions: %v", err)
	}
	if _, err := s.GetSessionUser("s1"); err != ErrNotFound {
		t.Fatalf("expected session deleted, got %v", err)
	}
}
