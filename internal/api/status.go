package api

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"exa/internal/db"
	"exa/internal/model"
)

func (rt *router) handlePublicStatusDefault(w http.ResponseWriter, r *http.Request) {
	page, err := rt.cfg.Store.GetDefaultStatusPage()
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "no status page configured")
			return
		}
		writeInternalError(w, r, "status operation failed", err)
		return
	}
	rt.servePublicStatus(w, r, page)
}

func (rt *router) handlePublicStatusBySlug(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	page, err := rt.cfg.Store.GetStatusPageBySlug(slug)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "status page not found")
			return
		}
		writeInternalError(w, r, "status operation failed", err)
		return
	}
	rt.servePublicStatus(w, r, page)
}

func (rt *router) servePublicStatus(w http.ResponseWriter, r *http.Request, page *model.StatusPage) {
	if !page.IsPublic {
		if _, ok := rt.cfg.Auth.AuthenticateRequest(r); !ok {
			writeError(w, http.StatusNotFound, "status page not found")
			return
		}
	}

	now := time.Now().UTC()
	_ = rt.cfg.Store.SyncMaintenanceStatuses(now)

	resp, err := rt.buildPublicStatus(page, now)
	if err != nil {
		writeInternalError(w, r, "status operation failed", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (rt *router) buildPublicStatus(page *model.StatusPage, now time.Time) (*model.PublicStatusResponse, error) {
	otherPages := []model.PageSummary{}

	pageMonitors, err := rt.cfg.Store.ListStatusPageMonitors(page.ID)
	if err != nil {
		return nil, err
	}
	allMonitors, err := rt.cfg.Store.ListMonitors()
	if err != nil {
		return nil, err
	}
	monitorByID := make(map[string]model.Monitor, len(allMonitors))
	for _, m := range allMonitors {
		monitorByID[m.ID] = m
	}

	allGroups, err := rt.cfg.Store.ListGroups()
	if err != nil {
		return nil, err
	}

	todayUTC := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	fromDayTime := todayUTC.AddDate(0, 0, -89)
	fromDay := fromDayTime.Format("2006-01-02")
	toDay := todayUTC.Format("2006-01-02")

	incidents90d, err := rt.cfg.Store.ListIncidentsInRange(page.ID, fromDayTime, now.Add(24*time.Hour))
	if err != nil {
		return nil, err
	}
	activeIncidents, err := rt.cfg.Store.ListActiveIncidents(page.ID)
	if err != nil {
		return nil, err
	}
	// Ensure unresolved incidents from earlier than 90 days ago still appear in day overlays.
	incSeen := make(map[string]struct{}, len(incidents90d))
	for _, inc := range incidents90d {
		incSeen[inc.ID] = struct{}{}
	}
	for _, inc := range activeIncidents {
		if _, ok := incSeen[inc.ID]; !ok {
			incidents90d = append(incidents90d, inc)
		}
	}

	pastCutoff := now.AddDate(0, 0, -14)
	pastIncidents := []model.Incident{}
	for _, inc := range incidents90d {
		if inc.Status == model.IncidentStatusResolved && !inc.CreatedAt.Before(pastCutoff) {
			pastIncidents = append(pastIncidents, inc)
		}
	}
	sort.Slice(pastIncidents, func(i, j int) bool {
		return pastIncidents[i].CreatedAt.After(pastIncidents[j].CreatedAt)
	})

	allMaint, err := rt.cfg.Store.ListMaintenancesForPage(page.ID)
	if err != nil {
		return nil, err
	}
	activeMaint := []model.Maintenance{}
	upcomingMaint := []model.Maintenance{}
	for _, maint := range allMaint {
		switch maint.Status {
		case model.MaintenanceStatusInProgress:
			activeMaint = append(activeMaint, maint)
		case model.MaintenanceStatusScheduled:
			upcomingMaint = append(upcomingMaint, maint)
		}
	}

	grouped := make(map[string][]model.PublicMonitor)
	ungrouped := []model.PublicMonitor{}
	pageMonStatuses := []model.ComponentStatus{}

	for _, spm := range pageMonitors {
		m, ok := monitorByID[spm.MonitorID]
		if !ok {
			continue
		}
		rollups, err := rt.cfg.Store.GetDailyRollups(m.ID, fromDay, toDay)
		if err != nil {
			return nil, err
		}
		pm := buildPublicMonitor(m, spm, page.ShowLatency, todayUTC, now, rollups, incidents90d, allMaint)
		pageMonStatuses = append(pageMonStatuses, pm.CurrentStatus)

		var gid string
		if spm.GroupID != nil && *spm.GroupID != "" {
			gid = *spm.GroupID
		} else if m.GroupID != nil && *m.GroupID != "" {
			gid = *m.GroupID
		}
		if gid != "" {
			grouped[gid] = append(grouped[gid], pm)
		} else {
			ungrouped = append(ungrouped, pm)
		}
	}

	publicGroups := []model.PublicGroup{}
	for _, g := range allGroups {
		mons, ok := grouped[g.ID]
		if !ok || len(mons) == 0 {
			continue
		}
		publicGroups = append(publicGroups, model.PublicGroup{
			ID:               g.ID,
			Name:             g.Name,
			Description:      g.Description,
			CollapsedDefault: g.CollapsedDefault,
			Monitors:         mons,
		})
	}

	overallStatus, overallLabel := computeOverallStatus(pageMonStatuses, len(activeMaint) > 0)
	allowSubs, _ := rt.cfg.Store.GetSetting("allow_subscriptions")

	return &model.PublicStatusResponse{
		Page:                 *page,
		OverallStatus:        overallStatus,
		OverallLabel:         overallLabel,
		UpdatedAt:            now,
		Groups:               publicGroups,
		UngroupedMonitors:    ungrouped,
		ActiveIncidents:      activeIncidents,
		ActiveMaintenances:   activeMaint,
		UpcomingMaintenances: upcomingMaint,
		PastIncidents:        pastIncidents,
		OtherPages:           otherPages,
		SubscriptionsEnabled: allowSubs == "true",
	}, nil
}

func buildPublicMonitor(
	m model.Monitor,
	spm model.StatusPageMonitor,
	showLatency bool,
	todayUTC, now time.Time,
	rollups []model.DailyRollup,
	incidents []model.Incident,
	maintenances []model.Maintenance,
) model.PublicMonitor {
	name := m.Name
	if spm.DisplayName != "" {
		name = spm.DisplayName
	}

	var lat *int
	if showLatency {
		lat = m.LastLatencyMs
	}

	rollupMap := make(map[string]model.DailyRollup, len(rollups))
	for _, r := range rollups {
		rollupMap[r.Day] = r
	}

	days := make([]model.PublicDay, 0, 90)
	var totalAll, upAll int

	for i := 89; i >= 0; i-- {
		d := todayUTC.AddDate(0, 0, -i)
		dayStr := d.Format("2006-01-02")
		dayEnd := d.Add(24 * time.Hour)

		dayIncs := []model.IncidentBrief{}
		for _, inc := range incidents {
			if !containsString(inc.MonitorIDs, m.ID) {
				continue
			}
			end := now
			if inc.ResolvedAt != nil {
				end = *inc.ResolvedAt
			}
			if inc.CreatedAt.Before(dayEnd) && !end.Before(d) {
				dayIncs = append(dayIncs, model.IncidentBrief{
					ID:     inc.ID,
					Title:  inc.Title,
					Impact: inc.Impact,
				})
			}
		}

		inMaint := false
		for _, maint := range maintenances {
			if !containsString(maint.MonitorIDs, m.ID) {
				continue
			}
			if maint.ScheduledStart.Before(dayEnd) && maint.ScheduledEnd.After(d) {
				inMaint = true
				break
			}
		}

		r, hasRollup := rollupMap[dayStr]
		pd := model.PublicDay{
			Day:       dayStr,
			Incidents: dayIncs,
		}
		if hasRollup && r.TotalChecks > 0 {
			pd.TotalChecks = r.TotalChecks
			pd.DownChecks = r.DownChecks
			pd.DowntimeSec = r.DowntimeSec
			if showLatency {
				pd.AvgLatencyMs = math.Round(r.AvgLatencyMs*100) / 100
			}
			upCount := r.UpChecks + r.DegradedChecks
			pct := math.Round(float64(upCount)/float64(r.TotalChecks)*10000) / 100
			pd.UptimePct = &pct
			totalAll += r.TotalChecks
			upAll += upCount

			switch {
			case r.DownChecks > 0 && ((r.UpChecks == 0 && r.DegradedChecks == 0) || pct < 95.0):
				pd.Status = "down"
			case r.DownChecks > 0 || r.DegradedChecks > 0:
				pd.Status = "degraded"
			case inMaint:
				pd.Status = "maintenance"
			default:
				pd.Status = "up"
			}
		} else if inMaint {
			pd.Status = "maintenance"
		} else {
			pd.Status = "no_data"
		}
		days = append(days, pd)
	}

	var uptime90d *float64
	if totalAll > 0 {
		v := math.Round(float64(upAll)/float64(totalAll)*10000) / 100
		uptime90d = &v
	}

	return model.PublicMonitor{
		ID:            m.ID,
		Name:          name,
		Slug:          m.Slug,
		Type:          m.Type,
		CurrentStatus: m.CurrentStatus,
		LastLatencyMs: lat,
		LastCheckedAt: m.LastCheckedAt,
		TLSDaysLeft:   m.TLSDaysLeft,
		Uptime90d:     uptime90d,
		Days:          days,
	}
}

func computeOverallStatus(statuses []model.ComponentStatus, hasActiveMaint bool) (model.OverallStatus, string) {
	if len(statuses) == 0 {
		if hasActiveMaint {
			return model.OverallStatusMaintenance, "Scheduled Maintenance in Progress"
		}
		return model.OverallStatusOperational, "All Systems Operational"
	}

	var downCount, degCount, maintCount int
	for _, st := range statuses {
		switch st {
		case model.ComponentStatusMajorOutage:
			downCount++
		case model.ComponentStatusDegraded:
			degCount++
		case model.ComponentStatusMaintenance:
			maintCount++
		}
	}

	switch {
	case downCount > 0 && downCount == len(statuses):
		return model.OverallStatusMajorOutage, "Major System Outage"
	case downCount > 0:
		return model.OverallStatusPartialOutage, "Partial System Outage"
	case degCount > 0:
		return model.OverallStatusDegraded, "Degraded System Performance"
	case maintCount > 0 || hasActiveMaint:
		return model.OverallStatusMaintenance, "Scheduled Maintenance in Progress"
	default:
		return model.OverallStatusOperational, "All Systems Operational"
	}
}

func (rt *router) handlePublicIncidents(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	pageObj, err := rt.resolvePublicPage(r, slug)
	if err != nil {
		writeError(w, http.StatusNotFound, "status page not found")
		return
	}

	pageNum, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if pageNum < 1 {
		pageNum = 1
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 100 {
		limit = 20
	}
	offset := (pageNum - 1) * limit

	incidents, err := rt.cfg.Store.ListIncidents(pageObj.ID, limit, offset)
	if err != nil {
		writeInternalError(w, r, "status operation failed", err)
		return
	}
	total, err := rt.cfg.Store.CountIncidents(pageObj.ID)
	if err != nil {
		writeInternalError(w, r, "status operation failed", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"incidents": incidents,
		"total":     total,
		"page":      pageNum,
		"limit":     limit,
	})
}

func (rt *router) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r, rt.cfg.TrustProxy)
	if !rt.subscribeLimiter.Allow(ip) {
		writeError(w, http.StatusTooManyRequests, "too many subscription attempts, try again later")
		return
	}

	allowSubs, err := rt.cfg.Store.GetSetting("allow_subscriptions")
	if err != nil {
		writeInternalError(w, r, "read allow_subscriptions failed", err)
		return
	}
	if allowSubs != "true" {
		writeError(w, http.StatusForbidden, "public subscriptions are disabled")
		return
	}

	slug := r.PathValue("slug")
	pageObj, err := rt.resolvePublicPage(r, slug)
	if err != nil {
		writeError(w, http.StatusNotFound, "status page not found")
		return
	}

	activeCount, err := rt.cfg.Store.CountActiveSubscribers(pageObj.ID)
	if err != nil {
		writeInternalError(w, r, "count subscribers failed", err)
		return
	}
	if activeCount >= 500 {
		writeError(w, http.StatusTooManyRequests, "subscriber limit reached for this status page")
		return
	}

	var req model.SubscribeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Endpoint = strings.TrimSpace(req.Endpoint)
	if req.Type != model.SubscriberTypeEmail && req.Type != model.SubscriberTypeWebhook {
		writeError(w, http.StatusBadRequest, "type must be email or webhook")
		return
	}
	if req.Type == model.SubscriberTypeEmail {
		if strings.ContainsAny(req.Endpoint, "\r\n") {
			writeError(w, http.StatusBadRequest, "valid email address is required")
			return
		}
		addr, err := mail.ParseAddress(req.Endpoint)
		if err != nil || addr.Address == "" || !strings.Contains(addr.Address, "@") {
			writeError(w, http.StatusBadRequest, "valid email address is required")
			return
		}
		req.Endpoint = addr.Address
	}
	if req.Type == model.SubscriberTypeWebhook {
		if err := rt.validateSubscribeWebhook(r.Context(), req.Endpoint); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	sub, err := rt.cfg.Store.CreateSubscriber(model.Subscriber{
		StatusPageID: pageObj.ID,
		Type:         req.Type,
		Endpoint:     req.Endpoint,
		Verified:     true,
		Active:       true,
	})
	if err != nil {
		writeInternalError(w, r, "create subscriber failed", err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"id":      sub.ID,
		"message": "Subscribed to status updates",
	})
}

func (rt *router) handleUnsubscribe(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if err := rt.cfg.Store.DeactivateSubscriberByToken(token); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "subscription not found")
			return
		}
		writeInternalError(w, r, "status operation failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"message": "You have been unsubscribed from status updates.",
	})
}

// Status Pages Admin CRUD.

func (rt *router) handleListStatusPages(w http.ResponseWriter, r *http.Request) {
	pages, err := rt.cfg.Store.ListStatusPages()
	if err != nil {
		writeInternalError(w, r, "status operation failed", err)
		return
	}
	writeJSON(w, http.StatusOK, pages)
}

func (rt *router) handleCreateStatusPage(w http.ResponseWriter, r *http.Request) {
	var req model.StatusPageUpsertRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	req.Slug = strings.TrimSpace(req.Slug)
	if req.Title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}
	isPublic := true
	if req.IsPublic != nil {
		isPublic = *req.IsPublic
	}
	showLatency := true
	if req.ShowLatency != nil {
		showLatency = *req.ShowLatency
	}
	page, err := rt.cfg.Store.CreateStatusPage(model.StatusPage{
		Slug:        req.Slug,
		Title:       req.Title,
		Description: req.Description,
		CustomCSS:   req.CustomCSS,
		IsDefault:   req.IsDefault,
		IsPublic:    isPublic,
		ShowLatency: showLatency,
	})
	if err != nil {
		if errors.Is(err, db.ErrConflict) {
			writeError(w, http.StatusConflict, "status page slug already exists")
			return
		}
		writeInternalError(w, r, "status operation failed", err)
		return
	}
	writeJSON(w, http.StatusCreated, page)
}

func (rt *router) handleUpdateStatusPage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	existing, err := rt.cfg.Store.GetStatusPageByID(id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "status page not found")
			return
		}
		writeInternalError(w, r, "status operation failed", err)
		return
	}

	var req model.StatusPageUpsertRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.Title) != "" {
		existing.Title = strings.TrimSpace(req.Title)
	}
	if strings.TrimSpace(req.Slug) != "" {
		existing.Slug = strings.TrimSpace(req.Slug)
	}
	existing.Description = req.Description
	existing.CustomCSS = req.CustomCSS
	existing.IsDefault = req.IsDefault
	if req.IsPublic != nil {
		existing.IsPublic = *req.IsPublic
	}
	if req.ShowLatency != nil {
		existing.ShowLatency = *req.ShowLatency
	}

	updated, err := rt.cfg.Store.UpdateStatusPage(*existing)
	if err != nil {
		if errors.Is(err, db.ErrConflict) {
			writeError(w, http.StatusConflict, "status page slug already exists")
			return
		}
		writeInternalError(w, r, "status operation failed", err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (rt *router) handleDeleteStatusPage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	page, err := rt.cfg.Store.GetStatusPageByID(id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "status page not found")
			return
		}
		writeInternalError(w, r, "status operation failed", err)
		return
	}
	if page.IsDefault {
		writeError(w, http.StatusBadRequest, "cannot delete default status page")
		return
	}
	if err := rt.cfg.Store.DeleteStatusPage(id); err != nil {
		writeInternalError(w, r, "status operation failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (rt *router) handleSetPageMonitors(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := rt.cfg.Store.GetStatusPageByID(id); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "status page not found")
			return
		}
		writeInternalError(w, r, "status operation failed", err)
		return
	}

	var req model.SetPageMonitorsRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := rt.cfg.Store.SetStatusPageMonitors(id, req.Monitors); err != nil {
		writeInternalError(w, r, "status operation failed", err)
		return
	}
	items, err := rt.cfg.Store.ListStatusPageMonitors(id)
	if err != nil {
		writeInternalError(w, r, "status operation failed", err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

// Incidents Admin CRUD.

func (rt *router) handleListIncidents(w http.ResponseWriter, r *http.Request) {
	pageID := r.URL.Query().Get("status_page_id")
	list, err := rt.cfg.Store.ListIncidents(pageID, 200, 0)
	if err != nil {
		writeInternalError(w, r, "status operation failed", err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (rt *router) handleCreateIncident(w http.ResponseWriter, r *http.Request) {
	var req model.IncidentCreateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	req.Message = strings.TrimSpace(req.Message)
	if req.Title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}
	if req.StatusPageID == "" {
		def, err := rt.cfg.Store.GetDefaultStatusPage()
		if err != nil {
			writeError(w, http.StatusBadRequest, "status_page_id is required")
			return
		}
		req.StatusPageID = def.ID
	}

	inc, err := rt.cfg.Store.CreateIncident(model.Incident{
		StatusPageID: req.StatusPageID,
		Title:        req.Title,
		Impact:       req.Impact,
		Status:       req.Status,
		MonitorIDs:   req.MonitorIDs,
	}, req.Message)
	if err != nil {
		writeInternalError(w, r, "status operation failed", err)
		return
	}

	rt.cfg.SSE.Broadcast(model.SSEEvent{Type: "incident_update", Data: inc})
	if rt.cfg.Notifier != nil && len(inc.Updates) > 0 {
		if page, err := rt.cfg.Store.GetStatusPageByID(inc.StatusPageID); err == nil {
			upd := inc.Updates[0]
			incCopy := *inc
			pageCopy := *page
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				rt.cfg.Notifier.NotifyIncidentUpdate(ctx, pageCopy, incCopy, upd)
			}()
		}
	}

	writeJSON(w, http.StatusCreated, inc)
}

func (rt *router) handleAddIncidentUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req model.IncidentUpdateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Message = strings.TrimSpace(req.Message)
	if req.Status == "" || req.Message == "" {
		writeError(w, http.StatusBadRequest, "status and message are required")
		return
	}

	inc, upd, err := rt.cfg.Store.AddIncidentUpdate(id, req.Status, req.Message)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "incident not found")
			return
		}
		writeInternalError(w, r, "status operation failed", err)
		return
	}

	rt.cfg.SSE.Broadcast(model.SSEEvent{Type: "incident_update", Data: inc})
	if rt.cfg.Notifier != nil && upd != nil {
		if page, err := rt.cfg.Store.GetStatusPageByID(inc.StatusPageID); err == nil {
			incCopy := *inc
			updCopy := *upd
			pageCopy := *page
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				rt.cfg.Notifier.NotifyIncidentUpdate(ctx, pageCopy, incCopy, updCopy)
			}()
		}
	}

	writeJSON(w, http.StatusOK, inc)
}

func (rt *router) handleDeleteIncident(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := rt.cfg.Store.DeleteIncident(id); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "incident not found")
			return
		}
		writeInternalError(w, r, "status operation failed", err)
		return
	}
	rt.cfg.SSE.Broadcast(model.SSEEvent{Type: "incident_update", Data: map[string]string{"deleted_id": id}})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// Maintenances Admin CRUD.

func (rt *router) handleListMaintenances(w http.ResponseWriter, r *http.Request) {
	_ = rt.cfg.Store.SyncMaintenanceStatuses(time.Now().UTC())
	pageID := r.URL.Query().Get("status_page_id")
	list, err := rt.cfg.Store.ListMaintenances(pageID)
	if err != nil {
		writeInternalError(w, r, "status operation failed", err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (rt *router) handleCreateMaintenance(w http.ResponseWriter, r *http.Request) {
	var req model.MaintenanceUpsertRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" || req.ScheduledStart.IsZero() || !req.ScheduledEnd.After(req.ScheduledStart) {
		writeError(w, http.StatusBadRequest, "title and valid scheduled_start/scheduled_end are required")
		return
	}
	if req.StatusPageID == "" {
		def, err := rt.cfg.Store.GetDefaultStatusPage()
		if err != nil {
			writeError(w, http.StatusBadRequest, "status_page_id is required")
			return
		}
		req.StatusPageID = def.ID
	}
	suppress := true
	if req.AutoSuppressAlerts != nil {
		suppress = *req.AutoSuppressAlerts
	}

	maint, err := rt.cfg.Store.CreateMaintenance(model.Maintenance{
		StatusPageID:       req.StatusPageID,
		Title:              req.Title,
		Description:        req.Description,
		ScheduledStart:     req.ScheduledStart.UTC(),
		ScheduledEnd:       req.ScheduledEnd.UTC(),
		AutoSuppressAlerts: suppress,
		MonitorIDs:         req.MonitorIDs,
	})
	if err != nil {
		writeInternalError(w, r, "status operation failed", err)
		return
	}

	rt.cfg.SSE.Broadcast(model.SSEEvent{Type: "maintenance_update", Data: maint})
	if rt.cfg.Notifier != nil {
		if page, err := rt.cfg.Store.GetStatusPageByID(maint.StatusPageID); err == nil {
			mCopy := *maint
			pCopy := *page
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				rt.cfg.Notifier.NotifyMaintenanceUpdate(ctx, pCopy, mCopy)
			}()
		}
	}
	writeJSON(w, http.StatusCreated, maint)
}

func (rt *router) handleUpdateMaintenance(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	existing, err := rt.cfg.Store.GetMaintenanceByID(id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "maintenance not found")
			return
		}
		writeInternalError(w, r, "status operation failed", err)
		return
	}

	var req model.MaintenanceUpsertRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.Title) != "" {
		existing.Title = strings.TrimSpace(req.Title)
	}
	existing.Description = req.Description
	if req.StatusPageID != "" {
		existing.StatusPageID = req.StatusPageID
	}
	if !req.ScheduledStart.IsZero() {
		existing.ScheduledStart = req.ScheduledStart.UTC()
	}
	if !req.ScheduledEnd.IsZero() {
		existing.ScheduledEnd = req.ScheduledEnd.UTC()
	}
	if !existing.ScheduledEnd.After(existing.ScheduledStart) {
		writeError(w, http.StatusBadRequest, "scheduled_end must be after scheduled_start")
		return
	}
	if req.AutoSuppressAlerts != nil {
		existing.AutoSuppressAlerts = *req.AutoSuppressAlerts
	}
	if req.MonitorIDs != nil {
		existing.MonitorIDs = req.MonitorIDs
	}

	updated, err := rt.cfg.Store.UpdateMaintenance(*existing)
	if err != nil {
		writeInternalError(w, r, "status operation failed", err)
		return
	}

	rt.cfg.SSE.Broadcast(model.SSEEvent{Type: "maintenance_update", Data: updated})
	if rt.cfg.Notifier != nil {
		if page, err := rt.cfg.Store.GetStatusPageByID(updated.StatusPageID); err == nil {
			mCopy := *updated
			pCopy := *page
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				rt.cfg.Notifier.NotifyMaintenanceUpdate(ctx, pCopy, mCopy)
			}()
		}
	}
	writeJSON(w, http.StatusOK, updated)
}

func (rt *router) handleDeleteMaintenance(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := rt.cfg.Store.DeleteMaintenance(id); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "maintenance not found")
			return
		}
		writeInternalError(w, r, "status operation failed", err)
		return
	}
	rt.cfg.SSE.Broadcast(model.SSEEvent{Type: "maintenance_update", Data: map[string]string{"deleted_id": id}})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// Notifications and Subscribers Admin CRUD.

func (rt *router) handleListNotifications(w http.ResponseWriter, r *http.Request) {
	list, err := rt.cfg.Store.ListNotificationChannels()
	if err != nil {
		writeInternalError(w, r, "list notifications failed", err)
		return
	}
	for i := range list {
		list[i] = redactNotificationChannel(list[i])
	}
	writeJSON(w, http.StatusOK, list)
}

func (rt *router) handleCreateNotification(w http.ResponseWriter, r *http.Request) {
	var req model.NotificationUpsertRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || !isValidNotificationType(req.Type) {
		writeError(w, http.StatusBadRequest, "valid name and notification type are required")
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	ch, err := rt.cfg.Store.CreateNotificationChannel(model.NotificationChannel{
		Name:      req.Name,
		Type:      req.Type,
		Config:    req.Config,
		IsDefault: req.IsDefault,
		Enabled:   enabled,
	})
	if err != nil {
		writeInternalError(w, r, "create notification failed", err)
		return
	}
	redacted := redactNotificationChannel(*ch)
	writeJSON(w, http.StatusCreated, &redacted)
}

func (rt *router) handleUpdateNotification(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	existing, err := rt.cfg.Store.GetNotificationChannel(id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "notification channel not found")
			return
		}
		writeInternalError(w, r, "get notification failed", err)
		return
	}
	var req model.NotificationUpsertRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.Name) != "" {
		existing.Name = strings.TrimSpace(req.Name)
	}
	if req.Type != "" {
		if !isValidNotificationType(req.Type) {
			writeError(w, http.StatusBadRequest, "invalid notification type")
			return
		}
		existing.Type = req.Type
	}
	if len(req.Config) > 0 && string(req.Config) != "null" {
		existing.Config = mergeNotificationSecrets(existing.Config, req.Config)
	}
	existing.IsDefault = req.IsDefault
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}
	updated, err := rt.cfg.Store.UpdateNotificationChannel(*existing)
	if err != nil {
		writeInternalError(w, r, "update notification failed", err)
		return
	}
	redacted := redactNotificationChannel(*updated)
	writeJSON(w, http.StatusOK, &redacted)
}

func (rt *router) handleDeleteNotification(w http.ResponseWriter, r *http.Request) {
	if err := rt.cfg.Store.DeleteNotificationChannel(r.PathValue("id")); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "notification channel not found")
			return
		}
		writeInternalError(w, r, "status operation failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (rt *router) handleTestNotification(w http.ResponseWriter, r *http.Request) {
	ch, err := rt.cfg.Store.GetNotificationChannel(r.PathValue("id"))
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "notification channel not found")
			return
		}
		writeInternalError(w, r, "status operation failed", err)
		return
	}
	if rt.cfg.Notifier == nil {
		writeError(w, http.StatusServiceUnavailable, "notifier unavailable")
		return
	}
	if err := rt.cfg.Notifier.TestChannel(r.Context(), *ch); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (rt *router) handleListSubscribers(w http.ResponseWriter, r *http.Request) {
	pageID := r.URL.Query().Get("status_page_id")
	subs, err := rt.cfg.Store.ListSubscribers(pageID)
	if err != nil {
		writeInternalError(w, r, "status operation failed", err)
		return
	}
	writeJSON(w, http.StatusOK, subs)
}

func (rt *router) handleDeleteSubscriber(w http.ResponseWriter, r *http.Request) {
	if err := rt.cfg.Store.DeleteSubscriber(r.PathValue("id")); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "subscriber not found")
			return
		}
		writeInternalError(w, r, "status operation failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (rt *router) resolvePublicPage(r *http.Request, slug string) (*model.StatusPage, error) {
	var page *model.StatusPage
	var err error
	if slug == "" || slug == "default" {
		page, err = rt.cfg.Store.GetDefaultStatusPage()
		if err != nil && slug != "" {
			page, err = rt.cfg.Store.GetStatusPageBySlug(slug)
		}
	} else {
		page, err = rt.cfg.Store.GetStatusPageBySlug(slug)
	}
	if err != nil {
		return nil, err
	}
	if !page.IsPublic {
		if _, ok := rt.cfg.Auth.AuthenticateRequest(r); !ok {
			return nil, db.ErrNotFound
		}
	}
	return page, nil
}

func isValidNotificationType(t model.NotificationType) bool {
	switch t {
	case model.NotificationTypeWebhook, model.NotificationTypeDiscord, model.NotificationTypeSlack,
		model.NotificationTypeTelegram, model.NotificationTypeEmail, model.NotificationTypeNtfy:
		return true
	default:
		return false
	}
}

func containsString(list []string, target string) bool {
	for _, item := range list {
		if item == target {
			return true
		}
	}
	return false
}

func (rt *router) validateSubscribeWebhook(ctx context.Context, raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("valid https webhook URL is required")
	}
	if !strings.EqualFold(u.Scheme, "https") || u.User != nil || u.Host == "" {
		return errors.New("webhook URL must use https without embedded credentials")
	}
	if port := u.Port(); port != "" && port != "443" {
		return errors.New("webhook URL must use standard HTTPS port")
	}
	host := strings.TrimSpace(u.Hostname())
	if host == "" {
		return errors.New("webhook URL hostname is required")
	}
	lowerHost := strings.ToLower(host)
	if lowerHost == "localhost" || strings.HasSuffix(lowerHost, ".localhost") ||
		strings.HasSuffix(lowerHost, ".local") || strings.HasSuffix(lowerHost, ".internal") {
		return errors.New("webhook host is not allowed")
	}
	if ip := net.ParseIP(host); ip != nil {
		if !isPublicRoutableIP(ip) {
			return errors.New("webhook target IP is not allowed")
		}
		return nil
	}
	if isNumericHostTrick(lowerHost) {
		return errors.New("webhook hostname format is not allowed")
	}

	lookup := rt.lookupIP
	if lookup == nil {
		lookup = net.DefaultResolver.LookupIP
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	ips, err := lookup(lookupCtx, "ip", host)
	if err != nil || len(ips) == 0 {
		return errors.New("webhook host could not be resolved")
	}
	for _, ip := range ips {
		if !isPublicRoutableIP(ip) {
			return errors.New("webhook target IP is not allowed")
		}
	}
	return nil
}

func isNumericHostTrick(host string) bool {
	if strings.HasPrefix(host, "0x") {
		return true
	}
	allDigits := len(host) > 0
	for _, r := range host {
		if r < '0' || r > '9' {
			allDigits = false
			break
		}
	}
	return allDigits
}

func isPublicRoutableIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
		if ip[0] == 0 {
			return false
		}
		// CGNAT 100.64.0.0/10.
		if ip[0] == 100 && ip[1] >= 64 && ip[1] <= 127 {
			return false
		}
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	// Explicit IPv6 ULA fc00::/7 check.
	if len(ip) == net.IPv6len && ip.To4() == nil && (ip[0]&0xfe) == 0xfc {
		return false
	}
	return true
}

var notificationSecretKeys = []string{"password", "bot_token", "secret", "token"}

func redactNotificationChannel(ch model.NotificationChannel) model.NotificationChannel {
	var m map[string]any
	if err := json.Unmarshal(ch.Config, &m); err != nil || m == nil {
		return ch
	}
	changed := false
	for _, k := range notificationSecretKeys {
		if v, ok := m[k].(string); ok && v != "" {
			m[k] = "********"
			changed = true
		}
	}
	if !changed {
		return ch
	}
	if b, err := json.Marshal(m); err == nil {
		ch.Config = b
	}
	return ch
}

func mergeNotificationSecrets(oldRaw, newRaw json.RawMessage) json.RawMessage {
	var oldMap, newMap map[string]any
	if err := json.Unmarshal(newRaw, &newMap); err != nil || newMap == nil {
		return newRaw
	}
	if err := json.Unmarshal(oldRaw, &oldMap); err != nil || oldMap == nil {
		return newRaw
	}
	for _, k := range notificationSecretKeys {
		oldVal, hasOld := oldMap[k].(string)
		if !hasOld || oldVal == "" {
			continue
		}
		newVal, hasNew := newMap[k].(string)
		if !hasNew || newVal == "" || newVal == "********" {
			newMap[k] = oldVal
		}
	}
	if b, err := json.Marshal(newMap); err == nil {
		return b
	}
	return newRaw
}
