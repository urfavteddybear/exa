import { useCallback, useEffect, useState } from 'preact/hooks';
import { api, ApiError } from '../../api';
import { CopyIcon } from '../../components/Icons';
import type { APIKey, User, UserRole } from '../../types';
import { formatDateTime } from '../../utils/format';

export function SettingsTab({ currentUser }: { currentUser: User }) {
  const isAdmin = currentUser.role === 'admin';
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [feedback, setFeedback] = useState<{ kind: 'success' | 'error'; msg: string } | null>(null);

  // Password change
  const [curPw, setCurPw] = useState('');
  const [newPw, setNewPw] = useState('');
  const [pwSaving, setPwSaving] = useState(false);

  // Instance settings
  const [retentionDays, setRetentionDays] = useState('90');
  const [metricsToken, setMetricsToken] = useState('');
  const [baseUrl, setBaseUrl] = useState('');
  const [allowSubscriptions, setAllowSubscriptions] = useState(false);
  const [settingsSaving, setSettingsSaving] = useState(false);

  // API keys
  const [apiKeys, setApiKeys] = useState<APIKey[]>([]);
  const [keyName, setKeyName] = useState('');
  const [keyRole, setKeyRole] = useState<UserRole>('admin');
  const [createdToken, setCreatedToken] = useState<string | null>(null);
  const [copiedToken, setCopiedToken] = useState(false);

  // Users
  const [users, setUsers] = useState<User[]>([]);
  const [newUsername, setNewUsername] = useState('');
  const [newEmail, setNewEmail] = useState('');
  const [newUserPw, setNewUserPw] = useState('');
  const [newUserRole, setNewUserRole] = useState<UserRole>('viewer');

  // Backup import
  const [importing, setImporting] = useState(false);

  const loadAll = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      if (!isAdmin) {
        setLoading(false);
        return;
      }
      const [settingsMap, keysList, usersList] = await Promise.all([
        api.getSettings(),
        api.listApiKeys(),
        api.listUsers(),
      ]);
      setRetentionDays(settingsMap.retention_days || '90');
      setMetricsToken(settingsMap.metrics_token || '');
      setBaseUrl(settingsMap.base_url || '');
      setAllowSubscriptions(settingsMap.allow_subscriptions === 'true');
      setApiKeys(keysList || []);
      setUsers(usersList || []);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to load settings.');
    } finally {
      setLoading(false);
    }
  }, [isAdmin]);

  useEffect(() => {
    void loadAll();
  }, [loadAll]);

  const handlePasswordChange = async (e: Event) => {
    e.preventDefault();
    if (!curPw || newPw.length < 10) {
      setFeedback({ kind: 'error', msg: 'New password must be at least 10 characters.' });
      return;
    }
    setPwSaving(true);
    setFeedback(null);
    try {
      await api.changePassword({ current_password: curPw, new_password: newPw });
      setCurPw('');
      setNewPw('');
      setFeedback({ kind: 'success', msg: 'Password updated.' });
    } catch (err) {
      setFeedback({
        kind: 'error',
        msg: err instanceof ApiError ? err.message : 'Password update failed.',
      });
    } finally {
      setPwSaving(false);
    }
  };

  const handleSaveSettings = async (e: Event) => {
    e.preventDefault();
    setSettingsSaving(true);
    setFeedback(null);
    try {
      await api.updateSettings({
        retention_days: retentionDays.trim() || '90',
        metrics_token: metricsToken.trim(),
        base_url: baseUrl.trim(),
        allow_subscriptions: allowSubscriptions ? 'true' : 'false',
      });
      setFeedback({ kind: 'success', msg: 'Instance settings saved.' });
    } catch (err) {
      setFeedback({
        kind: 'error',
        msg: err instanceof ApiError ? err.message : 'Failed to save settings.',
      });
    } finally {
      setSettingsSaving(false);
    }
  };

  const handleCreateApiKey = async (e: Event) => {
    e.preventDefault();
    if (!keyName.trim()) return;
    setFeedback(null);
    try {
      const created = await api.createApiKey({ name: keyName.trim(), role: keyRole });
      setCreatedToken(created.token);
      setCopiedToken(false);
      setKeyName('');
      await loadAll();
    } catch (err) {
      setFeedback({
        kind: 'error',
        msg: err instanceof ApiError ? err.message : 'Failed to create API key.',
      });
    }
  };

  const handleRevokeKey = async (id: string) => {
    try {
      await api.deleteApiKey(id);
      await loadAll();
    } catch (err) {
      setFeedback({
        kind: 'error',
        msg: err instanceof ApiError ? err.message : 'Failed to revoke API key.',
      });
    }
  };

  const handleCreateUser = async (e: Event) => {
    e.preventDefault();
    if (!newUsername.trim() || !newEmail.trim() || newUserPw.length < 10) {
      setFeedback({
        kind: 'error',
        msg: 'Username, email, and password (minimum 10 characters) are required.',
      });
      return;
    }
    setFeedback(null);
    try {
      await api.createUser({
        username: newUsername.trim(),
        email: newEmail.trim(),
        password: newUserPw,
        role: newUserRole,
      });
      setNewUsername('');
      setNewEmail('');
      setNewUserPw('');
      setFeedback({ kind: 'success', msg: 'User created.' });
      await loadAll();
    } catch (err) {
      setFeedback({
        kind: 'error',
        msg: err instanceof ApiError ? err.message : 'Failed to create user.',
      });
    }
  };

  const handleDeleteUser = async (id: string) => {
    try {
      await api.deleteUser(id);
      await loadAll();
    } catch (err) {
      setFeedback({
        kind: 'error',
        msg: err instanceof ApiError ? err.message : 'Failed to delete user.',
      });
    }
  };

  const handleExportBackup = async () => {
    setFeedback(null);
    try {
      const data = await api.exportBackup();
      const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `exa-backup-${new Date().toISOString().slice(0, 10)}.json`;
      a.click();
      URL.revokeObjectURL(url);
    } catch (err) {
      setFeedback({
        kind: 'error',
        msg: err instanceof ApiError ? err.message : 'Backup export failed.',
      });
    }
  };

  const handleImportBackup = async (e: Event) => {
    const input = e.currentTarget as HTMLInputElement;
    const file = input.files?.[0];
    if (!file) return;
    setImporting(true);
    setFeedback(null);
    try {
      const text = await file.text();
      const parsed = JSON.parse(text);
      const res = await api.importBackup(parsed);
      setFeedback({ kind: 'success', msg: res.message || 'Backup imported.' });
      await loadAll();
    } catch (err) {
      setFeedback({
        kind: 'error',
        msg: err instanceof Error ? err.message : 'Backup import failed.',
      });
    } finally {
      setImporting(false);
      input.value = '';
    }
  };

  if (loading) {
    return (
      <div class="state-box" role="status">
        <p class="state-box-title">Loading settings</p>
        <p class="state-box-desc" style={{ margin: 0 }}>
          Fetching instance configuration, API keys, and users.
        </p>
      </div>
    );
  }

  if (error) {
    return (
      <div class="state-box" role="alert">
        <p class="state-box-title">Unable to load settings</p>
        <p class="state-box-desc">{error}</p>
        <button type="button" class="btn btn-primary" onClick={() => void loadAll()}>
          Retry
        </button>
      </div>
    );
  }

  return (
    <div class="timeline-list">
      {feedback ? (
        <div class={`alert-inline alert-inline--${feedback.kind}`}>{feedback.msg}</div>
      ) : null}

      <section class="card" style={{ padding: '20px' }}>
        <h2 class="section-heading" style={{ marginBottom: '12px' }}>
          Account Password
        </h2>
        <form onSubmit={handlePasswordChange} class="form-grid">
          <label class="form-field">
            <span class="form-label">Current password</span>
            <input
              type="password"
              class="input"
              required
              autoComplete="current-password"
              value={curPw}
              onInput={(e) => setCurPw((e.currentTarget as HTMLInputElement).value)}
            />
          </label>
          <label class="form-field">
            <span class="form-label">New password</span>
            <input
              type="password"
              class="input"
              required
              minLength={10}
              autoComplete="new-password"
              value={newPw}
              onInput={(e) => setNewPw((e.currentTarget as HTMLInputElement).value)}
            />
            <span class="form-help">Minimum 10 characters.</span>
          </label>
          <div class="form-field form-field--full">
            <button type="submit" class="btn btn-primary" disabled={pwSaving} style={{ alignSelf: 'start' }}>
              {pwSaving ? 'Updating...' : 'Update password'}
            </button>
          </div>
        </form>
      </section>

      {isAdmin ? (
        <>
          <section class="card" style={{ padding: '20px' }}>
            <h2 class="section-heading" style={{ marginBottom: '12px' }}>
              Instance Configuration
            </h2>
            <form onSubmit={handleSaveSettings} class="form-grid">
              <label class="form-field">
                <span class="form-label">Heartbeat retention (days)</span>
                <input
                  type="number"
                  class="input mono"
                  min={1}
                  value={retentionDays}
                  onInput={(e) => setRetentionDays((e.currentTarget as HTMLInputElement).value)}
                />
              </label>
              <label class="form-field">
                <span class="form-label">Public base URL</span>
                <input
                  type="url"
                  class="input mono"
                  placeholder="https://status.example.com"
                  value={baseUrl}
                  onInput={(e) => setBaseUrl((e.currentTarget as HTMLInputElement).value)}
                />
              </label>
              <label class="form-field form-field--full">
                <span class="form-label">Prometheus /metrics bearer token (optional)</span>
                <input
                  type="text"
                  class="input mono"
                  placeholder="Leave blank to disable /metrics endpoint"
                  value={metricsToken}
                  onInput={(e) => setMetricsToken((e.currentTarget as HTMLInputElement).value)}
                />
                <span class="form-help">
                  When blank, /metrics returns 401. Set a bearer token to enable Prometheus scraping.
                </span>
              </label>
              <div class="form-field form-field--full">
                <label class="checkbox-row">
                  <input
                    type="checkbox"
                    checked={allowSubscriptions}
                    onChange={(e) =>
                      setAllowSubscriptions((e.currentTarget as HTMLInputElement).checked)
                    }
                  />
                  <span>Allow public status page subscriptions</span>
                </label>
                <span class="form-help">
                  When enabled, status page visitors can subscribe via email or HTTPS webhook.
                </span>
              </div>
              <div class="form-field form-field--full">
                <button
                  type="submit"
                  class="btn btn-primary"
                  disabled={settingsSaving}
                  style={{ alignSelf: 'start' }}
                >
                  {settingsSaving ? 'Saving...' : 'Save configuration'}
                </button>
              </div>
            </form>
          </section>

          <section class="card" style={{ padding: '20px' }}>
            <h2 class="section-heading" style={{ marginBottom: '12px' }}>
              API Keys
            </h2>

            {createdToken ? (
              <div class="alert-inline alert-inline--success" style={{ flexDirection: 'column' }}>
                <strong>Copy this API key now. It will not be shown again:</strong>
                <div class="code-inline-box" style={{ width: '100%' }}>
                  <span>{createdToken}</span>
                  <button
                    type="button"
                    class="btn btn-sm"
                    onClick={async () => {
                      await navigator.clipboard.writeText(createdToken);
                      setCopiedToken(true);
                    }}
                  >
                    <CopyIcon size={14} />
                    <span>{copiedToken ? 'Copied' : 'Copy'}</span>
                  </button>
                </div>
              </div>
            ) : null}

            <form onSubmit={handleCreateApiKey} class="form-grid" style={{ marginBottom: '16px' }}>
              <label class="form-field">
                <span class="form-label">Key label</span>
                <input
                  type="text"
                  class="input"
                  required
                  placeholder="CI deployment script"
                  value={keyName}
                  onInput={(e) => setKeyName((e.currentTarget as HTMLInputElement).value)}
                />
              </label>
              <label class="form-field">
                <span class="form-label">Role</span>
                <select
                  class="select"
                  value={keyRole}
                  onChange={(e) => setKeyRole((e.currentTarget as HTMLSelectElement).value as UserRole)}
                >
                  <option value="admin">Admin</option>
                  <option value="viewer">Viewer</option>
                </select>
              </label>
              <div class="form-field form-field--full">
                <button type="submit" class="btn btn-primary" style={{ alignSelf: 'start' }}>
                  Create API key
                </button>
              </div>
            </form>

            {apiKeys.length === 0 ? (
              <p class="form-help" style={{ margin: 0 }}>
                No API keys issued yet.
              </p>
            ) : (
              <div class="table-scroll">
                <table class="data-table">
                  <thead>
                    <tr>
                      <th>Name</th>
                      <th>Prefix</th>
                      <th>Role</th>
                      <th>Last used</th>
                      <th>Actions</th>
                    </tr>
                  </thead>
                  <tbody>
                    {apiKeys.map((k) => (
                      <tr key={k.id}>
                        <td>{k.name}</td>
                        <td class="mono">{k.key_prefix}</td>
                        <td>{k.role}</td>
                        <td class="mono">{formatDateTime(k.last_used_at)}</td>
                        <td>
                          <button
                            type="button"
                            class="btn btn-danger btn-sm"
                            onClick={() => void handleRevokeKey(k.id)}
                          >
                            Revoke
                          </button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </section>

          <section class="card" style={{ padding: '20px' }}>
            <h2 class="section-heading" style={{ marginBottom: '12px' }}>
              Users ({users.length})
            </h2>

            <form onSubmit={handleCreateUser} class="form-grid" style={{ marginBottom: '16px' }}>
              <label class="form-field">
                <span class="form-label">Username</span>
                <input
                  type="text"
                  class="input"
                  required
                  value={newUsername}
                  onInput={(e) => setNewUsername((e.currentTarget as HTMLInputElement).value)}
                />
              </label>
              <label class="form-field">
                <span class="form-label">Email</span>
                <input
                  type="email"
                  class="input"
                  required
                  value={newEmail}
                  onInput={(e) => setNewEmail((e.currentTarget as HTMLInputElement).value)}
                />
              </label>
              <label class="form-field">
                <span class="form-label">Password</span>
                <input
                  type="password"
                  class="input"
                  required
                  minLength={10}
                  value={newUserPw}
                  onInput={(e) => setNewUserPw((e.currentTarget as HTMLInputElement).value)}
                />
                <span class="form-help">Minimum 10 characters.</span>
              </label>
              <label class="form-field">
                <span class="form-label">Role</span>
                <select
                  class="select"
                  value={newUserRole}
                  onChange={(e) =>
                    setNewUserRole((e.currentTarget as HTMLSelectElement).value as UserRole)
                  }
                >
                  <option value="viewer">Viewer</option>
                  <option value="admin">Admin</option>
                </select>
              </label>
              <div class="form-field form-field--full">
                <button type="submit" class="btn btn-primary" style={{ alignSelf: 'start' }}>
                  Add user
                </button>
              </div>
            </form>

            <div class="table-scroll">
              <table class="data-table">
                <thead>
                  <tr>
                    <th>Username</th>
                    <th>Email</th>
                    <th>Role</th>
                    <th>Created</th>
                    <th>Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {users.map((u) => (
                    <tr key={u.id}>
                      <td>
                        <strong>{u.username}</strong>
                        {u.id === currentUser.id ? (
                          <span class="tag-pill" style={{ marginLeft: '8px' }}>
                            You
                          </span>
                        ) : null}
                      </td>
                      <td>{u.email}</td>
                      <td>{u.role}</td>
                      <td class="mono">{formatDateTime(u.created_at)}</td>
                      <td>
                        {u.id !== currentUser.id ? (
                          <button
                            type="button"
                            class="btn btn-danger btn-sm"
                            onClick={() => void handleDeleteUser(u.id)}
                          >
                            Delete
                          </button>
                        ) : null}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </section>

          <section class="card" style={{ padding: '20px' }}>
            <h2 class="section-heading" style={{ marginBottom: '8px' }}>
              Backup and Restore
            </h2>
            <p class="form-help" style={{ margin: '0 0 14px' }}>
              Export monitors, groups, status pages, notification channels, incidents, and maintenance windows as JSON.
              Exported backup files include notification channel secrets and monitor push tokens; store them securely.
            </p>
            <div class="action-row">
              <button type="button" class="btn btn-primary" onClick={() => void handleExportBackup()}>
                Export Backup JSON
              </button>
              <label class="btn" style={{ cursor: 'pointer' }}>
                <span>{importing ? 'Importing...' : 'Import Backup JSON'}</span>
                <input
                  type="file"
                  accept="application/json,.json"
                  class="sr-only"
                  disabled={importing}
                  onChange={handleImportBackup}
                />
              </label>
            </div>
          </section>
        </>
      ) : null}
    </div>
  );
}
