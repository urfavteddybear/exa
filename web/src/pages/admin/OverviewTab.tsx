import { useCallback, useEffect, useMemo, useState } from 'preact/hooks';
import { api, ApiError } from '../../api';
import { OverviewResponseChart } from '../../components/LatencyChart';
import { StatusBadge } from '../../components/StatusBadge';
import { Availability60Bar, Sparkline30 } from '../../components/UptimeBar';
import type {
  AdminOverviewResponse,
  ComponentGroup,
  NotificationChannel,
  OverviewMonitor,
  StatusPage,
} from '../../types';
import {
  componentStatusInfo,
  formatClockHMS,
  formatDuration,
  formatDurationSince,
  formatShortTime,
  statusSortWeight,
} from '../../utils/format';
import { MonitorFormModal } from './MonitorFormModal';

interface OverviewTabProps {
  isAdmin: boolean;
  onNavigate: (path: string) => void;
  onRefreshCounts?: () => void;
}

type FilterMode = 'all' | 'down' | 'degraded' | 'paused';

export function OverviewTab({ isAdmin, onNavigate, onRefreshCounts }: OverviewTabProps) {
  const [data, setData] = useState<AdminOverviewResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [search, setSearch] = useState('');
  const [filter, setFilter] = useState<FilterMode>('all');
  const [busyId, setBusyId] = useState<string | null>(null);

  const [createOpen, setCreateOpen] = useState(false);
  const [groups, setGroups] = useState<ComponentGroup[]>([]);
  const [channels, setChannels] = useState<NotificationChannel[]>([]);
  const [pages, setPages] = useState<StatusPage[]>([]);

  const loadOverview = useCallback(async (silent = false) => {
    if (!silent) {
      setLoading(true);
      setError(null);
    }
    try {
      const res = await api.overview();
      setData(res);
      setError(null);
    } catch (err) {
      if (!silent) {
        setError(err instanceof ApiError ? err.message : 'Unable to load operations overview.');
      }
    } finally {
      if (!silent) setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadOverview(false);
  }, [loadOverview]);

  const openNewMonitor = async () => {
    setCreateOpen(true);
    try {
      const [gList, cList, pList] = await Promise.all([
        api.listGroups(),
        isAdmin ? api.listNotifications().catch(() => [] as NotificationChannel[]) : Promise.resolve([] as NotificationChannel[]),
        api.listStatusPages().catch(() => [] as StatusPage[]),
      ]);
      setGroups(gList || []);
      setChannels(cList || []);
      setPages(pList || []);
    } catch {
      // Optional auxiliary lists can remain empty if unreachable.
    }
  };

  const handlePauseFromAlert = async (monitorId: string) => {
    setBusyId(monitorId);
    try {
      await api.pauseMonitor(monitorId);
      await loadOverview(true);
      onRefreshCounts?.();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Unable to pause monitor.');
    } finally {
      setBusyId(null);
    }
  };

  const filteredMonitors = useMemo(() => {
    if (!data) return [];
    const q = search.trim().toLowerCase();
    return (data.monitors || [])
      .filter((m) => {
        if (filter === 'down' && m.current_status !== 'major_outage') return false;
        if (filter === 'degraded' && m.current_status !== 'degraded') return false;
        if (filter === 'paused' && m.active) return false;
        if (!q) return true;
        return (
          m.name.toLowerCase().includes(q) ||
          m.target.toLowerCase().includes(q) ||
          (m.group_name || '').toLowerCase().includes(q)
        );
      })
      .slice()
      .sort((a, b) => {
        const wa = statusSortWeight(a.current_status);
        const wb = statusSortWeight(b.current_status);
        if (wa !== wb) return wa - wb;
        return a.name.localeCompare(b.name);
      });
  }, [data, search, filter]);

  const groupedRows = useMemo(() => {
    const groupsMap = new Map<string, OverviewMonitor[]>();
    for (const m of filteredMonitors) {
      const g = m.group_name || 'Ungrouped';
      const arr = groupsMap.get(g) || [];
      arr.push(m);
      groupsMap.set(g, arr);
    }
    return Array.from(groupsMap.entries());
  }, [filteredMonitors]);

  const handleSearchSubmit = (e: Event) => {
    e.preventDefault();
    const q = search.trim();
    if (q && filteredMonitors.length === 1) {
      onNavigate(`/admin/monitors/${encodeURIComponent(filteredMonitors[0].id)}`);
    } else if (q) {
      onNavigate(`/admin/monitors?q=${encodeURIComponent(q)}`);
    }
  };

  if (loading) {
    return (
      <div class="state-box" role="status">
        <p class="state-box-title">Loading overview</p>
        <p class="state-box-desc" style={{ margin: 0 }}>
          Aggregating monitor status, availability windows, and recent events.
        </p>
      </div>
    );
  }

  if (error || !data) {
    return (
      <div class="state-box" role="alert">
        <p class="state-box-title">Unable to load overview</p>
        <p class="state-box-desc">{error || 'Overview data unavailable.'}</p>
        <button type="button" class="btn btn-primary" onClick={() => void loadOverview(false)}>
          Retry
        </button>
      </div>
    );
  }

  const deltaMs =
    data.median_response_24h_ms !== null && data.median_response_prev_24h_ms !== null
      ? data.median_response_24h_ms - data.median_response_prev_24h_ms
      : null;

  return (
    <div>
      <div class="admin-topbar">
        <div class="breadcrumb-row">
          <span>{data.default_page_title || 'Exa'}</span>
          <span aria-hidden="true">/</span>
          <span class="breadcrumb-current">Overview</span>
        </div>

        <div class="topbar-controls">
          <form onSubmit={handleSearchSubmit}>
            <input
              type="search"
              class="input search-inline"
              aria-label="Search monitors"
              placeholder="Search monitors"
              value={search}
              onInput={(e) => setSearch((e.currentTarget as HTMLInputElement).value)}
            />
          </form>
          {isAdmin ? (
            <button type="button" class="btn btn-primary" onClick={() => void openNewMonitor()}>
              New monitor
            </button>
          ) : null}
        </div>
      </div>

      <section class="hero-telemetry" aria-label="System telemetry summary">
        <div>
          <div class="hero-eyebrow mono">
            SYSTEM STATUS, REFRESHED {formatClockHMS(data.refreshed_at)}
          </div>
          <div class="hero-big-stat">
            <span class="hero-big-number">
              {data.operational_monitors}/{data.total_monitors}
            </span>
            <span class="hero-big-label">monitors operational</span>
          </div>
        </div>

        <div class="kpi-strip">
          <div class="kpi-cell">
            <div class="kpi-label">Uptime, 30 days</div>
            <div class="kpi-value">
              {data.uptime_30d !== null ? `${data.uptime_30d.toFixed(2)}%` : 'No data'}
            </div>
            <div class="kpi-sub mono">
              {data.downtime_30d_sec > 0 ? `${formatDuration(data.downtime_30d_sec)} downtime` : '0s downtime'}
            </div>
          </div>

          <div class="kpi-cell">
            <div class="kpi-label">Median response, 24 h</div>
            <div class="kpi-value">
              {data.median_response_24h_ms !== null ? `${data.median_response_24h_ms} ms` : 'No data'}
            </div>
            <div class="kpi-sub mono">
              {deltaMs !== null
                ? `${deltaMs >= 0 ? '+' : ''}${deltaMs} ms vs prev 24h`
                : 'Last 24h samples'}
            </div>
          </div>

          <div class="kpi-cell">
            <div class="kpi-label">Open incidents</div>
            <div class="kpi-value">{data.open_incidents_count}</div>
            <div class="kpi-sub">
              {data.down_monitors} down, {data.degraded_monitors} degraded
            </div>
          </div>
        </div>
      </section>

      <section class="panel-card" style={{ marginBottom: '20px' }} aria-label="24-hour availability">
        <div class="panel-head" style={{ marginBottom: '10px' }}>
          <h2 class="panel-title">Availability across all monitors, last 24 h</h2>
          <span class="mono" style={{ fontSize: '0.875rem', fontWeight: 600 }}>
            {data.availability_24h_pct !== null ? `${data.availability_24h_pct.toFixed(2)}%` : 'No data'}
          </span>
        </div>
        <Availability60Bar buckets={data.availability_24h || []} />
      </section>

      {(data.active_alerts || []).map((alert) => {
        const info = componentStatusInfo(alert.current_status);
        const sinceDur = formatDurationSince(alert.since);
        return (
          <div key={alert.monitor_id} class="alert-callout" role="alert">
            <div>
              <div class="action-row" style={{ marginBottom: '4px' }}>
                <StatusBadge tone={info.tone} label={info.label} size={15} />
                <strong>{alert.monitor_name}</strong>
                {sinceDur ? <span class="form-help mono">for {sinceDur}</span> : null}
              </div>
              <div class="form-help">
                <span class="mono">{alert.target}</span>
                {alert.last_error ? ` | ${alert.last_error}` : ''}
              </div>
            </div>
            <div class="action-row">
              {isAdmin ? (
                <button
                  type="button"
                  class="btn btn-sm"
                  disabled={busyId === alert.monitor_id}
                  onClick={() => void handlePauseFromAlert(alert.monitor_id)}
                >
                  {busyId === alert.monitor_id ? 'Pausing...' : 'Pause'}
                </button>
              ) : null}
              <button
                type="button"
                class="btn btn-sm"
                onClick={() =>
                  onNavigate(`/admin/monitors/${encodeURIComponent(alert.monitor_id)}`)
                }
              >
                View monitor
              </button>
            </div>
          </div>
        );
      })}

      <div class="overview-grid">
        <section class="panel-card panel-card--flush" aria-label="Monitors summary">
          <div class="panel-head">
            <h2 class="panel-title">Monitors</h2>
            <div class="filter-pills" role="group" aria-label="Filter monitors">
              {(['all', 'down', 'degraded', 'paused'] as FilterMode[]).map((key) => (
                <button
                  key={key}
                  type="button"
                  class={`filter-pill${filter === key ? ' filter-pill--active' : ''}`}
                  aria-pressed={filter === key}
                  onClick={() => setFilter(key)}
                >
                  {key === 'all'
                    ? 'All'
                    : key === 'down'
                      ? `Down (${data.down_monitors})`
                      : key === 'degraded'
                        ? `Degraded (${data.degraded_monitors})`
                        : `Paused (${data.paused_monitors})`}
                </button>
              ))}
            </div>
          </div>

          {data.total_monitors === 0 ? (
            <div style={{ padding: '24px' }}>
              <p class="state-box-title">No monitors configured yet</p>
              <p class="state-box-desc">
                Add your first HTTP, TCP, Ping, DNS, or Push check to see live uptime and latency.
              </p>
              {isAdmin ? (
                <button
                  type="button"
                  class="btn btn-primary"
                  onClick={() => void openNewMonitor()}
                >
                  New monitor
                </button>
              ) : null}
            </div>
          ) : filteredMonitors.length === 0 ? (
            <div style={{ padding: '24px' }}>
              <p class="form-help" style={{ margin: 0 }}>
                No monitors match the selected filter.
              </p>
            </div>
          ) : (
            <div class="table-scroll">
              <table class="data-table">
                <thead>
                  <tr>
                    <th>Monitor</th>
                    <th>Status</th>
                    <th>Last 30 checks</th>
                    <th>Uptime 24 h</th>
                    <th>Response</th>
                  </tr>
                </thead>
                <tbody>
                  {groupedRows.map(([groupName, rows]) => (
                    <>
                      <tr key={`g-${groupName}`} class="group-divider-row">
                        <td colSpan={5}>{groupName}</td>
                      </tr>
                      {rows.slice(0, 20).map((m) => {
                        const info = componentStatusInfo(m.current_status);
                        return (
                          <tr
                            key={m.id}
                            class="table-row-clickable"
                            tabIndex={0}
                            onClick={() =>
                              onNavigate(`/admin/monitors/${encodeURIComponent(m.id)}`)
                            }
                            onKeyDown={(e) => {
                              if (e.key === 'Enter' || e.key === ' ') {
                                e.preventDefault();
                                onNavigate(`/admin/monitors/${encodeURIComponent(m.id)}`);
                              }
                            }}
                          >
                            <td>
                              <strong>{m.name}</strong>
                              <div class="form-help mono">{m.target}</div>
                            </td>
                            <td>
                              <StatusBadge
                                tone={info.tone}
                                label={m.active ? info.label : 'Paused'}
                                size={14}
                              />
                            </td>
                            <td>
                              <Sparkline30 checks={m.recent_checks || []} />
                            </td>
                            <td class="mono">
                              {m.uptime_24h !== null ? `${m.uptime_24h.toFixed(2)}%` : 'No data'}
                            </td>
                            <td class="mono">
                              {m.last_latency_ms !== null ? `${m.last_latency_ms} ms` : '-'}
                            </td>
                          </tr>
                        );
                      })}
                    </>
                  ))}
                </tbody>
              </table>
            </div>
          )}

          <div
            style={{
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'space-between',
              padding: '12px 16px',
              borderTop: '1px solid var(--border)',
              fontSize: '0.8125rem',
            }}
          >
            <span class="form-help">
              Showing {Math.min(filteredMonitors.length, data.total_monitors)} of {data.total_monitors}
            </span>
            <a
              href="/admin/monitors"
              onClick={(e) => {
                e.preventDefault();
                onNavigate('/admin/monitors');
              }}
            >
              View all monitors
            </a>
          </div>
        </section>

        <aside class="overview-rail" aria-label="Response time, activity, and TLS summary">
          <section class="panel-card">
            <div class="panel-head">
              <h2 class="panel-title">Response time</h2>
              <span class="form-help">Median, 24 h</span>
            </div>
            <OverviewResponseChart points={data.response_series_24h || []} />
          </section>

          <section class="panel-card">
            <div class="panel-head">
              <h2 class="panel-title">Activity</h2>
            </div>
            {!data.activity || data.activity.length === 0 ? (
              <p class="form-help" style={{ margin: 0 }}>
                No recent monitor or incident events recorded yet.
              </p>
            ) : (
              <ul class="activity-list">
                {data.activity.slice(0, 8).map((ev, idx) => (
                  <li key={`${ev.timestamp}-${idx}`} class="activity-item">
                    <span class={`activity-dot activity-dot--${ev.tone}`} aria-hidden="true" />
                    <div style={{ minWidth: 0 }}>
                      <div>
                        <strong>{ev.title}</strong>
                      </div>
                      {ev.detail ? <div class="form-help">{ev.detail}</div> : null}
                      <div class="form-help mono">{formatShortTime(ev.timestamp)}</div>
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </section>

          <section class="panel-card">
            <div class="panel-head">
              <h2 class="panel-title">TLS certificates</h2>
            </div>
            {!data.tls_certificates || data.tls_certificates.length === 0 ? (
              <p class="form-help" style={{ margin: 0 }}>
                No HTTPS certificate expiry data recorded yet.
              </p>
            ) : (
              <ul class="activity-list">
                {data.tls_certificates.map((cert) => {
                  const warn = cert.tls_days_left <= cert.warn_days;
                  return (
                    <li
                      key={cert.monitor_id}
                      class="activity-item"
                      style={{ justifyContent: 'space-between' }}
                    >
                      <div style={{ minWidth: 0 }}>
                        <a
                          href={`/admin/monitors/${encodeURIComponent(cert.monitor_id)}`}
                          onClick={(e) => {
                            e.preventDefault();
                            onNavigate(`/admin/monitors/${encodeURIComponent(cert.monitor_id)}`);
                          }}
                        >
                          {cert.name}
                        </a>
                        <div class="form-help mono">{cert.target}</div>
                      </div>
                      <span
                        class="mono"
                        style={{
                          fontWeight: 600,
                          color: warn ? 'var(--status-degraded)' : 'var(--status-up)',
                          whiteSpace: 'nowrap',
                        }}
                      >
                        {cert.tls_days_left}d left
                      </span>
                    </li>
                  );
                })}
              </ul>
            )}
          </section>
        </aside>
      </div>

      <MonitorFormModal
        open={createOpen}
        groups={groups}
        channels={channels}
        pages={pages}
        onClose={() => setCreateOpen(false)}
        onSaved={(saved) => {
          setCreateOpen(false);
          onRefreshCounts?.();
          onNavigate(`/admin/monitors/${encodeURIComponent(saved.id)}`);
        }}
      />
    </div>
  );
}
