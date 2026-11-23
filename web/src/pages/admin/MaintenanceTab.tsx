import { useCallback, useEffect, useState } from 'preact/hooks';
import { api, ApiError } from '../../api';
import { Modal } from '../../components/Modal';
import { MonitorMultiSelect } from '../../components/MonitorMultiSelect';
import { StatusBadge } from '../../components/StatusBadge';
import type { Maintenance, Monitor, StatusPage } from '../../types';
import { formatDateTime, maintenanceStatusLabel, toLocalDateTimeInput } from '../../utils/format';

export function MaintenanceTab({ isAdmin }: { isAdmin: boolean }) {
  const [items, setItems] = useState<Maintenance[]>([]);
  const [pages, setPages] = useState<StatusPage[]>([]);
  const [monitors, setMonitors] = useState<Monitor[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [modalOpen, setModalOpen] = useState(false);
  const [editId, setEditId] = useState<string | null>(null);
  const [pageId, setPageId] = useState('');
  const [title, setTitle] = useState('');
  const [description, setDescription] = useState('');
  const [startLocal, setStartLocal] = useState('');
  const [endLocal, setEndLocal] = useState('');
  const [suppressAlerts, setSuppressAlerts] = useState(true);
  const [monitorIds, setMonitorIds] = useState<string[]>([]);
  const [saving, setSaving] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);

  const loadAll = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const [mList, pList, monList] = await Promise.all([
        api.listMaintenances(),
        api.listStatusPages(),
        api.listMonitors(),
      ]);
      setItems(mList || []);
      setPages(pList || []);
      setMonitors(monList || []);
      if (pList && pList.length > 0 && !pageId) {
        const def = pList.find((p) => p.is_default) || pList[0];
        setPageId(def.id);
      }
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to load maintenance windows.');
    } finally {
      setLoading(false);
    }
  }, [pageId]);

  useEffect(() => {
    void loadAll();
  }, [loadAll]);

  const openCreate = () => {
    const now = new Date();
    const later = new Date(now.getTime() + 60 * 60 * 1000);
    setEditId(null);
    setTitle('');
    setDescription('');
    setStartLocal(toLocalDateTimeInput(now.toISOString()));
    setEndLocal(toLocalDateTimeInput(later.toISOString()));
    setSuppressAlerts(true);
    setMonitorIds([]);
    setFormError(null);
    setModalOpen(true);
  };

  const openEdit = (m: Maintenance) => {
    setEditId(m.id);
    setPageId(m.status_page_id);
    setTitle(m.title);
    setDescription(m.description);
    setStartLocal(toLocalDateTimeInput(m.scheduled_start));
    setEndLocal(toLocalDateTimeInput(m.scheduled_end));
    setSuppressAlerts(m.auto_suppress_alerts);
    setMonitorIds(m.monitor_ids || []);
    setFormError(null);
    setModalOpen(true);
  };

  const handleSave = async (e: Event) => {
    e.preventDefault();
    if (!title.trim() || !startLocal || !endLocal) {
      setFormError('Title, start time, and end time are required.');
      return;
    }
    const startISO = new Date(startLocal).toISOString();
    const endISO = new Date(endLocal).toISOString();
    if (new Date(endISO) <= new Date(startISO)) {
      setFormError('End time must be after start time.');
      return;
    }
    setSaving(true);
    setFormError(null);
    try {
      const payload = {
        status_page_id: pageId || pages[0]?.id || '',
        title: title.trim(),
        description: description.trim(),
        scheduled_start: startISO,
        scheduled_end: endISO,
        auto_suppress_alerts: suppressAlerts,
        monitor_ids: monitorIds,
      };
      if (editId) {
        await api.updateMaintenance(editId, payload);
      } else {
        await api.createMaintenance(payload);
      }
      setModalOpen(false);
      await loadAll();
    } catch (err) {
      setFormError(err instanceof ApiError ? err.message : 'Failed to save maintenance window.');
    } finally {
      setSaving(false);
    }
  };

  const handleDelete = async (id: string) => {
    try {
      await api.deleteMaintenance(id);
      await loadAll();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to delete maintenance window.');
    }
  };

  if (loading) {
    return (
      <div class="state-box" role="status">
        <p class="state-box-title">Loading maintenance windows</p>
        <p class="state-box-desc" style={{ margin: 0 }}>
          Fetching scheduled and active maintenance windows.
        </p>
      </div>
    );
  }

  if (error) {
    return (
      <div class="state-box" role="alert">
        <p class="state-box-title">Unable to load maintenance windows</p>
        <p class="state-box-desc">{error}</p>
        <button type="button" class="btn btn-primary" onClick={() => void loadAll()}>
          Retry
        </button>
      </div>
    );
  }

  return (
    <div>
      <div class="toolbar-row">
        <h2 class="section-heading">Scheduled Maintenance ({items.length})</h2>
        {isAdmin ? (
          <button type="button" class="btn btn-primary" onClick={openCreate}>
            Schedule Maintenance
          </button>
        ) : null}
      </div>

      {items.length === 0 ? (
        <div class="state-box">
          <h3 class="state-box-title">No maintenance windows scheduled</h3>
          <p class="state-box-desc">
            Schedule planned downtime to inform status page visitors and optionally suppress alerts.
          </p>
          {isAdmin ? (
            <button type="button" class="btn btn-primary" onClick={openCreate}>
              Schedule Maintenance
            </button>
          ) : null}
        </div>
      ) : (
        items.map((m) => (
          <article key={m.id} class="notice-card">
            <div class="notice-card-header">
              <div>
                <h3 class="notice-card-title">{m.title}</h3>
                <div class="notice-update-meta mono">
                  {formatDateTime(m.scheduled_start)} to {formatDateTime(m.scheduled_end)}
                </div>
                {m.auto_suppress_alerts ? (
                  <div class="form-help">Alerts suppressed during this window</div>
                ) : null}
              </div>
              <div class="action-row">
                <StatusBadge
                  tone={m.status === 'completed' ? 'up' : 'maint'}
                  label={maintenanceStatusLabel(m.status)}
                  size={15}
                />
                {isAdmin ? (
                  <>
                    <button type="button" class="btn btn-sm" onClick={() => openEdit(m)}>
                      Edit
                    </button>
                    <button
                      type="button"
                      class="btn btn-danger btn-sm"
                      onClick={() => void handleDelete(m.id)}
                    >
                      Delete
                    </button>
                  </>
                ) : null}
              </div>
            </div>
            {m.description ? <p style={{ margin: '10px 0 0' }}>{m.description}</p> : null}
          </article>
        ))
      )}

      <Modal
        open={modalOpen}
        title={editId ? 'Edit Maintenance Window' : 'Schedule Maintenance'}
        onClose={() => setModalOpen(false)}
        wide
      >
        <form onSubmit={handleSave} class="form-grid">
          {formError ? (
            <div class="alert-inline alert-inline--error form-field--full">{formError}</div>
          ) : null}

          <label class="form-field">
            <span class="form-label">Title</span>
            <input
              type="text"
              class="input"
              required
              placeholder="Database index migration"
              value={title}
              onInput={(e) => setTitle((e.currentTarget as HTMLInputElement).value)}
            />
          </label>

          <label class="form-field">
            <span class="form-label">Status page</span>
            <select
              class="select"
              value={pageId}
              onChange={(e) => setPageId((e.currentTarget as HTMLSelectElement).value)}
            >
              {pages.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.title}
                </option>
              ))}
            </select>
          </label>

          <label class="form-field">
            <span class="form-label">Start time (local)</span>
            <input
              type="datetime-local"
              class="input mono"
              required
              value={startLocal}
              onInput={(e) => setStartLocal((e.currentTarget as HTMLInputElement).value)}
            />
          </label>

          <label class="form-field">
            <span class="form-label">End time (local)</span>
            <input
              type="datetime-local"
              class="input mono"
              required
              value={endLocal}
              onInput={(e) => setEndLocal((e.currentTarget as HTMLInputElement).value)}
            />
          </label>

          <label class="form-field form-field--full">
            <span class="form-label">Description</span>
            <textarea
              class="textarea"
              value={description}
              onInput={(e) => setDescription((e.currentTarget as HTMLTextAreaElement).value)}
            />
          </label>

          <div class="form-field form-field--full">
            <label class="checkbox-row">
              <input
                type="checkbox"
                checked={suppressAlerts}
                onChange={(e) => setSuppressAlerts((e.currentTarget as HTMLInputElement).checked)}
              />
              <span>Automatically suppress monitor alerts during this maintenance window</span>
            </label>
          </div>

          {monitors.length > 0 ? (
            <div class="form-field form-field--full">
              <span class="form-label">Affected monitors</span>
              <MonitorMultiSelect
                label="Affected monitors"
                monitors={monitors}
                value={monitorIds}
                onChange={setMonitorIds}
              />
            </div>
          ) : null}

          <div class="form-field--full action-row" style={{ justifyContent: 'flex-end' }}>
            <button type="button" class="btn" onClick={() => setModalOpen(false)}>
              Cancel
            </button>
            <button type="submit" class="btn btn-primary" disabled={saving}>
              {saving ? 'Saving...' : editId ? 'Save window' : 'Schedule window'}
            </button>
          </div>
        </form>
      </Modal>
    </div>
  );
}
