package api

import (
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"time"

	"exa/internal/model"
)

func (rt *router) handleOverview(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	_ = rt.cfg.Store.SyncMaintenanceStatuses(now)

	monitors, err := rt.cfg.Store.ListMonitors()
	if err != nil {
		writeInternalError(w, r, "overview query failed", err)
		return
	}
	groups, err := rt.cfg.Store.ListGroups()
	if err != nil {
		writeInternalError(w, r, "overview query failed", err)
		return
	}
	groupNames := make(map[string]string, len(groups))
	for _, g := range groups {
		groupNames[g.ID] = g.Name
	}

	activeIncidents, err := rt.cfg.Store.ListActiveIncidents("")
	if err != nil {
		writeInternalError(w, r, "overview query failed", err)
		return
	}
	recentIncidents, err := rt.cfg.Store.ListIncidents("", 30, 0)
	if err != nil {
		writeInternalError(w, r, "overview query failed", err)
		return
	}
	maintenances, err := rt.cfg.Store.ListMaintenances("")
	if err != nil {
		writeInternalError(w, r, "overview query failed", err)
		return
	}

	var defaultSlug, defaultTitle string
	if defPage, err := rt.cfg.Store.GetDefaultStatusPage(); err == nil && defPage != nil {
		defaultSlug = defPage.Slug
		defaultTitle = defPage.Title
	}

	todayUTC := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	from30d := todayUTC.AddDate(0, 0, -29).Format("2006-01-02")
	to30d := todayUTC.Format("2006-01-02")
	rollups30d, err := rt.cfg.Store.ListAllDailyRollups(from30d, to30d)
	if err != nil {
		writeInternalError(w, r, "overview query failed", err)
		return
	}

	type monAgg struct {
		up    int
		total int
	}
	mon30d := make(map[string]*monAgg)
	var total30d, up30d, downtime30d int
	for _, ru := range rollups30d {
		agg := mon30d[ru.MonitorID]
		if agg == nil {
			agg = &monAgg{}
			mon30d[ru.MonitorID] = agg
		}
		good := ru.UpChecks + ru.DegradedChecks
		agg.up += good
		agg.total += ru.TotalChecks
		total30d += ru.TotalChecks
		up30d += good
		downtime30d += ru.DowntimeSec
	}

	var uptime30d *float64
	if total30d > 0 {
		v := round2(float64(up30d) / float64(total30d) * 100.0)
		uptime30d = &v
	}

	since48h := now.Add(-48 * time.Hour)
	since24h := now.Add(-24 * time.Hour)
	hbs48h, err := rt.cfg.Store.ListAllHeartbeatsSince(since48h, 50000)
	if err != nil {
		writeInternalError(w, r, "overview query failed", err)
		return
	}

	var lat24h, latPrev24h []int
	var total24h, up24h int
	hbs24hByMon := make(map[string][]model.Heartbeat)
	hbs48hByMon := make(map[string][]model.Heartbeat)

	// Bucket heartbeats into 60 x 24-min availability buckets and 48 x 30-min latency buckets.
	type availAcc struct {
		total    int
		up       int
		degraded int
		down     int
	}
	availAccs := make([]availAcc, 60)
	latBuckets := make([][]int, 48)

	for _, hb := range hbs48h {
		hbs48hByMon[hb.MonitorID] = append(hbs48hByMon[hb.MonitorID], hb)
		if hb.CheckedAt.Before(since24h) {
			latPrev24h = append(latPrev24h, hb.LatencyMs)
			continue
		}
		hbs24hByMon[hb.MonitorID] = append(hbs24hByMon[hb.MonitorID], hb)
		lat24h = append(lat24h, hb.LatencyMs)
		total24h++
		if hb.Status == model.HeartbeatStatusUp || hb.Status == model.HeartbeatStatusDegraded {
			up24h++
		}

		offset := hb.CheckedAt.Sub(since24h)
		if offset >= 0 {
			aIdx := int(offset / (24 * time.Minute))
			if aIdx >= 60 {
				aIdx = 59
			}
			availAccs[aIdx].total++
			switch hb.Status {
			case model.HeartbeatStatusUp:
				availAccs[aIdx].up++
			case model.HeartbeatStatusDegraded:
				availAccs[aIdx].degraded++
			case model.HeartbeatStatusDown:
				availAccs[aIdx].down++
			}

			lIdx := int(offset / (30 * time.Minute))
			if lIdx >= 48 {
				lIdx = 47
			}
			latBuckets[lIdx] = append(latBuckets[lIdx], hb.LatencyMs)
		}
	}

	availSeries := make([]model.OverviewBucket, 60)
	for i := 0; i < 60; i++ {
		acc := availAccs[i]
		b := model.OverviewBucket{
			Timestamp:   since24h.Add(time.Duration(i) * 24 * time.Minute),
			TotalChecks: acc.total,
			DownChecks:  acc.down,
		}
		if acc.total == 0 {
			b.Status = "no_data"
		} else {
			good := acc.up + acc.degraded
			pct := round2(float64(good) / float64(acc.total) * 100.0)
			b.UptimePct = &pct
			switch {
			case acc.down > 0 && (good == 0 || pct < 95.0):
				b.Status = "down"
			case acc.down > 0 || acc.degraded > 0:
				b.Status = "degraded"
			default:
				b.Status = "up"
			}
		}
		availSeries[i] = b
	}

	respSeries := make([]model.ResponsePoint, 48)
	for i := 0; i < 48; i++ {
		pt := model.ResponsePoint{
			Timestamp: since24h.Add(time.Duration(i) * 30 * time.Minute),
		}
		if len(latBuckets[i]) > 0 {
			med := medianInt(latBuckets[i])
			pt.MedianMs = &med
		}
		respSeries[i] = pt
	}

	var median24h, medianPrev24h *int
	if len(lat24h) > 0 {
		m := medianInt(lat24h)
		median24h = &m
	}
	if len(latPrev24h) > 0 {
		m := medianInt(latPrev24h)
		medianPrev24h = &m
	}

	var avail24hPct *float64
	if total24h > 0 {
		v := round2(float64(up24h) / float64(total24h) * 100.0)
		avail24hPct = &v
	}

	// Build monitor summaries, active alerts, TLS certificates, and state transitions.
	var opCount, degCount, downCount, pausedCount, maintCount int
	overviewMonitors := make([]model.OverviewMonitor, 0, len(monitors))
	activeAlerts := []model.OverviewAlert{}
	tlsCerts := []model.TLSCertSummary{}
	activity := []model.OverviewActivity{}

	for _, m := range monitors {
		if !m.Active {
			pausedCount++
		} else {
			switch m.CurrentStatus {
			case model.ComponentStatusOperational:
				opCount++
			case model.ComponentStatusDegraded:
				degCount++
			case model.ComponentStatusMajorOutage:
				downCount++
			case model.ComponentStatusMaintenance:
				maintCount++
			}
		}

		var gName string
		if m.GroupID != nil {
			gName = groupNames[*m.GroupID]
		}

		var u24h *float64
		if h24 := hbs24hByMon[m.ID]; len(h24) > 0 {
			var good24 int
			for _, h := range h24 {
				if h.Status == model.HeartbeatStatusUp || h.Status == model.HeartbeatStatusDegraded {
					good24++
				}
			}
			v := round2(float64(good24) / float64(len(h24)) * 100.0)
			u24h = &v
		}

		var u30d *float64
		if agg := mon30d[m.ID]; agg != nil && agg.total > 0 {
			v := round2(float64(agg.up) / float64(agg.total) * 100.0)
			u30d = &v
		}

		recent := hbs48hByMon[m.ID]
		if len(recent) == 0 {
			recent, _ = rt.cfg.Store.ListHeartbeats(m.ID, 30)
		}
		if len(recent) > 30 {
			recent = recent[:30]
		}
		recentAsc := reverseHeartbeats(recent)

		monCopy := m
		if !isAdminCaller(r) {
			monCopy.PushToken = ""
		}

		overviewMonitors = append(overviewMonitors, model.OverviewMonitor{
			Monitor:      monCopy,
			GroupName:    gName,
			Uptime24h:    u24h,
			Uptime30d:    u30d,
			RecentChecks: recentAsc,
		})

		if m.Active && (m.CurrentStatus == model.ComponentStatusMajorOutage || m.CurrentStatus == model.ComponentStatusDegraded) {
			sincePtr := findStatusSince(hbs48hByMon[m.ID], m.CurrentStatus, m.LastCheckedAt, m.UpdatedAt)
			incID := ""
			for _, inc := range activeIncidents {
				if containsString(inc.MonitorIDs, m.ID) {
					incID = inc.ID
					break
				}
			}
			activeAlerts = append(activeAlerts, model.OverviewAlert{
				MonitorID:     m.ID,
				MonitorName:   m.Name,
				Target:        m.Target,
				CurrentStatus: m.CurrentStatus,
				LastError:     m.LastError,
				Since:         sincePtr,
				IncidentID:    incID,
			})
		}

		if m.TLSDaysLeft != nil {
			var httpCfg model.HTTPConfig
			_ = json.Unmarshal(m.Config, &httpCfg)
			httpCfg = httpCfg.WithDefaults()
			tlsCerts = append(tlsCerts, model.TLSCertSummary{
				MonitorID:    m.ID,
				Name:         m.Name,
				Target:       m.Target,
				TLSDaysLeft:  *m.TLSDaysLeft,
				TLSExpiresAt: m.TLSExpiresAt,
				WarnDays:     httpCfg.TLSExpiryWarnDays,
			})
		}

		// Record state transitions within the last 24h for the activity feed.
		h24Asc := reverseHeartbeats(hbs24hByMon[m.ID])
		for i := 1; i < len(h24Asc); i++ {
			prev := h24Asc[i-1]
			curr := h24Asc[i]
			if prev.Status == curr.Status {
				continue
			}
			tone := "neutral"
			title := m.Name + " status changed"
			switch curr.Status {
			case model.HeartbeatStatusUp:
				tone = "up"
				title = m.Name + " recovered"
			case model.HeartbeatStatusDegraded:
				tone = "degraded"
				title = m.Name + " degraded"
			case model.HeartbeatStatusDown:
				tone = "down"
				title = m.Name + " went down"
			}
			detail := curr.Message
			if detail == "" {
				detail = string(prev.Status) + " -> " + string(curr.Status)
			}
			activity = append(activity, model.OverviewActivity{
				Timestamp: curr.CheckedAt,
				Tone:      tone,
				Title:     title,
				Detail:    detail,
				MonitorID: m.ID,
			})
		}
	}

	sort.Slice(tlsCerts, func(i, j int) bool {
		return tlsCerts[i].TLSDaysLeft < tlsCerts[j].TLSDaysLeft
	})

	for _, inc := range recentIncidents {
		if len(inc.Updates) > 0 {
			for _, upd := range inc.Updates {
				tone := incidentTone(upd.Status, inc.Impact)
				activity = append(activity, model.OverviewActivity{
					Timestamp:  upd.CreatedAt,
					Tone:       tone,
					Title:      inc.Title,
					Detail:     string(upd.Status) + ": " + upd.Message,
					IncidentID: inc.ID,
				})
			}
		} else {
			activity = append(activity, model.OverviewActivity{
				Timestamp:  inc.CreatedAt,
				Tone:       incidentTone(inc.Status, inc.Impact),
				Title:      inc.Title,
				Detail:     string(inc.Status),
				IncidentID: inc.ID,
			})
		}
	}

	for _, maint := range maintenances {
		activity = append(activity, model.OverviewActivity{
			Timestamp: maint.CreatedAt,
			Tone:      "maint",
			Title:     "Maintenance: " + maint.Title,
			Detail:    string(maint.Status),
		})
	}

	sort.Slice(activity, func(i, j int) bool {
		return activity[i].Timestamp.After(activity[j].Timestamp)
	})
	if len(activity) > 30 {
		activity = activity[:30]
	}

	writeJSON(w, http.StatusOK, model.AdminOverviewResponse{
		RefreshedAt:             now,
		TotalMonitors:           len(monitors),
		OperationalMonitors:     opCount,
		DegradedMonitors:        degCount,
		DownMonitors:            downCount,
		PausedMonitors:          pausedCount,
		MaintenanceMonitors:     maintCount,
		Uptime30d:               uptime30d,
		Downtime30dSec:          downtime30d,
		MedianResponse24hMs:     median24h,
		MedianResponsePrev24hMs: medianPrev24h,
		OpenIncidentsCount:      len(activeIncidents),
		Availability24hPct:      avail24hPct,
		Availability24h:         availSeries,
		ResponseSeries24h:       respSeries,
		ActiveAlerts:            activeAlerts,
		Monitors:                overviewMonitors,
		Activity:                activity,
		TLSCertificates:         tlsCerts,
		DefaultPageSlug:         defaultSlug,
		DefaultPageTitle:        defaultTitle,
		BaseURL:                 rt.resolveBaseURL(r),
	})
}

func incidentTone(st model.IncidentStatus, impact model.IncidentImpact) string {
	if st == model.IncidentStatusResolved {
		return "up"
	}
	switch impact {
	case model.IncidentImpactCritical, model.IncidentImpactMajor:
		return "down"
	case model.IncidentImpactMinor:
		return "degraded"
	default:
		return "neutral"
	}
}

func findStatusSince(hbsDesc []model.Heartbeat, current model.ComponentStatus, lastChecked *time.Time, updatedAt time.Time) *time.Time {
	if current == model.ComponentStatusMaintenance || current == model.ComponentStatusUnknown {
		t := updatedAt
		return &t
	}
	if len(hbsDesc) == 0 {
		if lastChecked != nil {
			t := *lastChecked
			return &t
		}
		t := updatedAt
		return &t
	}
	since := hbsDesc[0].CheckedAt
	for _, hb := range hbsDesc {
		if heartbeatToComponent(hb.Status) != current {
			break
		}
		since = hb.CheckedAt
	}
	return &since
}

func heartbeatToComponent(st model.HeartbeatStatus) model.ComponentStatus {
	switch st {
	case model.HeartbeatStatusUp:
		return model.ComponentStatusOperational
	case model.HeartbeatStatusDegraded:
		return model.ComponentStatusDegraded
	case model.HeartbeatStatusDown:
		return model.ComponentStatusMajorOutage
	default:
		return model.ComponentStatusUnknown
	}
}

func reverseHeartbeats(hbs []model.Heartbeat) []model.Heartbeat {
	if len(hbs) == 0 {
		return []model.Heartbeat{}
	}
	out := make([]model.Heartbeat, len(hbs))
	for i := range hbs {
		out[i] = hbs[len(hbs)-1-i]
	}
	return out
}

func medianInt(vals []int) int {
	if len(vals) == 0 {
		return 0
	}
	cp := make([]int, len(vals))
	copy(cp, vals)
	sort.Ints(cp)
	mid := len(cp) / 2
	if len(cp)%2 == 1 {
		return cp[mid]
	}
	return (cp[mid-1] + cp[mid]) / 2
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}
