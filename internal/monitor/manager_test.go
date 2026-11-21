package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"exa/internal/db"
	"exa/internal/model"
)

type spyBroadcaster struct {
	mu     sync.Mutex
	events []model.SSEEvent
}

func (b *spyBroadcaster) Broadcast(ev model.SSEEvent) {
	b.mu.Lock()
	b.events = append(b.events, ev)
	b.mu.Unlock()
}

func (b *spyBroadcaster) count(evType string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, ev := range b.events {
		if ev.Type == evType {
			n++
		}
	}
	return n
}

type stateChangeCall struct {
	MonitorID string
	Prev      model.ComponentStatus
	Next      model.ComponentStatus
	ErrMsg    string
}

type spyNotifier struct {
	mu    sync.Mutex
	calls []stateChangeCall
}

func (n *spyNotifier) NotifyMonitorStateChange(_ context.Context, m model.Monitor, prev, next model.ComponentStatus, errMsg string) {
	n.mu.Lock()
	n.calls = append(n.calls, stateChangeCall{
		MonitorID: m.ID,
		Prev:      prev,
		Next:      next,
		ErrMsg:    errMsg,
	})
	n.mu.Unlock()
}

func (n *spyNotifier) NotifyIncidentUpdate(context.Context, model.StatusPage, model.Incident, model.IncidentUpdate) {
}

func (n *spyNotifier) NotifyMaintenanceUpdate(context.Context, model.StatusPage, model.Maintenance) {
}

func (n *spyNotifier) TestChannel(context.Context, model.NotificationChannel) error {
	return nil
}

func (n *spyNotifier) snapshot() []stateChangeCall {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]stateChangeCall, len(n.calls))
	copy(out, n.calls)
	return out
}

func newTestStore(t *testing.T) *db.Store {
	t.Helper()
	store, err := db.OpenPath(":memory:")
	if err != nil {
		t.Fatalf("open memory store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestManager_HTTPProbeLoopAndGracefulStop(t *testing.T) {
	store := newTestStore(t)
	bus := &spyBroadcaster{}
	notif := &spyNotifier{}

	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer ts.Close()

	mon, err := store.CreateMonitor(model.Monitor{
		Name:        "Web API",
		Type:        model.MonitorTypeHTTP,
		Target:      ts.URL,
		IntervalSec: 1,
		TimeoutSec:  3,
		Active:      true,
	})
	if err != nil {
		t.Fatalf("create monitor: %v", err)
	}

	mgr := NewManager(store, notif, bus)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr.Start(ctx)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		hbs, err := store.ListHeartbeats(mon.ID, 10)
		if err == nil && len(hbs) >= 1 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}

	mgr.Stop()

	if hits.Load() < 1 {
		t.Fatalf("expected at least 1 HTTP probe hit, got %d", hits.Load())
	}
	hbs, err := store.ListHeartbeats(mon.ID, 10)
	if err != nil || len(hbs) < 1 {
		t.Fatalf("expected recorded heartbeats, got %v (err=%v)", hbs, err)
	}
	if hbs[0].Status != model.HeartbeatStatusUp {
		t.Fatalf("expected up heartbeat, got %s (%s)", hbs[0].Status, hbs[0].Message)
	}

	today := time.Now().UTC().Format("2006-01-02")
	rollups, err := store.GetDailyRollups(mon.ID, today, today)
	if err != nil || len(rollups) != 1 || rollups[0].UpChecks < 1 {
		t.Fatalf("expected daily rollup with up checks, got %+v (err=%v)", rollups, err)
	}

	if bus.count("heartbeat") < 1 {
		t.Fatal("expected at least 1 SSE heartbeat event")
	}
	// Initial unknown -> operational transition must not fire status_change or alerts.
	if bus.count("status_change") != 0 {
		t.Fatalf("expected 0 status_change events on initial up, got %d", bus.count("status_change"))
	}
	if len(notif.snapshot()) != 0 {
		t.Fatalf("expected 0 notifications on initial up, got %+v", notif.snapshot())
	}
}

func TestManager_AntiFlappingRetries(t *testing.T) {
	store := newTestStore(t)
	bus := &spyBroadcaster{}
	notif := &spyNotifier{}

	mon, err := store.CreateMonitor(model.Monitor{
		Name:        "Flaky Service",
		Type:        model.MonitorTypeHTTP,
		Target:      "http://example.invalid",
		IntervalSec: 1,
		Retries:     1,
		Active:      true,
	})
	if err != nil {
		t.Fatalf("create monitor: %v", err)
	}

	// Sequence: up -> down (suppressed by Retries=1) -> up -> down (suppressed) -> down (confirmed outage).
	seq := []model.HeartbeatStatus{
		model.HeartbeatStatusUp,
		model.HeartbeatStatusDown,
		model.HeartbeatStatusUp,
		model.HeartbeatStatusDown,
		model.HeartbeatStatusDown,
	}

	var idx atomic.Int32
	doneSeq := make(chan struct{})
	var closeOnce sync.Once

	mgr := NewManager(store, notif, bus)
	mgr.probeFn = func(_ context.Context, _ model.Monitor) model.ProbeResult {
		i := int(idx.Add(1)) - 1
		if i >= len(seq) {
			return model.ProbeResult{Status: model.HeartbeatStatusDown, Message: "down", CheckedAt: time.Now().UTC()}
		}
		if i == len(seq)-1 {
			closeOnce.Do(func() { close(doneSeq) })
		}
		return model.ProbeResult{
			Status:    seq[i],
			Message:   string(seq[i]),
			CheckedAt: time.Now().UTC(),
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.Start(ctx)

	select {
	case <-doneSeq:
		// Give the final probe result a moment to commit to SQLite before stopping.
		time.Sleep(100 * time.Millisecond)
	case <-time.After(8 * time.Second):
		mgr.Stop()
		t.Fatal("timed out waiting for probe sequence")
	}
	mgr.Stop()

	updated, err := store.GetMonitorByID(mon.ID)
	if err != nil {
		t.Fatalf("get monitor: %v", err)
	}
	if updated.CurrentStatus != model.ComponentStatusMajorOutage {
		t.Fatalf("expected final status major_outage, got %s", updated.CurrentStatus)
	}

	calls := notif.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected exactly 1 confirmed outage notification, got %+v", calls)
	}
	if calls[0].Prev != model.ComponentStatusOperational || calls[0].Next != model.ComponentStatusMajorOutage {
		t.Fatalf("unexpected transition: %+v", calls[0])
	}
}

func TestManager_ActiveMaintenanceSuppressesAlerts(t *testing.T) {
	store := newTestStore(t)
	bus := &spyBroadcaster{}
	notif := &spyNotifier{}

	page, err := store.CreateStatusPage(model.StatusPage{
		Slug:     "status",
		Title:    "Status",
		IsPublic: true,
	})
	if err != nil {
		t.Fatalf("create page: %v", err)
	}

	mon, err := store.CreateMonitor(model.Monitor{
		Name:        "Primary DB",
		Type:        model.MonitorTypeTCP,
		Target:      "127.0.0.1:5432",
		IntervalSec: 60,
		Active:      true,
	})
	if err != nil {
		t.Fatalf("create monitor: %v", err)
	}

	now := time.Now().UTC()
	if _, err := store.CreateMaintenance(model.Maintenance{
		StatusPageID:       page.ID,
		Title:              "Scheduled Vacuum",
		ScheduledStart:     now.Add(-10 * time.Minute),
		ScheduledEnd:       now.Add(20 * time.Minute),
		AutoSuppressAlerts: true,
		MonitorIDs:         []string{mon.ID},
	}); err != nil {
		t.Fatalf("create maintenance: %v", err)
	}

	mgr := NewManager(store, notif, bus)
	mgr.probeFn = func(_ context.Context, _ model.Monitor) model.ProbeResult {
		return model.ProbeResult{
			Status:    model.HeartbeatStatusDown,
			Message:   "connection refused",
			CheckedAt: time.Now().UTC(),
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.Start(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		m, err := store.GetMonitorByID(mon.ID)
		if err == nil && m.CurrentStatus == model.ComponentStatusMaintenance {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mgr.Stop()

	updated, err := store.GetMonitorByID(mon.ID)
	if err != nil {
		t.Fatalf("get monitor: %v", err)
	}
	if updated.CurrentStatus != model.ComponentStatusMaintenance {
		t.Fatalf("expected maintenance status, got %s", updated.CurrentStatus)
	}
	if bus.count("status_change") != 1 {
		t.Fatalf("expected 1 status_change SSE event into maintenance, got %d", bus.count("status_change"))
	}
	if len(notif.snapshot()) != 0 {
		t.Fatalf("expected 0 notifications during auto_suppress_alerts maintenance, got %+v", notif.snapshot())
	}
}

func TestManager_PushAndWatchdog(t *testing.T) {
	store := newTestStore(t)
	bus := &spyBroadcaster{}
	notif := &spyNotifier{}

	cfgBytes, _ := json.Marshal(model.PushConfig{GraceSec: 5})
	mon, err := store.CreateMonitor(model.Monitor{
		Name:        "Cron Backup",
		Type:        model.MonitorTypePush,
		IntervalSec: 10,
		DegradedMs:  100,
		Active:      true,
		Config:      cfgBytes,
	})
	if err != nil {
		t.Fatalf("create push monitor: %v", err)
	}

	mgr := NewManager(store, notif, bus)
	ctx := context.Background()

	// Invalid token must return ErrNotFound.
	if _, err := mgr.HandlePush(ctx, "bad-token", model.HeartbeatStatusUp, 10, "ok"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for bad token, got %v", err)
	}

	// Valid push sets operational and suppresses initial unknown -> operational alert.
	updated, err := mgr.HandlePush(ctx, mon.PushToken, model.HeartbeatStatusUp, 25, "backup ok")
	if err != nil {
		t.Fatalf("HandlePush failed: %v", err)
	}
	if updated.CurrentStatus != model.ComponentStatusOperational {
		t.Fatalf("expected operational status, got %s", updated.CurrentStatus)
	}
	if len(notif.snapshot()) != 0 {
		t.Fatalf("expected 0 alerts on initial push up, got %+v", notif.snapshot())
	}

	// Watchdog before deadline should do nothing.
	mgr.runHousekeepingOnce(ctx, time.Now().UTC().Add(5*time.Second))
	afterEarly, _ := store.GetMonitorByID(mon.ID)
	if afterEarly.CurrentStatus != model.ComponentStatusOperational {
		t.Fatalf("expected operational before deadline, got %s", afterEarly.CurrentStatus)
	}

	// Watchdog past interval + grace (10 + 5 = 15s) should mark major_outage and notify.
	mgr.runHousekeepingOnce(ctx, time.Now().UTC().Add(30*time.Second))
	afterExpired, _ := store.GetMonitorByID(mon.ID)
	if afterExpired.CurrentStatus != model.ComponentStatusMajorOutage {
		t.Fatalf("expected major_outage after push watchdog timeout, got %s", afterExpired.CurrentStatus)
	}
	calls := notif.snapshot()
	if len(calls) != 1 || calls[0].Next != model.ComponentStatusMajorOutage {
		t.Fatalf("expected 1 major_outage notification from watchdog, got %+v", calls)
	}
}

func TestManager_ReloadAndRemove(t *testing.T) {
	store := newTestStore(t)
	mgr := NewManager(store, nil, nil)
	mgr.probeFn = func(_ context.Context, _ model.Monitor) model.ProbeResult {
		return model.ProbeResult{Status: model.HeartbeatStatusUp, CheckedAt: time.Now().UTC()}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.Start(ctx)
	defer mgr.Stop()

	mon, err := store.CreateMonitor(model.Monitor{
		Name:        "Dynamic Check",
		Type:        model.MonitorTypeHTTP,
		Target:      "http://example.invalid",
		IntervalSec: 60,
		Active:      true,
	})
	if err != nil {
		t.Fatalf("create monitor: %v", err)
	}

	mgr.ReloadMonitor(mon.ID)
	mgr.mu.Lock()
	_, exists := mgr.workers[mon.ID]
	mgr.mu.Unlock()
	if !exists {
		t.Fatal("expected worker to exist after ReloadMonitor")
	}

	res := mgr.TestMonitorNow(ctx, *mon)
	if res.Status != model.HeartbeatStatusUp {
		t.Fatalf("expected TestMonitorNow up, got %s", res.Status)
	}

	mgr.RemoveMonitor(mon.ID)
	mgr.mu.Lock()
	_, existsAfter := mgr.workers[mon.ID]
	mgr.mu.Unlock()
	if existsAfter {
		t.Fatal("expected worker to be removed after RemoveMonitor")
	}
}

func TestManager_FastRetriesAlertQuickly(t *testing.T) {
	store := newTestStore(t)
	bus := &spyBroadcaster{}
	notif := &spyNotifier{}

	mon, err := store.CreateMonitor(model.Monitor{
		Name:        "Fast Retry API",
		Type:        model.MonitorTypeHTTP,
		Target:      "http://example.invalid",
		IntervalSec: 60,
		Retries:     2,
		Active:      true,
	})
	if err != nil {
		t.Fatalf("create monitor: %v", err)
	}

	var calls atomic.Int32
	mgr := NewManager(store, notif, bus)
	mgr.retryInterval = 20 * time.Millisecond
	mgr.probeFn = func(_ context.Context, _ model.Monitor) model.ProbeResult {
		calls.Add(1)
		return model.ProbeResult{
			Status:    model.HeartbeatStatusDown,
			Message:   "connection refused",
			CheckedAt: time.Now().UTC(),
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	start := time.Now()
	mgr.Start(ctx)

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if len(notif.snapshot()) >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	elapsed := time.Since(start)
	mgr.Stop()

	snaps := notif.snapshot()
	if len(snaps) != 1 {
		t.Fatalf("expected 1 outage notification within 500ms, got %d (calls=%d)", len(snaps), calls.Load())
	}
	if snaps[0].MonitorID != mon.ID || snaps[0].Next != model.ComponentStatusMajorOutage {
		t.Fatalf("unexpected notification payload: %+v", snaps[0])
	}
	if elapsed >= 500*time.Millisecond {
		t.Fatalf("expected fast retry outage confirmation < 500ms, took %v", elapsed)
	}
	if calls.Load() < 3 {
		t.Fatalf("expected at least 3 probe calls (initial + 2 retries), got %d", calls.Load())
	}
}
