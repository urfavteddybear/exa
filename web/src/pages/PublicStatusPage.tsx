import { useCallback, useEffect, useMemo, useRef, useState } from 'preact/hooks';
import { api, ApiError, subscribeStatusStream } from '../api';
import { Header } from '../components/Header';
import { ChevronIcon, MoonIcon, SunIcon } from '../components/Icons';
import { Modal } from '../components/Modal';
import { StatusBadge, ToneIcon } from '../components/StatusBadge';
import { UptimeBar } from '../components/UptimeBar';
import type {
  ComponentStatus,
  Incident,
  Maintenance,
  PublicGroup,
  PublicMonitor,
  PublicStatusResponse,
  SubscriberType,
} from '../types';
import {
  componentStatusInfo,
  formatDateTime,
  formatDayLabel,
  formatUTCTimestamp,
  incidentImpactLabel,
  incidentStatusLabel,
  maintenanceStatusLabel,
  overallStatusInfo,
  statusSortWeight,
} from '../utils/format';
import type { ThemeMode } from '../utils/theme';

interface PublicStatusPageProps {
  slug?: string;
  onNavigate: (path: string) => void;
  theme: ThemeMode;
  onToggleTheme: () => void;
}

function worstGroupStatus(monitors: PublicMonitor[]): ComponentStatus {
  if (monitors.length === 0) return 'unknown';
  let best = monitors[0].current_status;
  let bestWeight = statusSortWeight(best);
  for (const m of monitors) {
    const w = statusSortWeight(m.current_status);
    if (w < bestWeight) {
      best = m.current_status;
      bestWeight = w;
    }
  }
  return best;
}

function MonitorCardRow({ monitor, showLatency }: { monitor: PublicMonitor; showLatency: boolean }) {
  const info = componentStatusInfo(monitor.current_status);
  return (
    <div class="monitor-row">
      <div class="monitor-row-top">
        <div class="monitor-name-wrap">
          <span class="monitor-name">{monitor.name}</span>
          <span class="monitor-meta-chips">
            {showLatency && monitor.last_latency_ms !== null ? (
              <span class="tag-pill mono">{monitor.last_latency_ms} ms</span>
            ) : null}
            {monitor.tls_days_left !== null ? (
              <span class="tag-pill mono">TLS {monitor.tls_days_left}d</span>
            ) : null}
          </span>
        </div>
        <StatusBadge tone={info.tone} label={info.label} size={16} />
      </div>
      <UptimeBar monitorName={monitor.name} days={monitor.days || []} uptime90d={monitor.uptime_90d} />
    </div>
  );
}

function GroupSection({ group, showLatency }: { group: PublicGroup; showLatency: boolean }) {
  const [open, setOpen] = useState(!group.collapsed_default);
  const groupStatus = componentStatusInfo(worstGroupStatus(group.monitors || []));

  return (
    <div class="group-block">
      <button
        type="button"
        class="group-header-btn"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
      >
        <div class="group-title-wrap">
          <ChevronIcon size={16} open={open} />
          <div>
            <div class="group-title">{group.name}</div>
            {group.description ? <p class="group-desc">{group.description}</p> : null}
          </div>
        </div>
        <StatusBadge tone={groupStatus.tone} label={groupStatus.label} size={16} />
      </button>

      {open ? (
        <div class="group-monitors">
          {group.monitors && group.monitors.length > 0 ? (
            group.monitors.map((m) => <MonitorCardRow key={m.id} monitor={m} showLatency={showLatency} />)
          ) : (
            <div class="monitor-row">
              <p class="form-help" style={{ margin: 0 }}>
                No monitors assigned to this group yet.
              </p>
            </div>
          )}
        </div>
      ) : null}
    </div>
  );
}

function IncidentNoticeCard({ incident }: { incident: Incident }) {
  const tone =
    incident.impact === 'critical' || incident.impact === 'major'
      ? 'down'
      : incident.impact === 'minor'
        ? 'degraded'
        : 'maint';
  return (
    <article class="notice-card">
      <div class="notice-card-header">
        <div>
          <h3 class="notice-card-title">{incident.title}</h3>
          <div class="notice-update-meta">
            {incidentImpactLabel(incident.impact)} | Updated{' '}
            <span class="mono">{formatUTCTimestamp(incident.updated_at)}</span>
          </div>
        </div>
        <StatusBadge
          tone={incident.status === 'resolved' ? 'up' : tone}
          label={incidentStatusLabel(incident.status)}
          size={15}
        />
      </div>
      {incident.updates && incident.updates.length > 0 ? (
        <ul class="notice-updates">
          {incident.updates.map((upd) => (
            <li key={upd.id} class="notice-update-item">
              <strong>{incidentStatusLabel(upd.status)}:</strong> {upd.message}
              <div class="editorial-update-time mono">{formatUTCTimestamp(upd.created_at)}</div>
            </li>
          ))}
        </ul>
      ) : null}
    </article>
  );
}

function MaintenanceNoticeCard({ maint }: { maint: Maintenance }) {
  return (
    <article class="notice-card">
      <div class="notice-card-header">
        <div>
          <h3 class="notice-card-title">{maint.title}</h3>
          <div class="notice-update-meta mono">
            {formatDateTime(maint.scheduled_start)} to {formatDateTime(maint.scheduled_end)}
          </div>
        </div>
        <StatusBadge tone="maint" label={maintenanceStatusLabel(maint.status)} size={15} />
      </div>
      {maint.description ? (
        <p style={{ margin: '10px 0 0', fontSize: '0.875rem' }}>{maint.description}</p>
      ) : null}
    </article>
  );
}

function groupPastIncidentsByDay(incidents: Incident[]): Array<{ dayKey: string; items: Incident[] }> {
  const todayKey = new Date().toISOString().slice(0, 10);
  const map = new Map<string, Incident[]>();
  map.set(todayKey, []);
  for (const inc of incidents) {
    const key = (inc.created_at || inc.updated_at || todayKey).slice(0, 10);
    const list = map.get(key) || [];
    list.push(inc);
    map.set(key, list);
  }
  return Array.from(map.entries())
    .sort((a, b) => b[0].localeCompare(a[0]))
    .map(([dayKey, items]) => ({ dayKey, items }));
}

export function PublicStatusPage({ slug, onNavigate, theme, onToggleTheme }: PublicStatusPageProps) {
  const [data, setData] = useState<PublicStatusResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [subscribeOpen, setSubscribeOpen] = useState(false);
  const [subType, setSubType] = useState<SubscriberType>('email');
  const [subEndpoint, setSubEndpoint] = useState('');
  const [subSubmitting, setSubSubmitting] = useState(false);
  const [subError, setSubError] = useState<string | null>(null);
  const [subSuccess, setSubSuccess] = useState<string | null>(null);

  const [historyOpen, setHistoryOpen] = useState(false);
  const [historyLoading, setHistoryLoading] = useState(false);
  const [historyError, setHistoryError] = useState<string | null>(null);
  const [historyItems, setHistoryItems] = useState<Incident[]>([]);
  const [historyPage, setHistoryPage] = useState(1);
  const [historyTotal, setHistoryTotal] = useState(0);

  const debounceRef = useRef<number | null>(null);
  const statusSeq = useRef(0);
  const statusApplied = useRef(0);
  const historySeq = useRef(0);

  const loadStatus = useCallback(
    async (silent = false) => {
      // Skip responses older than one already applied. Not "latest only": a silent
      // refresh superseding the first load must not leave the spinner stuck.
      const id = ++statusSeq.current;
      if (!silent) {
        setLoading(true);
        setError(null);
      }
      try {
        const res = await api.publicStatus(slug);
        if (id < statusApplied.current) return;
        statusApplied.current = id;
        setData(res);
        setError(null);
        document.title = res.page.title ? `${res.page.title} | Exa` : 'Exa Status';
      } catch (err) {
        if (!silent && id >= statusApplied.current) {
          setError(err instanceof ApiError ? err.message : 'Unable to load status page.');
        }
      } finally {
        if (!silent) setLoading(false);
      }
    },
    [slug],
  );

  useEffect(() => {
    void loadStatus(false);
  }, [loadStatus]);

  useEffect(() => {
    const pageSlug = data?.page?.slug || slug || '';
    const unsubscribe = subscribeStatusStream(pageSlug, () => {
      if (debounceRef.current !== null) {
        window.clearTimeout(debounceRef.current);
      }
      debounceRef.current = window.setTimeout(() => {
        void loadStatus(true);
      }, 450);
    });
    return () => {
      if (debounceRef.current !== null) {
        window.clearTimeout(debounceRef.current);
      }
      unsubscribe();
    };
  }, [data?.page?.slug, slug, loadStatus]);

  useEffect(() => {
    const styleId = 'exa-page-custom-css';
    let el = document.getElementById(styleId) as HTMLStyleElement | null;
    const css = data?.page?.custom_css?.trim() || '';
    if (css) {
      if (!el) {
        el = document.createElement('style');
        el.id = styleId;
        document.head.appendChild(el);
      }
      el.textContent = css;
    } else if (el) {
      el.remove();
    }
    return () => {
      document.getElementById(styleId)?.remove();
    };
  }, [data?.page?.custom_css]);

  const loadHistory = useCallback(
    async (pageNum: number) => {
      const targetSlug = data?.page?.slug || slug || 'default';
      const id = ++historySeq.current;
      setHistoryLoading(true);
      setHistoryError(null);
      try {
        const res = await api.publicIncidents(targetSlug, pageNum, 20);
        if (id !== historySeq.current) return;
        setHistoryItems(res.incidents || []);
        setHistoryTotal(res.total || 0);
        setHistoryPage(res.page || pageNum);
      } catch (err) {
        if (id !== historySeq.current) return;
        setHistoryError(err instanceof ApiError ? err.message : 'Unable to load incident history.');
      } finally {
        if (id === historySeq.current) setHistoryLoading(false);
      }
    },
    [data?.page?.slug, slug],
  );

  const openHistoryModal = () => {
    setHistoryOpen(true);
    void loadHistory(1);
  };

  const handleSubscribeSubmit = async (e: Event) => {
    e.preventDefault();
    if (!data?.page?.slug) return;
    const endpoint = subEndpoint.trim();
    if (!endpoint) {
      setSubError(subType === 'email' ? 'Enter an email address.' : 'Enter a webhook URL.');
      return;
    }
    setSubSubmitting(true);
    setSubError(null);
    setSubSuccess(null);
    try {
      const res = await api.subscribe(data.page.slug, { type: subType, endpoint });
      setSubSuccess(res.message || 'Subscription saved.');
      setSubEndpoint('');
    } catch (err) {
      setSubError(err instanceof ApiError ? err.message : 'Subscription failed.');
    } finally {
      setSubSubmitting(false);
    }
  };

  const totalMonitors = useMemo(() => {
    if (!data) return 0;
    const grouped = (data.groups || []).reduce((acc, g) => acc + (g.monitors?.length || 0), 0);
    return grouped + (data.ungrouped_monitors?.length || 0);
  }, [data]);

  const pastDays = useMemo(() => groupPastIncidentsByDay(data?.past_incidents || []), [data?.past_incidents]);

  const pageSlug = data?.page?.slug || slug || '';
  const rssHref = pageSlug
    ? `/api/v1/status/${encodeURIComponent(pageSlug)}/feed.rss`
    : '/api/v1/status/feed.rss';
  const atomHref = pageSlug
    ? `/api/v1/status/${encodeURIComponent(pageSlug)}/feed.atom`
    : '/api/v1/status/feed.atom';

  const bannerInfo = data ? overallStatusInfo(data.overall_status) : { label: '', tone: 'unknown' as const };
  const totalHistoryPages = Math.max(1, Math.ceil(historyTotal / 20));

  return (
    <div class="page-shell">
      <a href="#main-status" class="skip-link">
        Skip to service status
      </a>

      <Header
        title={data?.page?.title || 'Status'}
        currentSlug={data?.page?.slug}
        otherPages={data?.other_pages}
        onNavigate={onNavigate}
        onOpenSubscribe={
          data && Boolean(data.subscriptions_enabled)
            ? () => {
                setSubError(null);
                setSubSuccess(null);
                setSubscribeOpen(true);
              }
            : undefined
        }
        theme={theme}
        onToggleTheme={onToggleTheme}
      />

      <main id="main-status" class="main-content" style={{ paddingTop: '8px' }}>
        <div class="container-public">
          {loading ? (
            <div class="state-box" role="status" aria-live="polite">
              <h1 class="state-box-title">Loading service status</h1>
              <p class="state-box-desc" style={{ margin: 0 }}>
                Fetching current monitor readings and incident updates.
              </p>
            </div>
          ) : error ? (
            <div class="state-box" role="alert">
              <h1 class="state-box-title">Unable to load status page</h1>
              <p class="state-box-desc">{error}</p>
              <div class="action-row">
                <button type="button" class="btn btn-primary" onClick={() => void loadStatus(false)}>
                  Retry
                </button>
                <a
                  href="/admin"
                  class="btn"
                  onClick={(e) => {
                    e.preventDefault();
                    onNavigate('/admin');
                  }}
                >
                  Open Admin Console
                </a>
              </div>
            </div>
          ) : data ? (
            <>
              {data.page.description ? (
                <p style={{ margin: '0 0 16px', color: 'var(--text-muted)' }}>{data.page.description}</p>
              ) : null}

              {totalMonitors === 0 ? (
                <section class="status-banner status-banner--unknown" aria-label="Overall system status">
                  <div class="status-banner-main">
                    <ToneIcon tone="unknown" size={22} />
                    <div>
                      <h1 class="status-banner-title">No monitors configured yet</h1>
                      <p style={{ margin: '4px 0 0', fontSize: '0.875rem', color: 'var(--text-muted)' }}>
                        Open Admin Console to add your first HTTP, TCP, Ping, DNS, or Push check.
                      </p>
                    </div>
                  </div>
                  <a
                    href="/admin"
                    class="btn btn-primary"
                    onClick={(e) => {
                      e.preventDefault();
                      onNavigate('/admin');
                    }}
                  >
                    Open Admin Console
                  </a>
                </section>
              ) : (
                <section
                  class={`status-banner status-banner--${bannerInfo.tone}`}
                  aria-label="Overall system status"
                >
                  <div class="status-banner-main">
                    <ToneIcon tone={bannerInfo.tone} size={22} />
                    <h1 class="status-banner-title">{data.overall_label || bannerInfo.label}</h1>
                  </div>
                  <div class="status-banner-meta mono">Refreshed {formatUTCTimestamp(data.updated_at)}</div>
                </section>
              )}

              {data.active_incidents && data.active_incidents.length > 0 ? (
                <section class="section-block" aria-labelledby="active-incidents-heading">
                  <div class="section-heading-row">
                    <h2 id="active-incidents-heading" class="section-heading">
                      Active Incidents
                    </h2>
                  </div>
                  {data.active_incidents.map((inc) => (
                    <IncidentNoticeCard key={inc.id} incident={inc} />
                  ))}
                </section>
              ) : null}

              {data.active_maintenances && data.active_maintenances.length > 0 ? (
                <section class="section-block" aria-labelledby="active-maint-heading">
                  <div class="section-heading-row">
                    <h2 id="active-maint-heading" class="section-heading">
                      Maintenance in Progress
                    </h2>
                  </div>
                  {data.active_maintenances.map((m) => (
                    <MaintenanceNoticeCard key={m.id} maint={m} />
                  ))}
                </section>
              ) : null}

              {data.upcoming_maintenances && data.upcoming_maintenances.length > 0 ? (
                <section class="section-block" aria-labelledby="upcoming-maint-heading">
                  <div class="section-heading-row">
                    <h2 id="upcoming-maint-heading" class="section-heading">
                      Scheduled Maintenance
                    </h2>
                  </div>
                  {data.upcoming_maintenances.map((m) => (
                    <MaintenanceNoticeCard key={m.id} maint={m} />
                  ))}
                </section>
              ) : null}

              {totalMonitors > 0 ? (
                <section class="section-block" aria-labelledby="components-heading">
                  <h2 id="components-heading" class="sr-only">
                    Services
                  </h2>
                  <div class="status-legend-bar">
                    <div class="legend-swatches">
                      <span class="legend-item">
                        <span class="legend-swatch legend-swatch--up" aria-hidden="true" />
                        Operational
                      </span>
                      <span class="legend-item">
                        <span class="legend-swatch legend-swatch--degraded" aria-hidden="true" />
                        Degraded
                      </span>
                      <span class="legend-item">
                        <span class="legend-swatch legend-swatch--down" aria-hidden="true" />
                        Outage
                      </span>
                    </div>
                    <div>
                      <span>Uptime over the past 90 days. </span>
                      <button
                        type="button"
                        class="btn-link"
                        style={{ padding: '0 2px', color: 'var(--text)' }}
                        onClick={openHistoryModal}
                      >
                        View historical uptime.
                      </button>
                    </div>
                  </div>

                  <div class="components-card">
                    {(data.groups || []).map((g) => (
                      <GroupSection key={g.id} group={g} showLatency={data.page.show_latency} />
                    ))}

                    {(data.ungrouped_monitors || []).map((m) => (
                      <MonitorCardRow key={m.id} monitor={m} showLatency={data.page.show_latency} />
                    ))}
                  </div>
                </section>
              ) : null}

              <section class="section-block" aria-labelledby="past-incidents-heading">
                <div class="section-heading-row">
                  <h2 id="past-incidents-heading" class="section-heading">
                    Past Incidents
                  </h2>
                </div>

                <div class="editorial-timeline">
                  {pastDays.map(({ dayKey, items }) => (
                    <div key={dayKey} class="editorial-day-group">
                      <h3 class="editorial-day-heading">{formatDayLabel(dayKey)}</h3>
                      {items.length === 0 ? (
                        <p style={{ margin: 0, color: 'var(--text-muted)', fontSize: '0.875rem' }}>
                          No incidents reported today.
                        </p>
                      ) : (
                        items.map((inc) => (
                          <article key={inc.id}>
                            <h4 class={`editorial-incident-title editorial-incident-title--${inc.impact}`}>
                              {inc.title}
                            </h4>
                            {inc.updates && inc.updates.length > 0 ? (
                              <ul class="editorial-updates-list">
                                {inc.updates.map((upd) => (
                                  <li key={upd.id} class="editorial-update-line">
                                    <strong>{incidentStatusLabel(upd.status)}</strong>{' '}
                                    <span>{upd.message}</span>
                                    <div class="editorial-update-time mono">
                                      {formatUTCTimestamp(upd.created_at)}
                                    </div>
                                  </li>
                                ))}
                              </ul>
                            ) : (
                              <div class="editorial-update-time mono">
                                {incidentStatusLabel(inc.status)} ({formatUTCTimestamp(inc.updated_at)})
                              </div>
                            )}
                          </article>
                        ))
                      )}
                    </div>
                  ))}
                </div>
              </section>
            </>
          ) : null}
        </div>
      </main>

      <footer class="site-footer">
        <div class="container-public">
          <div class="footer-inner">
            <div class="footer-links">
              <button type="button" class="btn-link" onClick={openHistoryModal}>
                Incident History
              </button>
              <a href={rssHref}>RSS</a>
              <a href={atomHref}>Atom</a>
            </div>
            <div class="footer-links">
              <button
                type="button"
                class="btn-link"
                onClick={onToggleTheme}
                aria-label={theme === 'dark' ? 'Switch to light theme' : 'Switch to dark theme'}
              >
                {theme === 'dark' ? <SunIcon size={15} /> : <MoonIcon size={15} />}
                <span>{theme === 'dark' ? 'Light theme' : 'Dark theme'}</span>
              </button>
              <a
                href="/admin"
                onClick={(e) => {
                  e.preventDefault();
                  onNavigate('/admin');
                }}
              >
                Admin Console
              </a>
              <span>Powered by Exa</span>
            </div>
          </div>
        </div>
      </footer>

      {Boolean(data?.subscriptions_enabled) ? (
        <Modal
          open={subscribeOpen}
          title={`Subscribe to ${data?.page?.title || 'status'} updates`}
          onClose={() => setSubscribeOpen(false)}
        >
        <div class="action-row" style={{ marginBottom: '16px' }}>
          <button
            type="button"
            class={`btn btn-sm${subType === 'email' ? ' btn-primary' : ''}`}
            onClick={() => {
              setSubType('email');
              setSubEndpoint('');
              setSubError(null);
              setSubSuccess(null);
            }}
          >
            Email
          </button>
          <button
            type="button"
            class={`btn btn-sm${subType === 'webhook' ? ' btn-primary' : ''}`}
            onClick={() => {
              setSubType('webhook');
              setSubEndpoint('');
              setSubError(null);
              setSubSuccess(null);
            }}
          >
            Webhook
          </button>
        </div>

        <form onSubmit={handleSubscribeSubmit}>
          {subError ? <div class="alert-inline alert-inline--error">{subError}</div> : null}
          {subSuccess ? <div class="alert-inline alert-inline--success">{subSuccess}</div> : null}

          <label class="form-field" style={{ marginBottom: '16px' }}>
            <span class="form-label">
              {subType === 'email' ? 'Email address' : 'Webhook endpoint URL'}
            </span>
            <input
              type={subType === 'email' ? 'email' : 'url'}
              class="input"
              required
              placeholder={subType === 'email' ? 'alerts@example.com' : 'https://hooks.example.com/status'}
              value={subEndpoint}
              onInput={(e) => setSubEndpoint((e.currentTarget as HTMLInputElement).value)}
            />
          </label>

          <div class="action-row" style={{ justifyContent: 'space-between' }}>
            <span class="form-help">
              Feeds: <a href={rssHref}>RSS</a> | <a href={atomHref}>Atom</a>
            </span>
            <button type="submit" class="btn btn-primary" disabled={subSubmitting}>
              {subSubmitting ? 'Subscribing...' : 'Subscribe'}
            </button>
          </div>
        </form>
        </Modal>
      ) : null}

      <Modal open={historyOpen} title="Incident History" onClose={() => setHistoryOpen(false)} wide>
        {historyLoading ? (
          <p class="form-help" role="status">
            Loading incident archive...
          </p>
        ) : historyError ? (
          <div class="alert-inline alert-inline--error">
            <span>{historyError}</span>
            <button type="button" class="btn btn-sm" onClick={() => void loadHistory(historyPage)}>
              Retry
            </button>
          </div>
        ) : historyItems.length === 0 ? (
          <p style={{ margin: 0, color: 'var(--text-muted)' }}>
            No archived incidents recorded for this status page yet.
          </p>
        ) : (
          <>
            <div class="editorial-timeline">
              {historyItems.map((inc) => (
                <IncidentNoticeCard key={inc.id} incident={inc} />
              ))}
            </div>
            <div class="action-row" style={{ justifyContent: 'space-between', marginTop: '16px' }}>
              <span class="form-help mono">
                Page {historyPage} of {totalHistoryPages} ({historyTotal} total)
              </span>
              <div class="action-row">
                <button
                  type="button"
                  class="btn btn-sm"
                  disabled={historyPage <= 1}
                  onClick={() => void loadHistory(historyPage - 1)}
                >
                  Previous
                </button>
                <button
                  type="button"
                  class="btn btn-sm"
                  disabled={historyPage >= totalHistoryPages}
                  onClick={() => void loadHistory(historyPage + 1)}
                >
                  Next
                </button>
              </div>
            </div>
          </>
        )}
      </Modal>
    </div>
  );
}
