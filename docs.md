# Exa Documentation

This document covers self-hosted deployment (Docker and native binary), configuration, security operations, and the full HTTP API reference for Exa.

## 1. Architecture Overview

Exa is compiled into a single Go binary containing an embedded Preact SPA (`web/dist`) and an embedded pure-Go SQLite engine (`modernc.org/sqlite`) running in WAL mode.

Core runtime components:
- **Probe scheduler**: executes periodic checks (`http`, `tcp`, `ping`, `dns`) with configurable intervals, timeouts, retries, and degraded latency thresholds.
- **Push receiver**: accepts inbound heartbeats from cron jobs and workers via token URLs (`push` monitors) and marks monitors down when the configured grace window expires.
- **Status & incident engine**: computes 90-day daily uptime rollups, overall status banners, incident timelines, and scheduled maintenance windows.
- **Notification dispatcher**: sends state change, incident, and maintenance alerts across `webhook` (HMAC-SHA256), `discord`, `slack`, `telegram`, `email` (SMTP), and `ntfy`, plus optional public email/HTTPS webhook subscribers.
- **Real-time & integration outputs**: Server-Sent Events (`/api/v1/stream`), Prometheus `/metrics`, SVG badges, RSS 2.0 / Atom 1.0 feeds, and JSON backup export/import.

---

## 2. Configuration Reference

Settings can be supplied via environment variables or CLI flags (CLI flags override defaults; environment variables populate the CLI flag defaults).

| Environment Variable | CLI Flag | Default | Description |
|---|---|---|---|
| `PORT` | `-port` | `8080` | HTTP listen port |
| `HOST` | `-host` | `0.0.0.0` | HTTP listen interface |
| `DATA_DIR` | `-data-dir` | `./data` (`/data` in Docker) | Directory storing `exa.db` (`0700` dir, `0600` file) |
| `BASE_URL` | `-base-url` | Auto-detected from request | Public base URL written into RSS/Atom feeds, push URLs, and unsubscribe links; persisted to `base_url` setting on startup when set |
| `COOKIE_SECURE` | none | `true` if `BASE_URL` starts with `https://`, else `false` | Forces `Secure` attribute on the `exa_session` cookie |
| `TRUST_PROXY` | none | `false` | Reads client IP from `X-Forwarded-For` and scheme from `X-Forwarded-Proto`; enable only behind a reverse proxy that overwrites those headers |
| none | `-version` | `false` | Prints version and exits |

### Instance Settings (`settings` table)

Managed via the Admin Settings UI or `GET/PUT /api/v1/settings`:

| Key | Default | Description |
|---|---|---|
| `retention_days` | `90` | Heartbeat retention horizon in days |
| `metrics_token` | empty | Bearer token required by `/metrics`; `/metrics` returns `401` when empty |
| `base_url` | empty | Persisted public base URL override |
| `allow_subscriptions` | `"false"` | Enables public status page subscriptions when `"true"` |
| `setup_completed` | `"false"` | Read-only system flag set during `/api/v1/setup` (ignored by `PUT /api/v1/settings`) |

---

## 3. Deployment Guide

### 3.1 Docker Compose (Recommended)

The repository ships with a multi-stage [Dockerfile](Dockerfile) (`node:22-alpine` -> `golang:1.26-alpine` -> `alpine:3.21`) and a hardened [docker-compose.yml](docker-compose.yml):
- Binds to `127.0.0.1:8080:8080` (loopback only, designed to sit behind a TLS reverse proxy)
- Runs as non-root `USER exa` with a read-only root filesystem (`read_only: true`) and writable `/tmp` (`tmpfs`) and `/data`
- Drops all Linux capabilities (`cap_drop: [ALL]`) and sets `no-new-privileges:true`
- Enables `net.ipv4.ping_group_range: "0 2147483647"` so ICMP `ping` monitors work using unprivileged datagram sockets without `CAP_NET_RAW`, alongside `iputils-ping` in the runtime image

#### Fixing Linux Bind-Mount Permissions (`SQLITE_CANTOPEN` / error 14)

`docker-compose.yml` mounts `./data:/data`. If `./data` does not exist on the host before starting, Docker creates it owned by `root:root` (`0755`), which prevents the non-root `exa` user inside the container from creating `/data/exa.db` and causes:

```text
msg="failed to open database" err="apply pragma \"PRAGMA journal_mode=WAL;\": unable to open database file (14)"
```

Choose one of these fixes before running `docker compose up -d`:

1. **Bind mount (`./data:/data`)**: inspect the UID/GID of `exa` inside the built image and chown `./data`:

```sh
docker compose build
docker compose run --rm --no-deps --entrypoint id exa
# Example output: uid=100(exa) gid=101(exa)
mkdir -p ./data
sudo chown -R 100:101 ./data
docker compose up -d
```

On hosts with SELinux enforcing (Fedora, RHEL, Rocky Linux), append `:Z` to the volume mount (`./data:/data:Z`).

2. **Named Docker volume**: replace `./data:/data` in `docker-compose.yml` with `exa_data:/data` and declare top-level `volumes: exa_data:`. Docker automatically initializes an empty named volume with the container image's `/data` ownership (`exa:exa`).

#### Production Environment Overrides

Edit `docker-compose.yml` for your public domain and proxy setup:

```yaml
    environment:
      PORT: "8080"
      DATA_DIR: /data
      TRUST_PROXY: "true"
      BASE_URL: "https://status.example.com"
      COOKIE_SECURE: "true"
```

Note on `ping` monitors:
- With bridge networking (default in `docker-compose.yml`), `net.ipv4.ping_group_range: "0 2147483647"` allows unprivileged `udp4`/`udp6` ICMP sockets inside the container.
- If you switch to `network_mode: host` or deploy on a runtime that blocks container `sysctls`, remove the `sysctls` block and grant `cap_add: [NET_RAW]` instead.

---

### 3.2 Native Binary + systemd (Linux)

Prerequisites: Node.js 22+ and Go 1.26+.

1. Build the embedded frontend bundle and static Go binary:

```sh
cd web
npm ci
npm run build
cd ..
CGO_ENABLED=0 go build -ldflags="-s -w" -o exa ./cmd/exa
```

2. Install the binary and create a dedicated service user:

```sh
sudo install -m 0755 exa /usr/local/bin/exa
sudo useradd --system --home /var/lib/exa --shell /usr/sbin/nologin exa
sudo install -d -o exa -g exa -m 0700 /var/lib/exa
```

3. Create `/etc/systemd/system/exa.service`:

```ini
[Unit]
Description=Exa Uptime Monitor
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=exa
Group=exa
Environment=PORT=8080
Environment=HOST=127.0.0.1
Environment=DATA_DIR=/var/lib/exa
Environment=BASE_URL=https://status.example.com
Environment=COOKIE_SECURE=true
Environment=TRUST_PROXY=true
ExecStart=/usr/local/bin/exa
Restart=on-failure
RestartSec=3
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/var/lib/exa
AmbientCapabilities=CAP_NET_RAW

[Install]
WantedBy=multi-user.target
```

4. Enable and start the service:

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now exa
sudo systemctl status exa
```

---

### 3.3 Reverse Proxy (HTTPS + SSE)

Exa serves HTTP and streams live updates via Server-Sent Events (`/api/v1/stream`). Always terminate TLS at your reverse proxy, disable response buffering for SSE, and overwrite forwarding headers when `TRUST_PROXY=true`.

#### Caddy

```caddyfile
status.example.com {
    reverse_proxy 127.0.0.1:8080 {
        flush_interval -1
        header_up X-Forwarded-For {remote_host}
        header_up X-Forwarded-Proto {scheme}
    }
}
```

#### Nginx

```nginx
server {
    listen 443 ssl http2;
    server_name status.example.com;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_buffering off;
        proxy_cache off;
        proxy_read_timeout 3600s;
    }
}
```

---

## 4. Security & Operations

- **Authentication & Sessions**: browser logins issue a random session token returned in the `exa_session` cookie (`HttpOnly`, `SameSite=Lax`, 7-day TTL) while storing only its SHA-256 hash in SQLite. API automation uses bearer tokens (`Authorization: Bearer exa_<hex>`).
- **Roles**: `admin` has full access; `viewer` can read overview, monitors (with `push_token` redacted), groups, incidents, maintenances, and status pages, and cannot view or modify notification channels, subscribers, users, API keys, settings, or backups.
- **Password Policy**: initial setup, user creation, and password updates require at least `10` characters. Changing password revokes all existing sessions for that user and reissues the current session.
- **CSRF Protection**: state-changing (`POST`, `PUT`, `PATCH`, `DELETE`) requests authenticated via `exa_session` cookie validate `Sec-Fetch-Site` (`same-origin` / `none`) and `Origin` against the request `Host`.
- **Rate & Connection Limits**:
  - `POST /api/v1/auth/login`: 10 attempts per 5 minutes per IP, and 10 per 5 minutes per username
  - `POST /api/v1/setup`: 5 attempts per 10 minutes per IP
  - `POST /api/v1/status/{slug}/subscribe`: 5 attempts per 10 minutes per IP; capped at 500 active subscribers per status page
  - `GET` / `POST /api/v1/push/{token}`: 120 requests per minute per IP
  - `GET /api/v1/stream`: capped at 500 concurrent SSE connections globally and 10 per IP
- **Subscriber Webhook SSRF Guard**: when `allow_subscriptions=true`, public webhook subscribers require `https://`, non-private routable IP addresses at subscribe and delivery time, and reject HTTP `3xx` redirects.
- **Response Security Headers**: Exa attaches `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: strict-origin-when-cross-origin`, `Permissions-Policy: camera=(), microphone=(), geolocation=()`, and a self-contained `Content-Security-Policy` (`default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'`).
- **Backups**: exported backup JSON (`GET /api/v1/backup/export`) contains monitor configs (including `push_token`), notification channel secrets, and subscriber records. Store exports securely. Backup imports (`POST /api/v1/backup/import`) accept payloads up to `10 MiB`.

---

## 5. HTTP API Reference

All JSON API errors use the format:

```json
{
  "error": "human-readable message"
}
```

Authenticated endpoints accept either:
- Session cookie (`Cookie: exa_session=...`)
- Bearer API key (`Authorization: Bearer exa_...`)

### 5.1 Health, Push, Stream, Metrics, Badges & Feeds

| Method | Path | Auth | Description |
|---|---|---|---|
| `GET` | `/api/v1/health` | Public | Returns `{"status":"ok","version":"...","uptime_sec":123}` |
| `GET`, `POST` | `/api/v1/push/{token}` | Token in path | Records a heartbeat for a `push` monitor. Query params or POST JSON fields: `status` (`up`, `degraded`, `down`; default `up`), `latency_ms` or `ping` (int >= 0), `msg` or `message` (string). Returns `{"ok":true,"monitor_id":"...","current_status":"..."}` |
| `GET` | `/api/v1/stream` | Optional | Server-Sent Events stream (`heartbeat`, `status_change`, `incident_update`, `maintenance_update`). Unauthenticated callers pass `?slug=<page_slug>` (defaults to the default public page) and receive only sanitized events for monitors/incidents belonging to that public page; authenticated callers receive full events |
| `GET` | `/metrics` | Bearer `metrics_token` | Prometheus text exposition (`exa_uptime_seconds`, `exa_monitor_up`, `exa_monitor_latency_ms`, `exa_monitor_tls_days_left`). Returns `401` if `metrics_token` is unset or mismatched |
| `GET` | `/api/v1/badge/{slug}/status`, `/api/v1/badge/{slug}/status.svg` | Public if on a public page, else Auth | Shield SVG badge for monitor `current_status` |
| `GET` | `/api/v1/badge/{slug}/uptime`, `/api/v1/badge/{slug}/uptime.svg` | Public if on a public page, else Auth | Shield SVG badge for 90-day uptime percentage |
| `GET` | `/api/v1/status/feed.rss`, `/api/v1/status/{slug}/feed.rss` | Public if page is public, else Auth | RSS 2.0 incident feed |
| `GET` | `/api/v1/status/feed.atom`, `/api/v1/status/{slug}/feed.atom` | Public if page is public, else Auth | Atom 1.0 incident feed |

### 5.2 Public Status & Subscriptions

| Method | Path | Auth | Description |
|---|---|---|---|
| `GET` | `/api/v1/status` | Public (if default page is public) | Full payload for the default status page (`page`, `overall_status`, `overall_label`, `groups` with 90-day `days`, `ungrouped_monitors`, `active_incidents`, `active_maintenances`, `upcoming_maintenances`, `past_incidents`, `other_pages`, `subscriptions_enabled`) |
| `GET` | `/api/v1/status/{slug}` | Public if `is_public=true` | Same payload for the specified status page slug (or `default`) |
| `GET` | `/api/v1/status/{slug}/incidents` | Public if `is_public=true` | Paginated incident list. Query params: `page` (default `1`), `limit` (`1..100`, default `20`). Returns `{incidents, total, page, limit}` |
| `POST` | `/api/v1/status/{slug}/subscribe` | Public (if `allow_subscriptions=true`) | Body: `{"type":"email"|"webhook","endpoint":"..."}`. Returns `403` when public subscriptions are disabled, `201` on success |
| `GET` | `/api/v1/unsubscribe/{token}` | Public token | Deactivates a subscriber matching `{token}` |

### 5.3 Setup & Authentication

| Method | Path | Auth | Description |
|---|---|---|---|
| `GET` | `/api/v1/setup/status` | Public | Returns `{"setup_completed":true|false}` |
| `POST` | `/api/v1/setup` | Public (once) | Creates initial `admin` user + default status page and sets `exa_session` cookie. Body: `{"username","email","password","site_title"}` (`password` >= 10 chars). Returns `409` if already completed |
| `POST` | `/api/v1/auth/login` | Public | Body: `{"username","password"}`. Sets `exa_session` cookie and returns `{"user":...}` |
| `POST` | `/api/v1/auth/logout` | Public | Revokes current session token and clears `exa_session` cookie |
| `GET` | `/api/v1/auth/me` | Viewer+ | Returns current user or API key principal |
| `PUT` | `/api/v1/auth/password` | Session user (Viewer+) | Body: `{"current_password","new_password"}` (`new_password` >= 10 chars). Revokes old sessions and issues a new cookie |

### 5.4 Overview, Monitors & Component Groups

| Method | Path | Role | Description |
|---|---|---|---|
| `GET` | `/api/v1/overview` | Viewer+ | Admin dashboard snapshot (`total_monitors`, `operational_monitors`, `degraded_monitors`, `down_monitors`, `paused_monitors`, `maintenance_monitors`, `uptime_30d`, `downtime_30d_sec`, `median_response_24h_ms`, `median_response_prev_24h_ms`, `open_incidents_count`, `availability_24h_pct`, `availability_24h`, `response_series_24h`, `active_alerts`, `monitors`, `activity`, `tls_certificates`) |
| `GET` | `/api/v1/monitors` | Viewer+ | Lists all monitors (`push_token` omitted for `viewer`) |
| `POST` | `/api/v1/monitors` | Admin | Creates a monitor and starts scheduling if `active` is true |
| `GET` | `/api/v1/monitors/{id}` | Viewer+ | Monitor detail with `heartbeats`, `daily_rollups`, `recent_checks_60`, `uptime_24h`/`30d`/`90d`, `avg_latency_24h`, `status_since`, `incidents`. Query param: `range=1h|24h|7d|30d` (default `24h`) |
| `PUT` | `/api/v1/monitors/{id}` | Admin | Updates monitor configuration and channel/status-page links |
| `DELETE` | `/api/v1/monitors/{id}` | Admin | Deletes monitor and stops scheduler loop |
| `POST` | `/api/v1/monitors/{id}/pause` | Admin | Sets `active=false` and pauses checks |
| `POST` | `/api/v1/monitors/{id}/resume` | Admin | Sets `active=true` and resumes checks |
| `POST` | `/api/v1/monitors/{id}/test` | Admin | Runs a synchronous probe immediately and returns `ProbeResult` without waiting for the next tick |
| `GET` | `/api/v1/groups` | Viewer+ | Lists component groups |
| `POST` | `/api/v1/groups` | Admin | Creates a component group (`name`, `description`, `sort_order`, `collapsed_default`) |
| `PUT` | `/api/v1/groups/{id}` | Admin | Updates a component group |
| `DELETE` | `/api/v1/groups/{id}` | Admin | Deletes a component group |

#### Monitor Upsert Payload & `config` Schemas

`POST /api/v1/monitors` and `PUT /api/v1/monitors/{id}` accept:
- `name` (string), `slug` (string), `type` (`http` | `tcp` | `ping` | `dns` | `push`), `target` (string)
- `group_id` (`string|null`), `interval_sec` (int), `timeout_sec` (int), `retries` (int), `degraded_ms` (int), `active` (bool), `sort_order` (int)
- `config` (JSON object) or `config_json` (JSON string)
- `notification_channel_ids` (`string[]`), `attach_default_page` (`bool`), `status_page_ids` (`string[]`)

Type-specific `config` fields:
- **`http`**: `method` (default `GET`), `expected_status` (default `200-299`), `keyword`, `keyword_invert` (bool), `json_path`, `json_expected`, `headers` (`map[string]string`), `body` (string), `follow_redirects` (bool, default `true`), `ignore_tls` (bool), `tls_expiry_warn_days` (int, default `14`)
- **`tcp`**: `send_data` (string), `expect_data` (string)
- **`ping`**: `packet_count` (int, default `2`)
- **`dns`**: `record_type` (`A`, `AAAA`, `CNAME`, `MX`, `TXT`, `NS`; default `A`), `resolver` (`host:port`), `expected_value` (string)
- **`push`**: `grace_sec` (int, default `60`)

### 5.5 Incidents, Maintenances & Status Pages

| Method | Path | Role | Description |
|---|---|---|---|
| `GET` | `/api/v1/incidents` | Viewer+ | Lists up to 200 incidents; optional `?status_page_id=<id>` |
| `POST` | `/api/v1/incidents` | Admin | Body: `{"status_page_id","title","impact":"none|minor|major|critical","status":"investigating|identified|monitoring|resolved","message","monitor_ids":[]}` |
| `POST` | `/api/v1/incidents/{id}/updates` | Admin | Appends timeline update: `{"status","message"}` |
| `DELETE` | `/api/v1/incidents/{id}` | Admin | Deletes incident |
| `GET` | `/api/v1/maintenances` | Viewer+ | Lists maintenances; optional `?status_page_id=<id>` |
| `POST` | `/api/v1/maintenances` | Admin | Body: `{"status_page_id","title","description","scheduled_start","scheduled_end","auto_suppress_alerts":true,"monitor_ids":[]}` |
| `PUT` | `/api/v1/maintenances/{id}` | Admin | Updates maintenance window |
| `DELETE` | `/api/v1/maintenances/{id}` | Admin | Deletes maintenance window |
| `GET` | `/api/v1/status-pages` | Viewer+ | Lists configured status pages |
| `POST` | `/api/v1/status-pages` | Admin | Body: `{"slug","title","description","custom_css","is_default":false,"is_public":true,"show_latency":true}` |
| `PUT` | `/api/v1/status-pages/{id}` | Admin | Updates status page metadata |
| `DELETE` | `/api/v1/status-pages/{id}` | Admin | Deletes non-default status page (`400` if `is_default=true`) |
| `PUT` | `/api/v1/status-pages/{id}/monitors` | Admin | Replaces attached monitors (`{"monitors":[{"monitor_id","group_id","display_name","sort_order"}]}`) |

### 5.6 Notifications & Subscribers (Admin Only)

| Method | Path | Role | Description |
|---|---|---|---|
| `GET` | `/api/v1/notifications` | Admin | Lists notification channels (secret fields masked as `"********"`) |
| `POST` | `/api/v1/notifications` | Admin | Body: `{"name","type","config":{...},"is_default":false,"enabled":true}` |
| `PUT` | `/api/v1/notifications/{id}` | Admin | Updates channel; sending `"********"` or omitting a secret preserves the existing stored secret |
| `DELETE` | `/api/v1/notifications/{id}` | Admin | Deletes notification channel |
| `POST` | `/api/v1/notifications/{id}/test` | Admin | Dispatches a test alert using the stored channel config |
| `GET` | `/api/v1/subscribers` | Admin | Lists subscribers; optional `?status_page_id=<id>` |
| `DELETE` | `/api/v1/subscribers/{id}` | Admin | Removes a subscriber |

Notification `config` schemas by `type`:
- **`webhook`**: `{"url":"https://...","secret":"hmac-key","headers":{"X-Custom":"val"}}` (`secret` and sensitive header values are masked)
- **`discord`**: `{"webhook_url":"https://discord.com/api/webhooks/...","username":"Exa"}`
- **`slack`**: `{"webhook_url":"https://hooks.slack.com/services/...","channel":"#alerts"}`
- **`telegram`**: `{"bot_token":"123456:ABC...","chat_id":"-100..."}` (`bot_token` is masked)
- **`email`**: `{"smtp_host":"smtp.example.com","smtp_port":587,"username":"...","password":"...","from":"alerts@example.com","to":["ops@example.com"],"use_tls":true}` (`password` is masked)
- **`ntfy`**: `{"server_url":"https://ntfy.sh","topic":"exa-alerts","token":"tk_...","priority":"high"}` (`token` is masked)

### 5.7 API Keys, Users, Settings & Backups (Admin Only)

| Method | Path | Role | Description |
|---|---|---|---|
| `GET` | `/api/v1/api-keys` | Admin | Lists API key metadata (hashes never returned) |
| `POST` | `/api/v1/api-keys` | Admin | Body: `{"name","role":"admin|viewer"}`. Returns created metadata plus raw `token` once |
| `DELETE` | `/api/v1/api-keys/{id}` | Admin | Revokes API key |
| `GET` | `/api/v1/users` | Admin | Lists users |
| `POST` | `/api/v1/users` | Admin | Body: `{"username","email","password","role":"admin|viewer"}` (`password` >= 10 chars) |
| `DELETE` | `/api/v1/users/{id}` | Admin | Deletes user (cannot delete self) |
| `GET` | `/api/v1/settings` | Admin | Returns key-value settings map |
| `PUT` | `/api/v1/settings` | Admin | Body: `{"key":"value",...}` (ignores `setup_completed`) |
| `GET` | `/api/v1/backup/export` | Admin | Downloads `exa-backup.json` (`BackupData` JSON) |
| `POST` | `/api/v1/backup/import` | Admin | Restores `BackupData` JSON (max `10 MiB`) and reloads active monitors |

---

## 6. Common `curl` Examples

Create an HTTP monitor using an API key:

```sh
curl -fsS -X POST "https://status.example.com/api/v1/monitors" \
  -H "Authorization: Bearer exa_YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Public API",
    "slug": "public-api",
    "type": "http",
    "target": "https://api.example.com/health",
    "interval_sec": 60,
    "timeout_sec": 10,
    "retries": 2,
    "degraded_ms": 800,
    "attach_default_page": true,
    "config": {
      "method": "GET",
      "expected_status": "200-299",
      "tls_expiry_warn_days": 14
    }
  }'
```

Send a passive push heartbeat:

```sh
curl -fsS "https://status.example.com/api/v1/push/YOUR_PUSH_TOKEN?status=up&latency_ms=42&msg=backup_ok"
```

Scrape Prometheus metrics:

```sh
curl -fsS -H "Authorization: Bearer YOUR_METRICS_TOKEN" \
  "https://status.example.com/metrics"
```

Export a JSON backup:

```sh
curl -fsS -H "Authorization: Bearer exa_YOUR_API_KEY" \
  -o exa-backup.json \
  "https://status.example.com/api/v1/backup/export"
```
