import { useState } from 'preact/hooks';
import { api, ApiError } from '../api';
import { Header } from '../components/Header';
import type { User } from '../types';
import type { ThemeMode } from '../utils/theme';

interface SetupPageProps {
  onComplete: (user: User) => void;
  onNavigate: (path: string) => void;
  theme: ThemeMode;
  onToggleTheme: () => void;
}

export function SetupPage({ onComplete, onNavigate, theme, onToggleTheme }: SetupPageProps) {
  const [siteTitle, setSiteTitle] = useState('Service Status');
  const [username, setUsername] = useState('');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const handleSubmit = async (e: Event) => {
    e.preventDefault();
    setError(null);
    if (!username.trim() || !email.trim() || !password) {
      setError('Complete all required fields.');
      return;
    }
    if (password.length < 10) {
      setError('Password must be at least 10 characters.');
      return;
    }
    if (password !== confirmPassword) {
      setError('Passwords do not match.');
      return;
    }
    setSubmitting(true);
    try {
      const res = await api.setup({
        username: username.trim(),
        email: email.trim(),
        password,
        site_title: siteTitle.trim() || 'Service Status',
      });
      onComplete(res.user);
      onNavigate('/admin');
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Initial setup failed.');
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div class="page-shell">
      <Header
        title="Exa Initial Setup"
        onNavigate={onNavigate}
        theme={theme}
        onToggleTheme={onToggleTheme}
        showAdminLink
      />
      <main class="main-content">
        <div class="auth-wrap">
          <div class="auth-card">
            <h1 style={{ margin: '0 0 8px', fontSize: '1.25rem' }}>Set up Exa</h1>
            <p style={{ margin: '0 0 20px', color: 'var(--text-muted)', fontSize: '0.875rem' }}>
              Create the administrator account and default public status page.
            </p>

            {error ? <div class="alert-inline alert-inline--error">{error}</div> : null}

            <form onSubmit={handleSubmit} class="form-grid">
              <label class="form-field form-field--full">
                <span class="form-label">Status page title</span>
                <input
                  type="text"
                  class="input"
                  required
                  value={siteTitle}
                  onInput={(e) => setSiteTitle((e.currentTarget as HTMLInputElement).value)}
                />
              </label>

              <label class="form-field form-field--full">
                <span class="form-label">Admin username</span>
                <input
                  type="text"
                  class="input"
                  required
                  autoComplete="username"
                  placeholder="admin"
                  value={username}
                  onInput={(e) => setUsername((e.currentTarget as HTMLInputElement).value)}
                />
              </label>

              <label class="form-field form-field--full">
                <span class="form-label">Admin email</span>
                <input
                  type="email"
                  class="input"
                  required
                  autoComplete="email"
                  placeholder="admin@example.com"
                  value={email}
                  onInput={(e) => setEmail((e.currentTarget as HTMLInputElement).value)}
                />
              </label>

              <label class="form-field form-field--full">
                <span class="form-label">Password</span>
                <input
                  type="password"
                  class="input"
                  required
                  minLength={10}
                  autoComplete="new-password"
                  value={password}
                  onInput={(e) => setPassword((e.currentTarget as HTMLInputElement).value)}
                />
                <span class="form-help">Minimum 10 characters.</span>
              </label>

              <label class="form-field form-field--full">
                <span class="form-label">Confirm password</span>
                <input
                  type="password"
                  class="input"
                  required
                  minLength={10}
                  autoComplete="new-password"
                  value={confirmPassword}
                  onInput={(e) => setConfirmPassword((e.currentTarget as HTMLInputElement).value)}
                />
              </label>

              <div class="form-field form-field--full" style={{ marginTop: '8px' }}>
                <button type="submit" class="btn btn-primary" disabled={submitting}>
                  {submitting ? 'Creating instance...' : 'Create administrator account'}
                </button>
              </div>
            </form>
          </div>
        </div>
      </main>
    </div>
  );
}
