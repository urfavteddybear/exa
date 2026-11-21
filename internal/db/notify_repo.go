package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"exa/internal/model"
)

func (s *Store) ListNotificationChannels() ([]model.NotificationChannel, error) {
	return s.queryChannels(`SELECT id, name, type, config_json, is_default, enabled, created_at FROM notification_channels ORDER BY created_at ASC`)
}

func (s *Store) GetNotificationChannel(id string) (*model.NotificationChannel, error) {
	list, err := s.queryChannels(
		`SELECT id, name, type, config_json, is_default, enabled, created_at FROM notification_channels WHERE id = ?`,
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

func (s *Store) CreateNotificationChannel(ch model.NotificationChannel) (*model.NotificationChannel, error) {
	if ch.ID == "" {
		ch.ID = newID()
	}
	if ch.CreatedAt.IsZero() {
		ch.CreatedAt = time.Now().UTC()
	}
	cfg := normalizeJSON(ch.Config)
	ch.Config = json.RawMessage(cfg)

	_, err := s.db.Exec(
		`INSERT INTO notification_channels (id, name, type, config_json, is_default, enabled, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		ch.ID, ch.Name, string(ch.Type), cfg, boolToInt(ch.IsDefault), boolToInt(ch.Enabled), formatTime(ch.CreatedAt),
	)
	if err != nil {
		return nil, err
	}
	return &ch, nil
}

func (s *Store) UpdateNotificationChannel(ch model.NotificationChannel) (*model.NotificationChannel, error) {
	cfg := normalizeJSON(ch.Config)
	ch.Config = json.RawMessage(cfg)
	res, err := s.db.Exec(
		`UPDATE notification_channels SET name = ?, type = ?, config_json = ?, is_default = ?, enabled = ? WHERE id = ?`,
		ch.Name, string(ch.Type), cfg, boolToInt(ch.IsDefault), boolToInt(ch.Enabled), ch.ID,
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
	return s.GetNotificationChannel(ch.ID)
}

func (s *Store) DeleteNotificationChannel(id string) error {
	res, err := s.db.Exec(`DELETE FROM notification_channels WHERE id = ?`, id)
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

func (s *Store) ListChannelsForMonitor(monitorID string) ([]model.NotificationChannel, error) {
	var linkedCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM monitor_notifications WHERE monitor_id = ?`, monitorID).Scan(&linkedCount); err != nil {
		return nil, err
	}
	if linkedCount > 0 {
		return s.queryChannels(
			`SELECT c.id, c.name, c.type, c.config_json, c.is_default, c.enabled, c.created_at
			 FROM notification_channels c
			 JOIN monitor_notifications mn ON mn.channel_id = c.id
			 WHERE mn.monitor_id = ? AND c.enabled = 1
			 ORDER BY c.created_at ASC`,
			monitorID,
		)
	}
	return s.queryChannels(
		`SELECT id, name, type, config_json, is_default, enabled, created_at
		 FROM notification_channels
		 WHERE is_default = 1 AND enabled = 1
		 ORDER BY created_at ASC`,
	)
}

func (s *Store) ListChannelIDsForMonitor(monitorID string) ([]string, error) {
	rows, err := s.db.Query(`SELECT channel_id FROM monitor_notifications WHERE monitor_id = ?`, monitorID)
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

func (s *Store) SetMonitorNotificationChannels(monitorID string, channelIDs []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`DELETE FROM monitor_notifications WHERE monitor_id = ?`, monitorID); err != nil {
		return err
	}
	for _, cid := range channelIDs {
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO monitor_notifications (monitor_id, channel_id) VALUES (?, ?)`,
			monitorID, cid,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) queryChannels(query string, args ...any) ([]model.NotificationChannel, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.NotificationChannel{}
	for rows.Next() {
		var ch model.NotificationChannel
		var cType, cfgStr, createdStr string
		var isDef, enabled int
		if err := rows.Scan(&ch.ID, &ch.Name, &cType, &cfgStr, &isDef, &enabled, &createdStr); err != nil {
			return nil, err
		}
		ch.Type = model.NotificationType(cType)
		ch.Config = json.RawMessage(normalizeJSON([]byte(cfgStr)))
		ch.IsDefault = isDef == 1
		ch.Enabled = enabled == 1
		ch.CreatedAt = parseTime(createdStr)
		out = append(out, ch)
	}
	return out, rows.Err()
}

// Subscribers.

func (s *Store) ListSubscribers(pageID string) ([]model.Subscriber, error) {
	query := `SELECT id, status_page_id, type, endpoint, token, verified, active, created_at FROM subscribers`
	var args []any
	if pageID != "" {
		query += ` WHERE status_page_id = ?`
		args = append(args, pageID)
	}
	query += ` ORDER BY created_at DESC`
	return s.querySubscribers(query, args...)
}

func (s *Store) CountActiveSubscribers(pageID string) (int, error) {
	var count int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM subscribers WHERE status_page_id = ? AND active = 1`,
		pageID,
	).Scan(&count)
	return count, err
}

func (s *Store) ListActiveSubscribers(pageID string) ([]model.Subscriber, error) {
	return s.querySubscribers(
		`SELECT id, status_page_id, type, endpoint, token, verified, active, created_at
		 FROM subscribers WHERE status_page_id = ? AND active = 1 AND verified = 1
		 ORDER BY created_at ASC`,
		pageID,
	)
}

func (s *Store) CreateSubscriber(sub model.Subscriber) (*model.Subscriber, error) {
	if sub.ID == "" {
		sub.ID = newID()
	}
	if sub.Token == "" {
		sub.Token = newToken(16)
	}
	if sub.CreatedAt.IsZero() {
		sub.CreatedAt = time.Now().UTC()
	}

	// Reactivate an existing unsubscribed endpoint instead of failing on uniqueness.
	var existingID string
	err := s.db.QueryRow(
		`SELECT id FROM subscribers WHERE status_page_id = ? AND type = ? AND endpoint = ?`,
		sub.StatusPageID, string(sub.Type), sub.Endpoint,
	).Scan(&existingID)
	if err == nil && existingID != "" {
		if _, err := s.db.Exec(
			`UPDATE subscribers SET active = 1, verified = 1 WHERE id = ?`,
			existingID,
		); err != nil {
			return nil, err
		}
		list, err := s.querySubscribers(
			`SELECT id, status_page_id, type, endpoint, token, verified, active, created_at FROM subscribers WHERE id = ?`,
			existingID,
		)
		if err != nil {
			return nil, err
		}
		return &list[0], nil
	}

	_, err = s.db.Exec(
		`INSERT INTO subscribers (id, status_page_id, type, endpoint, token, verified, active, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		sub.ID, sub.StatusPageID, string(sub.Type), sub.Endpoint, sub.Token,
		boolToInt(sub.Verified), boolToInt(sub.Active), formatTime(sub.CreatedAt),
	)
	if err != nil {
		return nil, err
	}
	return &sub, nil
}

func (s *Store) DeactivateSubscriberByToken(token string) error {
	res, err := s.db.Exec(`UPDATE subscribers SET active = 0 WHERE token = ?`, token)
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

func (s *Store) DeleteSubscriber(id string) error {
	res, err := s.db.Exec(`DELETE FROM subscribers WHERE id = ?`, id)
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

func (s *Store) querySubscribers(query string, args ...any) ([]model.Subscriber, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.Subscriber{}
	for rows.Next() {
		var sub model.Subscriber
		var sType, createdStr string
		var verified, active int
		if err := rows.Scan(&sub.ID, &sub.StatusPageID, &sType, &sub.Endpoint, &sub.Token, &verified, &active, &createdStr); err != nil {
			return nil, err
		}
		sub.Type = model.SubscriberType(sType)
		sub.Verified = verified == 1
		sub.Active = active == 1
		sub.CreatedAt = parseTime(createdStr)
		out = append(out, sub)
	}
	return out, rows.Err()
}

// Settings and Backup.

func (s *Store) GetSetting(key string) (string, error) {
	var val string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&val)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	return val, nil
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value,
	)
	return err
}

func (s *Store) GetAllSettings() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

func (s *Store) ExportBackup() (*model.BackupData, error) {
	settings, err := s.GetAllSettings()
	if err != nil {
		return nil, err
	}
	groups, err := s.ListGroups()
	if err != nil {
		return nil, err
	}
	monitors, err := s.ListMonitors()
	if err != nil {
		return nil, err
	}
	pages, err := s.ListStatusPages()
	if err != nil {
		return nil, err
	}

	var spmList []model.StatusPageMonitor
	for _, p := range pages {
		items, err := s.ListStatusPageMonitors(p.ID)
		if err != nil {
			return nil, err
		}
		spmList = append(spmList, items...)
	}

	channels, err := s.ListNotificationChannels()
	if err != nil {
		return nil, err
	}

	mnRows, err := s.db.Query(`SELECT monitor_id, channel_id FROM monitor_notifications`)
	if err != nil {
		return nil, err
	}
	defer mnRows.Close()
	mnList := []model.MonitorNotification{}
	for mnRows.Next() {
		var mn model.MonitorNotification
		if err := mnRows.Scan(&mn.MonitorID, &mn.ChannelID); err != nil {
			return nil, err
		}
		mnList = append(mnList, mn)
	}
	if err := mnRows.Err(); err != nil {
		return nil, err
	}

	hbRows, err := s.db.Query(
		`SELECT id, monitor_id, status, latency_ms, status_code, message, checked_at
		 FROM heartbeats ORDER BY id ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer hbRows.Close()
	heartbeats := []model.Heartbeat{}
	for hbRows.Next() {
		var h model.Heartbeat
		var st, checkedStr string
		if err := hbRows.Scan(&h.ID, &h.MonitorID, &st, &h.LatencyMs, &h.StatusCode, &h.Message, &checkedStr); err != nil {
			return nil, err
		}
		h.Status = model.HeartbeatStatus(st)
		h.CheckedAt = parseTime(checkedStr)
		heartbeats = append(heartbeats, h)
	}
	if err := hbRows.Err(); err != nil {
		return nil, err
	}

	ruRows, err := s.db.Query(
		`SELECT monitor_id, day, total_checks, up_checks, degraded_checks, down_checks,
		        sum_latency_ms, min_latency_ms, max_latency_ms, downtime_sec
		 FROM daily_rollups ORDER BY monitor_id ASC, day ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer ruRows.Close()
	dailyRollups := []model.BackupDailyRollup{}
	for ruRows.Next() {
		var ru model.BackupDailyRollup
		if err := ruRows.Scan(
			&ru.MonitorID, &ru.Day, &ru.TotalChecks, &ru.UpChecks, &ru.DegradedChecks,
			&ru.DownChecks, &ru.SumLatencyMs, &ru.MinLatencyMs, &ru.MaxLatencyMs, &ru.DowntimeSec,
		); err != nil {
			return nil, err
		}
		dailyRollups = append(dailyRollups, ru)
	}
	if err := ruRows.Err(); err != nil {
		return nil, err
	}

	incidents, err := s.ListIncidents("", 1000000, 0)
	if err != nil {
		return nil, err
	}
	maintenances, err := s.ListMaintenances("")
	if err != nil {
		return nil, err
	}
	subscribers, err := s.ListSubscribers("")
	if err != nil {
		return nil, err
	}

	return &model.BackupData{
		Version:              2,
		ExportedAt:           time.Now().UTC(),
		Settings:             settings,
		ComponentGroups:      groups,
		Monitors:             monitors,
		Heartbeats:           heartbeats,
		DailyRollups:         dailyRollups,
		StatusPages:          pages,
		StatusPageMonitors:   spmList,
		NotificationChannels: channels,
		MonitorNotifications: mnList,
		Incidents:            incidents,
		Maintenances:         maintenances,
		Subscribers:          subscribers,
	}, nil
}

func (s *Store) ImportBackup(data model.BackupData) error {
	if data.Version != 0 && data.Version != 1 && data.Version != 2 {
		return fmt.Errorf("unsupported backup version: %d", data.Version)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Preserve user accounts, sessions, and API keys while restoring all monitoring and status page data.
	clearTables := []string{
		"monitor_notifications",
		"maintenance_monitors",
		"maintenances",
		"incident_monitors",
		"incident_updates",
		"incidents",
		"subscribers",
		"status_page_monitors",
		"status_pages",
		"notification_channels",
		"heartbeats",
		"daily_rollups",
		"monitors",
		"component_groups",
		"settings",
	}
	for _, tbl := range clearTables {
		if _, err := tx.Exec("DELETE FROM " + tbl); err != nil {
			return fmt.Errorf("clear %s: %w", tbl, err)
		}
	}

	for k, v := range data.Settings {
		if k == "setup_completed" {
			var userCount int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&userCount); err != nil {
				return err
			}
			if userCount > 0 {
				v = "true"
			}
		}
		if _, err := tx.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)`, k, v); err != nil {
			return err
		}
	}
	// Preserve setup completion flag if users already exist.
	var userCount int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&userCount); err != nil {
		return err
	}
	if userCount > 0 {
		if _, err := tx.Exec(`INSERT OR REPLACE INTO settings (key, value) VALUES ('setup_completed', 'true')`); err != nil {
			return err
		}
	}

	for _, g := range data.ComponentGroups {
		if _, err := tx.Exec(
			`INSERT INTO component_groups (id, name, description, sort_order, collapsed_default, created_at)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			g.ID, g.Name, g.Description, g.SortOrder, boolToInt(g.CollapsedDefault), formatTime(g.CreatedAt),
		); err != nil {
			return err
		}
	}

	for _, m := range data.Monitors {
		if !isValidImportMonitorType(m.Type) {
			return fmt.Errorf("invalid monitor type in backup: %q", m.Type)
		}
		if m.IntervalSec < 10 {
			m.IntervalSec = 10
		}
		if m.TimeoutSec < 1 {
			m.TimeoutSec = 1
		}
		if m.Retries < 0 {
			m.Retries = 0
		}
		var pushTok sql.NullString
		if m.PushToken != "" {
			pushTok = sql.NullString{String: m.PushToken, Valid: true}
		}
		if m.CurrentStatus == "" {
			m.CurrentStatus = model.ComponentStatusUnknown
		}
		if _, err := tx.Exec(
			`INSERT INTO monitors (id, name, slug, type, target, group_id, interval_sec, timeout_sec, retries, degraded_ms,
			 active, sort_order, config_json, push_token, current_status, last_checked_at, last_latency_ms,
			 tls_expires_at, tls_days_left, last_error, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			m.ID, m.Name, m.Slug, string(m.Type), m.Target, nullableString(m.GroupID), m.IntervalSec, m.TimeoutSec,
			m.Retries, m.DegradedMs, boolToInt(m.Active), m.SortOrder, normalizeJSON(m.Config), pushTok, string(m.CurrentStatus),
			formatNullTime(m.LastCheckedAt), nullableInt(m.LastLatencyMs), formatNullTime(m.TLSExpiresAt),
			nullableInt(m.TLSDaysLeft), m.LastError, formatTime(m.CreatedAt), formatTime(m.UpdatedAt),
		); err != nil {
			return err
		}
	}

	if len(data.Heartbeats) > 0 {
		hbStmt, err := tx.Prepare(
			`INSERT INTO heartbeats (id, monitor_id, status, latency_ms, status_code, message, checked_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		)
		if err != nil {
			return err
		}
		for _, h := range data.Heartbeats {
			if h.Status != model.HeartbeatStatusUp && h.Status != model.HeartbeatStatusDegraded && h.Status != model.HeartbeatStatusDown {
				continue
			}
			var idArg any
			if h.ID > 0 {
				idArg = h.ID
			}
			if _, err := hbStmt.Exec(idArg, h.MonitorID, string(h.Status), h.LatencyMs, h.StatusCode, h.Message, formatTime(h.CheckedAt)); err != nil {
				_ = hbStmt.Close()
				return err
			}
		}
		if err := hbStmt.Close(); err != nil {
			return err
		}
	}

	if len(data.DailyRollups) > 0 {
		ruStmt, err := tx.Prepare(
			`INSERT INTO daily_rollups
			 (monitor_id, day, total_checks, up_checks, degraded_checks, down_checks, sum_latency_ms, min_latency_ms, max_latency_ms, downtime_sec)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		)
		if err != nil {
			return err
		}
		for _, ru := range data.DailyRollups {
			if _, err := ruStmt.Exec(
				ru.MonitorID, ru.Day, ru.TotalChecks, ru.UpChecks, ru.DegradedChecks,
				ru.DownChecks, ru.SumLatencyMs, ru.MinLatencyMs, ru.MaxLatencyMs, ru.DowntimeSec,
			); err != nil {
				_ = ruStmt.Close()
				return err
			}
		}
		if err := ruStmt.Close(); err != nil {
			return err
		}
	}

	for _, p := range data.StatusPages {
		if _, err := tx.Exec(
			`INSERT INTO status_pages (id, slug, title, description, custom_css, is_default, is_public, show_latency, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			p.ID, p.Slug, p.Title, p.Description, p.CustomCSS,
			boolToInt(p.IsDefault), boolToInt(p.IsPublic), boolToInt(p.ShowLatency),
			formatTime(p.CreatedAt), formatTime(p.UpdatedAt),
		); err != nil {
			return err
		}
	}

	for _, spm := range data.StatusPageMonitors {
		if _, err := tx.Exec(
			`INSERT INTO status_page_monitors (status_page_id, monitor_id, group_id, display_name, sort_order)
			 VALUES (?, ?, ?, ?, ?)`,
			spm.StatusPageID, spm.MonitorID, nullableString(spm.GroupID), spm.DisplayName, spm.SortOrder,
		); err != nil {
			return err
		}
	}

	for _, ch := range data.NotificationChannels {
		if !isValidImportChannelType(ch.Type) {
			return fmt.Errorf("invalid notification channel type in backup: %q", ch.Type)
		}
		if _, err := tx.Exec(
			`INSERT INTO notification_channels (id, name, type, config_json, is_default, enabled, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			ch.ID, ch.Name, string(ch.Type), normalizeJSON(ch.Config), boolToInt(ch.IsDefault), boolToInt(ch.Enabled), formatTime(ch.CreatedAt),
		); err != nil {
			return err
		}
	}

	for _, mn := range data.MonitorNotifications {
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO monitor_notifications (monitor_id, channel_id) VALUES (?, ?)`,
			mn.MonitorID, mn.ChannelID,
		); err != nil {
			return err
		}
	}

	for _, inc := range data.Incidents {
		if _, err := tx.Exec(
			`INSERT INTO incidents (id, status_page_id, title, impact, status, created_at, resolved_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			inc.ID, inc.StatusPageID, inc.Title, string(inc.Impact), string(inc.Status),
			formatTime(inc.CreatedAt), formatNullTime(inc.ResolvedAt), formatTime(inc.UpdatedAt),
		); err != nil {
			return err
		}
		for _, upd := range inc.Updates {
			if _, err := tx.Exec(
				`INSERT INTO incident_updates (id, incident_id, status, message, created_at) VALUES (?, ?, ?, ?, ?)`,
				upd.ID, inc.ID, string(upd.Status), upd.Message, formatTime(upd.CreatedAt),
			); err != nil {
				return err
			}
		}
		for _, mid := range inc.MonitorIDs {
			if _, err := tx.Exec(
				`INSERT OR IGNORE INTO incident_monitors (incident_id, monitor_id) VALUES (?, ?)`,
				inc.ID, mid,
			); err != nil {
				return err
			}
		}
	}

	for _, m := range data.Maintenances {
		if _, err := tx.Exec(
			`INSERT INTO maintenances (id, status_page_id, title, description, status, scheduled_start, scheduled_end, auto_suppress_alerts, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			m.ID, m.StatusPageID, m.Title, m.Description, string(m.Status),
			formatTime(m.ScheduledStart), formatTime(m.ScheduledEnd), boolToInt(m.AutoSuppressAlerts), formatTime(m.CreatedAt),
		); err != nil {
			return err
		}
		for _, mid := range m.MonitorIDs {
			if _, err := tx.Exec(
				`INSERT OR IGNORE INTO maintenance_monitors (maintenance_id, monitor_id) VALUES (?, ?)`,
				m.ID, mid,
			); err != nil {
				return err
			}
		}
	}

	for _, sub := range data.Subscribers {
		if _, err := tx.Exec(
			`INSERT INTO subscribers (id, status_page_id, type, endpoint, token, verified, active, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			sub.ID, sub.StatusPageID, string(sub.Type), sub.Endpoint, sub.Token,
			boolToInt(sub.Verified), boolToInt(sub.Active), formatTime(sub.CreatedAt),
		); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func isValidImportMonitorType(t model.MonitorType) bool {
	switch t {
	case model.MonitorTypeHTTP, model.MonitorTypeTCP, model.MonitorTypePing, model.MonitorTypeDNS, model.MonitorTypePush:
		return true
	default:
		return false
	}
}

func isValidImportChannelType(t model.NotificationType) bool {
	switch t {
	case model.NotificationTypeWebhook, model.NotificationTypeDiscord, model.NotificationTypeSlack,
		model.NotificationTypeTelegram, model.NotificationTypeEmail, model.NotificationTypeNtfy:
		return true
	default:
		return false
	}
}
