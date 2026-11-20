package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"exa/internal/model"
)

func (s *Store) ListGroups() ([]model.ComponentGroup, error) {
	rows, err := s.db.Query(
		`SELECT id, name, description, sort_order, collapsed_default, created_at
		 FROM component_groups ORDER BY sort_order ASC, created_at ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	groups := []model.ComponentGroup{}
	for rows.Next() {
		var g model.ComponentGroup
		var collapsed int
		var createdStr string
		if err := rows.Scan(&g.ID, &g.Name, &g.Description, &g.SortOrder, &collapsed, &createdStr); err != nil {
			return nil, err
		}
		g.CollapsedDefault = collapsed == 1
		g.CreatedAt = parseTime(createdStr)
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

func (s *Store) CreateGroup(g model.ComponentGroup) (*model.ComponentGroup, error) {
	if g.ID == "" {
		g.ID = newID()
	}
	if g.CreatedAt.IsZero() {
		g.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.Exec(
		`INSERT INTO component_groups (id, name, description, sort_order, collapsed_default, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		g.ID, g.Name, g.Description, g.SortOrder, boolToInt(g.CollapsedDefault), formatTime(g.CreatedAt),
	)
	if err != nil {
		return nil, err
	}
	return &g, nil
}

func (s *Store) UpdateGroup(g model.ComponentGroup) (*model.ComponentGroup, error) {
	res, err := s.db.Exec(
		`UPDATE component_groups SET name = ?, description = ?, sort_order = ?, collapsed_default = ? WHERE id = ?`,
		g.Name, g.Description, g.SortOrder, boolToInt(g.CollapsedDefault), g.ID,
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
	return &g, nil
}

func (s *Store) DeleteGroup(id string) error {
	res, err := s.db.Exec(`DELETE FROM component_groups WHERE id = ?`, id)
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

const monitorSelectCols = `id, name, slug, type, target, group_id, interval_sec, timeout_sec, retries, degraded_ms,
	active, sort_order, config_json, push_token, current_status, last_checked_at, last_latency_ms,
	tls_expires_at, tls_days_left, last_error, created_at, updated_at`

func (s *Store) ListMonitors() ([]model.Monitor, error) {
	return s.queryMonitors(`SELECT ` + monitorSelectCols + ` FROM monitors ORDER BY sort_order ASC, created_at ASC`)
}

func (s *Store) ListActiveMonitors() ([]model.Monitor, error) {
	return s.queryMonitors(`SELECT ` + monitorSelectCols + ` FROM monitors WHERE active = 1 ORDER BY sort_order ASC, created_at ASC`)
}

func (s *Store) queryMonitors(query string, args ...any) ([]model.Monitor, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	monitors := []model.Monitor{}
	for rows.Next() {
		m, err := scanMonitor(rows)
		if err != nil {
			return nil, err
		}
		monitors = append(monitors, *m)
	}
	return monitors, rows.Err()
}

func (s *Store) GetMonitorByID(id string) (*model.Monitor, error) {
	m, err := scanMonitor(s.db.QueryRow(`SELECT `+monitorSelectCols+` FROM monitors WHERE id = ?`, id))
	if err != nil {
		return nil, mapErr(err)
	}
	return m, nil
}

func (s *Store) GetMonitorBySlug(slug string) (*model.Monitor, error) {
	m, err := scanMonitor(s.db.QueryRow(`SELECT `+monitorSelectCols+` FROM monitors WHERE slug = ?`, slug))
	if err != nil {
		return nil, mapErr(err)
	}
	return m, nil
}

func (s *Store) GetMonitorByPushToken(token string) (*model.Monitor, error) {
	if token == "" {
		return nil, ErrNotFound
	}
	m, err := scanMonitor(s.db.QueryRow(`SELECT `+monitorSelectCols+` FROM monitors WHERE push_token = ?`, token))
	if err != nil {
		return nil, mapErr(err)
	}
	return m, nil
}

func (s *Store) CreateMonitor(m model.Monitor) (*model.Monitor, error) {
	now := time.Now().UTC()
	if m.ID == "" {
		m.ID = newID()
	}
	if m.Slug == "" {
		m.Slug = slugify(m.Name, m.ID[:6])
	}
	if m.IntervalSec <= 0 {
		m.IntervalSec = 60
	}
	if m.TimeoutSec <= 0 {
		m.TimeoutSec = 10
	}
	if m.Retries < 0 {
		m.Retries = 0
	}
	if m.CurrentStatus == "" {
		m.CurrentStatus = model.ComponentStatusUnknown
	}
	if m.Type == model.MonitorTypePush && m.PushToken == "" {
		m.PushToken = newToken(16)
	}
	cfg := normalizeJSON(m.Config)
	m.Config = json.RawMessage(cfg)
	if m.CreatedAt.IsZero() {
		m.CreatedAt = now
	}
	if m.UpdatedAt.IsZero() {
		m.UpdatedAt = now
	}

	var pushTok sql.NullString
	if m.PushToken != "" {
		pushTok = sql.NullString{String: m.PushToken, Valid: true}
	}

	_, err := s.db.Exec(
		`INSERT INTO monitors (id, name, slug, type, target, group_id, interval_sec, timeout_sec, retries, degraded_ms,
		 active, sort_order, config_json, push_token, current_status, last_checked_at, last_latency_ms,
		 tls_expires_at, tls_days_left, last_error, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.Name, m.Slug, string(m.Type), m.Target, nullableString(m.GroupID), m.IntervalSec, m.TimeoutSec,
		m.Retries, m.DegradedMs, boolToInt(m.Active), m.SortOrder, cfg, pushTok, string(m.CurrentStatus),
		formatNullTime(m.LastCheckedAt), nullableInt(m.LastLatencyMs), formatNullTime(m.TLSExpiresAt),
		nullableInt(m.TLSDaysLeft), m.LastError, formatTime(m.CreatedAt), formatTime(m.UpdatedAt),
	)
	if err != nil {
		if isUniqueErr(err) {
			return nil, ErrConflict
		}
		return nil, err
	}
	return &m, nil
}

func (s *Store) UpdateMonitor(m model.Monitor) (*model.Monitor, error) {
	if m.IntervalSec <= 0 {
		m.IntervalSec = 60
	}
	if m.TimeoutSec <= 0 {
		m.TimeoutSec = 10
	}
	if m.Retries < 0 {
		m.Retries = 0
	}
	if m.Type == model.MonitorTypePush && m.PushToken == "" {
		m.PushToken = newToken(16)
	}
	cfg := normalizeJSON(m.Config)
	m.Config = json.RawMessage(cfg)
	m.UpdatedAt = time.Now().UTC()

	var pushTok sql.NullString
	if m.PushToken != "" {
		pushTok = sql.NullString{String: m.PushToken, Valid: true}
	}

	res, err := s.db.Exec(
		`UPDATE monitors SET name = ?, slug = ?, type = ?, target = ?, group_id = ?, interval_sec = ?, timeout_sec = ?,
		 retries = ?, degraded_ms = ?, active = ?, sort_order = ?, config_json = ?, push_token = ?, updated_at = ?
		 WHERE id = ?`,
		m.Name, m.Slug, string(m.Type), m.Target, nullableString(m.GroupID), m.IntervalSec, m.TimeoutSec,
		m.Retries, m.DegradedMs, boolToInt(m.Active), m.SortOrder, cfg, pushTok, formatTime(m.UpdatedAt), m.ID,
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
	return s.GetMonitorByID(m.ID)
}

func (s *Store) DeleteMonitor(id string) error {
	res, err := s.db.Exec(`DELETE FROM monitors WHERE id = ?`, id)
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

func (s *Store) SetMonitorActive(id string, active bool) error {
	res, err := s.db.Exec(
		`UPDATE monitors SET active = ?, updated_at = ? WHERE id = ?`,
		boolToInt(active), formatTime(time.Now().UTC()), id,
	)
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

func (s *Store) RecordHeartbeat(ctx context.Context, monitorID string, res model.ProbeResult, finalStatus model.ComponentStatus, intervalSec int) error {
	checkedAt := res.CheckedAt.UTC()
	if checkedAt.IsZero() {
		checkedAt = time.Now().UTC()
	}
	checkedStr := formatTime(checkedAt)
	day := checkedAt.Format("2006-01-02")

	var upInc, degInc, downInc, downtimeInc int
	switch res.Status {
	case model.HeartbeatStatusUp:
		upInc = 1
	case model.HeartbeatStatusDegraded:
		degInc = 1
	case model.HeartbeatStatusDown:
		downInc = 1
		if intervalSec > 0 {
			downtimeInc = intervalSec
		}
	}

	lastErr := ""
	if res.Status == model.HeartbeatStatusDown || res.Status == model.HeartbeatStatusDegraded {
		lastErr = res.Message
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO heartbeats (monitor_id, status, latency_ms, status_code, message, checked_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		monitorID, string(res.Status), res.LatencyMs, res.StatusCode, res.Message, checkedStr,
	); err != nil {
		return fmt.Errorf("insert heartbeat: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO daily_rollups
		 (monitor_id, day, total_checks, up_checks, degraded_checks, down_checks, sum_latency_ms, min_latency_ms, max_latency_ms, downtime_sec)
		 VALUES (?, ?, 1, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(monitor_id, day) DO UPDATE SET
		   total_checks = total_checks + 1,
		   up_checks = up_checks + excluded.up_checks,
		   degraded_checks = degraded_checks + excluded.degraded_checks,
		   down_checks = down_checks + excluded.down_checks,
		   sum_latency_ms = sum_latency_ms + excluded.sum_latency_ms,
		   min_latency_ms = CASE WHEN excluded.min_latency_ms < min_latency_ms THEN excluded.min_latency_ms ELSE min_latency_ms END,
		   max_latency_ms = CASE WHEN excluded.max_latency_ms > max_latency_ms THEN excluded.max_latency_ms ELSE max_latency_ms END,
		   downtime_sec = downtime_sec + excluded.downtime_sec`,
		monitorID, day, upInc, degInc, downInc, int64(res.LatencyMs), res.LatencyMs, res.LatencyMs, downtimeInc,
	); err != nil {
		return fmt.Errorf("upsert daily_rollups: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE monitors SET
		   current_status = ?,
		   last_checked_at = ?,
		   last_latency_ms = ?,
		   tls_expires_at = COALESCE(?, tls_expires_at),
		   tls_days_left = COALESCE(?, tls_days_left),
		   last_error = ?,
		   updated_at = ?
		 WHERE id = ?`,
		string(finalStatus), checkedStr, res.LatencyMs,
		formatNullTime(res.TLSExpiresAt), nullableInt(res.TLSDaysLeft),
		lastErr, checkedStr, monitorID,
	); err != nil {
		return fmt.Errorf("update monitor state: %w", err)
	}

	return tx.Commit()
}

func (s *Store) ListHeartbeats(monitorID string, limit int) ([]model.Heartbeat, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(
		`SELECT id, monitor_id, status, latency_ms, status_code, message, checked_at
		 FROM heartbeats WHERE monitor_id = ? ORDER BY checked_at DESC, id DESC LIMIT ?`,
		monitorID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanHeartbeats(rows)
}

func (s *Store) ListHeartbeatsSince(monitorID string, since time.Time, limit int) ([]model.Heartbeat, error) {
	if limit <= 0 {
		limit = 5000
	}
	rows, err := s.db.Query(
		`SELECT id, monitor_id, status, latency_ms, status_code, message, checked_at
		 FROM heartbeats WHERE monitor_id = ? AND checked_at >= ?
		 ORDER BY checked_at DESC, id DESC LIMIT ?`,
		monitorID, formatTime(since), limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanHeartbeats(rows)
}

func (s *Store) ListAllHeartbeatsSince(since time.Time, limit int) ([]model.Heartbeat, error) {
	if limit <= 0 {
		limit = 10000
	}
	rows, err := s.db.Query(
		`SELECT id, monitor_id, status, latency_ms, status_code, message, checked_at
		 FROM heartbeats WHERE checked_at >= ?
		 ORDER BY checked_at DESC, id DESC LIMIT ?`,
		formatTime(since), limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanHeartbeats(rows)
}

func scanHeartbeats(rows *sql.Rows) ([]model.Heartbeat, error) {
	hbs := []model.Heartbeat{}
	for rows.Next() {
		var h model.Heartbeat
		var st, checkedStr string
		if err := rows.Scan(&h.ID, &h.MonitorID, &st, &h.LatencyMs, &h.StatusCode, &h.Message, &checkedStr); err != nil {
			return nil, err
		}
		h.Status = model.HeartbeatStatus(st)
		h.CheckedAt = parseTime(checkedStr)
		hbs = append(hbs, h)
	}
	return hbs, rows.Err()
}

func (s *Store) PruneHeartbeats(olderThan time.Time) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM heartbeats WHERE checked_at < ?`, formatTime(olderThan))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) GetDailyRollups(monitorID, fromDay, toDay string) ([]model.DailyRollup, error) {
	rows, err := s.db.Query(
		`SELECT monitor_id, day, total_checks, up_checks, degraded_checks, down_checks,
		        sum_latency_ms, min_latency_ms, max_latency_ms, downtime_sec
		 FROM daily_rollups
		 WHERE monitor_id = ? AND day >= ? AND day <= ?
		 ORDER BY day ASC`,
		monitorID, fromDay, toDay,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDailyRollups(rows)
}

func (s *Store) ListAllDailyRollups(fromDay, toDay string) ([]model.DailyRollup, error) {
	rows, err := s.db.Query(
		`SELECT monitor_id, day, total_checks, up_checks, degraded_checks, down_checks,
		        sum_latency_ms, min_latency_ms, max_latency_ms, downtime_sec
		 FROM daily_rollups
		 WHERE day >= ? AND day <= ?
		 ORDER BY day ASC`,
		fromDay, toDay,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDailyRollups(rows)
}

func scanDailyRollups(rows *sql.Rows) ([]model.DailyRollup, error) {
	rollups := []model.DailyRollup{}
	for rows.Next() {
		var r model.DailyRollup
		if err := rows.Scan(
			&r.MonitorID, &r.Day, &r.TotalChecks, &r.UpChecks, &r.DegradedChecks,
			&r.DownChecks, &r.SumLatencyMs, &r.MinLatencyMs, &r.MaxLatencyMs, &r.DowntimeSec,
		); err != nil {
			return nil, err
		}
		if r.TotalChecks > 0 {
			r.AvgLatencyMs = float64(r.SumLatencyMs) / float64(r.TotalChecks)
		}
		rollups = append(rollups, r)
	}
	return rollups, rows.Err()
}

type scanner interface {
	Scan(dest ...any) error
}

func scanMonitor(row scanner) (*model.Monitor, error) {
	var m model.Monitor
	var mType, cfgStr, curStatus, createdStr, updatedStr string
	var groupID, pushTok, lastCheckedStr, tlsExpStr sql.NullString
	var lastLat, tlsDays sql.NullInt64
	var active int

	err := row.Scan(
		&m.ID, &m.Name, &m.Slug, &mType, &m.Target, &groupID,
		&m.IntervalSec, &m.TimeoutSec, &m.Retries, &m.DegradedMs,
		&active, &m.SortOrder, &cfgStr, &pushTok, &curStatus,
		&lastCheckedStr, &lastLat, &tlsExpStr, &tlsDays, &m.LastError,
		&createdStr, &updatedStr,
	)
	if err != nil {
		return nil, err
	}

	m.Type = model.MonitorType(mType)
	m.Active = active == 1
	m.Config = json.RawMessage(normalizeJSON([]byte(cfgStr)))
	m.CurrentStatus = model.ComponentStatus(curStatus)
	m.CreatedAt = parseTime(createdStr)
	m.UpdatedAt = parseTime(updatedStr)
	m.LastCheckedAt = parseNullTime(lastCheckedStr)
	m.TLSExpiresAt = parseNullTime(tlsExpStr)
	if groupID.Valid {
		v := groupID.String
		m.GroupID = &v
	}
	if pushTok.Valid {
		m.PushToken = pushTok.String
	}
	if lastLat.Valid {
		v := int(lastLat.Int64)
		m.LastLatencyMs = &v
	}
	if tlsDays.Valid {
		v := int(tlsDays.Int64)
		m.TLSDaysLeft = &v
	}
	return &m, nil
}

func nullableString(v *string) sql.NullString {
	if v == nil || *v == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: *v, Valid: true}
}

func nullableInt(v *int) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*v), Valid: true}
}

func slugify(name, fallbackSuffix string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
		} else if !prevDash && b.Len() > 0 {
			b.WriteByte('-')
			prevDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "monitor-" + fallbackSuffix
	}
	return out
}
