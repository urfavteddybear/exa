package db

import (
	"database/sql"
	"fmt"
	"time"

	"exa/internal/model"
)

const statusPageCols = `id, slug, title, description, custom_css, is_default, is_public, show_latency, created_at, updated_at`

func (s *Store) ListStatusPages() ([]model.StatusPage, error) {
	rows, err := s.db.Query(`SELECT ` + statusPageCols + ` FROM status_pages ORDER BY is_default DESC, created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	pages := []model.StatusPage{}
	for rows.Next() {
		p, err := scanStatusPage(rows)
		if err != nil {
			return nil, err
		}
		pages = append(pages, *p)
	}
	return pages, rows.Err()
}

func (s *Store) GetDefaultStatusPage() (*model.StatusPage, error) {
	p, err := scanStatusPage(s.db.QueryRow(`SELECT ` + statusPageCols + ` FROM status_pages ORDER BY is_default DESC, created_at ASC LIMIT 1`))
	if err != nil {
		return nil, mapErr(err)
	}
	return p, nil
}

func (s *Store) GetStatusPageByID(id string) (*model.StatusPage, error) {
	p, err := scanStatusPage(s.db.QueryRow(`SELECT `+statusPageCols+` FROM status_pages WHERE id = ?`, id))
	if err != nil {
		return nil, mapErr(err)
	}
	return p, nil
}

func (s *Store) GetStatusPageBySlug(slug string) (*model.StatusPage, error) {
	p, err := scanStatusPage(s.db.QueryRow(`SELECT `+statusPageCols+` FROM status_pages WHERE slug = ?`, slug))
	if err != nil {
		return nil, mapErr(err)
	}
	return p, nil
}

func (s *Store) CreateStatusPage(p model.StatusPage) (*model.StatusPage, error) {
	now := time.Now().UTC()
	if p.ID == "" {
		p.ID = newID()
	}
	if p.Slug == "" {
		p.Slug = slugify(p.Title, p.ID[:6])
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = now
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if p.IsDefault {
		if _, err := tx.Exec(`UPDATE status_pages SET is_default = 0`); err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(
		`INSERT INTO status_pages (id, slug, title, description, custom_css, is_default, is_public, show_latency, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Slug, p.Title, p.Description, p.CustomCSS,
		boolToInt(p.IsDefault), boolToInt(p.IsPublic), boolToInt(p.ShowLatency),
		formatTime(p.CreatedAt), formatTime(p.UpdatedAt),
	); err != nil {
		if isUniqueErr(err) {
			return nil, ErrConflict
		}
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *Store) UpdateStatusPage(p model.StatusPage) (*model.StatusPage, error) {
	p.UpdatedAt = time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if p.IsDefault {
		if _, err := tx.Exec(`UPDATE status_pages SET is_default = 0 WHERE id != ?`, p.ID); err != nil {
			return nil, err
		}
	}
	res, err := tx.Exec(
		`UPDATE status_pages SET slug = ?, title = ?, description = ?, custom_css = ?, is_default = ?, is_public = ?, show_latency = ?, updated_at = ?
		 WHERE id = ?`,
		p.Slug, p.Title, p.Description, p.CustomCSS,
		boolToInt(p.IsDefault), boolToInt(p.IsPublic), boolToInt(p.ShowLatency),
		formatTime(p.UpdatedAt), p.ID,
	)
	if err != nil {
		if isUniqueErr(err) {
			return nil, ErrConflict
		}
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetStatusPageByID(p.ID)
}

func (s *Store) DeleteStatusPage(id string) error {
	res, err := s.db.Exec(`DELETE FROM status_pages WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetStatusPageMonitors(pageID string, items []model.StatusPageMonitor) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`DELETE FROM status_page_monitors WHERE status_page_id = ?`, pageID); err != nil {
		return err
	}
	for i, item := range items {
		order := item.SortOrder
		if order == 0 && i > 0 {
			order = i
		}
		if _, err := tx.Exec(
			`INSERT INTO status_page_monitors (status_page_id, monitor_id, group_id, display_name, sort_order)
			 VALUES (?, ?, ?, ?, ?)`,
			pageID, item.MonitorID, nullableString(item.GroupID), item.DisplayName, order,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) IsMonitorOnPublicStatusPage(monitorID string) (bool, error) {
	var count int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM status_page_monitors spm
		 JOIN status_pages sp ON sp.id = spm.status_page_id
		 WHERE spm.monitor_id = ? AND sp.is_public = 1`,
		monitorID,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *Store) ListPublicPagesForMonitor(monitorID string) ([]model.PublicMonitorPageBinding, error) {
	rows, err := s.db.Query(
		`SELECT sp.id, sp.slug, sp.title, sp.description, sp.custom_css, sp.is_default, sp.is_public, sp.show_latency, sp.created_at, sp.updated_at, spm.display_name
		 FROM status_page_monitors spm
		 JOIN status_pages sp ON sp.id = spm.status_page_id
		 WHERE spm.monitor_id = ? AND sp.is_public = 1
		 ORDER BY sp.is_default DESC, sp.created_at ASC`,
		monitorID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.PublicMonitorPageBinding{}
	for rows.Next() {
		var p model.StatusPage
		var isDef, isPub, showLat int
		var createdStr, updatedStr, displayName string
		if err := rows.Scan(
			&p.ID, &p.Slug, &p.Title, &p.Description, &p.CustomCSS,
			&isDef, &isPub, &showLat, &createdStr, &updatedStr, &displayName,
		); err != nil {
			return nil, err
		}
		p.IsDefault = isDef == 1
		p.IsPublic = isPub == 1
		p.ShowLatency = showLat == 1
		p.CreatedAt = parseTime(createdStr)
		p.UpdatedAt = parseTime(updatedStr)
		out = append(out, model.PublicMonitorPageBinding{
			Page:        p,
			DisplayName: displayName,
		})
	}
	return out, rows.Err()
}

func (s *Store) IsMonitorOnStatusPage(pageID, monitorID string) (bool, error) {
	var count int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM status_page_monitors WHERE status_page_id = ? AND monitor_id = ?`,
		pageID, monitorID,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *Store) ListStatusPageMonitors(pageID string) ([]model.StatusPageMonitor, error) {
	rows, err := s.db.Query(
		`SELECT status_page_id, monitor_id, group_id, display_name, sort_order
		 FROM status_page_monitors WHERE status_page_id = ? ORDER BY sort_order ASC`,
		pageID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.StatusPageMonitor{}
	for rows.Next() {
		var spm model.StatusPageMonitor
		var groupID sql.NullString
		if err := rows.Scan(&spm.StatusPageID, &spm.MonitorID, &groupID, &spm.DisplayName, &spm.SortOrder); err != nil {
			return nil, err
		}
		if groupID.Valid {
			v := groupID.String
			spm.GroupID = &v
		}
		out = append(out, spm)
	}
	return out, rows.Err()
}

func (s *Store) ListStatusPageIDsForMonitor(monitorID string) ([]string, error) {
	rows, err := s.db.Query(`SELECT status_page_id FROM status_page_monitors WHERE monitor_id = ?`, monitorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) AttachMonitorToDefaultPage(monitorID string, groupID *string, sortOrder int) error {
	page, err := s.GetDefaultStatusPage()
	if err != nil {
		if err == ErrNotFound {
			return nil
		}
		return err
	}
	_, err = s.db.Exec(
		`INSERT OR IGNORE INTO status_page_monitors (status_page_id, monitor_id, group_id, display_name, sort_order)
		 VALUES (?, ?, ?, '', ?)`,
		page.ID, monitorID, nullableString(groupID), sortOrder,
	)
	return err
}

func (s *Store) AttachMonitorToPages(monitorID string, pageIDs []string, groupID *string, sortOrder int) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`DELETE FROM status_page_monitors WHERE monitor_id = ?`, monitorID); err != nil {
		return err
	}
	for _, pid := range pageIDs {
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO status_page_monitors (status_page_id, monitor_id, group_id, display_name, sort_order)
			 VALUES (?, ?, ?, '', ?)`,
			pid, monitorID, nullableString(groupID), sortOrder,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Incidents.

func (s *Store) ListIncidents(pageID string, limit, offset int) ([]model.Incident, error) {
	if limit <= 0 {
		limit = 20
	}
	query := `SELECT id, status_page_id, title, impact, status, created_at, resolved_at, updated_at FROM incidents`
	var args []any
	if pageID != "" {
		query += ` WHERE status_page_id = ?`
		args = append(args, pageID)
	}
	query += ` ORDER BY created_at DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	return s.queryIncidents(query, args...)
}

func (s *Store) ListIncidentsForMonitor(monitorID string, limit int) ([]model.Incident, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.queryIncidents(
		`SELECT i.id, i.status_page_id, i.title, i.impact, i.status, i.created_at, i.resolved_at, i.updated_at
		 FROM incidents i
		 JOIN incident_monitors im ON im.incident_id = i.id
		 WHERE im.monitor_id = ?
		 ORDER BY i.created_at DESC
		 LIMIT ?`,
		monitorID, limit,
	)
}

func (s *Store) CountIncidents(pageID string) (int, error) {
	query := `SELECT COUNT(*) FROM incidents`
	var args []any
	if pageID != "" {
		query += ` WHERE status_page_id = ?`
		args = append(args, pageID)
	}
	var count int
	if err := s.db.QueryRow(query, args...).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) ListActiveIncidents(pageID string) ([]model.Incident, error) {
	query := `SELECT id, status_page_id, title, impact, status, created_at, resolved_at, updated_at
		FROM incidents WHERE status != 'resolved'`
	var args []any
	if pageID != "" {
		query += ` AND status_page_id = ?`
		args = append(args, pageID)
	}
	query += ` ORDER BY created_at DESC`
	return s.queryIncidents(query, args...)
}

func (s *Store) ListIncidentsInRange(pageID string, fromUTC, toUTC time.Time) ([]model.Incident, error) {
	query := `SELECT id, status_page_id, title, impact, status, created_at, resolved_at, updated_at
		FROM incidents WHERE created_at >= ? AND created_at <= ?`
	args := []any{formatTime(fromUTC), formatTime(toUTC)}
	if pageID != "" {
		query += ` AND status_page_id = ?`
		args = append(args, pageID)
	}
	query += ` ORDER BY created_at DESC`
	return s.queryIncidents(query, args...)
}

func (s *Store) GetIncidentByID(id string) (*model.Incident, error) {
	incs, err := s.queryIncidents(
		`SELECT id, status_page_id, title, impact, status, created_at, resolved_at, updated_at FROM incidents WHERE id = ?`,
		id,
	)
	if err != nil {
		return nil, err
	}
	if len(incs) == 0 {
		return nil, ErrNotFound
	}
	return &incs[0], nil
}

func (s *Store) CreateIncident(inc model.Incident, initialMessage string) (*model.Incident, error) {
	now := time.Now().UTC()
	if inc.ID == "" {
		inc.ID = newID()
	}
	if inc.Impact == "" {
		inc.Impact = model.IncidentImpactMinor
	}
	if inc.Status == "" {
		inc.Status = model.IncidentStatusInvestigating
	}
	if inc.CreatedAt.IsZero() {
		inc.CreatedAt = now
	}
	if inc.UpdatedAt.IsZero() {
		inc.UpdatedAt = inc.CreatedAt
	}
	if inc.Status == model.IncidentStatusResolved && inc.ResolvedAt == nil {
		t := inc.UpdatedAt
		inc.ResolvedAt = &t
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(
		`INSERT INTO incidents (id, status_page_id, title, impact, status, created_at, resolved_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		inc.ID, inc.StatusPageID, inc.Title, string(inc.Impact), string(inc.Status),
		formatTime(inc.CreatedAt), formatNullTime(inc.ResolvedAt), formatTime(inc.UpdatedAt),
	); err != nil {
		return nil, err
	}

	if initialMessage != "" {
		upd := model.IncidentUpdate{
			ID:         newID(),
			IncidentID: inc.ID,
			Status:     inc.Status,
			Message:    initialMessage,
			CreatedAt:  inc.CreatedAt,
		}
		if _, err := tx.Exec(
			`INSERT INTO incident_updates (id, incident_id, status, message, created_at) VALUES (?, ?, ?, ?, ?)`,
			upd.ID, upd.IncidentID, string(upd.Status), upd.Message, formatTime(upd.CreatedAt),
		); err != nil {
			return nil, err
		}
	}

	for _, mid := range inc.MonitorIDs {
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO incident_monitors (incident_id, monitor_id) VALUES (?, ?)`,
			inc.ID, mid,
		); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetIncidentByID(inc.ID)
}

func (s *Store) AddIncidentUpdate(incidentID string, status model.IncidentStatus, message string) (*model.Incident, *model.IncidentUpdate, error) {
	now := time.Now().UTC()
	nowStr := formatTime(now)

	tx, err := s.db.Begin()
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var exists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM incidents WHERE id = ?`, incidentID).Scan(&exists); err != nil {
		return nil, nil, err
	}
	if exists == 0 {
		return nil, nil, ErrNotFound
	}

	upd := model.IncidentUpdate{
		ID:         newID(),
		IncidentID: incidentID,
		Status:     status,
		Message:    message,
		CreatedAt:  now,
	}
	if _, err := tx.Exec(
		`INSERT INTO incident_updates (id, incident_id, status, message, created_at) VALUES (?, ?, ?, ?, ?)`,
		upd.ID, upd.IncidentID, string(upd.Status), upd.Message, nowStr,
	); err != nil {
		return nil, nil, err
	}

	var resolvedAt *time.Time
	if status == model.IncidentStatusResolved {
		resolvedAt = &now
	}
	if _, err := tx.Exec(
		`UPDATE incidents SET status = ?, resolved_at = ?, updated_at = ? WHERE id = ?`,
		string(status), formatNullTime(resolvedAt), nowStr, incidentID,
	); err != nil {
		return nil, nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	inc, err := s.GetIncidentByID(incidentID)
	if err != nil {
		return nil, nil, err
	}
	return inc, &upd, nil
}

func (s *Store) DeleteIncident(id string) error {
	res, err := s.db.Exec(`DELETE FROM incidents WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) queryIncidents(query string, args ...any) ([]model.Incident, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	incs := []model.Incident{}
	for rows.Next() {
		var inc model.Incident
		var impact, status, createdStr, updatedStr string
		var resolvedStr sql.NullString
		if err := rows.Scan(&inc.ID, &inc.StatusPageID, &inc.Title, &impact, &status, &createdStr, &resolvedStr, &updatedStr); err != nil {
			return nil, err
		}
		inc.Impact = model.IncidentImpact(impact)
		inc.Status = model.IncidentStatus(status)
		inc.CreatedAt = parseTime(createdStr)
		inc.ResolvedAt = parseNullTime(resolvedStr)
		inc.UpdatedAt = parseTime(updatedStr)
		inc.Updates = []model.IncidentUpdate{}
		inc.MonitorIDs = []string{}
		incs = append(incs, inc)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range incs {
		updates, err := s.listIncidentUpdates(incs[i].ID)
		if err != nil {
			return nil, err
		}
		incs[i].Updates = updates

		mids, err := s.listIncidentMonitorIDs(incs[i].ID)
		if err != nil {
			return nil, err
		}
		incs[i].MonitorIDs = mids
	}
	return incs, nil
}

func (s *Store) listIncidentUpdates(incidentID string) ([]model.IncidentUpdate, error) {
	rows, err := s.db.Query(
		`SELECT id, incident_id, status, message, created_at
		 FROM incident_updates WHERE incident_id = ? ORDER BY created_at DESC`,
		incidentID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.IncidentUpdate{}
	for rows.Next() {
		var u model.IncidentUpdate
		var status, createdStr string
		if err := rows.Scan(&u.ID, &u.IncidentID, &status, &u.Message, &createdStr); err != nil {
			return nil, err
		}
		u.Status = model.IncidentStatus(status)
		u.CreatedAt = parseTime(createdStr)
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) listIncidentMonitorIDs(incidentID string) ([]string, error) {
	rows, err := s.db.Query(`SELECT monitor_id FROM incident_monitors WHERE incident_id = ?`, incidentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Maintenances.

func (s *Store) ListMaintenances(pageID string) ([]model.Maintenance, error) {
	query := `SELECT id, status_page_id, title, description, status, scheduled_start, scheduled_end, auto_suppress_alerts, created_at FROM maintenances`
	var args []any
	if pageID != "" {
		query += ` WHERE status_page_id = ?`
		args = append(args, pageID)
	}
	query += ` ORDER BY scheduled_start DESC`
	return s.queryMaintenances(query, args...)
}

func (s *Store) ListMaintenancesForPage(pageID string) ([]model.Maintenance, error) {
	return s.ListMaintenances(pageID)
}

func (s *Store) GetMaintenanceByID(id string) (*model.Maintenance, error) {
	list, err := s.queryMaintenances(
		`SELECT id, status_page_id, title, description, status, scheduled_start, scheduled_end, auto_suppress_alerts, created_at
		 FROM maintenances WHERE id = ?`,
		id,
	)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrNotFound
	}
	return &list[0], nil
}

func (s *Store) CreateMaintenance(m model.Maintenance) (*model.Maintenance, error) {
	now := time.Now().UTC()
	if m.ID == "" {
		m.ID = newID()
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = now
	}
	m.Status = deriveMaintenanceStatus(now, m.ScheduledStart, m.ScheduledEnd)

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(
		`INSERT INTO maintenances (id, status_page_id, title, description, status, scheduled_start, scheduled_end, auto_suppress_alerts, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.StatusPageID, m.Title, m.Description, string(m.Status),
		formatTime(m.ScheduledStart), formatTime(m.ScheduledEnd),
		boolToInt(m.AutoSuppressAlerts), formatTime(m.CreatedAt),
	); err != nil {
		return nil, err
	}

	for _, mid := range m.MonitorIDs {
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO maintenance_monitors (maintenance_id, monitor_id) VALUES (?, ?)`,
			m.ID, mid,
		); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetMaintenanceByID(m.ID)
}

func (s *Store) UpdateMaintenance(m model.Maintenance) (*model.Maintenance, error) {
	now := time.Now().UTC()
	m.Status = deriveMaintenanceStatus(now, m.ScheduledStart, m.ScheduledEnd)

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.Exec(
		`UPDATE maintenances SET status_page_id = ?, title = ?, description = ?, status = ?, scheduled_start = ?, scheduled_end = ?, auto_suppress_alerts = ?
		 WHERE id = ?`,
		m.StatusPageID, m.Title, m.Description, string(m.Status),
		formatTime(m.ScheduledStart), formatTime(m.ScheduledEnd),
		boolToInt(m.AutoSuppressAlerts), m.ID,
	)
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, ErrNotFound
	}

	if _, err := tx.Exec(`DELETE FROM maintenance_monitors WHERE maintenance_id = ?`, m.ID); err != nil {
		return nil, err
	}
	for _, mid := range m.MonitorIDs {
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO maintenance_monitors (maintenance_id, monitor_id) VALUES (?, ?)`,
			m.ID, mid,
		); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetMaintenanceByID(m.ID)
}

func (s *Store) DeleteMaintenance(id string) error {
	res, err := s.db.Exec(`DELETE FROM maintenances WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SyncMaintenanceStatuses(now time.Time) error {
	nowStr := formatTime(now)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(
		`UPDATE maintenances SET status = 'in_progress' WHERE status = 'scheduled' AND scheduled_start <= ? AND scheduled_end > ?`,
		nowStr, nowStr,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`UPDATE maintenances SET status = 'completed' WHERE status != 'completed' AND scheduled_end <= ?`,
		nowStr,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`UPDATE maintenances SET status = 'scheduled' WHERE status = 'in_progress' AND scheduled_start > ?`,
		nowStr,
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) IsMonitorInActiveMaintenance(monitorID string, now time.Time) (bool, bool, error) {
	nowStr := formatTime(now)
	rows, err := s.db.Query(
		`SELECT m.auto_suppress_alerts
		 FROM maintenances m
		 JOIN maintenance_monitors mm ON mm.maintenance_id = m.id
		 WHERE mm.monitor_id = ? AND m.scheduled_start <= ? AND m.scheduled_end > ? AND m.status != 'completed'`,
		monitorID, nowStr, nowStr,
	)
	if err != nil {
		return false, false, fmt.Errorf("query active maintenance: %w", err)
	}
	defer rows.Close()

	inMaint := false
	suppress := false
	for rows.Next() {
		inMaint = true
		var autoSuppress int
		if err := rows.Scan(&autoSuppress); err != nil {
			return false, false, err
		}
		if autoSuppress == 1 {
			suppress = true
		}
	}
	return inMaint, suppress, rows.Err()
}

func (s *Store) queryMaintenances(query string, args ...any) ([]model.Maintenance, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []model.Maintenance{}
	for rows.Next() {
		var m model.Maintenance
		var status, startStr, endStr, createdStr string
		var suppress int
		if err := rows.Scan(&m.ID, &m.StatusPageID, &m.Title, &m.Description, &status, &startStr, &endStr, &suppress, &createdStr); err != nil {
			return nil, err
		}
		m.Status = model.MaintenanceStatus(status)
		m.ScheduledStart = parseTime(startStr)
		m.ScheduledEnd = parseTime(endStr)
		m.AutoSuppressAlerts = suppress == 1
		m.CreatedAt = parseTime(createdStr)
		m.MonitorIDs = []string{}
		list = append(list, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range list {
		mids, err := s.listMaintenanceMonitorIDs(list[i].ID)
		if err != nil {
			return nil, err
		}
		list[i].MonitorIDs = mids
	}
	return list, nil
}

func (s *Store) listMaintenanceMonitorIDs(maintenanceID string) ([]string, error) {
	rows, err := s.db.Query(`SELECT monitor_id FROM maintenance_monitors WHERE maintenance_id = ?`, maintenanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func deriveMaintenanceStatus(now, start, end time.Time) model.MaintenanceStatus {
	if !end.After(now) {
		return model.MaintenanceStatusCompleted
	}
	if !start.After(now) {
		return model.MaintenanceStatusInProgress
	}
	return model.MaintenanceStatusScheduled
}

func scanStatusPage(row scanner) (*model.StatusPage, error) {
	var p model.StatusPage
	var isDef, isPub, showLat int
	var createdStr, updatedStr string
	if err := row.Scan(
		&p.ID, &p.Slug, &p.Title, &p.Description, &p.CustomCSS,
		&isDef, &isPub, &showLat, &createdStr, &updatedStr,
	); err != nil {
		return nil, err
	}
	p.IsDefault = isDef == 1
	p.IsPublic = isPub == 1
	p.ShowLatency = showLat == 1
	p.CreatedAt = parseTime(createdStr)
	p.UpdatedAt = parseTime(updatedStr)
	return &p, nil
}
