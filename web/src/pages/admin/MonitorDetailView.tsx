import { useCallback, useEffect, useMemo, useRef, useState } from 'preact/hooks';
import { api, ApiError } from '../../api';
import { CopyIcon } from '../../components/Icons';
import { LatencyChart } from '../../components/LatencyChart';
import { Modal } from '../../components/Modal';
import { StatusBadge } from '../../components/StatusBadge';
import { Heartbeat60Strip } from '../../components/UptimeBar';
import type {
  ComponentGroup,
  HTTPConfig,
  MonitorDetailResponse,
  MonitorRange,
  NotificationChannel,
  ProbeResult,
  StatusPage,
} from '../../types';
import {
  componentStatusInfo,
  formatDateTime,
  formatDurationSince,
  heartbeatStatusInfo,
  incidentImpactLabel,
  incidentStatusLabel,
} from '../../utils/format';
import { MonitorFormModal } from './MonitorFormModal';

interface MonitorDetailViewProps {
  monitorId: string;
  isAdmin: boolean;
  onNavigate: (path: string) => void;
  onRefreshCounts?: () => void;
}

export function MonitorDetailView({
  monitorId,
  isAdmin,
  onNavigate,
  onRefreshCounts,
}: MonitorDetailViewProps) {
  const [detail, setDetail] = useState<MonitorDetailResponse | null>(null);
  const [groups, setGroups] = useState<ComponentGroup[]>([]);
  const [channels, setChannels] = useState<NotificationChannel[]>([]);
  const [pages, setPages] = useState<StatusPage[]>([]);
  const [range, setRange] = useState<MonitorRange>('24h');

  const [loading, setLoading] = useState(true);
  const [rangeLoading, setRangeLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState<ProbeResult | null>(null);
  const [actionBusy, setActionBusy] = useState(false);
  const [feedback, setFeedback] = useState<string | null>(null);
  const [copiedKey, setCopiedKey] = useState<string | null>(null);
  const [bottomTab, setBottomTab] = useState<'checks' | 'incidents'>('checks');

  const [editOpen, setEditOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const detailSeq = useRef(0);

  const loadDetail = useCallback(
    async (selectedRange: MonitorRange, initial = false) => {
      const id = ++detailSeq.current;
      if (initial) {
        setLoading(true);
        setError(null);
      } else {
        setRangeLoading(true);
      }
      try {
        const [res, gList, cList, pList] = await Promise.all([
          api.getMonitor(monitorId, selectedRange),
          initial ? api.listGroups().catch(() => [] as ComponentGroup[]) : Promise.resolve(null),
          initial && isAdmin
            ? api.listNotifications().catch(() => [] as NotificationChannel[])
            : Promise.resolve(null),
          initial ? api.listStatusPages().catch(() => [] as StatusPage[]) : Promise.resolve(null),
        ]);
        if (gList) setGroups(gList);
        if (cList) setChannels(cList);
        if (pList) setPages(pList);
        // Drop responses superseded by a newer request (e.g. fast range switching).
        if (id !== detailSeq.current) return;
        setDetail(res);
        setError(null);
      } catch (err) {
        if (id !== detailSeq.current) return;
        setError(err instanceof ApiError ? err.message : 'Unable to load monitor details.');
      } finally {
        if (id === detailSeq.current) {
          setLoading(false);
          setRangeLoading(false);
        }
      }
    },
    [monitorId, isAdmin],
  );

  useEffect(() => {
    setTestResult(null);
    setFeedback(null);
    void loadDetail(range, true);
  }, [monitorId]);

  const handleRangeChange = (nextRange: MonitorRange) => {
    setRange(nextRange);
    void loadDetail(nextRange, false);
  };

  const handleTestNow = async () => {
    if (!detail) return;
    setTesting(true);
    setFeedback(null);
    try {
      const res = await api.testMonitor(detail.id);
      setTestResult(res);
      await loadDetail(range, false);
      onRefreshCounts?.();
    } catch (err) {
      setFeedback(err instanceof ApiError ? err.message : 'Test check failed.');
    } finally {
      setTesting(false);
    }
  };

  const handleTogglePause = async () => {
    if (!detail) return;
    setActionBusy(true);
    setFeedback(null);
    try {
      if (detail.active) {
        await api.pauseMonitor(detail.id);
        setFeedback('Monitor paused.');
      } else {
        await api.resumeMonitor(detail.id);
        setFeedback('Monitor resumed.');
      }
      await loadDetail(range, false);
      onRefreshCounts?.();
    } catch (err) {
      setFeedback(err instanceof ApiError ? err.message : 'Unable to update monitor state.');
    } finally {
      setActionBusy(false);
    }
  };

  const handleDelete = async () => {
    if (!detail) return;
    setDeleting(true);
    try {
      await api.deleteMonitor(detail.id);
      setDeleteOpen(false);
      onRefreshCounts?.();
      onNavigate('/admin/monitors');
    } catch (err) {
      setFeedback(err instanceof ApiError ? err.message : 'Delete failed.');
      setDeleteOpen(false);
    } finally {
      setDeleting(false);
    }
  };

  const handleCopy = async (key: string, value: string) => {
    try {
      await navigator.clipboard.writeText(value);
      setCopiedKey(key);
      window.setTimeout(() => setCopiedKey(null), 2000);
    } catch {
      setFeedback('Clipboard write was blocked by browser permissions.');
    }
  };

  const groupName = useMemo(() => {
    if (!detail?.group_id) return '';
    return groups.find((g) => g.id === detail.group_id)?.name || '';
  }, [detail?.group_id, groups]);

  if (loading) {
    return (
      <div class="state-box" role="status">
        <p class="state-box-title">Loading monitor</p>
        <p class="state-box-desc" style={{ margin: 0 }}>
          Fetching probe configuration, heartbeats, and uptime metrics.
        </p>
      </div>
    );
  }

  if (error || !detail) {
    return (
      <div class="state-box" role="alert">
        <p class="state-box-title">Unable to load monitor</p>
        <p class="state-box-desc">{error || 'Monitor not found.'}</p>
        <div class="action-row">
          <button
            type="button"
            class="btn"
            onClick={() => onNavigate('/admin/monitors')}
          >
            Back to Monitors
          </button>
          <button
            type="button"
            class="btn btn-primary"
            onClick={() => void loadDetail(range, true)}
          >
            Retry
          </button>
        </div>
      </div>
    );
  }

  const statusInfo = componentStatusInfo(detail.current_status);
  const statusDur = formatDurationSince(detail.status_since || detail.updated_at);
  const httpCfg = (detail.config || {}) as HTTPConfig;
  const methodPrefix =
    detail.type === 'http'
      ? (httpCfg.method || 'GET').toUpperCase()
      : detail.type.toUpperCase();

  const origin = window.location.origin;
  const pushUrl = detail.push_token ? `${origin}/api/v1/push/${detail.push_token}` : '';
  const badgeMarkdown = `![${detail.name} status](${origin}/api/v1/badge/${detail.slug}/status.svg)`;
  const checks60 =
    detail.recent_checks_60 && detail.recent_checks_60.length > 0
      ? detail.recent_checks_60
      : (detail.heartbeats || []).slice(0, 60).slice().reverse();

  return (
    <div>
      <div class="admin-topbar">
        <nav class="breadcrumb-row" aria-label="Breadcrumb">
          <a
            href="/admin/monitors"
            onClick={(e) => {
              e.preventDefault();
              onNavigate('/admin/monitors');
            }}
          >
            Monitors
          </a>
          {groupName ? (
            <>
              <span aria-hidden="true">/</span>
              <span>{groupName}</span>
            </>
          ) : null}
          <span aria-hidden="true">/</span>
          <span class="breadcrumb-current">{detail.name}</span>
        </nav>

        {isAdmin ? (
          <div class="topbar-controls">
            <button
              type="button"
              class="btn btn-sm"
              disabled={actionBusy}
              onClick={() => void handleTogglePause()}
            >
              {detail.active ? 'Pause' : 'Resume'}
            </button>
            <button type="button" class="btn btn-sm" onClick={() => setEditOpen(true)}>
              Edit
            </button>
            <button
              type="button"
              class="btn btn-primary btn-sm"
              disabled={testing}
              onClick={() => void handleTestNow()}
            >
              {testing ? 'Checking...' : 'Check now'}
            </button>
          </div>
        ) : null}
      </div>

      <section class="detail-hero">
        <div>
          <div class="action-row">
            <StatusBadge
              tone={statusInfo.tone}
              label={detail.active ? statusInfo.label : 'Paused'}
              size={15}
            />
            {statusDur ? <span class="form-help mono">for {statusDur}</span> : null}
          </div>
          <h1 class="detail-title">{detail.name}</h1>
          <div class="form-help mono">
            {methodPrefix} {detail.target}
          </div>
        </div>

        <div class="kpi-strip" style={{ minWidth: 'min(100%, 480px)' }}>
          <div class="kpi-cell">
            <div class="kpi-label">Current response</div>
            <div class="kpi-value">
              {detail.last_latency_ms !== null ? `${detail.last_latency_ms} ms` : 'No data'}
            </div>
            <div class="kpi-sub mono">
              {detail.avg_latency_24h !== null && detail.avg_latency_24h !== undefined
                ? `24h avg ${detail.avg_latency_24h} ms`
                : 'Last check'}
            </div>
          </div>

          <div class="kpi-cell">
            <div class="kpi-label">Uptime, 30 days</div>
            <div class="kpi-value">
              {detail.uptime_30d !== null && detail.uptime_30d !== undefined
                ? `${detail.uptime_30d.toFixed(2)}%`
                : 'No data'}
            </div>
            <div class="kpi-sub mono">
              24h{' '}
              {detail.uptime_24h !== null && detail.uptime_24h !== undefined
                ? `${detail.uptime_24h.toFixed(2)}%`
                : 'N/A'}{' '}
              | 90d{' '}
              {detail.uptime_90d !== null && detail.uptime_90d !== undefined
                ? `${detail.uptime_90d.toFixed(2)}%`
                : 'N/A'}
            </div>
          </div>

          <div class="kpi-cell">
            <div class="kpi-label">
              {detail.tls_days_left !== null ? 'Certificate' : 'Schedule'}
            </div>
            <div class="kpi-value">
              {detail.tls_days_left !== null
                ? `${detail.tls_days_left} days`
                : `${detail.interval_sec} s`}
            </div>
            <div class="kpi-sub">
              {detail.tls_days_left !== null ? 'Until TLS expiry' : 'Check interval'}
            </div>
          </div>
        </div>
      </section>

      {feedback ? <div class="alert-inline">{feedback}</div> : null}

      {testResult ? (
        <div
          class={`alert-inline ${testResult.status === 'down' ? 'alert-inline--error' : 'alert-inline--success'}`}
        >
          <div>
            <strong>Live check result:</strong>{' '}
            <StatusBadge
              tone={heartbeatStatusInfo(testResult.status).tone}
              label={heartbeatStatusInfo(testResult.status).label}
              size={14}
            />{' '}
            <span class="mono">{testResult.latency_ms} ms</span>
            {testResult.status_code ? <span class="mono"> | HTTP {testResult.status_code}</span> : null}
            {testResult.message ? ` | ${testResult.message}` : ''}
          </div>
        </div>
      ) : null}

      {detail.last_error ? (
        <div class="alert-inline alert-inline--error">
          <span>
            <strong>Last error:</strong> {detail.last_error}
          </span>
        </div>
      ) : null}

      {detail.type === 'push' && pushUrl ? (
        <div class="panel-card" style={{ marginBottom: '20px' }}>
          <div class="form-field">
            <span class="form-label">Push heartbeat URL</span>
            <div class="code-inline-box">
              <span>{pushUrl}</span>
              <button
                type="button"
                class="btn btn-sm"
                onClick={() => void handleCopy('push', pushUrl)}
              >
                <CopyIcon size={14} />
                <span>{copiedKey === 'push' ? 'Copied' : 'Copy'}</span>
              </button>
            </div>
          </div>
        </div>
      ) : null}

      <section class="panel-card" style={{ marginBottom: '20px' }}>
        <div class="panel-head" style={{ marginBottom: '10px' }}>
          <h2 class="panel-title">Heartbeat, last 60 checks</h2>
          <span class="form-help">Select any check segment for details</span>
        </div>
        <Heartbeat60Strip checks={checks60} intervalSec={detail.interval_sec} />
      </section>

      <LatencyChart
        heartbeats={detail.heartbeats || []}
        degradedMs={detail.degraded_ms}
        range={range}
        onRangeChange={handleRangeChange}
        loading={rangeLoading}
      />

      <div class="detail-bottom-grid">
        <section class="panel-card panel-card--flush">
          <div class="panel-head">
            <div class="filter-pills" role="tablist" aria-label="Monitor logs">
              <button
                type="button"
                role="tab"
                class={`filter-pill${bottomTab === 'checks' ? ' filter-pill--active' : ''}`}
                aria-selected={bottomTab === 'checks'}
                onClick={() => setBottomTab('checks')}
              >
                Recent checks ({(detail.heartbeats || []).length})
              </button>
              <button
                type="button"
                role="tab"
                class={`filter-pill${bottomTab === 'incidents' ? ' filter-pill--active' : ''}`}
                aria-selected={bottomTab === 'incidents'}
                onClick={() => setBottomTab('incidents')}
              >
                Incident history ({(detail.incidents || []).length})
              </button>
            </div>
          </div>

          {bottomTab === 'checks' ? (
            !detail.heartbeats || detail.heartbeats.length === 0 ? (
              <p class="form-help" style={{ padding: '16px', margin: 0 }}>
                No check history recorded yet.
              </p>
            ) : (
              <div class="table-scroll">
                <table class="data-table">
                  <thead>
                    <tr>
                      <th>Time</th>
                      <th>Status</th>
                      <th>Latency</th>
                      <th>Code</th>
                      <th>Message</th>
                    </tr>
                  </thead>
                  <tbody>
                    {detail.heartbeats.slice(0, 25).map((hb) => {
                      const hbInfo = heartbeatStatusInfo(hb.status);
                      return (
                        <tr key={hb.id}>
                          <td class="mono">{formatDateTime(hb.checked_at)}</td>
                          <td>
                            <StatusBadge tone={hbInfo.tone} label={hbInfo.label} size={14} />
                          </td>
                          <td class="mono">{hb.latency_ms} ms</td>
                          <td class="mono">{hb.status_code || '-'}</td>
                          <td>{hb.message || 'OK'}</td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            )
          ) : !detail.incidents || detail.incidents.length === 0 ? (
            <p class="form-help" style={{ padding: '16px', margin: 0 }}>
              No incidents linked to this monitor.
            </p>
          ) : (
            <div style={{ padding: '16px' }} class="editorial-timeline">
              {detail.incidents.map((inc) => (
                <div key={inc.id} style={{ borderBottom: '1px solid var(--border)', paddingBottom: '12px' }}>
                  <div class="action-row" style={{ justifyContent: 'space-between' }}>
                    <strong>{inc.title}</strong>
                    <span class="tag-pill">{incidentStatusLabel(inc.status)}</span>
                  </div>
                  <div class="form-help mono">
                    {incidentImpactLabel(inc.impact)} | {formatDateTime(inc.created_at)}
                  </div>
                </div>
              ))}
            </div>
          )}

          <div style={{ padding: '16px', borderTop: '1px solid var(--border)' }}>
            <div class="form-label" style={{ marginBottom: '6px' }}>
              SVG status badge markdown
            </div>
            <div class="code-inline-box">
              <span>{badgeMarkdown}</span>
              <button
                type="button"
                class="btn btn-sm"
                onClick={() => void handleCopy('badge', badgeMarkdown)}
              >
                <CopyIcon size={14} />
                <span>{copiedKey === 'badge' ? 'Copied' : 'Copy'}</span>
              </button>
            </div>
          </div>
        </section>

        <section class="panel-card">
          <div class="panel-head">
            <h2 class="panel-title">Check configuration</h2>
          </div>
          <dl class="kv-list" style={{ marginBottom: '20px' }}>
            <dt>Type</dt>
            <dd class="mono">{detail.type.toUpperCase()}</dd>
            <dt>Interval</dt>
            <dd class="mono">{detail.interval_sec} s</dd>
            <dt>Timeout</dt>
            <dd class="mono">{detail.timeout_sec} s</dd>
            <dt>Retries</dt>
            <dd class="mono">{detail.retries}</dd>
            <dt>Degraded threshold</dt>
            <dd class="mono">{detail.degraded_ms > 0 ? `${detail.degraded_ms} ms` : 'Disabled'}</dd>
            <dt>Last check</dt>
            <dd class="mono">{formatDateTime(detail.last_checked_at)}</dd>
            {detail.channel_names && detail.channel_names.length > 0 ? (
              <>
                <dt>Notifications</dt>
                <dd>{detail.channel_names.join(', ')}</dd>
              </>
            ) : null}
            {detail.status_page_names && detail.status_page_names.length > 0 ? (
              <>
                <dt>Status pages</dt>
                <dd>{detail.status_page_names.join(', ')}</dd>
              </>
            ) : null}
          </dl>

          {isAdmin ? (
            <button
              type="button"
              class="btn btn-danger"
              style={{ width: '100%' }}
              onClick={() => setDeleteOpen(true)}
            >
              Delete monitor
            </button>
          ) : null}
        </section>
      </div>

      <MonitorFormModal
        open={editOpen}
        initial={detail}
        groups={groups}
        channels={channels}
        pages={pages}
        onClose={() => setEditOpen(false)}
        onSaved={() => {
          setEditOpen(false);
          onRefreshCounts?.();
          void loadDetail(range, false);
        }}
      />

      <Modal
        open={deleteOpen}
        title="Delete monitor"
        onClose={() => setDeleteOpen(false)}
        footer={
          <>
            <button type="button" class="btn" onClick={() => setDeleteOpen(false)}>
              Cancel
            </button>
            <button
              type="button"
              class="btn btn-danger"
              disabled={deleting}
              onClick={() => void handleDelete()}
            >
              {deleting ? 'Deleting...' : 'Delete monitor'}
            </button>
          </>
        }
      >
        <p style={{ margin: 0 }}>
          Delete <strong>{detail.name}</strong> and its recorded check history? This cannot be undone.
        </p>
      </Modal>
    </div>
  );
}
