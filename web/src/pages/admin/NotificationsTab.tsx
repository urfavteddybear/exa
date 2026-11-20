import { useCallback, useEffect, useState } from 'preact/hooks';
import { api, ApiError } from '../../api';
import { Modal } from '../../components/Modal';
import type { NotificationChannel, NotificationType, Subscriber } from '../../types';
import { formatDateTime } from '../../utils/format';

interface ChannelFormState {
  name: string;
  type: NotificationType;
  is_default: boolean;
  enabled: boolean;
  webhook_url: string;
  webhook_secret: string;
  discord_username: string;
  slack_channel: string;
  telegram_bot_token: string;
  telegram_chat_id: string;
  smtp_host: string;
  smtp_port: number;
  smtp_user: string;
  smtp_pass: string;
  smtp_from: string;
  smtp_to: string;
  smtp_tls: boolean;
  ntfy_server_url: string;
  ntfy_topic: string;
  ntfy_token: string;
  ntfy_priority: string;
}

function emptyChannelForm(): ChannelFormState {
  return {
    name: '',
    type: 'webhook',
    is_default: false,
    enabled: true,
    webhook_url: '',
    webhook_secret: '',
    discord_username: '',
    slack_channel: '',
    telegram_bot_token: '',
    telegram_chat_id: '',
    smtp_host: '',
    smtp_port: 587,
    smtp_user: '',
    smtp_pass: '',
    smtp_from: '',
    smtp_to: '',
    smtp_tls: true,
    ntfy_server_url: 'https://ntfy.sh',
    ntfy_topic: '',
    ntfy_token: '',
    ntfy_priority: 'default',
  };
}

function channelToForm(ch: NotificationChannel): ChannelFormState {
  const c = (ch.config || {}) as Record<string, unknown>;
  const base = emptyChannelForm();
  return {
    ...base,
    name: ch.name,
    type: ch.type,
    is_default: ch.is_default,
    enabled: ch.enabled,
    webhook_url: String(c.url || c.webhook_url || ''),
    webhook_secret: String(c.secret || ''),
    discord_username: String(c.username || ''),
    slack_channel: String(c.channel || ''),
    telegram_bot_token: String(c.bot_token || ''),
    telegram_chat_id: String(c.chat_id || ''),
    smtp_host: String(c.smtp_host || ''),
    smtp_port: Number(c.smtp_port) || 587,
    smtp_user: String(c.username || ''),
    smtp_pass: String(c.password || ''),
    smtp_from: String(c.from || ''),
    smtp_to: Array.isArray(c.to) ? c.to.join(', ') : '',
    smtp_tls: c.use_tls !== false,
    ntfy_server_url: String(c.server_url || 'https://ntfy.sh'),
    ntfy_topic: String(c.topic || ''),
    ntfy_token: String(c.token || ''),
    ntfy_priority: String(c.priority || 'default'),
  };
}

function buildChannelConfig(f: ChannelFormState): Record<string, unknown> {
  switch (f.type) {
    case 'webhook':
      return { url: f.webhook_url.trim(), secret: f.webhook_secret.trim() || undefined };
    case 'discord':
      return { webhook_url: f.webhook_url.trim(), username: f.discord_username.trim() || undefined };
    case 'slack':
      return { webhook_url: f.webhook_url.trim(), channel: f.slack_channel.trim() || undefined };
    case 'telegram':
      return { bot_token: f.telegram_bot_token.trim(), chat_id: f.telegram_chat_id.trim() };
    case 'email':
      return {
        smtp_host: f.smtp_host.trim(),
        smtp_port: Number(f.smtp_port) || 587,
        username: f.smtp_user.trim() || undefined,
        password: f.smtp_pass || undefined,
        from: f.smtp_from.trim(),
        to: f.smtp_to
          .split(',')
          .map((s) => s.trim())
          .filter(Boolean),
        use_tls: f.smtp_tls,
      };
    case 'ntfy':
      return {
        server_url: f.ntfy_server_url.trim() || 'https://ntfy.sh',
        topic: f.ntfy_topic.trim(),
        token: f.ntfy_token.trim() || undefined,
        priority: f.ntfy_priority || 'default',
      };
  }
}

export function NotificationsTab({ isAdmin }: { isAdmin: boolean }) {
  const [channels, setChannels] = useState<NotificationChannel[]>([]);
  const [subscribers, setSubscribers] = useState<Subscriber[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [feedback, setFeedback] = useState<{ kind: 'success' | 'error'; msg: string } | null>(null);

  const [modalOpen, setModalOpen] = useState(false);
  const [editId, setEditId] = useState<string | null>(null);
  const [form, setForm] = useState<ChannelFormState>(emptyChannelForm);
  const [saving, setSaving] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);
  const [testingId, setTestingId] = useState<string | null>(null);

  const loadAll = useCallback(async () => {
    if (!isAdmin) {
      setLoading(false);
      return;
    }
    setLoading(true);
    setError(null);
    try {
      const [cList, sList] = await Promise.all([api.listNotifications(), api.listSubscribers()]);
      setChannels(cList || []);
      setSubscribers(sList || []);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to load notifications.');
    } finally {
      setLoading(false);
    }
  }, [isAdmin]);

  useEffect(() => {
    void loadAll();
  }, [loadAll]);

  const handleSave = async (e: Event) => {
    e.preventDefault();
    if (!form.name.trim()) {
      setFormError('Channel name is required.');
      return;
    }
    setSaving(true);
    setFormError(null);
    try {
      const payload = {
        name: form.name.trim(),
        type: form.type,
        config: buildChannelConfig(form),
        is_default: form.is_default,
        enabled: form.enabled,
      };
      if (editId) {
        await api.updateNotification(editId, payload);
      } else {
        await api.createNotification(payload);
      }
      setModalOpen(false);
      await loadAll();
    } catch (err) {
      setFormError(err instanceof ApiError ? err.message : 'Failed to save notification channel.');
    } finally {
      setSaving(false);
    }
  };

  const handleTest = async (ch: NotificationChannel) => {
    setTestingId(ch.id);
    setFeedback(null);
    try {
      const res = await api.testNotification(ch.id);
      setFeedback({
        kind: 'success',
        msg: res.message || `Test notification sent via ${ch.name}.`,
      });
    } catch (err) {
      setFeedback({
        kind: 'error',
        msg: err instanceof ApiError ? err.message : `Test failed for ${ch.name}.`,
      });
    } finally {
      setTestingId(null);
    }
  };

  const handleDeleteChannel = async (id: string) => {
    try {
      await api.deleteNotification(id);
      await loadAll();
    } catch (err) {
      setFeedback({
        kind: 'error',
        msg: err instanceof ApiError ? err.message : 'Failed to delete channel.',
      });
    }
  };

  const handleDeleteSubscriber = async (id: string) => {
    try {
      await api.deleteSubscriber(id);
      await loadAll();
    } catch (err) {
      setFeedback({
        kind: 'error',
        msg: err instanceof ApiError ? err.message : 'Failed to remove subscriber.',
      });
    }
  };

  if (!isAdmin) {
    return (
      <div class="state-box">
        <h2 class="state-box-title">Administrator access required</h2>
        <p class="state-box-desc" style={{ margin: 0 }}>
          Administrator role is required to view and manage notification channels and subscribers.
        </p>
      </div>
    );
  }

  if (loading) {
    return (
      <div class="state-box" role="status">
        <p class="state-box-title">Loading notifications</p>
        <p class="state-box-desc" style={{ margin: 0 }}>
          Fetching alert channels and status page subscribers.
        </p>
      </div>
    );
  }

  if (error) {
    return (
      <div class="state-box" role="alert">
        <p class="state-box-title">Unable to load notifications</p>
        <p class="state-box-desc">{error}</p>
        <button type="button" class="btn btn-primary" onClick={() => void loadAll()}>
          Retry
        </button>
      </div>
    );
  }

  return (
    <div>
      {feedback ? (
        <div class={`alert-inline alert-inline--${feedback.kind}`}>{feedback.msg}</div>
      ) : null}

      <section class="section-block">
        <div class="toolbar-row">
          <h2 class="section-heading">Notification Channels ({channels.length})</h2>
          {isAdmin ? (
            <button
              type="button"
              class="btn btn-primary"
              onClick={() => {
                setEditId(null);
                setForm(emptyChannelForm());
                setFormError(null);
                setModalOpen(true);
              }}
            >
              Add Channel
            </button>
          ) : null}
        </div>

        {channels.length === 0 ? (
          <div class="state-box">
            <h3 class="state-box-title">No notification channels configured</h3>
            <p class="state-box-desc">
              Add a Webhook, Discord, Slack, Telegram, Email SMTP, or ntfy channel to receive alerts.
            </p>
            {isAdmin ? (
              <button
                type="button"
                class="btn btn-primary"
                onClick={() => {
                  setEditId(null);
                  setForm(emptyChannelForm());
                  setModalOpen(true);
                }}
              >
                Add Channel
              </button>
            ) : null}
          </div>
        ) : (
          <div class="card">
            <div class="table-scroll">
              <table class="data-table">
                <thead>
                  <tr>
                    <th>Name</th>
                    <th>Provider</th>
                    <th>State</th>
                    <th>Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {channels.map((ch) => (
                    <tr key={ch.id}>
                      <td>
                        <strong>{ch.name}</strong>
                        {ch.is_default ? (
                          <span class="tag-pill" style={{ marginLeft: '8px' }}>
                            Default
                          </span>
                        ) : null}
                      </td>
                      <td class="mono">{ch.type}</td>
                      <td>{ch.enabled ? 'Enabled' : 'Disabled'}</td>
                      <td>
                        {isAdmin ? (
                          <div class="action-row">
                            <button
                              type="button"
                              class="btn btn-sm"
                              disabled={testingId === ch.id}
                              onClick={() => void handleTest(ch)}
                            >
                              {testingId === ch.id ? 'Sending...' : 'Send Test'}
                            </button>
                            <button
                              type="button"
                              class="btn btn-sm"
                              onClick={() => {
                                setEditId(ch.id);
                                setForm(channelToForm(ch));
                                setFormError(null);
                                setModalOpen(true);
                              }}
                            >
                              Edit
                            </button>
                            <button
                              type="button"
                              class="btn btn-danger btn-sm"
                              onClick={() => void handleDeleteChannel(ch.id)}
                            >
                              Delete
                            </button>
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
      </section>

      <section class="section-block">
        <div class="toolbar-row">
          <h2 class="section-heading">Public Status Subscribers ({subscribers.length})</h2>
        </div>

        {subscribers.length === 0 ? (
          <div class="card" style={{ padding: '16px 20px' }}>
            <p style={{ margin: 0, color: 'var(--text-muted)', fontSize: '0.875rem' }}>
              No email or webhook subscribers have signed up on public status pages yet.
            </p>
          </div>
        ) : (
          <div class="card">
            <div class="table-scroll">
              <table class="data-table">
                <thead>
                  <tr>
                    <th>Type</th>
                    <th>Endpoint</th>
                    <th>Status</th>
                    <th>Subscribed</th>
                    <th>Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {subscribers.map((sub) => (
                    <tr key={sub.id}>
                      <td class="mono">{sub.type}</td>
                      <td class="mono">{sub.endpoint}</td>
                      <td>{sub.active ? (sub.verified ? 'Verified' : 'Active') : 'Inactive'}</td>
                      <td class="mono">{formatDateTime(sub.created_at)}</td>
                      <td>
                        {isAdmin ? (
                          <button
                            type="button"
                            class="btn btn-danger btn-sm"
                            onClick={() => void handleDeleteSubscriber(sub.id)}
                          >
                            Remove
                          </button>
                        ) : null}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        )}
      </section>

      <Modal
        open={modalOpen}
        title={editId ? 'Edit Notification Channel' : 'Add Notification Channel'}
        onClose={() => setModalOpen(false)}
        wide
      >
        <form onSubmit={handleSave} class="form-grid">
          {formError ? (
            <div class="alert-inline alert-inline--error form-field--full">{formError}</div>
          ) : null}

          <label class="form-field">
            <span class="form-label">Channel name</span>
            <input
              type="text"
              class="input"
              required
              placeholder="On-call Slack"
              value={form.name}
              onInput={(e) =>
                setForm((f) => ({ ...f, name: (e.currentTarget as HTMLInputElement).value }))
              }
            />
          </label>

          <label class="form-field">
            <span class="form-label">Provider</span>
            <select
              class="select"
              value={form.type}
              onChange={(e) =>
                setForm((f) => ({
                  ...f,
                  type: (e.currentTarget as HTMLSelectElement).value as NotificationType,
                }))
              }
            >
              <option value="webhook">Webhook</option>
              <option value="discord">Discord</option>
              <option value="slack">Slack</option>
              <option value="telegram">Telegram</option>
              <option value="email">Email (SMTP)</option>
              <option value="ntfy">ntfy</option>
            </select>
          </label>

          {form.type === 'webhook' || form.type === 'discord' || form.type === 'slack' ? (
            <label class="form-field form-field--full">
              <span class="form-label">Webhook URL</span>
              <input
                type="url"
                class="input mono"
                required
                placeholder="https://hooks.example.com/notify"
                value={form.webhook_url}
                onInput={(e) =>
                  setForm((f) => ({
                    ...f,
                    webhook_url: (e.currentTarget as HTMLInputElement).value,
                  }))
                }
              />
            </label>
          ) : null}

          {form.type === 'webhook' ? (
            <label class="form-field form-field--full">
              <span class="form-label">HMAC Secret (optional)</span>
              <input
                type="password"
                class="input mono"
                value={form.webhook_secret}
                onInput={(e) =>
                  setForm((f) => ({
                    ...f,
                    webhook_secret: (e.currentTarget as HTMLInputElement).value,
                  }))
                }
              />
              {editId ? (
                <span class="form-help">Leave as ******** to keep the stored secret.</span>
              ) : null}
            </label>
          ) : null}

          {form.type === 'discord' ? (
            <label class="form-field form-field--full">
              <span class="form-label">Display username (optional)</span>
              <input
                type="text"
                class="input"
                placeholder="Exa Monitor"
                value={form.discord_username}
                onInput={(e) =>
                  setForm((f) => ({
                    ...f,
                    discord_username: (e.currentTarget as HTMLInputElement).value,
                  }))
                }
              />
            </label>
          ) : null}

          {form.type === 'slack' ? (
            <label class="form-field form-field--full">
              <span class="form-label">Channel override (optional)</span>
              <input
                type="text"
                class="input mono"
                placeholder="#ops-alerts"
                value={form.slack_channel}
                onInput={(e) =>
                  setForm((f) => ({
                    ...f,
                    slack_channel: (e.currentTarget as HTMLInputElement).value,
                  }))
                }
              />
            </label>
          ) : null}

          {form.type === 'telegram' ? (
            <>
              <label class="form-field">
                <span class="form-label">Bot token</span>
                <input
                  type="password"
                  class="input mono"
                  required
                  value={form.telegram_bot_token}
                  onInput={(e) =>
                    setForm((f) => ({
                      ...f,
                      telegram_bot_token: (e.currentTarget as HTMLInputElement).value,
                    }))
                  }
                />
                {editId ? (
                  <span class="form-help">Leave as ******** to keep the stored secret.</span>
                ) : null}
              </label>
              <label class="form-field">
                <span class="form-label">Chat ID</span>
                <input
                  type="text"
                  class="input mono"
                  required
                  value={form.telegram_chat_id}
                  onInput={(e) =>
                    setForm((f) => ({
                      ...f,
                      telegram_chat_id: (e.currentTarget as HTMLInputElement).value,
                    }))
                  }
                />
              </label>
            </>
          ) : null}

          {form.type === 'email' ? (
            <>
              <label class="form-field">
                <span class="form-label">SMTP host</span>
                <input
                  type="text"
                  class="input mono"
                  required
                  placeholder="smtp.example.com"
                  value={form.smtp_host}
                  onInput={(e) =>
                    setForm((f) => ({
                      ...f,
                      smtp_host: (e.currentTarget as HTMLInputElement).value,
                    }))
                  }
                />
              </label>
              <label class="form-field">
                <span class="form-label">SMTP port</span>
                <input
                  type="number"
                  class="input mono"
                  required
                  value={form.smtp_port}
                  onInput={(e) =>
                    setForm((f) => ({
                      ...f,
                      smtp_port: Number((e.currentTarget as HTMLInputElement).value),
                    }))
                  }
                />
              </label>
              <label class="form-field">
                <span class="form-label">SMTP username (optional)</span>
                <input
                  type="text"
                  class="input"
                  value={form.smtp_user}
                  onInput={(e) =>
                    setForm((f) => ({
                      ...f,
                      smtp_user: (e.currentTarget as HTMLInputElement).value,
                    }))
                  }
                />
              </label>
              <label class="form-field">
                <span class="form-label">SMTP password (optional)</span>
                <input
                  type="password"
                  class="input"
                  value={form.smtp_pass}
                  onInput={(e) =>
                    setForm((f) => ({
                      ...f,
                      smtp_pass: (e.currentTarget as HTMLInputElement).value,
                    }))
                  }
                />
                {editId ? (
                  <span class="form-help">Leave as ******** to keep the stored secret.</span>
                ) : null}
              </label>
              <label class="form-field">
                <span class="form-label">From address</span>
                <input
                  type="email"
                  class="input"
                  required
                  placeholder="alerts@example.com"
                  value={form.smtp_from}
                  onInput={(e) =>
                    setForm((f) => ({
                      ...f,
                      smtp_from: (e.currentTarget as HTMLInputElement).value,
                    }))
                  }
                />
              </label>
              <label class="form-field">
                <span class="form-label">Recipients (comma separated)</span>
                <input
                  type="text"
                  class="input"
                  required
                  placeholder="ops@example.com"
                  value={form.smtp_to}
                  onInput={(e) =>
                    setForm((f) => ({
                      ...f,
                      smtp_to: (e.currentTarget as HTMLInputElement).value,
                    }))
                  }
                />
              </label>
              <div class="form-field form-field--full">
                <label class="checkbox-row">
                  <input
                    type="checkbox"
                    checked={form.smtp_tls}
                    onChange={(e) =>
                      setForm((f) => ({
                        ...f,
                        smtp_tls: (e.currentTarget as HTMLInputElement).checked,
                      }))
                    }
                  />
                  <span>Use TLS / STARTTLS</span>
                </label>
              </div>
            </>
          ) : null}

          {form.type === 'ntfy' ? (
            <>
              <label class="form-field">
                <span class="form-label">Server URL</span>
                <input
                  type="url"
                  class="input mono"
                  value={form.ntfy_server_url}
                  onInput={(e) =>
                    setForm((f) => ({
                      ...f,
                      ntfy_server_url: (e.currentTarget as HTMLInputElement).value,
                    }))
                  }
                />
              </label>
              <label class="form-field">
                <span class="form-label">Topic</span>
                <input
                  type="text"
                  class="input mono"
                  required
                  value={form.ntfy_topic}
                  onInput={(e) =>
                    setForm((f) => ({
                      ...f,
                      ntfy_topic: (e.currentTarget as HTMLInputElement).value,
                    }))
                  }
                />
              </label>
              <label class="form-field">
                <span class="form-label">Access token (optional)</span>
                <input
                  type="password"
                  class="input mono"
                  value={form.ntfy_token}
                  onInput={(e) =>
                    setForm((f) => ({
                      ...f,
                      ntfy_token: (e.currentTarget as HTMLInputElement).value,
                    }))
                  }
                />
                {editId ? (
                  <span class="form-help">Leave as ******** to keep the stored secret.</span>
                ) : null}
              </label>
              <label class="form-field">
                <span class="form-label">Priority</span>
                <select
                  class="select"
                  value={form.ntfy_priority}
                  onChange={(e) =>
                    setForm((f) => ({
                      ...f,
                      ntfy_priority: (e.currentTarget as HTMLSelectElement).value,
                    }))
                  }
                >
                  <option value="min">Min</option>
                  <option value="low">Low</option>
                  <option value="default">Default</option>
                  <option value="high">High</option>
                  <option value="urgent">Urgent</option>
                </select>
              </label>
            </>
          ) : null}

          <div class="form-field--full action-row">
            <label class="checkbox-row">
              <input
                type="checkbox"
                checked={form.enabled}
                onChange={(e) =>
                  setForm((f) => ({
                    ...f,
                    enabled: (e.currentTarget as HTMLInputElement).checked,
                  }))
                }
              />
              <span>Channel enabled</span>
            </label>
            <label class="checkbox-row">
              <input
                type="checkbox"
                checked={form.is_default}
                onChange={(e) =>
                  setForm((f) => ({
                    ...f,
                    is_default: (e.currentTarget as HTMLInputElement).checked,
                  }))
                }
              />
              <span>Attach to new monitors by default</span>
            </label>
          </div>

          <div class="form-field--full action-row" style={{ justifyContent: 'flex-end' }}>
            <button type="button" class="btn" onClick={() => setModalOpen(false)}>
              Cancel
            </button>
            <button type="submit" class="btn btn-primary" disabled={saving}>
              {saving ? 'Saving...' : editId ? 'Save channel' : 'Create channel'}
            </button>
          </div>
        </form>
      </Modal>
    </div>
  );
}
