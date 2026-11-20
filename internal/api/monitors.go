package api

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strings"
	"time"

	"exa/internal/db"
	"exa/internal/model"
)

func (rt *router) handleListMonitors(w http.ResponseWriter, r *http.Request) {
	monitors, err := rt.cfg.Store.ListMonitors()
	if err != nil {
		writeInternalError(w, r, "list monitors failed", err)
		return
	}
	if !isAdminCaller(r) {
		for i := range monitors {
			monitors[i].PushToken = ""
		}
	}
	writeJSON(w, http.StatusOK, monitors)
}

func (rt *router) handleGetMonitor(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	mon, err := rt.cfg.Store.GetMonitorByID(id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "monitor not found")
			return
		}
		writeInternalError(w, r, "get monitor failed", err)
		return
	}
	if !isAdminCaller(r) {
		mon.PushToken = ""
	}

	now := time.Now().UTC()
	rangeParam, windowDur := parseMonitorRange(r.URL.Query().Get("range"))
	since := now.Add(-windowDur)

	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	fromDay := today.AddDate(0, 0, -89).Format("2006-01-02")
	toDay := today.Format("2006-01-02")
	rollups, err := rt.cfg.Store.GetDailyRollups(mon.ID, fromDay, toDay)
	if err != nil {
		writeInternalError(w, r, "get daily rollups failed", err)
		return
	}

	hbsWindow, err := rt.cfg.Store.ListHeartbeatsSince(mon.ID, since, 5000)
	if err != nil {
		writeInternalError(w, r, "list heartbeats window failed", err)
		return
	}
	if len(hbsWindow) > 360 {
		hbsWindow = downsampleHeartbeats(hbsWindow, 360)
	} else if len(hbsWindow) == 0 && (rangeParam == "7d" || rangeParam == "30d") {
		hbsWindow = rollupHeartbeatsForRange(mon.ID, rollups, since)
	}

	hbsRecent, err := rt.cfg.Store.ListHeartbeats(mon.ID, 200)
	if err != nil {
		writeInternalError(w, r, "list recent heartbeats failed", err)
		return
	}
	recent60 := hbsRecent
	if len(recent60) > 60 {
		recent60 = recent60[:60]
	}
	recentChecks60 := reverseHeartbeats(recent60)

	hbs24h := hbsWindow
	if rangeParam != "24h" {
		hbs24h, err = rt.cfg.Store.ListHeartbeatsSince(mon.ID, now.Add(-24*time.Hour), 5000)
		if err != nil {
			writeInternalError(w, r, "list 24h heartbeats failed", err)
			return
		}
	}

	var uptime24h *float64
	var avgLat24h *int
	if len(hbs24h) > 0 {
		var good24, sumLat int
		for _, h := range hbs24h {
			if h.Status == model.HeartbeatStatusUp || h.Status == model.HeartbeatStatusDegraded {
				good24++
			}
			sumLat += h.LatencyMs
		}
		v := round2(float64(good24) / float64(len(hbs24h)) * 100.0)
		uptime24h = &v
		avg := int(math.Round(float64(sumLat) / float64(len(hbs24h))))
		avgLat24h = &avg
	}

	from30Day := today.AddDate(0, 0, -29).Format("2006-01-02")
	var total30, good30, total90, good90 int
	for _, ru := range rollups {
		good := ru.UpChecks + ru.DegradedChecks
		total90 += ru.TotalChecks
		good90 += good
		if ru.Day >= from30Day {
			total30 += ru.TotalChecks
			good30 += good
		}
	}
	var uptime30d, uptime90d *float64
	if total30 > 0 {
		v := round2(float64(good30) / float64(total30) * 100.0)
		uptime30d = &v
	}
	if total90 > 0 {
		v := round2(float64(good90) / float64(total90) * 100.0)
		uptime90d = &v
	}

	statusSince := findStatusSince(hbsRecent, mon.CurrentStatus, mon.LastCheckedAt, mon.UpdatedAt)

	chanIDs, err := rt.cfg.Store.ListChannelIDsForMonitor(mon.ID)
	if err != nil {
		writeInternalError(w, r, "list channel ids failed", err)
		return
	}
	channels, err := rt.cfg.Store.ListChannelsForMonitor(mon.ID)
	if err != nil {
		writeInternalError(w, r, "list channels failed", err)
		return
	}
	channelNames := make([]string, 0, len(channels))
	for _, ch := range channels {
		channelNames = append(channelNames, ch.Name)
	}

	pageIDs, err := rt.cfg.Store.ListStatusPageIDsForMonitor(mon.ID)
	if err != nil {
		writeInternalError(w, r, "list status page ids failed", err)
		return
	}
	allPages, err := rt.cfg.Store.ListStatusPages()
	if err != nil {
		writeInternalError(w, r, "list status pages failed", err)
		return
	}
	pageTitles := make(map[string]string, len(allPages))
	for _, p := range allPages {
		pageTitles[p.ID] = p.Title
	}
	statusPageNames := make([]string, 0, len(pageIDs))
	for _, pid := range pageIDs {
		if title, ok := pageTitles[pid]; ok {
			statusPageNames = append(statusPageNames, title)
		}
	}

	incidents, err := rt.cfg.Store.ListIncidentsForMonitor(mon.ID, 20)
	if err != nil {
		writeInternalError(w, r, "list monitor incidents failed", err)
		return
	}

	writeJSON(w, http.StatusOK, model.MonitorDetailResponse{
		Monitor:                *mon,
		Heartbeats:             hbsWindow,
		DailyRollups:           rollups,
		NotificationChannelIDs: chanIDs,
		StatusPageIDs:          pageIDs,
		Uptime24h:              uptime24h,
		Uptime30d:              uptime30d,
		Uptime90d:              uptime90d,
		AvgLatency24h:          avgLat24h,
		StatusSince:            statusSince,
		RecentChecks60:         recentChecks60,
		Incidents:              incidents,
		ChannelNames:           channelNames,
		StatusPageNames:        statusPageNames,
		Range:                  rangeParam,
	})
}

func parseMonitorRange(raw string) (string, time.Duration) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1h":
		return "1h", 1 * time.Hour
	case "7d":
		return "7d", 7 * 24 * time.Hour
	case "30d":
		return "30d", 30 * 24 * time.Hour
	default:
		return "24h", 24 * time.Hour
	}
}

func downsampleHeartbeats(hbs []model.Heartbeat, target int) []model.Heartbeat {
	if len(hbs) <= target || target <= 0 {
		return hbs
	}
	step := float64(len(hbs)) / float64(target)
	out := make([]model.Heartbeat, 0, target)
	for i := 0; i < target; i++ {
		idx := int(float64(i) * step)
		if idx >= len(hbs) {
			idx = len(hbs) - 1
		}
		out = append(out, hbs[idx])
	}
	return out
}

func rollupHeartbeatsForRange(monitorID string, rollups []model.DailyRollup, since time.Time) []model.Heartbeat {
	sinceDay := since.UTC().Format("2006-01-02")
	out := []model.Heartbeat{}
	for i := len(rollups) - 1; i >= 0; i-- {
		ru := rollups[i]
		if ru.Day < sinceDay || ru.TotalChecks == 0 {
			continue
		}
		dayTime, err := time.Parse("2006-01-02", ru.Day)
		if err != nil {
			continue
		}
		st := model.HeartbeatStatusUp
		if ru.DownChecks > 0 && ru.UpChecks == 0 {
			st = model.HeartbeatStatusDown
		} else if ru.DownChecks > 0 || ru.DegradedChecks > 0 {
			st = model.HeartbeatStatusDegraded
		}
		out = append(out, model.Heartbeat{
			MonitorID: monitorID,
			Status:    st,
			LatencyMs: int(math.Round(ru.AvgLatencyMs)),
			CheckedAt: dayTime.UTC().Add(12 * time.Hour),
		})
	}
	return out
}

func (rt *router) handleCreateMonitor(w http.ResponseWriter, r *http.Request) {
	var req model.MonitorUpsertRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	req.Target = strings.TrimSpace(req.Target)
	req.Slug = strings.TrimSpace(req.Slug)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if !isValidMonitorType(req.Type) {
		writeError(w, http.StatusBadRequest, "invalid monitor type")
		return
	}
	if req.Type != model.MonitorTypePush && req.Target == "" {
		writeError(w, http.StatusBadRequest, "target is required for active probes")
		return
	}

	active := true
	if req.Active != nil {
		active = *req.Active
	}
	cfgRaw := resolveMonitorConfig(req.Config, req.ConfigJSON)

	created, err := rt.cfg.Store.CreateMonitor(model.Monitor{
		Name:        req.Name,
		Slug:        req.Slug,
		Type:        req.Type,
		Target:      req.Target,
		GroupID:     req.GroupID,
		IntervalSec: req.IntervalSec,
		TimeoutSec:  req.TimeoutSec,
		Retries:     req.Retries,
		DegradedMs:  req.DegradedMs,
		Active:      active,
		SortOrder:   req.SortOrder,
		Config:      cfgRaw,
	})
	if err != nil {
		if errors.Is(err, db.ErrConflict) {
			writeError(w, http.StatusConflict, "monitor slug already exists")
			return
		}
		writeInternalError(w, r, "monitor operation failed", err)
		return
	}

	if len(req.StatusPageIDs) > 0 {
		_ = rt.cfg.Store.AttachMonitorToPages(created.ID, req.StatusPageIDs, created.GroupID, created.SortOrder)
	} else if req.AttachDefaultPage == nil || *req.AttachDefaultPage {
		_ = rt.cfg.Store.AttachMonitorToDefaultPage(created.ID, created.GroupID, created.SortOrder)
	}

	if len(req.NotificationChannelIDs) > 0 {
		_ = rt.cfg.Store.SetMonitorNotificationChannels(created.ID, req.NotificationChannelIDs)
	}

	if rt.cfg.Monitors != nil && created.Active {
		rt.cfg.Monitors.ReloadMonitor(created.ID)
	}

	writeJSON(w, http.StatusCreated, created)
}

func (rt *router) handleUpdateMonitor(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	existing, err := rt.cfg.Store.GetMonitorByID(id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "monitor not found")
			return
		}
		writeInternalError(w, r, "monitor operation failed", err)
		return
	}

	var req model.MonitorUpsertRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if strings.TrimSpace(req.Name) != "" {
		existing.Name = strings.TrimSpace(req.Name)
	}
	if strings.TrimSpace(req.Slug) != "" {
		existing.Slug = strings.TrimSpace(req.Slug)
	}
	if req.Type != "" {
		if !isValidMonitorType(req.Type) {
			writeError(w, http.StatusBadRequest, "invalid monitor type")
			return
		}
		existing.Type = req.Type
	}
	if req.Target != "" || existing.Type == model.MonitorTypePush {
		existing.Target = strings.TrimSpace(req.Target)
	}
	existing.GroupID = req.GroupID
	if req.IntervalSec > 0 {
		existing.IntervalSec = req.IntervalSec
	}
	if req.TimeoutSec > 0 {
		existing.TimeoutSec = req.TimeoutSec
	}
	if req.Retries >= 0 {
		existing.Retries = req.Retries
	}
	existing.DegradedMs = req.DegradedMs
	existing.SortOrder = req.SortOrder
	if req.Active != nil {
		existing.Active = *req.Active
	}
	if len(req.Config) > 0 || req.ConfigJSON != "" {
		existing.Config = resolveMonitorConfig(req.Config, req.ConfigJSON)
	}

	updated, err := rt.cfg.Store.UpdateMonitor(*existing)
	if err != nil {
		if errors.Is(err, db.ErrConflict) {
			writeError(w, http.StatusConflict, "monitor slug already exists")
			return
		}
		writeInternalError(w, r, "monitor operation failed", err)
		return
	}

	if req.NotificationChannelIDs != nil {
		_ = rt.cfg.Store.SetMonitorNotificationChannels(updated.ID, req.NotificationChannelIDs)
	}
	if req.StatusPageIDs != nil {
		_ = rt.cfg.Store.AttachMonitorToPages(updated.ID, req.StatusPageIDs, updated.GroupID, updated.SortOrder)
	}

	if rt.cfg.Monitors != nil {
		if updated.Active {
			rt.cfg.Monitors.ReloadMonitor(updated.ID)
		} else {
			rt.cfg.Monitors.RemoveMonitor(updated.ID)
		}
	}

	writeJSON(w, http.StatusOK, updated)
}

func (rt *router) handleDeleteMonitor(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := rt.cfg.Store.DeleteMonitor(id); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "monitor not found")
			return
		}
		writeInternalError(w, r, "monitor operation failed", err)
		return
	}
	if rt.cfg.Monitors != nil {
		rt.cfg.Monitors.RemoveMonitor(id)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (rt *router) handlePauseMonitor(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := rt.cfg.Store.SetMonitorActive(id, false); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "monitor not found")
			return
		}
		writeInternalError(w, r, "monitor operation failed", err)
		return
	}
	if rt.cfg.Monitors != nil {
		rt.cfg.Monitors.RemoveMonitor(id)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (rt *router) handleResumeMonitor(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := rt.cfg.Store.SetMonitorActive(id, true); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "monitor not found")
			return
		}
		writeInternalError(w, r, "monitor operation failed", err)
		return
	}
	if rt.cfg.Monitors != nil {
		rt.cfg.Monitors.ReloadMonitor(id)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (rt *router) handleTestMonitor(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	mon, err := rt.cfg.Store.GetMonitorByID(id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "monitor not found")
			return
		}
		writeInternalError(w, r, "monitor operation failed", err)
		return
	}
	if rt.cfg.Monitors == nil {
		writeError(w, http.StatusServiceUnavailable, "monitor controller unavailable")
		return
	}
	res := rt.cfg.Monitors.TestMonitorNow(r.Context(), *mon)
	writeJSON(w, http.StatusOK, res)
}

func (rt *router) handleListGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := rt.cfg.Store.ListGroups()
	if err != nil {
		writeInternalError(w, r, "monitor operation failed", err)
		return
	}
	writeJSON(w, http.StatusOK, groups)
}

func (rt *router) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	var req model.GroupUpsertRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	grp, err := rt.cfg.Store.CreateGroup(model.ComponentGroup{
		Name:             req.Name,
		Description:      req.Description,
		SortOrder:        req.SortOrder,
		CollapsedDefault: req.CollapsedDefault,
	})
	if err != nil {
		writeInternalError(w, r, "monitor operation failed", err)
		return
	}
	writeJSON(w, http.StatusCreated, grp)
}

func (rt *router) handleUpdateGroup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req model.GroupUpsertRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	grp, err := rt.cfg.Store.UpdateGroup(model.ComponentGroup{
		ID:               id,
		Name:             req.Name,
		Description:      req.Description,
		SortOrder:        req.SortOrder,
		CollapsedDefault: req.CollapsedDefault,
	})
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "group not found")
			return
		}
		writeInternalError(w, r, "monitor operation failed", err)
		return
	}
	writeJSON(w, http.StatusOK, grp)
}

func (rt *router) handleDeleteGroup(w http.ResponseWriter, r *http.Request) {
	if err := rt.cfg.Store.DeleteGroup(r.PathValue("id")); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "group not found")
			return
		}
		writeInternalError(w, r, "monitor operation failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func isValidMonitorType(t model.MonitorType) bool {
	switch t {
	case model.MonitorTypeHTTP, model.MonitorTypeTCP, model.MonitorTypePing, model.MonitorTypeDNS, model.MonitorTypePush:
		return true
	default:
		return false
	}
}

func resolveMonitorConfig(raw json.RawMessage, rawStr string) json.RawMessage {
	if len(raw) > 0 && string(raw) != "null" {
		return raw
	}
	if strings.TrimSpace(rawStr) != "" {
		return json.RawMessage(rawStr)
	}
	return json.RawMessage("{}")
}
