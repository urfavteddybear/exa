import { useCallback, useEffect, useMemo, useState } from 'preact/hooks';
import { api, ApiError } from '../../api';
import { Modal } from '../../components/Modal';
import { StatusBadge } from '../../components/StatusBadge';
import { Sparkline30 } from '../../components/UptimeBar';
import type {
  ComponentGroup,
  NotificationChannel,
  OverviewMonitor,
  StatusPage,
} from '../../types';
import { componentStatusInfo, formatDateTime, statusSortWeight } from '../../utils/format';
import { MonitorFormModal } from './MonitorFormModal';

interface MonitorsTabProps {
  isAdmin: boolean;
  initialQuery?: string;
  onNavigate: (path: string) => void;
  onRefreshCounts?: () => void;
}

export function MonitorsTab({
  isAdmin,
  initialQuery = '',
  onNavigate,
  onRefreshCounts,
}: MonitorsTabProps) {
  const [monitors, setMonitors] = useState<OverviewMonitor[]>([]);
  const [groups, setGroups] = useState<ComponentGroup[]>([]);
  const [channels, setChannels] = useState<NotificationChannel[]>([]);
  const [pages, setPages] = useState<StatusPage[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [search, setSearch] = useState(initialQuery);
  const [statusFilter, setStatusFilter] = useState('all');

  const [createOpen, setCreateOpen] = useState(false);

  const [groupsModalOpen, setGroupsModalOpen] = useState(false);
  const [groupEditId, setGroupEditId] = useState<string | null>(null);
  const [groupName, setGroupName] = useState('');
  const [groupDesc, setGroupDesc] = useState('');
  const [groupSort, setGroupSort] = useState(0);
  const [groupCollapsed, setGroupCollapsed] = useState(false);
  const [groupError, setGroupError] = useState<string | null>(null);
  const [groupSaving, setGroupSaving] = useState(false);

  const loadAll = useCallback(async (silent = false) => {
    if (!silent) {
      setLoading(true);
      setError(null);
    }
    try {
      const [ov, gList, cList, pList] = await Promise.all([
        api.overview(),
        api.listGroups(),
        isAdmin ? api.listNotifications().catch(() => [] as NotificationChannel[]) : Promise.resolve([] as NotificationChannel[]),
        api.listStatusPages().catch(() => [] as StatusPage[]),
      ]);
      setMonitors(ov.monitors || []);
      setGroups(gList || []);
      setChannels(cList || []);
      setPages(pList || []);
    } catch (err) {
      if (!silent) {
        setError(err instanceof ApiError ? err.message : 'Failed to load monitors.');
      }
    } finally {
      if (!silent) setLoading(false);
    }
  }, [isAdmin]);

  useEffect(() => {
    void loadAll(false);
  }, [loadAll]);

  const filteredMonitors = useMemo(() => {
    const q = search.trim().toLowerCase();
    return monitors
      .filter((m) => {
        if (
          statusFilter === 'failing' &&
          m.current_status !== 'major_outage' &&
          m.current_status !== 'degraded'
        ) {
          return false;
        }
        if (statusFilter === 'paused' && m.active) return false;
        if (
          statusFilter !== 'all' &&
          statusFilter !== 'failing' &&
          statusFilter !== 'paused' &&
          m.type !== statusFilter
        ) {
          return false;
        }
        if (!q) return true;
        return (
          m.name.toLowerCase().includes(q) ||
          m.target.toLowerCase().includes(q) ||
          m.slug.toLowerCase().includes(q) ||
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
  }, [monitors, search, statusFilter]);

  const groupedRows = useMemo(() => {
    const map = new Map<string, OverviewMonitor[]>();
    for (const m of filteredMonitors) {
      const g = m.group_name || 'Ungrouped';
      const arr = map.get(g) || [];
      arr.push(m);
      map.set(g, arr);
    }
    return Array.from(map.entries());
  }, [filteredMonitors]);

  const resetGroupForm = () => {
    setGroupEditId(null);
    setGroupName('');
    setGroupDesc('');
    setGroupSort(0);
    setGroupCollapsed(false);
    setGroupError(null);
  };

  const handleSaveGroup = async (e: Event) => {
    e.preventDefault();
    if (!groupName.trim()) {
      setGroupError('Group name is required.');
      return;
    }
    setGroupSaving(true);
    setGroupError(null);
    try {
      const payload = {
        name: groupName.trim(),
        description: groupDesc.trim(),
        sort_order: Number(groupSort) || 0,
        collapsed_default: groupCollapsed,
      };
      if (groupEditId) {
        await api.updateGroup(groupEditId, payload);
      } else {
        await api.createGroup(payload);
      }
      resetGroupForm();
      await loadAll(true);
    } catch (err) {
      setGroupError(err instanceof ApiError ? err.message : 'Failed to save group.');
    } finally {
      setGroupSaving(false);
    }
  };

  const handleDeleteGroup = async (id: string) => {
    setGroupError(null);
    try {
      await api.deleteGroup(id);
      if (groupEditId === id) resetGroupForm();
      await loadAll(true);
    } catch (err) {
      setGroupError(err instanceof ApiError ? err.message : 'Failed to delete group.');
    }
  };

  if (loading) {
    return (
      <div class="state-box" role="status">
        <p class="state-box-title">Loading monitors</p>
        <p class="state-box-desc" style={{ margin: 0 }}>
          Fetching configured probes and component groups.
        </p>
      </div>
    );
  }

  if (error) {
    return (
      <div class="state-box" role="alert">
        <p class="state-box-title">Unable to load monitors</p>
        <p class="state-box-desc">{error}</p>
        <button type="button" class="btn btn-primary" onClick={() => void loadAll(false)}>
          Retry
        </button>
      </div>
    );
  }

  return (
    <div>
      <div class="admin-topbar">
        <div class="breadcrumb-row">
          <span>Admin</span>
          <span aria-hidden="true">/</span>
          <span class="breadcrumb-current">Monitors ({monitors.length})</span>
        </div>

        <div class="topbar-controls">
          <input
            type="search"
            class="input search-inline"
            aria-label="Search monitors"
            placeholder="Search monitors"
            value={search}
            onInput={(e) => setSearch((e.currentTarget as HTMLInputElement).value)}
          />
          <select
            class="select"
            style={{ width: 'auto', minWidth: '160px' }}
            aria-label="Filter monitors"
            value={statusFilter}
            onChange={(e) => setStatusFilter((e.currentTarget as HTMLSelectElement).value)}
          >
            <option value="all">All monitors</option>
            <option value="failing">Failing or degraded</option>
            <option value="paused">Paused</option>
            <option value="http">HTTP</option>
            <option value="tcp">TCP</option>
            <option value="ping">Ping</option>
            <option value="dns">DNS</option>
            <option value="push">Push</option>
          </select>
          {isAdmin ? (
            <>
              <button
                type="button"
                class="btn"
                onClick={() => {
                  resetGroupForm();
                  setGroupsModalOpen(true);
                }}
              >
                Manage Groups ({groups.length})
              </button>
              <button type="button" class="btn btn-primary" onClick={() => setCreateOpen(true)}>
                New monitor
              </button>
            </>
          ) : null}
        </div>
      </div>

      {monitors.length === 0 ? (
        <div class="state-box">
          <h2 class="state-box-title">No monitors configured yet</h2>
          <p class="state-box-desc">
            Add an HTTP, TCP, Ping, DNS, or Push check to begin tracking availability and response times.
          </p>
          {isAdmin ? (
            <button type="button" class="btn btn-primary" onClick={() => setCreateOpen(true)}>
              New monitor
            </button>
          ) : null}
        </div>
      ) : filteredMonitors.length === 0 ? (
        <div class="state-box">
          <h2 class="state-box-title">No matching monitors</h2>
          <p class="state-box-desc" style={{ margin: 0 }}>
            Adjust your search text or status filter to see configured monitors.
          </p>
        </div>
      ) : (
        <div class="panel-card panel-card--flush">
          <div class="table-scroll">
            <table class="data-table">
              <thead>
                <tr>
                  <th>Monitor</th>
                  <th>Type</th>
                  <th>Status</th>
                  <th>Last 30 checks</th>
                  <th>Uptime 24 h</th>
                  <th>Response</th>
                  <th>Last checked</th>
                </tr>
              </thead>
              <tbody>
                {groupedRows.map(([groupName, rows]) => (
                  <>
                    <tr key={`g-${groupName}`} class="group-divider-row">
                      <td colSpan={7}>{groupName}</td>
                    </tr>
                    {rows.map((m) => {
                      const info = componentStatusInfo(m.current_status);
                      return (
                        <tr
                          key={m.id}
                          class="table-row-clickable"
                          tabIndex={0}
                          onClick={() => onNavigate(`/admin/monitors/${encodeURIComponent(m.id)}`)}
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
                          <td class="mono">{m.type.toUpperCase()}</td>
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
                          <td class="mono">{formatDateTime(m.last_checked_at)}</td>
                        </tr>
                      );
                    })}
                  </>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

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

      <Modal
        open={groupsModalOpen}
        title="Component Groups"
        onClose={() => setGroupsModalOpen(false)}
        wide
      >
        {groupError ? <div class="alert-inline alert-inline--error">{groupError}</div> : null}

        <form onSubmit={handleSaveGroup} class="form-grid" style={{ marginBottom: '20px' }}>
          <label class="form-field">
            <span class="form-label">Group name</span>
            <input
              type="text"
              class="input"
              required
              placeholder="Core API"
              value={groupName}
              onInput={(e) => setGroupName((e.currentTarget as HTMLInputElement).value)}
            />
          </label>

          <label class="form-field">
            <span class="form-label">Sort order</span>
            <input
              type="number"
              class="input mono"
              value={groupSort}
              onInput={(e) => setGroupSort(Number((e.currentTarget as HTMLInputElement).value))}
            />
          </label>

          <label class="form-field form-field--full">
            <span class="form-label">Description (optional)</span>
            <input
              type="text"
              class="input"
              value={groupDesc}
              onInput={(e) => setGroupDesc((e.currentTarget as HTMLInputElement).value)}
            />
          </label>

          <div class="form-field--full action-row" style={{ justifyContent: 'space-between' }}>
            <label class="checkbox-row">
              <input
                type="checkbox"
                checked={groupCollapsed}
                onChange={(e) => setGroupCollapsed((e.currentTarget as HTMLInputElement).checked)}
              />
              <span>Collapsed by default on public status page</span>
            </label>
            <div class="action-row">
              {groupEditId ? (
                <button type="button" class="btn btn-sm" onClick={resetGroupForm}>
                  Cancel edit
                </button>
              ) : null}
              <button type="submit" class="btn btn-primary btn-sm" disabled={groupSaving}>
                {groupSaving ? 'Saving...' : groupEditId ? 'Update group' : 'Add group'}
              </button>
            </div>
          </div>
        </form>

        {groups.length === 0 ? (
          <p class="form-help">No component groups created yet.</p>
        ) : (
          <div class="table-scroll">
            <table class="data-table">
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Order</th>
                  <th>Default</th>
                  <th>Actions</th>
                </tr>
              </thead>
              <tbody>
                {groups.map((g) => (
                  <tr key={g.id}>
                    <td>
                      <strong>{g.name}</strong>
                      {g.description ? <div class="form-help">{g.description}</div> : null}
                    </td>
                    <td class="mono">{g.sort_order}</td>
                    <td>{g.collapsed_default ? 'Collapsed' : 'Expanded'}</td>
                    <td>
                      <div class="action-row">
                        <button
                          type="button"
                          class="btn btn-sm"
                          onClick={() => {
                            setGroupEditId(g.id);
                            setGroupName(g.name);
                            setGroupDesc(g.description);
                            setGroupSort(g.sort_order);
                            setGroupCollapsed(g.collapsed_default);
                          }}
                        >
                          Edit
                        </button>
                        <button
                          type="button"
                          class="btn btn-danger btn-sm"
                          onClick={() => void handleDeleteGroup(g.id)}
                        >
                          Delete
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Modal>
    </div>
  );
}
