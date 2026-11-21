import { useCallback, useEffect, useState } from 'preact/hooks';
import { api, ApiError } from '../../api';
import { CopyIcon } from '../../components/Icons';
import type { APIKey, S3BackupObject, User, UserRole } from '../../types';
import { formatDateTime } from '../../utils/format';

function formatByteSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  const kb = bytes / 1024;
  if (kb < 1024) return `${kb.toFixed(1)} KB`;
  return `${(kb / 1024).toFixed(2)} MB`;
}

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

  // S3 backup settings and objects
  const [s3Enabled, setS3Enabled] = useState(false);
  const [s3Endpoint, setS3Endpoint] = useState('');
  const [s3Region, setS3Region] = useState('us-east-1');
  const [s3Bucket, setS3Bucket] = useState('');
  const [s3Prefix, setS3Prefix] = useState('exa-backups/');
  const [s3AccessKeyId, setS3AccessKeyId] = useState('');
  const [s3SecretKey, setS3SecretKey] = useState('');
  const [s3ForcePathStyle, setS3ForcePathStyle] = useState(true);
  const [s3Schedule, setS3Schedule] = useState('off');
  const [s3RetentionCount, setS3RetentionCount] = useState('14');
  const [s3LastBackupAt, setS3LastBackupAt] = useState('');
  const [s3LastBackupKey, setS3LastBackupKey] = useState('');
  const [s3LastError, setS3LastError] = useState('');
  const [s3Saving, setS3Saving] = useState(false);
  const [s3Testing, setS3Testing] = useState(false);
  const [s3Running, setS3Running] = useState(false);
  const [s3Objects, setS3Objects] = useState<S3BackupObject[]>([]);
  const [s3ListLoading, setS3ListLoading] = useState(false);
  const [s3ListError, setS3ListError] = useState<string | null>(null);
  const [s3ObjectBusy, setS3ObjectBusy] = useState<string | null>(null);

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

  // Local backup export/import
  const [exporting, setExporting] = useState(false);
  const [importing, setImporting] = useState(false);

  const applySettingsMap = (settingsMap: Record<string, string>) => {
    setRetentionDays(settingsMap.retention_days || '90');
    setMetricsToken(settingsMap.metrics_token || '');
    setBaseUrl(settingsMap.base_url || '');
    setAllowSubscriptions(settingsMap.allow_subscriptions === 'true');

    setS3Enabled(settingsMap.s3_enabled === 'true');
    setS3Endpoint(settingsMap.s3_endpoint || '');
    setS3Region(settingsMap.s3_region || 'us-east-1');
    setS3Bucket(settingsMap.s3_bucket || '');
    setS3Prefix(
      Object.prototype.hasOwnProperty.call(settingsMap, 's3_prefix')
        ? settingsMap.s3_prefix
        : 'exa-backups/',
    );
    setS3AccessKeyId(settingsMap.s3_access_key_id || '');
    setS3SecretKey(settingsMap.s3_secret_access_key || '');
    setS3ForcePathStyle(settingsMap.s3_force_path_style !== 'false');
    setS3Schedule(settingsMap.s3_schedule || 'off');
    setS3RetentionCount(settingsMap.s3_retention_count || '14');
    setS3LastBackupAt(settingsMap.s3_last_backup_at || '');
    setS3LastBackupKey(settingsMap.s3_last_backup_key || '');
    setS3LastError(settingsMap.s3_last_error || '');
  };

  const loadS3Backups = useCallback(async () => {
    setS3ListLoading(true);
    setS3ListError(null);
    try {
      const items = await api.listS3Backups();
      setS3Objects(items || []);
    } catch (err) {
      setS3ListError(err instanceof ApiError ? err.message : 'Failed to list remote backups.');
    } finally {
      setS3ListLoading(false);
    }
  }, []);

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
      applySettingsMap(settingsMap);
      setApiKeys(keysList || []);
      setUsers(usersList || []);
      if (settingsMap.s3_endpoint && settingsMap.s3_bucket) {
        void loadS3Backups();
      }
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to load settings.');
    } finally {
      setLoading(false);
    }
  }, [isAdmin, loadS3Backups]);

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
      const updated = await api.updateSettings({
        retention_days: retentionDays.trim() || '90',
        metrics_token: metricsToken.trim(),
        base_url: baseUrl.trim(),
        allow_subscriptions: allowSubscriptions ? 'true' : 'false',
      });
      applySettingsMap(updated);
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

  const handleSaveS3Settings = async (e: Event) => {
    e.preventDefault();
    setS3Saving(true);
    setFeedback(null);
    try {
      const updated = await api.updateSettings({
        s3_enabled: s3Enabled ? 'true' : 'false',
        s3_endpoint: s3Endpoint.trim(),
        s3_region: s3Region.trim() || 'us-east-1',
        s3_bucket: s3Bucket.trim(),
        s3_prefix: s3Prefix.trim(),
        s3_access_key_id: s3AccessKeyId.trim(),
        s3_secret_access_key: s3SecretKey,
        s3_force_path_style: s3ForcePathStyle ? 'true' : 'false',
        s3_schedule: s3Schedule,
        s3_retention_count: s3RetentionCount.trim() || '14',
      });
      applySettingsMap(updated);
      setFeedback({ kind: 'success', msg: 'S3 backup settings saved.' });
    } catch (err) {
      setFeedback({
        kind: 'error',
        msg: err instanceof ApiError ? err.message : 'Failed to save S3 backup settings.',
      });
    } finally {
      setS3Saving(false);
    }
  };

  const handleTestS3 = async () => {
    setS3Testing(true);
    setFeedback(null);
    try {
      await api.testS3Backup({
        s3_endpoint: s3Endpoint.trim(),
        s3_region: s3Region.trim() || 'us-east-1',
        s3_bucket: s3Bucket.trim(),
        s3_prefix: s3Prefix.trim(),
        s3_access_key_id: s3AccessKeyId.trim(),
        s3_secret_access_key: s3SecretKey,
        s3_force_path_style: s3ForcePathStyle ? 'true' : 'false',
      });
      setFeedback({ kind: 'success', msg: 'S3 connection verified.' });
    } catch (err) {
      setFeedback({
        kind: 'error',
        msg: err instanceof ApiError ? err.message : 'S3 connection test failed.',
      });
    } finally {
      setS3Testing(false);
    }
  };

  const handleRunS3Backup = async () => {
    setS3Running(true);
    setFeedback(null);
    try {
      const res = await api.runS3Backup();
      setFeedback({
        kind: 'success',
        msg: `Uploaded ${res.key} (${formatByteSize(res.size)}, ${res.monitors} monitors, ${res.heartbeats} heartbeats, ${res.daily_rollups} rollups).`,
      });
      const settingsMap = await api.getSettings();
      applySettingsMap(settingsMap);
      await loadS3Backups();
    } catch (err) {
      setFeedback({
        kind: 'error',
        msg: err instanceof ApiError ? err.message : 'S3 backup upload failed.',
      });
    } finally {
      setS3Running(false);
    }
  };

  const handleRestoreS3 = async (key: string) => {
    if (!window.confirm(`Restore backup from "${key}"? Existing monitor data will be replaced.`)) {
      return;
    }
    setS3ObjectBusy(key);
    setFeedback(null);
    try {
      const res = await api.restoreS3Backup(key);
      setFeedback({
        kind: 'success',
        msg: `Restored ${res.monitors ?? 0} monitors, ${res.heartbeats ?? 0} heartbeats, and ${res.daily_rollups ?? 0} rollups from ${key}.`,
      });
      await loadAll();
    } catch (err) {
      setFeedback({
        kind: 'error',
        msg: err instanceof ApiError ? err.message : 'Remote backup restore failed.',
      });
    } finally {
      setS3ObjectBusy(null);
    }
  };

  const handleDeleteS3 = async (key: string) => {
    if (!window.confirm(`Delete remote backup object "${key}"? This cannot be undone.`)) {
      return;
    }
    setS3ObjectBusy(key);
    setFeedback(null);
    try {
      await api.deleteS3Backup(key);
      setFeedback({ kind: 'success', msg: `Deleted ${key}.` });
      await loadS3Backups();
    } catch (err) {
      setFeedback({
        kind: 'error',
        msg: err instanceof ApiError ? err.message : 'Failed to delete remote backup.',
      });
    } finally {
      setS3ObjectBusy(null);
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

  const handleExportBackup = async (gzip: boolean) => {
    setExporting(true);
    setFeedback(null);
    try {
      const blob = await api.exportBackupBlob(gzip);
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      const dateStr = new Date().toISOString().slice(0, 10);
      a.download = gzip ? `exa-backup-${dateStr}.json.gz` : `exa-backup-${dateStr}.json`;
      a.click();
      URL.revokeObjectURL(url);
    } catch (err) {
      setFeedback({
        kind: 'error',
        msg: err instanceof ApiError ? err.message : 'Backup export failed.',
      });
    } finally {
      setExporting(false);
    }
  };

  const handleImportBackup = async (e: Event) => {
    const input = e.currentTarget as HTMLInputElement;
    const file = input.files?.[0];
    if (!file) return;
    setImporting(true);
    setFeedback(null);
    try {
      const res = await api.importBackupFile(file);
      const summary =
        res.monitors !== undefined
          ? `Imported ${res.monitors} monitors, ${res.heartbeats ?? 0} heartbeats, and ${res.daily_rollups ?? 0} daily rollups.`
          : res.message || 'Backup imported.';
      setFeedback({ kind: 'success', msg: summary });
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
    <div class="editorial-timeline">
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
              Exports include all monitoring history (heartbeats and daily rollups), monitors, component groups, status pages, incidents, maintenances, notification channels, and subscribers. User accounts, sessions, and API keys are excluded. Exported files include notification channel secrets and monitor push tokens; store them securely.
            </p>
            <div class="action-row">
              <button
                type="button"
                class="btn btn-primary"
                disabled={exporting}
                onClick={() => void handleExportBackup(true)}
              >
                {exporting ? 'Exporting...' : 'Export Full Backup (.json.gz)'}
              </button>
              <button
                type="button"
                class="btn"
                disabled={exporting}
                onClick={() => void handleExportBackup(false)}
              >
                Export Plain JSON (.json)
              </button>
              <label class="btn" style={{ cursor: 'pointer' }}>
                <span>{importing ? 'Importing...' : 'Import Backup (.json / .json.gz)'}</span>
                <input
                  type="file"
                  accept=".json,.json.gz,application/json,application/gzip"
                  class="sr-only"
                  disabled={importing}
                  onChange={handleImportBackup}
                />
              </label>
            </div>
          </section>

          <section class="card" style={{ padding: '20px' }}>
            <h2 class="section-heading" style={{ marginBottom: '8px' }}>
              Remote Backup (S3-Compatible Storage)
            </h2>
            <p class="form-help" style={{ margin: '0 0 16px' }}>
              Configure AWS S3, Cloudflare R2, MinIO, or Backblaze B2 for manual and scheduled gzipped backups.
            </p>

            <form onSubmit={handleSaveS3Settings} class="form-grid" style={{ marginBottom: '16px' }}>
              <div class="form-field form-field--full">
                <label class="checkbox-row">
                  <input
                    type="checkbox"
                    checked={s3Enabled}
                    onChange={(e) => setS3Enabled((e.currentTarget as HTMLInputElement).checked)}
                  />
                  <span>Enable scheduled S3 backups</span>
                </label>
              </div>

              <label class="form-field form-field--full">
                <span class="form-label">Endpoint URL</span>
                <input
                  type="url"
                  class="input mono"
                  placeholder="https://s3.us-east-1.amazonaws.com"
                  value={s3Endpoint}
                  onInput={(e) => setS3Endpoint((e.currentTarget as HTMLInputElement).value)}
                />
              </label>

              <label class="form-field">
                <span class="form-label">Region</span>
                <input
                  type="text"
                  class="input mono"
                  placeholder="us-east-1"
                  value={s3Region}
                  onInput={(e) => setS3Region((e.currentTarget as HTMLInputElement).value)}
                />
              </label>

              <label class="form-field">
                <span class="form-label">Bucket</span>
                <input
                  type="text"
                  class="input mono"
                  placeholder="exa-backups"
                  value={s3Bucket}
                  onInput={(e) => setS3Bucket((e.currentTarget as HTMLInputElement).value)}
                />
              </label>

              <label class="form-field">
                <span class="form-label">Key prefix</span>
                <input
                  type="text"
                  class="input mono"
                  placeholder="exa-backups/"
                  value={s3Prefix}
                  onInput={(e) => setS3Prefix((e.currentTarget as HTMLInputElement).value)}
                />
              </label>

              <label class="form-field">
                <span class="form-label">Access Key ID</span>
                <input
                  type="text"
                  class="input mono"
                  value={s3AccessKeyId}
                  onInput={(e) => setS3AccessKeyId((e.currentTarget as HTMLInputElement).value)}
                />
              </label>

              <label class="form-field">
                <span class="form-label">Secret Access Key</span>
                <input
                  type="password"
                  class="input mono"
                  placeholder="********"
                  value={s3SecretKey}
                  onInput={(e) => setS3SecretKey((e.currentTarget as HTMLInputElement).value)}
                />
                <span class="form-help">Leave as ******** to keep the stored secret.</span>
              </label>

              <label class="form-field">
                <span class="form-label">Schedule</span>
                <select
                  class="select"
                  value={s3Schedule}
                  onChange={(e) => setS3Schedule((e.currentTarget as HTMLSelectElement).value)}
                >
                  <option value="off">Off</option>
                  <option value="hourly">Hourly</option>
                  <option value="daily">Daily</option>
                  <option value="weekly">Weekly</option>
                </select>
              </label>

              <label class="form-field">
                <span class="form-label">Retention count</span>
                <input
                  type="number"
                  class="input mono"
                  min={0}
                  value={s3RetentionCount}
                  onInput={(e) => setS3RetentionCount((e.currentTarget as HTMLInputElement).value)}
                />
                <span class="form-help">0 = keep all remote backup objects.</span>
              </label>

              <div class="form-field form-field--full">
                <label class="checkbox-row">
                  <input
                    type="checkbox"
                    checked={s3ForcePathStyle}
                    onChange={(e) =>
                      setS3ForcePathStyle((e.currentTarget as HTMLInputElement).checked)
                    }
                  />
                  <span>Force path-style URLs</span>
                </label>
              </div>

              <div class="form-field--full action-row">
                <button type="submit" class="btn btn-primary" disabled={s3Saving}>
                  {s3Saving ? 'Saving...' : 'Save S3 Settings'}
                </button>
                <button
                  type="button"
                  class="btn"
                  disabled={s3Testing}
                  onClick={() => void handleTestS3()}
                >
                  {s3Testing ? 'Testing...' : 'Test Connection'}
                </button>
                <button
                  type="button"
                  class="btn"
                  disabled={s3Running}
                  onClick={() => void handleRunS3Backup()}
                >
                  {s3Running ? 'Uploading...' : 'Backup Now'}
                </button>
                <button
                  type="button"
                  class="btn"
                  disabled={s3ListLoading}
                  onClick={() => void loadS3Backups()}
                >
                  {s3ListLoading ? 'Refreshing...' : 'Refresh List'}
                </button>
              </div>
            </form>

            {s3LastBackupAt || s3LastBackupKey ? (
              <div class="alert-inline">
                <span>
                  Last backup: <strong class="mono">{formatDateTime(s3LastBackupAt)}</strong>
                  {s3LastBackupKey ? <span class="mono"> ({s3LastBackupKey})</span> : null}
                </span>
              </div>
            ) : null}

            {s3LastError ? (
              <div class="alert-inline alert-inline--error">
                <span>
                  <strong>Last S3 backup error:</strong> {s3LastError}
                </span>
              </div>
            ) : null}

            {s3ListLoading ? (
              <p class="form-help" role="status">
                Loading remote backup objects...
              </p>
            ) : s3ListError ? (
              <div class="alert-inline alert-inline--error">{s3ListError}</div>
            ) : s3Objects.length === 0 ? (
              <p class="form-help" style={{ margin: 0 }}>
                No remote backups listed. Configure S3 credentials above and click Refresh List or Backup Now.
              </p>
            ) : (
              <div class="table-scroll">
                <table class="data-table">
                  <thead>
                    <tr>
                      <th>Key</th>
                      <th>Size</th>
                      <th>Last Modified</th>
                      <th>Actions</th>
                    </tr>
                  </thead>
                  <tbody>
                    {s3Objects.map((obj) => (
                      <tr key={obj.key}>
                        <td class="mono">{obj.key}</td>
                        <td class="mono">{formatByteSize(obj.size)}</td>
                        <td class="mono">{formatDateTime(obj.last_modified)}</td>
                        <td>
                          <div class="action-row">
                            <button
                              type="button"
                              class="btn btn-sm"
                              disabled={s3ObjectBusy === obj.key}
                              onClick={() => void handleRestoreS3(obj.key)}
                            >
                              Restore
                            </button>
                            <button
                              type="button"
                              class="btn btn-danger btn-sm"
                              disabled={s3ObjectBusy === obj.key}
                              onClick={() => void handleDeleteS3(obj.key)}
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
          </section>
        </>
      ) : null}
    </div>
  );
}
