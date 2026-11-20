package monitor

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"

	"exa/internal/db"
	"exa/internal/model"
	"exa/internal/probe"
)

const (
	defaultHousekeepingInterval = 30 * time.Second
	defaultPruneInterval        = 1 * time.Hour
	defaultRetentionDays        = 30
)

type workerHandle struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// Manager orchestrates active probe workers, push heartbeats, and maintenance housekeeping.
type Manager struct {
	store    *db.Store
	notifier model.Notifier
	bus      model.EventBroadcaster
	probeFn  func(ctx context.Context, m model.Monitor) model.ProbeResult

	housekeepingInterval time.Duration
	pruneInterval        time.Duration

	mu        sync.Mutex
	ctx       context.Context
	cancel    context.CancelFunc
	stopping  bool
	wg        sync.WaitGroup
	workers   map[string]*workerHandle
	lastPrune time.Time
}

// NewManager creates a Manager ready to be started.
func NewManager(store *db.Store, notifier model.Notifier, bus model.EventBroadcaster) *Manager {
	return &Manager{
		store:                store,
		notifier:             notifier,
		bus:                  bus,
		probeFn:              probe.Probe,
		housekeepingInterval: defaultHousekeepingInterval,
		pruneInterval:        defaultPruneInterval,
		workers:              make(map[string]*workerHandle),
	}
}

// Start launches probe workers for all active monitors and starts the housekeeping loop.
func (m *Manager) Start(ctx context.Context) {
	m.mu.Lock()
	if m.ctx != nil && !m.stopping {
		m.mu.Unlock()
		return
	}
	m.ctx, m.cancel = context.WithCancel(ctx)
	m.stopping = false
	m.wg.Add(1)
	go m.runHousekeepingLoop(m.ctx)
	m.mu.Unlock()

	m.ReloadAll()
}

// Stop cancels all active workers and the housekeeping loop and waits for them to exit.
func (m *Manager) Stop() {
	m.mu.Lock()
	if m.ctx == nil {
		m.mu.Unlock()
		return
	}
	m.stopping = true
	cancel := m.cancel
	m.cancel = nil
	m.ctx = nil
	m.workers = make(map[string]*workerHandle)
	m.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	m.wg.Wait()
}

// ReloadMonitor restarts or stops the worker for the given monitor ID based on its DB state.
func (m *Manager) ReloadMonitor(id string) {
	if id == "" || m.store == nil {
		return
	}

	m.mu.Lock()
	old := m.workers[id]
	if old != nil {
		delete(m.workers, id)
	}
	m.mu.Unlock()

	if old != nil {
		old.cancel()
		<-old.done
	}

	mon, err := m.store.GetMonitorByID(id)
	if err != nil || !mon.Active || mon.Type == model.MonitorTypePush {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopping || m.ctx == nil || m.ctx.Err() != nil {
		return
	}
	m.startWorkerLocked(*mon)
}

// RemoveMonitor stops any running worker for the given monitor ID.
func (m *Manager) RemoveMonitor(id string) {
	if id == "" {
		return
	}
	m.mu.Lock()
	old := m.workers[id]
	if old != nil {
		delete(m.workers, id)
	}
	m.mu.Unlock()

	if old != nil {
		old.cancel()
		<-old.done
	}
}

// ReloadAll reconciles all active probe workers against the database.
func (m *Manager) ReloadAll() {
	if m.store == nil {
		return
	}

	m.mu.Lock()
	oldWorkers := make([]*workerHandle, 0, len(m.workers))
	for id, h := range m.workers {
		oldWorkers = append(oldWorkers, h)
		delete(m.workers, id)
	}
	m.mu.Unlock()

	for _, h := range oldWorkers {
		h.cancel()
	}
	for _, h := range oldWorkers {
		<-h.done
	}

	monitors, err := m.store.ListActiveMonitors()
	if err != nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopping || m.ctx == nil || m.ctx.Err() != nil {
		return
	}
	for _, mon := range monitors {
		if mon.Type == model.MonitorTypePush {
			continue
		}
		m.startWorkerLocked(mon)
	}
}

// TestMonitorNow executes a single ad-hoc probe without persisting heartbeats.
func (m *Manager) TestMonitorNow(ctx context.Context, mon model.Monitor) model.ProbeResult {
	return m.probeFn(ctx, mon)
}

// HandlePush records an inbound heartbeat for a push monitor identified by token.
func (m *Manager) HandlePush(ctx context.Context, token string, status model.HeartbeatStatus, latencyMs int, msg string) (*model.Monitor, error) {
	mon, err := m.store.GetMonitorByPushToken(token)
	if err != nil {
		return nil, err
	}
	if !mon.Active {
		return mon, nil
	}

	if status == "" {
		status = model.HeartbeatStatusUp
	}
	now := time.Now().UTC()
	res := model.ProbeResult{
		Status:    status,
		LatencyMs: latencyMs,
		Message:   msg,
		CheckedAt: now,
	}
	if res.Status == model.HeartbeatStatusUp && mon.DegradedMs > 0 && res.LatencyMs > mon.DegradedMs {
		res.Status = model.HeartbeatStatusDegraded
	}
	if res.Message == "" {
		res.Message = "push heartbeat received"
	}

	inMaint, suppressAlerts, _ := m.store.IsMonitorInActiveMaintenance(mon.ID, now)
	finalStatus := mapHeartbeatToComponentStatus(res.Status)
	if inMaint {
		finalStatus = model.ComponentStatusMaintenance
	}

	prevStatus := mon.CurrentStatus
	if err := m.store.RecordHeartbeat(ctx, mon.ID, res, finalStatus, mon.IntervalSec); err != nil {
		return nil, err
	}

	m.emitHeartbeatEvents(ctx, *mon, res, prevStatus, finalStatus, inMaint && suppressAlerts)
	return m.store.GetMonitorByID(mon.ID)
}

func (m *Manager) startWorkerLocked(mon model.Monitor) {
	if old := m.workers[mon.ID]; old != nil {
		old.cancel()
		delete(m.workers, mon.ID)
	}
	wCtx, wCancel := context.WithCancel(m.ctx)
	done := make(chan struct{})
	m.workers[mon.ID] = &workerHandle{cancel: wCancel, done: done}
	m.wg.Add(1)
	go m.runWorker(wCtx, mon, done)
}

func (m *Manager) runWorker(ctx context.Context, mon model.Monitor, done chan struct{}) {
	defer close(done)
	defer m.wg.Done()

	interval := time.Duration(mon.IntervalSec) * time.Second
	if interval <= 0 {
		interval = 60 * time.Second
	}

	prevStatus := mon.CurrentStatus
	consecutiveDown := 0

	runOnce := func() {
		res := m.probeFn(ctx, mon)
		if ctx.Err() != nil {
			return
		}
		if res.CheckedAt.IsZero() {
			res.CheckedAt = time.Now().UTC()
		}

		inMaint, suppressAlerts, _ := m.store.IsMonitorInActiveMaintenance(mon.ID, res.CheckedAt)

		var finalStatus model.ComponentStatus
		if res.Status == model.HeartbeatStatusDown {
			consecutiveDown++
			if consecutiveDown <= mon.Retries {
				// Hold previous status until consecutive failures exceed configured retries.
				finalStatus = prevStatus
				if finalStatus == "" {
					finalStatus = model.ComponentStatusUnknown
				}
			} else {
				finalStatus = model.ComponentStatusMajorOutage
			}
		} else {
			consecutiveDown = 0
			finalStatus = mapHeartbeatToComponentStatus(res.Status)
		}
		if inMaint {
			finalStatus = model.ComponentStatusMaintenance
		}

		if err := m.store.RecordHeartbeat(ctx, mon.ID, res, finalStatus, mon.IntervalSec); err != nil {
			return
		}

		m.emitHeartbeatEvents(ctx, mon, res, prevStatus, finalStatus, inMaint && suppressAlerts)
		prevStatus = finalStatus
	}

	runOnce()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runOnce()
		}
	}
}

func (m *Manager) runHousekeepingLoop(ctx context.Context) {
	defer m.wg.Done()

	m.runHousekeepingOnce(ctx, time.Now().UTC())

	ticker := time.NewTicker(m.housekeepingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			m.runHousekeepingOnce(ctx, now.UTC())
		}
	}
}

func (m *Manager) runHousekeepingOnce(ctx context.Context, now time.Time) {
	if m.store == nil || ctx.Err() != nil {
		return
	}

	_ = m.store.SyncMaintenanceStatuses(now)
	m.checkPushWatchdog(ctx, now)

	m.mu.Lock()
	shouldPrune := m.lastPrune.IsZero() || now.Sub(m.lastPrune) >= m.pruneInterval
	if shouldPrune {
		m.lastPrune = now
	}
	m.mu.Unlock()

	if shouldPrune {
		days := defaultRetentionDays
		if val, err := m.store.GetSetting("heartbeat_retention_days"); err == nil && strings.TrimSpace(val) != "" {
			if parsed, convErr := strconv.Atoi(strings.TrimSpace(val)); convErr == nil && parsed > 0 {
				days = parsed
			}
		}
		cutoff := now.AddDate(0, 0, -days)
		_, _ = m.store.PruneHeartbeats(cutoff)
		_ = m.store.CleanExpiredSessions()
	}
}

func (m *Manager) checkPushWatchdog(ctx context.Context, now time.Time) {
	monitors, err := m.store.ListActiveMonitors()
	if err != nil {
		return
	}

	for _, mon := range monitors {
		if ctx.Err() != nil {
			return
		}
		if mon.Type != model.MonitorTypePush {
			continue
		}

		var cfg model.PushConfig
		if len(mon.Config) > 0 {
			_ = json.Unmarshal(mon.Config, &cfg)
		}
		cfg = cfg.WithDefaults()

		ref := mon.CreatedAt
		if mon.LastCheckedAt != nil && !mon.LastCheckedAt.IsZero() {
			ref = *mon.LastCheckedAt
		}
		interval := mon.IntervalSec
		if interval <= 0 {
			interval = 60
		}
		deadline := ref.Add(time.Duration(interval+cfg.GraceSec) * time.Second)
		if !now.After(deadline) {
			continue
		}

		inMaint, suppressAlerts, _ := m.store.IsMonitorInActiveMaintenance(mon.ID, now)
		finalStatus := model.ComponentStatusMajorOutage
		if inMaint {
			finalStatus = model.ComponentStatusMaintenance
		}

		res := model.ProbeResult{
			Status:    model.HeartbeatStatusDown,
			LatencyMs: 0,
			Message:   "push heartbeat missed",
			CheckedAt: now,
		}

		prevStatus := mon.CurrentStatus
		if err := m.store.RecordHeartbeat(ctx, mon.ID, res, finalStatus, mon.IntervalSec); err != nil {
			continue
		}
		m.emitHeartbeatEvents(ctx, mon, res, prevStatus, finalStatus, inMaint && suppressAlerts)
	}
}

func (m *Manager) emitHeartbeatEvents(
	ctx context.Context,
	mon model.Monitor,
	res model.ProbeResult,
	prevStatus, finalStatus model.ComponentStatus,
	suppressNotify bool,
) {
	if m.bus != nil {
		m.bus.Broadcast(model.SSEEvent{
			Type: "heartbeat",
			Data: map[string]any{
				"monitor_id":     mon.ID,
				"status":         res.Status,
				"current_status": finalStatus,
				"latency_ms":     res.LatencyMs,
				"status_code":    res.StatusCode,
				"message":        res.Message,
				"checked_at":     res.CheckedAt,
			},
		})
	}

	if prevStatus == finalStatus {
		return
	}

	// Suppress initial unknown -> operational transition noise on first startup check.
	if (prevStatus == "" || prevStatus == model.ComponentStatusUnknown) && finalStatus == model.ComponentStatusOperational {
		return
	}

	if m.bus != nil {
		m.bus.Broadcast(model.SSEEvent{
			Type: "status_change",
			Data: map[string]any{
				"monitor_id":      mon.ID,
				"previous_status": prevStatus,
				"current_status":  finalStatus,
				"message":         res.Message,
				"checked_at":      res.CheckedAt,
			},
		})
	}

	if !suppressNotify && m.notifier != nil {
		m.notifier.NotifyMonitorStateChange(ctx, mon, prevStatus, finalStatus, res.Message)
	}
}

func mapHeartbeatToComponentStatus(st model.HeartbeatStatus) model.ComponentStatus {
	switch st {
	case model.HeartbeatStatusUp:
		return model.ComponentStatusOperational
	case model.HeartbeatStatusDegraded:
		return model.ComponentStatusDegraded
	default:
		return model.ComponentStatusMajorOutage
	}
}
