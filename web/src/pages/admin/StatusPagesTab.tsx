import { useCallback, useEffect, useState } from 'preact/hooks';
import { api, ApiError } from '../../api';
import { Modal } from '../../components/Modal';
import type { ComponentGroup, Monitor, StatusPage, StatusPageMonitor } from '../../types';
import { slugify } from '../../utils/format';

interface AssignRow {
  monitor_id: string;
  name: string;
  included: boolean;
  group_id: string;
  display_name: string;
  sort_order: number;
}

export function StatusPagesTab({
  isAdmin,
  onNavigate,
}: {
  isAdmin: boolean;
  onNavigate: (path: string) => void;
}) {
  const [pages, setPages] = useState<StatusPage[]>([]);
  const [monitors, setMonitors] = useState<Monitor[]>([]);
  const [groups, setGroups] = useState<ComponentGroup[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [modalOpen, setModalOpen] = useState(false);
  const [editId, setEditId] = useState<string | null>(null);
  const [title, setTitle] = useState('');
  const [slug, setSlug] = useState('');
  const [description, setDescription] = useState('');
  const [customCss, setCustomCss] = useState('');
  const [isDefault, setIsDefault] = useState(false);
  const [isPublic, setIsPublic] = useState(true);
  const [showLatency, setShowLatency] = useState(true);
  const [saving, setSaving] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);

  const [assignPage, setAssignPage] = useState<StatusPage | null>(null);
  const [assignRows, setAssignRows] = useState<AssignRow[]>([]);
  const [assignLoading, setAssignLoading] = useState(false);
  const [assignSaving, setAssignSaving] = useState(false);
  const [assignError, setAssignError] = useState<string | null>(null);

  const loadAll = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const [pList, mList, gList] = await Promise.all([
        api.listStatusPages(),
        api.listMonitors(),
        api.listGroups(),
      ]);
      setPages(pList || []);
      setMonitors(mList || []);
      setGroups(gList || []);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to load status pages.');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadAll();
  }, [loadAll]);

  const openCreate = () => {
    setEditId(null);
    setTitle('');
    setSlug('');
    setDescription('');
    setCustomCss('');
    setIsDefault(false);
    setIsPublic(true);
    setShowLatency(true);
    setFormError(null);
    setModalOpen(true);
  };

  const openEdit = (p: StatusPage) => {
    setEditId(p.id);
    setTitle(p.title);
    setSlug(p.slug);
    setDescription(p.description);
    setCustomCss(p.custom_css);
    setIsDefault(p.is_default);
    setIsPublic(p.is_public);
    setShowLatency(p.show_latency);
    setFormError(null);
    setModalOpen(true);
  };

  const handleSave = async (e: Event) => {
    e.preventDefault();
    if (!title.trim() || !slug.trim()) {
      setFormError('Title and slug are required.');
      return;
    }
    setSaving(true);
    setFormError(null);
    try {
      const payload = {
        slug: slugify(slug),
        title: title.trim(),
        description: description.trim(),
        custom_css: customCss,
        is_default: isDefault,
        is_public: isPublic,
        show_latency: showLatency,
      };
      if (editId) {
        await api.updateStatusPage(editId, payload);
      } else {
        await api.createStatusPage(payload);
      }
      setModalOpen(false);
      await loadAll();
    } catch (err) {
      setFormError(err instanceof ApiError ? err.message : 'Failed to save status page.');
    } finally {
      setSaving(false);
    }
  };

  const handleDelete = async (id: string) => {
    try {
      await api.deleteStatusPage(id);
      await loadAll();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to delete status page.');
    }
  };

  const openAssign = async (page: StatusPage) => {
    setAssignPage(page);
    setAssignError(null);
    setAssignLoading(true);
    try {
      const existing = new Map<string, { group_id: string; display_name: string; sort_order: number }>();
      try {
        const pub = await api.publicStatus(page.slug);
        let order = 0;
        for (const g of pub.groups || []) {
          for (const m of g.monitors || []) {
            existing.set(m.id, { group_id: g.id, display_name: m.name, sort_order: order++ });
          }
        }
        for (const m of pub.ungrouped_monitors || []) {
          existing.set(m.id, { group_id: '', display_name: m.name, sort_order: order++ });
        }
      } catch {
        // Private status pages may not return public status; fall back to empty initial map.
      }
      setAssignRows(
        monitors.map((m, idx) => {
          const hit = existing.get(m.id);
          return {
            monitor_id: m.id,
            name: m.name,
            included: Boolean(hit),
            group_id: hit ? hit.group_id : m.group_id || '',
            display_name: hit && hit.display_name !== m.name ? hit.display_name : '',
            sort_order: hit ? hit.sort_order : idx,
          };
        }),
      );
    } finally {
      setAssignLoading(false);
    }
  };

  const handleSaveAssign = async (e: Event) => {
    e.preventDefault();
    if (!assignPage) return;
    setAssignSaving(true);
    setAssignError(null);
    try {
      const payload: StatusPageMonitor[] = assignRows
        .filter((r) => r.included)
        .map((r) => ({
          status_page_id: assignPage.id,
          monitor_id: r.monitor_id,
          group_id: r.group_id || null,
          display_name: r.display_name.trim(),
          sort_order: Number(r.sort_order) || 0,
        }));
      await api.setStatusPageMonitors(assignPage.id, payload);
      setAssignPage(null);
    } catch (err) {
      setAssignError(err instanceof ApiError ? err.message : 'Failed to assign monitors.');
    } finally {
      setAssignSaving(false);
    }
  };

  if (loading) {
    return (
      <div class="state-box" role="status">
        <p class="state-box-title">Loading status pages</p>
        <p class="state-box-desc" style={{ margin: 0 }}>
          Fetching configured public and private status pages.
        </p>
      </div>
    );
  }

  if (error) {
    return (
      <div class="state-box" role="alert">
        <p class="state-box-title">Unable to load status pages</p>
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
        <h2 class="section-heading">Status Pages ({pages.length})</h2>
        {isAdmin ? (
          <button type="button" class="btn btn-primary" onClick={openCreate}>
            New Status Page
          </button>
        ) : null}
      </div>

      {pages.length === 0 ? (
        <div class="state-box">
          <h3 class="state-box-title">No status pages created</h3>
          <p class="state-box-desc">Create a status page to publish uptime history and incident updates.</p>
          {isAdmin ? (
            <button type="button" class="btn btn-primary" onClick={openCreate}>
              New Status Page
            </button>
          ) : null}
        </div>
      ) : (
        <div class="card">
          <div class="table-scroll">
            <table class="data-table">
              <thead>
                <tr>
                  <th>Title</th>
                  <th>Slug</th>
                  <th>Visibility</th>
                  <th>Latency</th>
                  <th>Actions</th>
                </tr>
              </thead>
              <tbody>
                {pages.map((p) => (
                  <tr key={p.id}>
                    <td>
                      <strong>{p.title}</strong>
                      {p.is_default ? (
                        <span class="tag-pill" style={{ marginLeft: '8px' }}>
                          Default
                        </span>
                      ) : null}
                      {p.description ? <div class="form-help">{p.description}</div> : null}
                    </td>
                    <td class="mono">
                      <a
                        href={`/s/${encodeURIComponent(p.slug)}`}
                        onClick={(e) => {
                          e.preventDefault();
                          onNavigate(`/s/${encodeURIComponent(p.slug)}`);
                        }}
                      >
                        /s/{p.slug}
                      </a>
                    </td>
                    <td>{p.is_public ? 'Public' : 'Private'}</td>
                    <td>{p.show_latency ? 'Shown' : 'Hidden'}</td>
                    <td>
                      {isAdmin ? (
                        <div class="action-row">
                          <button type="button" class="btn btn-sm" onClick={() => void openAssign(p)}>
                            Assign Monitors
                          </button>
                          <button type="button" class="btn btn-sm" onClick={() => openEdit(p)}>
                            Edit
                          </button>
                          {!p.is_default ? (
                            <button
                              type="button"
                              class="btn btn-danger btn-sm"
                              onClick={() => void handleDelete(p.id)}
                            >
                              Delete
                            </button>
                          ) : null}
                        </div>
                      ) : null}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      <Modal
        open={modalOpen}
        title={editId ? 'Edit Status Page' : 'New Status Page'}
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
              placeholder="Customer API Status"
              value={title}
              onInput={(e) => {
                const v = (e.currentTarget as HTMLInputElement).value;
                setTitle(v);
                if (!editId) setSlug(slugify(v));
              }}
            />
          </label>

          <label class="form-field">
            <span class="form-label">URL slug</span>
            <input
              type="text"
              class="input mono"
              required
              placeholder="customer-api"
              value={slug}
              onInput={(e) => setSlug((e.currentTarget as HTMLInputElement).value)}
            />
          </label>

          <label class="form-field form-field--full">
            <span class="form-label">Description (optional)</span>
            <input
              type="text"
              class="input"
              value={description}
              onInput={(e) => setDescription((e.currentTarget as HTMLInputElement).value)}
            />
          </label>

          <label class="form-field form-field--full">
            <span class="form-label">Custom CSS (optional)</span>
            <textarea
              class="textarea mono"
              placeholder=".status-banner { border-radius: 6px; }"
              value={customCss}
              onInput={(e) => setCustomCss((e.currentTarget as HTMLTextAreaElement).value)}
            />
          </label>

          <div class="form-field--full action-row">
            <label class="checkbox-row">
              <input
                type="checkbox"
                checked={isPublic}
                onChange={(e) => setIsPublic((e.currentTarget as HTMLInputElement).checked)}
              />
              <span>Publicly accessible</span>
            </label>
            <label class="checkbox-row">
              <input
                type="checkbox"
                checked={showLatency}
                onChange={(e) => setShowLatency((e.currentTarget as HTMLInputElement).checked)}
              />
              <span>Show response latency</span>
            </label>
            <label class="checkbox-row">
              <input
                type="checkbox"
                checked={isDefault}
                onChange={(e) => setIsDefault((e.currentTarget as HTMLInputElement).checked)}
              />
              <span>Default status page</span>
            </label>
          </div>

          <div class="form-field--full action-row" style={{ justifyContent: 'flex-end' }}>
            <button type="button" class="btn" onClick={() => setModalOpen(false)}>
              Cancel
            </button>
            <button type="submit" class="btn btn-primary" disabled={saving}>
              {saving ? 'Saving...' : editId ? 'Save page' : 'Create page'}
            </button>
          </div>
        </form>
      </Modal>

      <Modal
        open={assignPage !== null}
        title={`Assign Monitors: ${assignPage?.title || ''}`}
        onClose={() => setAssignPage(null)}
        wide
      >
        {assignLoading ? (
          <p class="form-help" role="status">
            Loading current monitor assignments...
          </p>
        ) : (
          <form onSubmit={handleSaveAssign}>
            {assignError ? <div class="alert-inline alert-inline--error">{assignError}</div> : null}

            {assignRows.length === 0 ? (
              <p class="form-help">No monitors exist yet. Create monitors first in the Monitors tab.</p>
            ) : (
              <div class="table-scroll" style={{ marginBottom: '16px' }}>
                <table class="data-table">
                  <thead>
                    <tr>
                      <th>Show</th>
                      <th>Monitor</th>
                      <th>Group</th>
                      <th>Display Name Override</th>
                      <th>Order</th>
                    </tr>
                  </thead>
                  <tbody>
                    {assignRows.map((row, idx) => (
                      <tr key={row.monitor_id}>
                        <td>
                          <input
                            type="checkbox"
                            aria-label={`Include ${row.name}`}
                            checked={row.included}
                            onChange={(e) => {
                              const checked = (e.currentTarget as HTMLInputElement).checked;
                              setAssignRows((prev) =>
                                prev.map((r, i) => (i === idx ? { ...r, included: checked } : r)),
                              );
                            }}
                          />
                        </td>
                        <td>
                          <strong>{row.name}</strong>
                        </td>
                        <td>
                          <select
                            class="select"
                            style={{ minHeight: '44px' }}
                            value={row.group_id}
                            onChange={(e) => {
                              const val = (e.currentTarget as HTMLSelectElement).value;
                              setAssignRows((prev) =>
                                prev.map((r, i) => (i === idx ? { ...r, group_id: val } : r)),
                              );
                            }}
                          >
                            <option value="">Ungrouped</option>
                            {groups.map((g) => (
                              <option key={g.id} value={g.id}>
                                {g.name}
                              </option>
                            ))}
                          </select>
                        </td>
                        <td>
                          <input
                            type="text"
                            class="input"
                            style={{ minHeight: '44px' }}
                            placeholder={row.name}
                            value={row.display_name}
                            onInput={(e) => {
                              const val = (e.currentTarget as HTMLInputElement).value;
                              setAssignRows((prev) =>
                                prev.map((r, i) => (i === idx ? { ...r, display_name: val } : r)),
                              );
                            }}
                          />
                        </td>
                        <td style={{ width: '84px' }}>
                          <input
                            type="number"
                            class="input mono"
                            style={{ minHeight: '44px' }}
                            value={row.sort_order}
                            onInput={(e) => {
                              const val = Number((e.currentTarget as HTMLInputElement).value);
                              setAssignRows((prev) =>
                                prev.map((r, i) => (i === idx ? { ...r, sort_order: val } : r)),
                              );
                            }}
                          />
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}

            <div class="action-row" style={{ justifyContent: 'flex-end' }}>
              <button type="button" class="btn" onClick={() => setAssignPage(null)}>
                Cancel
              </button>
              <button type="submit" class="btn btn-primary" disabled={assignSaving}>
                {assignSaving ? 'Saving...' : 'Save Assignments'}
              </button>
            </div>
          </form>
        )}
      </Modal>
    </div>
  );
}
