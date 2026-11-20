import { useEffect, useState } from 'preact/hooks';
import { api, ApiError } from '../../api';
import { Modal } from '../../components/Modal';
import type {
  ComponentGroup,
  DNSConfig,
  HTTPConfig,
  Monitor,
  MonitorDetailResponse,
  MonitorType,
  NotificationChannel,
  PingConfig,
  PushConfig,
  StatusPage,
  TCPConfig,
} from '../../types';
import { slugify } from '../../utils/format';

export interface MonitorFormState {
  name: string;
  slug: string;
  type: MonitorType;
  target: string;
  group_id: string;
  interval_sec: number;
  timeout_sec: number;
  retries: number;
  degraded_ms: number;
  sort_order: number;
  active: boolean;
  attach_default_page: boolean;
  notification_channel_ids: string[];
  status_page_ids: string[];
  http_method: string;
  http_expected_status: string;
  http_keyword: string;
  http_keyword_invert: boolean;
  http_json_path: string;
  http_json_expected: string;
  http_headers_text: string;
  http_body: string;
  http_follow_redirects: boolean;
  http_ignore_tls: boolean;
  http_tls_warn_days: number;
  tcp_send_data: string;
  tcp_expect_data: string;
  ping_packet_count: number;
  dns_record_type: string;
  dns_resolver: string;
  dns_expected_value: string;
  push_grace_sec: number;
}

export function emptyMonitorForm(): MonitorFormState {
  return {
    name: '',
    slug: '',
    type: 'http',
    target: '',
    group_id: '',
    interval_sec: 60,
    timeout_sec: 10,
    retries: 2,
    degraded_ms: 0,
    sort_order: 0,
    active: true,
    attach_default_page: true,
    notification_channel_ids: [],
    status_page_ids: [],
    http_method: 'GET',
    http_expected_status: '200-299',
    http_keyword: '',
    http_keyword_invert: false,
    http_json_path: '',
    http_json_expected: '',
    http_headers_text: '',
    http_body: '',
    http_follow_redirects: true,
    http_ignore_tls: false,
    http_tls_warn_days: 14,
    tcp_send_data: '',
    tcp_expect_data: '',
    ping_packet_count: 2,
    dns_record_type: 'A',
    dns_resolver: '',
    dns_expected_value: '',
    push_grace_sec: 60,
  };
}

export function monitorToForm(m: MonitorDetailResponse): MonitorFormState {
  const cfg = (m.config || {}) as Record<string, unknown>;
  const http = cfg as HTTPConfig;
  const tcp = cfg as TCPConfig;
  const ping = cfg as PingConfig;
  const dns = cfg as DNSConfig;
  const push = cfg as PushConfig;
  const headersObj = http.headers && typeof http.headers === 'object' ? http.headers : null;
  return {
    name: m.name,
    slug: m.slug,
    type: m.type,
    target: m.target,
    group_id: m.group_id || '',
    interval_sec: m.interval_sec || 60,
    timeout_sec: m.timeout_sec || 10,
    retries: m.retries ?? 2,
    degraded_ms: m.degraded_ms ?? 0,
    sort_order: m.sort_order ?? 0,
    active: m.active,
    attach_default_page: false,
    notification_channel_ids: m.notification_channel_ids || [],
    status_page_ids: m.status_page_ids || [],
    http_method: http.method || 'GET',
    http_expected_status: http.expected_status || '200-299',
    http_keyword: http.keyword || '',
    http_keyword_invert: Boolean(http.keyword_invert),
    http_json_path: http.json_path || '',
    http_json_expected: http.json_expected || '',
    http_headers_text: headersObj ? JSON.stringify(headersObj, null, 2) : '',
    http_body: http.body || '',
    http_follow_redirects: http.follow_redirects !== false,
    http_ignore_tls: Boolean(http.ignore_tls),
    http_tls_warn_days: http.tls_expiry_warn_days || 14,
    tcp_send_data: tcp.send_data || '',
    tcp_expect_data: tcp.expect_data || '',
    ping_packet_count: ping.packet_count || 2,
    dns_record_type: dns.record_type || 'A',
    dns_resolver: dns.resolver || '',
    dns_expected_value: dns.expected_value || '',
    push_grace_sec: push.grace_sec || 60,
  };
}

function buildProbeConfig(form: MonitorFormState): Record<string, unknown> {
  switch (form.type) {
    case 'http': {
      let headers: Record<string, string> | undefined;
      const rawHeaders = form.http_headers_text.trim();
      if (rawHeaders) {
        const parsed = JSON.parse(rawHeaders);
        if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
          throw new Error('HTTP headers must be a JSON object of string key-value pairs.');
        }
        headers = parsed as Record<string, string>;
      }
      return {
        method: form.http_method || 'GET',
        expected_status: form.http_expected_status.trim() || '200-299',
        keyword: form.http_keyword,
        keyword_invert: form.http_keyword_invert,
        json_path: form.http_json_path,
        json_expected: form.http_json_expected,
        headers,
        body: form.http_body,
        follow_redirects: form.http_follow_redirects,
        ignore_tls: form.http_ignore_tls,
        tls_expiry_warn_days: Number(form.http_tls_warn_days) || 14,
      };
    }
    case 'tcp':
      return {
        send_data: form.tcp_send_data,
        expect_data: form.tcp_expect_data,
      };
    case 'ping':
      return {
        packet_count: Number(form.ping_packet_count) || 2,
      };
    case 'dns':
      return {
        record_type: form.dns_record_type || 'A',
        resolver: form.dns_resolver.trim(),
        expected_value: form.dns_expected_value.trim(),
      };
    case 'push':
      return {
        grace_sec: Number(form.push_grace_sec) || 60,
      };
  }
}

interface MonitorFormModalProps {
  open: boolean;
  initial?: MonitorDetailResponse | null;
  groups: ComponentGroup[];
  channels: NotificationChannel[];
  pages: StatusPage[];
  onClose: () => void;
  onSaved: (saved: Monitor) => void;
}

export function MonitorFormModal({
  open,
  initial,
  groups,
  channels,
  pages,
  onClose,
  onSaved,
}: MonitorFormModalProps) {
  const editingId = initial?.id || null;
  const [form, setForm] = useState<MonitorFormState>(emptyMonitorForm);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (!open) return;
    setError(null);
    setForm(initial ? monitorToForm(initial) : emptyMonitorForm());
  }, [open, initial]);

  const handleSubmit = async (e: Event) => {
    e.preventDefault();
    setError(null);
    if (!form.name.trim()) {
      setError('Monitor name is required.');
      return;
    }
    if (form.type !== 'push' && !form.target.trim()) {
      setError('Target is required for this monitor type.');
      return;
    }
    let config: Record<string, unknown>;
    try {
      config = buildProbeConfig(form);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Invalid probe configuration.');
      return;
    }

    setSaving(true);
    try {
      const payload = {
        name: form.name.trim(),
        slug: form.slug.trim() || slugify(form.name),
        type: form.type,
        target: form.type === 'push' ? form.target.trim() || 'push' : form.target.trim(),
        group_id: form.group_id || null,
        interval_sec: Math.max(10, Number(form.interval_sec) || 60),
        timeout_sec: Math.max(1, Number(form.timeout_sec) || 10),
        retries: Math.max(0, Number(form.retries) || 0),
        degraded_ms: Math.max(0, Number(form.degraded_ms) || 0),
        active: form.active,
        sort_order: Number(form.sort_order) || 0,
        config,
        notification_channel_ids: form.notification_channel_ids,
        attach_default_page: editingId ? undefined : form.attach_default_page,
        status_page_ids: form.status_page_ids.length > 0 ? form.status_page_ids : undefined,
      };
      const saved = editingId
        ? await api.updateMonitor(editingId, payload)
        : await api.createMonitor(payload);
      onSaved(saved);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to save monitor.');
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      open={open}
      title={editingId ? 'Edit Monitor' : 'New Monitor'}
      onClose={onClose}
      wide
    >
      <form onSubmit={handleSubmit} class="form-grid">
        {error ? (
          <div class="alert-inline alert-inline--error form-field--full">{error}</div>
        ) : null}

        <label class="form-field">
          <span class="form-label">Name</span>
          <input
            type="text"
            class="input"
            required
            placeholder="Primary API"
            value={form.name}
            onInput={(e) => {
              const name = (e.currentTarget as HTMLInputElement).value;
              setForm((f) => ({
                ...f,
                name,
                slug: editingId ? f.slug : slugify(name),
              }));
            }}
          />
        </label>

        <label class="form-field">
          <span class="form-label">Slug</span>
          <input
            type="text"
            class="input mono"
            placeholder="primary-api"
            value={form.slug}
            onInput={(e) =>
              setForm((f) => ({ ...f, slug: (e.currentTarget as HTMLInputElement).value }))
            }
          />
        </label>

        <label class="form-field">
          <span class="form-label">Check type</span>
          <select
            class="select"
            value={form.type}
            onChange={(e) =>
              setForm((f) => ({
                ...f,
                type: (e.currentTarget as HTMLSelectElement).value as MonitorType,
              }))
            }
          >
            <option value="http">HTTP / HTTPS</option>
            <option value="tcp">TCP Port</option>
            <option value="ping">ICMP Ping</option>
            <option value="dns">DNS Record</option>
            <option value="push">Push Heartbeat</option>
          </select>
        </label>

        <label class="form-field">
          <span class="form-label">Component group</span>
          <select
            class="select"
            value={form.group_id}
            onChange={(e) =>
              setForm((f) => ({ ...f, group_id: (e.currentTarget as HTMLSelectElement).value }))
            }
          >
            <option value="">Ungrouped</option>
            {groups.map((g) => (
              <option key={g.id} value={g.id}>
                {g.name}
              </option>
            ))}
          </select>
        </label>

        {form.type !== 'push' ? (
          <label class="form-field form-field--full">
            <span class="form-label">Target</span>
            <input
              type="text"
              class="input mono"
              required
              placeholder={
                form.type === 'http'
                  ? 'https://api.example.com/health'
                  : form.type === 'tcp'
                    ? 'db.example.com:5432'
                    : 'example.com'
              }
              value={form.target}
              onInput={(e) =>
                setForm((f) => ({ ...f, target: (e.currentTarget as HTMLInputElement).value }))
              }
            />
          </label>
        ) : null}

        <label class="form-field">
          <span class="form-label">Interval (seconds)</span>
          <input
            type="number"
            class="input mono"
            min={10}
            value={form.interval_sec}
            onInput={(e) =>
              setForm((f) => ({
                ...f,
                interval_sec: Number((e.currentTarget as HTMLInputElement).value),
              }))
            }
          />
        </label>

        <label class="form-field">
          <span class="form-label">Timeout (seconds)</span>
          <input
            type="number"
            class="input mono"
            min={1}
            value={form.timeout_sec}
            onInput={(e) =>
              setForm((f) => ({
                ...f,
                timeout_sec: Number((e.currentTarget as HTMLInputElement).value),
              }))
            }
          />
        </label>

        <label class="form-field">
          <span class="form-label">Retries before outage</span>
          <input
            type="number"
            class="input mono"
            min={0}
            value={form.retries}
            onInput={(e) =>
              setForm((f) => ({
                ...f,
                retries: Number((e.currentTarget as HTMLInputElement).value),
              }))
            }
          />
        </label>

        <label class="form-field">
          <span class="form-label">Degraded threshold (ms, 0 = off)</span>
          <input
            type="number"
            class="input mono"
            min={0}
            value={form.degraded_ms}
            onInput={(e) =>
              setForm((f) => ({
                ...f,
                degraded_ms: Number((e.currentTarget as HTMLInputElement).value),
              }))
            }
          />
        </label>

        {form.type === 'http' ? (
          <>
            <label class="form-field">
              <span class="form-label">HTTP method</span>
              <select
                class="select"
                value={form.http_method}
                onChange={(e) =>
                  setForm((f) => ({
                    ...f,
                    http_method: (e.currentTarget as HTMLSelectElement).value,
                  }))
                }
              >
                <option value="GET">GET</option>
                <option value="HEAD">HEAD</option>
                <option value="POST">POST</option>
                <option value="PUT">PUT</option>
              </select>
            </label>

            <label class="form-field">
              <span class="form-label">Expected status codes</span>
              <input
                type="text"
                class="input mono"
                placeholder="200-299"
                value={form.http_expected_status}
                onInput={(e) =>
                  setForm((f) => ({
                    ...f,
                    http_expected_status: (e.currentTarget as HTMLInputElement).value,
                  }))
                }
              />
            </label>

            <label class="form-field">
              <span class="form-label">Body keyword (optional)</span>
              <input
                type="text"
                class="input"
                value={form.http_keyword}
                onInput={(e) =>
                  setForm((f) => ({
                    ...f,
                    http_keyword: (e.currentTarget as HTMLInputElement).value,
                  }))
                }
              />
            </label>

            <label class="form-field">
              <span class="form-label">TLS expiry warning (days)</span>
              <input
                type="number"
                class="input mono"
                min={1}
                value={form.http_tls_warn_days}
                onInput={(e) =>
                  setForm((f) => ({
                    ...f,
                    http_tls_warn_days: Number((e.currentTarget as HTMLInputElement).value),
                  }))
                }
              />
            </label>

            <label class="form-field">
              <span class="form-label">JSON path (optional)</span>
              <input
                type="text"
                class="input mono"
                placeholder="status"
                value={form.http_json_path}
                onInput={(e) =>
                  setForm((f) => ({
                    ...f,
                    http_json_path: (e.currentTarget as HTMLInputElement).value,
                  }))
                }
              />
            </label>

            <label class="form-field">
              <span class="form-label">JSON expected value</span>
              <input
                type="text"
                class="input mono"
                placeholder="ok"
                value={form.http_json_expected}
                onInput={(e) =>
                  setForm((f) => ({
                    ...f,
                    http_json_expected: (e.currentTarget as HTMLInputElement).value,
                  }))
                }
              />
            </label>

            <label class="form-field form-field--full">
              <span class="form-label">Custom headers (JSON object, optional)</span>
              <textarea
                class="textarea mono"
                placeholder='{"Accept": "application/json"}'
                value={form.http_headers_text}
                onInput={(e) =>
                  setForm((f) => ({
                    ...f,
                    http_headers_text: (e.currentTarget as HTMLTextAreaElement).value,
                  }))
                }
              />
            </label>

            <label class="form-field form-field--full">
              <span class="form-label">Request body (optional)</span>
              <textarea
                class="textarea mono"
                value={form.http_body}
                onInput={(e) =>
                  setForm((f) => ({
                    ...f,
                    http_body: (e.currentTarget as HTMLTextAreaElement).value,
                  }))
                }
              />
            </label>

            <div class="form-field--full action-row">
              <label class="checkbox-row">
                <input
                  type="checkbox"
                  checked={form.http_follow_redirects}
                  onChange={(e) =>
                    setForm((f) => ({
                      ...f,
                      http_follow_redirects: (e.currentTarget as HTMLInputElement).checked,
                    }))
                  }
                />
                <span>Follow redirects</span>
              </label>
              <label class="checkbox-row">
                <input
                  type="checkbox"
                  checked={form.http_ignore_tls}
                  onChange={(e) =>
                    setForm((f) => ({
                      ...f,
                      http_ignore_tls: (e.currentTarget as HTMLInputElement).checked,
                    }))
                  }
                />
                <span>Ignore TLS errors</span>
              </label>
              <label class="checkbox-row">
                <input
                  type="checkbox"
                  checked={form.http_keyword_invert}
                  onChange={(e) =>
                    setForm((f) => ({
                      ...f,
                      http_keyword_invert: (e.currentTarget as HTMLInputElement).checked,
                    }))
                  }
                />
                <span>Invert keyword match</span>
              </label>
            </div>
          </>
        ) : null}

        {form.type === 'tcp' ? (
          <>
            <label class="form-field">
              <span class="form-label">Send data (optional)</span>
              <input
                type="text"
                class="input mono"
                value={form.tcp_send_data}
                onInput={(e) =>
                  setForm((f) => ({
                    ...f,
                    tcp_send_data: (e.currentTarget as HTMLInputElement).value,
                  }))
                }
              />
            </label>
            <label class="form-field">
              <span class="form-label">Expect data (optional)</span>
              <input
                type="text"
                class="input mono"
                value={form.tcp_expect_data}
                onInput={(e) =>
                  setForm((f) => ({
                    ...f,
                    tcp_expect_data: (e.currentTarget as HTMLInputElement).value,
                  }))
                }
              />
            </label>
          </>
        ) : null}

        {form.type === 'ping' ? (
          <label class="form-field">
            <span class="form-label">Packet count</span>
            <input
              type="number"
              class="input mono"
              min={1}
              max={10}
              value={form.ping_packet_count}
              onInput={(e) =>
                setForm((f) => ({
                  ...f,
                  ping_packet_count: Number((e.currentTarget as HTMLInputElement).value),
                }))
              }
            />
          </label>
        ) : null}

        {form.type === 'dns' ? (
          <>
            <label class="form-field">
              <span class="form-label">Record type</span>
              <select
                class="select"
                value={form.dns_record_type}
                onChange={(e) =>
                  setForm((f) => ({
                    ...f,
                    dns_record_type: (e.currentTarget as HTMLSelectElement).value,
                  }))
                }
              >
                <option value="A">A</option>
                <option value="AAAA">AAAA</option>
                <option value="CNAME">CNAME</option>
                <option value="MX">MX</option>
                <option value="TXT">TXT</option>
                <option value="NS">NS</option>
              </select>
            </label>
            <label class="form-field">
              <span class="form-label">Custom resolver (optional)</span>
              <input
                type="text"
                class="input mono"
                placeholder="1.1.1.1:53"
                value={form.dns_resolver}
                onInput={(e) =>
                  setForm((f) => ({
                    ...f,
                    dns_resolver: (e.currentTarget as HTMLInputElement).value,
                  }))
                }
              />
            </label>
            <label class="form-field form-field--full">
              <span class="form-label">Expected answer substring (optional)</span>
              <input
                type="text"
                class="input mono"
                value={form.dns_expected_value}
                onInput={(e) =>
                  setForm((f) => ({
                    ...f,
                    dns_expected_value: (e.currentTarget as HTMLInputElement).value,
                  }))
                }
              />
            </label>
          </>
        ) : null}

        {form.type === 'push' ? (
          <label class="form-field">
            <span class="form-label">Grace period (seconds)</span>
            <input
              type="number"
              class="input mono"
              min={5}
              value={form.push_grace_sec}
              onInput={(e) =>
                setForm((f) => ({
                  ...f,
                  push_grace_sec: Number((e.currentTarget as HTMLInputElement).value),
                }))
              }
            />
          </label>
        ) : null}

        {channels.length > 0 ? (
          <div class="form-field form-field--full">
            <span class="form-label">Notification channels</span>
            <div class="action-row">
              {channels.map((ch) => (
                <label key={ch.id} class="checkbox-row">
                  <input
                    type="checkbox"
                    checked={form.notification_channel_ids.includes(ch.id)}
                    onChange={(e) => {
                      const checked = (e.currentTarget as HTMLInputElement).checked;
                      setForm((f) => ({
                        ...f,
                        notification_channel_ids: checked
                          ? [...f.notification_channel_ids, ch.id]
                          : f.notification_channel_ids.filter((id) => id !== ch.id),
                      }));
                    }}
                  />
                  <span>
                    {ch.name} ({ch.type})
                  </span>
                </label>
              ))}
            </div>
          </div>
        ) : null}

        {!editingId ? (
          <div class="form-field form-field--full">
            <label class="checkbox-row">
              <input
                type="checkbox"
                checked={form.attach_default_page}
                onChange={(e) =>
                  setForm((f) => ({
                    ...f,
                    attach_default_page: (e.currentTarget as HTMLInputElement).checked,
                  }))
                }
              />
              <span>Attach to default public status page</span>
            </label>
          </div>
        ) : pages.length > 0 ? (
          <div class="form-field form-field--full">
            <span class="form-label">Visible on status pages</span>
            <div class="action-row">
              {pages.map((p) => (
                <label key={p.id} class="checkbox-row">
                  <input
                    type="checkbox"
                    checked={form.status_page_ids.includes(p.id)}
                    onChange={(e) => {
                      const checked = (e.currentTarget as HTMLInputElement).checked;
                      setForm((f) => ({
                        ...f,
                        status_page_ids: checked
                          ? [...f.status_page_ids, p.id]
                          : f.status_page_ids.filter((id) => id !== p.id),
                      }));
                    }}
                  />
                  <span>{p.title}</span>
                </label>
              ))}
            </div>
          </div>
        ) : null}

        <div class="form-field--full action-row" style={{ justifyContent: 'flex-end' }}>
          <button type="button" class="btn" onClick={onClose}>
            Cancel
          </button>
          <button type="submit" class="btn btn-primary" disabled={saving}>
            {saving ? 'Saving...' : editingId ? 'Save changes' : 'Create monitor'}
          </button>
        </div>
      </form>
    </Modal>
  );
}
