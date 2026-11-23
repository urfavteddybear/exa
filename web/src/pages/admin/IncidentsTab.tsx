import { useCallback, useEffect, useState } from 'preact/hooks';
import { api, ApiError } from '../../api';
import { Modal } from '../../components/Modal';
import { MonitorMultiSelect } from '../../components/MonitorMultiSelect';
import { StatusBadge } from '../../components/StatusBadge';
import type {
  Incident,
  IncidentImpact,
  IncidentStatus,
  Monitor,
  StatusPage,
} from '../../types';
import { formatDateTime, incidentImpactLabel, incidentStatusLabel } from '../../utils/format';

export function IncidentsTab({ isAdmin }: { isAdmin: boolean }) {
  const [incidents, setIncidents] = useState<Incident[]>([]);
  const [pages, setPages] = useState<StatusPage[]>([]);
  const [monitors, setMonitors] = useState<Monitor[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [createOpen, setCreateOpen] = useState(false);
  const [title, setTitle] = useState('');
  const [pageId, setPageId] = useState('');
  const [impact, setImpact] = useState<IncidentImpact>('minor');
  const [status, setStatus] = useState<IncidentStatus>('investigating');
  const [message, setMessage] = useState('');
  const [selectedMonitorIds, setSelectedMonitorIds] = useState<string[]>([]);
  const [creating, setCreating] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);

  const [updateTarget, setUpdateTarget] = useState<Incident | null>(null);
  const [updStatus, setUpdStatus] = useState<IncidentStatus>('identified');
  const [updMessage, setUpdMessage] = useState('');
  const [updating, setUpdating] = useState(false);
  const [updError, setUpdError] = useState<string | null>(null);

  const [deleteTarget, setDeleteTarget] = useState<Incident | null>(null);

  const loadAll = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const [incList, pageList, monList] = await Promise.all([
        api.listIncidents(),
        api.listStatusPages(),
        api.listMonitors(),
      ]);
      setIncidents(incList || []);
      setPages(pageList || []);
      setMonitors(monList || []);
      if (pageList && pageList.length > 0 && !pageId) {
        const def = pageList.find((p) => p.is_default) || pageList[0];
        setPageId(def.id);
      }
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to load incidents.');
    } finally {
      setLoading(false);
    }
  }, [pageId]);

  useEffect(() => {
    void loadAll();
  }, [loadAll]);

  const handleCreate = async (e: Event) => {
    e.preventDefault();
    if (!title.trim() || !message.trim()) {
      setFormError('Title and initial update message are required.');
      return;
    }
    setCreating(true);
    setFormError(null);
    try {
      await api.createIncident({
        status_page_id: pageId || pages[0]?.id || '',
        title: title.trim(),
        impact,
        status,
        message: message.trim(),
        monitor_ids: selectedMonitorIds,
      });
      setCreateOpen(false);
      setTitle('');
      setMessage('');
      setSelectedMonitorIds([]);
      await loadAll();
    } catch (err) {
      setFormError(err instanceof ApiError ? err.message : 'Failed to create incident.');
    } finally {
      setCreating(false);
    }
  };

  const handlePostUpdate = async (e: Event) => {
    e.preventDefault();
    if (!updateTarget || !updMessage.trim()) {
      setUpdError('Update message is required.');
      return;
    }
    setUpdating(true);
    setUpdError(null);
    try {
      await api.addIncidentUpdate(updateTarget.id, {
        status: updStatus,
        message: updMessage.trim(),
      });
      setUpdateTarget(null);
      setUpdMessage('');
      await loadAll();
    } catch (err) {
      setUpdError(err instanceof ApiError ? err.message : 'Failed to post update.');
    } finally {
      setUpdating(false);
    }
  };

  const handleDelete = async () => {
    if (!deleteTarget) return;
    try {
      await api.deleteIncident(deleteTarget.id);
      setDeleteTarget(null);
      await loadAll();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to delete incident.');
      setDeleteTarget(null);
    }
  };

  if (loading) {
    return (
      <div class="state-box" role="status">
        <p class="state-box-title">Loading incidents</p>
        <p class="state-box-desc" style={{ margin: 0 }}>
          Fetching active and resolved incident timelines.
        </p>
      </div>
    );
  }

  if (error) {
    return (
      <div class="state-box" role="alert">
        <p class="state-box-title">Unable to load incidents</p>
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
        <h2 class="section-heading">Incidents ({incidents.length})</h2>
        {isAdmin ? (
          <button
            type="button"
            class="btn btn-primary"
            onClick={() => {
              setFormError(null);
              setCreateOpen(true);
            }}
          >
            Report Incident
          </button>
        ) : null}
      </div>

      {incidents.length === 0 ? (
        <div class="state-box">
          <h3 class="state-box-title">No incidents recorded</h3>
          <p class="state-box-desc">
            Create an incident to communicate active investigations and resolutions on a status page.
          </p>
          {isAdmin ? (
            <button type="button" class="btn btn-primary" onClick={() => setCreateOpen(true)}>
              Report Incident
            </button>
          ) : null}
        </div>
      ) : (
        incidents.map((inc) => {
          const tone =
            inc.status === 'resolved'
              ? 'up'
              : inc.impact === 'critical' || inc.impact === 'major'
                ? 'down'
                : 'degraded';
          return (
            <article key={inc.id} class="notice-card">
              <div class="notice-card-header">
                <div>
                  <h3 class="notice-card-title">{inc.title}</h3>
                  <div class="notice-update-meta">
                    {incidentImpactLabel(inc.impact)} | Created{' '}
                    <span class="mono">{formatDateTime(inc.created_at)}</span>
                  </div>
                </div>
                <div class="action-row">
                  <StatusBadge tone={tone} label={incidentStatusLabel(inc.status)} size={15} />
                  {isAdmin ? (
                    <>
                      <button
                        type="button"
                        class="btn btn-sm"
                        onClick={() => {
                          setUpdateTarget(inc);
                          setUpdStatus(inc.status === 'investigating' ? 'identified' : 'resolved');
                          setUpdMessage('');
                          setUpdError(null);
                        }}
                      >
                        Post Update
                      </button>
                      <button
                        type="button"
                        class="btn btn-danger btn-sm"
                        onClick={() => setDeleteTarget(inc)}
                      >
                        Delete
                      </button>
                    </>
                  ) : null}
                </div>
              </div>

              {inc.updates && inc.updates.length > 0 ? (
                <ul class="notice-updates">
                  {inc.updates.map((upd) => (
                    <li key={upd.id} class="notice-update-item">
                      <strong>{incidentStatusLabel(upd.status)}:</strong> {upd.message}{' '}
                      <span class="notice-update-meta mono">({formatDateTime(upd.created_at)})</span>
                    </li>
                  ))}
                </ul>
              ) : null}
            </article>
          );
        })
      )}

      <Modal open={createOpen} title="Report Incident" onClose={() => setCreateOpen(false)} wide>
        <form onSubmit={handleCreate} class="form-grid">
          {formError ? (
            <div class="alert-inline alert-inline--error form-field--full">{formError}</div>
          ) : null}

          <label class="form-field form-field--full">
            <span class="form-label">Incident title</span>
            <input
              type="text"
              class="input"
              required
              placeholder="Elevated API error rates"
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
            <span class="form-label">Impact</span>
            <select
              class="select"
              value={impact}
              onChange={(e) => setImpact((e.currentTarget as HTMLSelectElement).value as IncidentImpact)}
            >
              <option value="none">None (Informational)</option>
              <option value="minor">Minor</option>
              <option value="major">Major</option>
              <option value="critical">Critical</option>
            </select>
          </label>

          <label class="form-field">
            <span class="form-label">Initial status</span>
            <select
              class="select"
              value={status}
              onChange={(e) => setStatus((e.currentTarget as HTMLSelectElement).value as IncidentStatus)}
            >
              <option value="investigating">Investigating</option>
              <option value="identified">Identified</option>
              <option value="monitoring">Monitoring</option>
              <option value="resolved">Resolved</option>
            </select>
          </label>

          <label class="form-field form-field--full">
            <span class="form-label">Message</span>
            <textarea
              class="textarea"
              required
              placeholder="Describe what is happening and what checks are underway."
              value={message}
              onInput={(e) => setMessage((e.currentTarget as HTMLTextAreaElement).value)}
            />
          </label>

          {monitors.length > 0 ? (
            <div class="form-field form-field--full">
              <span class="form-label">Affected monitors</span>
              <MonitorMultiSelect
                label="Affected monitors"
                monitors={monitors}
                value={selectedMonitorIds}
                onChange={setSelectedMonitorIds}
              />
            </div>
          ) : null}

          <div class="form-field--full action-row" style={{ justifyContent: 'flex-end' }}>
            <button type="button" class="btn" onClick={() => setCreateOpen(false)}>
              Cancel
            </button>
            <button type="submit" class="btn btn-primary" disabled={creating}>
              {creating ? 'Publishing...' : 'Publish Incident'}
            </button>
          </div>
        </form>
      </Modal>

      <Modal
        open={updateTarget !== null}
        title={`Update: ${updateTarget?.title || ''}`}
        onClose={() => setUpdateTarget(null)}
      >
        <form onSubmit={handlePostUpdate} class="form-grid">
          {updError ? (
            <div class="alert-inline alert-inline--error form-field--full">{updError}</div>
          ) : null}

          <label class="form-field form-field--full">
            <span class="form-label">Status</span>
            <select
              class="select"
              value={updStatus}
              onChange={(e) =>
                setUpdStatus((e.currentTarget as HTMLSelectElement).value as IncidentStatus)
              }
            >
              <option value="investigating">Investigating</option>
              <option value="identified">Identified</option>
              <option value="monitoring">Monitoring</option>
              <option value="resolved">Resolved</option>
            </select>
          </label>

          <label class="form-field form-field--full">
            <span class="form-label">Update message</span>
            <textarea
              class="textarea"
              required
              value={updMessage}
              onInput={(e) => setUpdMessage((e.currentTarget as HTMLTextAreaElement).value)}
            />
          </label>

          <div class="form-field--full action-row" style={{ justifyContent: 'flex-end' }}>
            <button type="button" class="btn" onClick={() => setUpdateTarget(null)}>
              Cancel
            </button>
            <button type="submit" class="btn btn-primary" disabled={updating}>
              {updating ? 'Posting...' : 'Post Update'}
            </button>
          </div>
        </form>
      </Modal>

      <Modal
        open={deleteTarget !== null}
        title="Delete incident"
        onClose={() => setDeleteTarget(null)}
        footer={
          <>
            <button type="button" class="btn" onClick={() => setDeleteTarget(null)}>
              Cancel
            </button>
            <button type="button" class="btn btn-danger" onClick={() => void handleDelete()}>
              Delete
            </button>
          </>
        }
      >
        <p style={{ margin: 0 }}>
          Delete incident <strong>{deleteTarget?.title}</strong> and its timeline updates?
        </p>
      </Modal>
    </div>
  );
}
