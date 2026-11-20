# Exa

Exa is a lightweight, self-hosted uptime monitor and status page built as a single Go binary with an embedded SQLite database and Preact web interface.

## Features

- Active probes: HTTP/HTTPS (status code ranges, keyword match, JSON path check, TLS expiry warning), TCP (optional send/expect), ICMP ping (with OS fallback), and DNS (`A`, `AAAA`, `CNAME`, `MX`, `TXT`, `NS`).
- Passive push monitors: heartbeat endpoint for cron jobs and background workers with configurable grace intervals.
- Public status pages: multi-page support, collapsible component groups, 90-day daily uptime bars with interactive day summaries, 14-day incident timeline, and paginated incident history.
- Incidents and maintenance: timeline updates (`investigating`, `identified`, `monitoring`, `resolved`) and scheduled maintenance windows with optional alert suppression.
- Real-time updates: Server-Sent Events (SSE) stream for live monitor and status page refreshes.
- Notifications and subscribers: Webhook (HMAC-SHA256 signed), Discord, Slack, Telegram, Email (SMTP), and ntfy, plus optional public email/webhook subscribers.
- Integrations: Prometheus `/metrics`, SVG status and uptime badges, RSS 2.0 and Atom 1.0 feeds, API keys, and JSON backup export/import.

## Quick Start

### Binary

1. Build the frontend bundle and Go binary:

```sh
cd web
npm ci
npm run build
cd ..
CGO_ENABLED=0 go build -ldflags="-s -w" -o exa ./cmd/exa
```

2. Run Exa and open `http://localhost:8080/setup` to create the initial admin account (minimum 10-character password):

```sh
./exa
```

### Docker Compose

Start Exa with persistent SQLite storage in `./data`, read-only root filesystem, dropped capabilities, and loopback port binding (`127.0.0.1:8080`):

```sh
docker compose up -d --build
```

Open `http://127.0.0.1:8080/setup` to complete initial setup.

## Configuration

Exa reads configuration from environment variables or matching CLI flags (`-port`, `-host`, `-data-dir`, `-base-url`, `-version`).

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8080` | HTTP listen port |
| `HOST` | `0.0.0.0` | HTTP listen address |
| `DATA_DIR` | `./data` (`/data` in Docker) | Directory for `exa.db` SQLite files |
| `BASE_URL` | auto-detected from request | Public base URL used in feeds, push URLs, and unsubscribe links |
| `COOKIE_SECURE` | `false` (`true` if `BASE_URL` uses `https://`) | Set `Secure` flag on `exa_session` cookies |
| `TRUST_PROXY` | `false` | Trust `X-Forwarded-For` and `X-Forwarded-Proto` headers; enable only behind a proxy that overwrites them |

## Security Defaults

- Passwords for initial setup, admin-created users, and password changes require at least 10 characters.
- Prometheus `/metrics` returns `401` until you configure `metrics_token` in Admin Settings.
- Public status page subscriptions (`allow_subscriptions`) are disabled by default; enable them in Admin Settings when you want visitors to subscribe via email or HTTPS webhook.
- `docker-compose.yml` binds to `127.0.0.1:8080`, drops all Linux capabilities, sets `no-new-privileges:true`, and mounts the root filesystem read-only with `/data` and `/tmp` writable.
- `docker-compose.yml` sets `net.ipv4.ping_group_range: "0 2147483647"` so unprivileged ICMP ping monitors work without `CAP_NET_RAW`. If you deploy with `network_mode: host` or in a runtime that restricts container `sysctls`, grant `cap_add: [NET_RAW]` instead.

## Reverse Proxy

Exa streams live updates over Server-Sent Events at `/api/v1/stream`. Disable response buffering on the proxy so events reach browsers immediately, and overwrite forwarding headers when `TRUST_PROXY=true`.

### Nginx

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

### Caddy

```caddyfile
status.example.com {
    reverse_proxy 127.0.0.1:8080 {
        flush_interval -1
        header_up X-Forwarded-For {remote_host}
        header_up X-Forwarded-Proto {scheme}
    }
}
```

## Integrations

### Prometheus Metrics

Set `metrics_token` in Admin Settings to enable `/metrics` in Prometheus text exposition format (`exa_uptime_seconds`, `exa_monitor_up`, `exa_monitor_latency_ms`, `exa_monitor_tls_days_left`). Requests without a valid token receive `401`:

```sh
curl -H "Authorization: Bearer YOUR_METRICS_TOKEN" http://localhost:8080/metrics
```

### SVG Badges

Embed live monitor status or 90-day uptime badges in Markdown or HTML using the monitor slug:

```markdown
![Status](https://status.example.com/api/v1/badge/public-api/status.svg)
![Uptime](https://status.example.com/api/v1/badge/public-api/uptime.svg)
```

### RSS and Atom Feeds

Subscribe to incident updates for the default status page or a named slug:

- Default RSS: `/api/v1/status/feed.rss`
- Default Atom: `/api/v1/status/feed.atom`
- Page RSS: `/api/v1/status/{slug}/feed.rss`
- Page Atom: `/api/v1/status/{slug}/feed.atom`

### Push Monitor API

Create a `push` monitor in the Admin Console to get its unique push token, then ping it via `GET` or `POST`:

```sh
curl -fsS "https://status.example.com/api/v1/push/YOUR_PUSH_TOKEN?status=up&latency_ms=42&msg=ok"
```

Accepted query parameters: `status` (`up`, `degraded`, `down`; defaults to `up`), `latency_ms` (integer), and `msg` (status message).
