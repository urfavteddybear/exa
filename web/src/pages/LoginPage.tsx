import { useState } from 'preact/hooks';
import { api, ApiError } from '../api';
import { Header } from '../components/Header';
import type { User } from '../types';
import type { ThemeMode } from '../utils/theme';

interface LoginPageProps {
  onLogin: (user: User) => void;
  onNavigate: (path: string) => void;
  theme: ThemeMode;
  onToggleTheme: () => void;
}

export function LoginPage({ onLogin, onNavigate, theme, onToggleTheme }: LoginPageProps) {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const handleSubmit = async (e: Event) => {
    e.preventDefault();
    setError(null);
    if (!username.trim() || !password) {
      setError('Enter your username and password.');
      return;
    }
    setSubmitting(true);
    try {
      const res = await api.login({ username: username.trim(), password });
      onLogin(res.user);
      onNavigate('/admin');
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Sign in failed.');
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div class="page-shell">
      <Header
        title="Exa Operations"
        onNavigate={onNavigate}
        theme={theme}
        onToggleTheme={onToggleTheme}
        showAdminLink
      />
      <main class="main-content">
        <div class="auth-wrap">
          <div class="auth-card">
            <h1 style={{ margin: '0 0 8px', fontSize: '1.25rem' }}>Admin Console</h1>
            <p style={{ margin: '0 0 20px', color: 'var(--text-muted)', fontSize: '0.875rem' }}>
              Sign in to manage monitors, incidents, maintenance windows, and notifications.
            </p>

            {error ? <div class="alert-inline alert-inline--error">{error}</div> : null}

            <form onSubmit={handleSubmit} class="form-grid">
              <label class="form-field form-field--full">
                <span class="form-label">Username</span>
                <input
                  type="text"
                  class="input"
                  required
                  autoComplete="username"
                  placeholder="Your username"
                  value={username}
                  onInput={(e) => setUsername((e.currentTarget as HTMLInputElement).value)}
                />
              </label>

              <label class="form-field form-field--full">
                <span class="form-label">Password</span>
                <input
                  type="password"
                  class="input"
                  required
                  autoComplete="current-password"
                  value={password}
                  onInput={(e) => setPassword((e.currentTarget as HTMLInputElement).value)}
                />
              </label>

              <div class="form-field form-field--full" style={{ marginTop: '8px' }}>
                <button type="submit" class="btn btn-primary" disabled={submitting}>
                  {submitting ? 'Signing in...' : 'Sign in'}
                </button>
              </div>
            </form>
          </div>
        </div>
      </main>
    </div>
  );
}
