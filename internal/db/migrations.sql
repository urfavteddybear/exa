CREATE TABLE IF NOT EXISTS users (
	id TEXT PRIMARY KEY,
	username TEXT NOT NULL UNIQUE,
	email TEXT NOT NULL DEFAULT '',
	password_hash TEXT NOT NULL,
	role TEXT NOT NULL DEFAULT 'admin',
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
	id TEXT PRIMARY KEY,
	user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	expires_at TEXT NOT NULL,
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS api_keys (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	key_prefix TEXT NOT NULL,
	key_hash TEXT NOT NULL UNIQUE,
	role TEXT NOT NULL DEFAULT 'admin',
	last_used_at TEXT,
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS component_groups (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	sort_order INTEGER NOT NULL DEFAULT 0,
	collapsed_default INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS monitors (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	slug TEXT NOT NULL UNIQUE,
	type TEXT NOT NULL,
	target TEXT NOT NULL DEFAULT '',
	group_id TEXT REFERENCES component_groups(id) ON DELETE SET NULL,
	interval_sec INTEGER NOT NULL DEFAULT 60,
	timeout_sec INTEGER NOT NULL DEFAULT 10,
	retries INTEGER NOT NULL DEFAULT 1,
	degraded_ms INTEGER NOT NULL DEFAULT 0,
	active INTEGER NOT NULL DEFAULT 1,
	sort_order INTEGER NOT NULL DEFAULT 0,
	config_json TEXT NOT NULL DEFAULT '{}',
	push_token TEXT UNIQUE,
	current_status TEXT NOT NULL DEFAULT 'unknown',
	last_checked_at TEXT,
	last_latency_ms INTEGER,
	tls_expires_at TEXT,
	tls_days_left INTEGER,
	last_error TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS heartbeats (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	monitor_id TEXT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
	status TEXT NOT NULL,
	latency_ms INTEGER NOT NULL DEFAULT 0,
	status_code INTEGER NOT NULL DEFAULT 0,
	message TEXT NOT NULL DEFAULT '',
	checked_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_heartbeats_monitor_checked
	ON heartbeats(monitor_id, checked_at DESC);

CREATE TABLE IF NOT EXISTS daily_rollups (
	monitor_id TEXT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
	day TEXT NOT NULL,
	total_checks INTEGER NOT NULL DEFAULT 0,
	up_checks INTEGER NOT NULL DEFAULT 0,
	degraded_checks INTEGER NOT NULL DEFAULT 0,
	down_checks INTEGER NOT NULL DEFAULT 0,
	sum_latency_ms INTEGER NOT NULL DEFAULT 0,
	min_latency_ms INTEGER NOT NULL DEFAULT 0,
	max_latency_ms INTEGER NOT NULL DEFAULT 0,
	downtime_sec INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (monitor_id, day)
);

CREATE TABLE IF NOT EXISTS status_pages (
	id TEXT PRIMARY KEY,
	slug TEXT NOT NULL UNIQUE,
	title TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	custom_css TEXT NOT NULL DEFAULT '',
	is_default INTEGER NOT NULL DEFAULT 0,
	is_public INTEGER NOT NULL DEFAULT 1,
	show_latency INTEGER NOT NULL DEFAULT 1,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS status_page_monitors (
	status_page_id TEXT NOT NULL REFERENCES status_pages(id) ON DELETE CASCADE,
	monitor_id TEXT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
	group_id TEXT REFERENCES component_groups(id) ON DELETE SET NULL,
	display_name TEXT NOT NULL DEFAULT '',
	sort_order INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (status_page_id, monitor_id)
);

CREATE TABLE IF NOT EXISTS incidents (
	id TEXT PRIMARY KEY,
	status_page_id TEXT NOT NULL REFERENCES status_pages(id) ON DELETE CASCADE,
	title TEXT NOT NULL,
	impact TEXT NOT NULL DEFAULT 'minor',
	status TEXT NOT NULL DEFAULT 'investigating',
	created_at TEXT NOT NULL,
	resolved_at TEXT,
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS incident_updates (
	id TEXT PRIMARY KEY,
	incident_id TEXT NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
	status TEXT NOT NULL,
	message TEXT NOT NULL,
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS incident_monitors (
	incident_id TEXT NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
	monitor_id TEXT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
	PRIMARY KEY (incident_id, monitor_id)
);

CREATE TABLE IF NOT EXISTS maintenances (
	id TEXT PRIMARY KEY,
	status_page_id TEXT NOT NULL REFERENCES status_pages(id) ON DELETE CASCADE,
	title TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'scheduled',
	scheduled_start TEXT NOT NULL,
	scheduled_end TEXT NOT NULL,
	auto_suppress_alerts INTEGER NOT NULL DEFAULT 1,
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS maintenance_monitors (
	maintenance_id TEXT NOT NULL REFERENCES maintenances(id) ON DELETE CASCADE,
	monitor_id TEXT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
	PRIMARY KEY (maintenance_id, monitor_id)
);

CREATE TABLE IF NOT EXISTS notification_channels (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	type TEXT NOT NULL,
	config_json TEXT NOT NULL DEFAULT '{}',
	is_default INTEGER NOT NULL DEFAULT 0,
	enabled INTEGER NOT NULL DEFAULT 1,
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS monitor_notifications (
	monitor_id TEXT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
	channel_id TEXT NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
	PRIMARY KEY (monitor_id, channel_id)
);

CREATE TABLE IF NOT EXISTS subscribers (
	id TEXT PRIMARY KEY,
	status_page_id TEXT NOT NULL REFERENCES status_pages(id) ON DELETE CASCADE,
	type TEXT NOT NULL,
	endpoint TEXT NOT NULL,
	token TEXT NOT NULL UNIQUE,
	verified INTEGER NOT NULL DEFAULT 1,
	active INTEGER NOT NULL DEFAULT 1,
	created_at TEXT NOT NULL,
	UNIQUE(status_page_id, type, endpoint)
);

CREATE TABLE IF NOT EXISTS settings (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
