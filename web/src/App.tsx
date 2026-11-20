import { useCallback, useEffect, useState } from 'preact/hooks';
import { api } from './api';
import { AdminPage, type AdminTabKey } from './pages/AdminPage';
import { LoginPage } from './pages/LoginPage';
import { PublicStatusPage } from './pages/PublicStatusPage';
import { SetupPage } from './pages/SetupPage';
import type { User } from './types';
import { useTheme } from './utils/theme';

type Route =
  | { kind: 'public'; slug?: string }
  | { kind: 'setup' }
  | { kind: 'login' }
  | { kind: 'admin'; tab: AdminTabKey; monitorId?: string; searchQuery?: string };

function parseRoute(fullPath: string): Route {
  const [rawPath, rawQuery = ''] = fullPath.split('?');
  const clean = rawPath.replace(/\/+$/, '') || '/';
  const params = new URLSearchParams(rawQuery);
  const q = params.get('q') || '';

  if (clean === '/setup') return { kind: 'setup' };
  if (clean === '/login') return { kind: 'login' };
  if (clean === '/admin') return { kind: 'admin', tab: 'overview' };
  if (clean === '/admin/monitors') {
    return { kind: 'admin', tab: 'monitors', searchQuery: q };
  }
  if (clean.startsWith('/admin/monitors/')) {
    const id = decodeURIComponent(clean.slice('/admin/monitors/'.length));
    if (id) return { kind: 'admin', tab: 'monitor-detail', monitorId: id };
    return { kind: 'admin', tab: 'monitors', searchQuery: q };
  }
  if (clean.startsWith('/admin/')) {
    const sub = clean.slice('/admin/'.length);
    const valid: AdminTabKey[] = [
      'overview',
      'monitors',
      'incidents',
      'maintenance',
      'status-pages',
      'notifications',
      'settings',
    ];
    if (valid.includes(sub as AdminTabKey)) {
      return { kind: 'admin', tab: sub as AdminTabKey };
    }
    return { kind: 'admin', tab: 'overview' };
  }
  if (clean.startsWith('/s/')) {
    const slug = decodeURIComponent(clean.slice(3));
    return { kind: 'public', slug };
  }
  return { kind: 'public' };
}

export function App() {
  const [path, setPath] = useState(() => window.location.pathname + window.location.search);
  const route = parseRoute(path);
  const surface = route.kind === 'public' ? 'public' : 'admin';
  const [theme, toggleTheme] = useTheme(surface);
  const [setupCompleted, setSetupCompleted] = useState<boolean | null>(null);
  const [user, setUser] = useState<User | null | undefined>(undefined);

  const navigate = useCallback((nextPath: string) => {
    const current = window.location.pathname + window.location.search;
    if (current !== nextPath) {
      window.history.pushState(null, '', nextPath);
    }
    setPath(nextPath);
    window.scrollTo(0, 0);
  }, []);

  useEffect(() => {
    const onPop = () => setPath(window.location.pathname + window.location.search);
    window.addEventListener('popstate', onPop);
    return () => window.removeEventListener('popstate', onPop);
  }, []);

  useEffect(() => {
    let active = true;
    const syncAuthAndSetup = async () => {
      try {
        const st = await api.setupStatus();
        if (!active) return;
        setSetupCompleted(st.setup_completed);
        if (!st.setup_completed) {
          setUser(null);
          if (route.kind === 'admin' || route.kind === 'login') {
            navigate('/setup');
          }
          return;
        }
        if (route.kind === 'setup') {
          navigate('/login');
          return;
        }
        if (route.kind === 'admin' || route.kind === 'login') {
          try {
            const me = await api.me();
            if (!active) return;
            setUser(me.user);
            if (route.kind === 'login') navigate('/admin');
          } catch {
            if (!active) return;
            setUser(null);
            if (route.kind === 'admin') navigate('/login');
          }
        }
      } catch {
        if (!active) return;
        if (setupCompleted === null) setSetupCompleted(true);
        if (user === undefined) setUser(null);
      }
    };
    void syncAuthAndSetup();
    return () => {
      active = false;
    };
  }, [route.kind, navigate]);

  const handleLogout = async () => {
    try {
      await api.logout();
    } catch {
      // Clear local session state regardless of network response.
    }
    setUser(null);
    navigate('/login');
  };

  if (route.kind === 'setup') {
    if (setupCompleted === null) {
      return (
        <main class="main-content">
          <div class="auth-wrap">
            <div class="state-box" role="status">
              <p class="state-box-title">Checking instance setup</p>
              <p class="state-box-desc" style={{ margin: 0 }}>
                Verifying initialization status.
              </p>
            </div>
          </div>
        </main>
      );
    }
    return (
      <SetupPage
        onComplete={(u) => {
          setSetupCompleted(true);
          setUser(u);
        }}
        onNavigate={navigate}
        theme={theme}
        onToggleTheme={toggleTheme}
      />
    );
  }

  if (route.kind === 'login') {
    return (
      <LoginPage
        onLogin={(u) => setUser(u)}
        onNavigate={navigate}
        theme={theme}
        onToggleTheme={toggleTheme}
      />
    );
  }

  if (route.kind === 'admin') {
    if (!user) {
      return (
        <main class="main-content">
          <div class="auth-wrap">
            <div class="state-box" role="status">
              <p class="state-box-title">Checking session</p>
              <p class="state-box-desc" style={{ margin: 0 }}>
                Verifying administrator authentication.
              </p>
            </div>
          </div>
        </main>
      );
    }
    return (
      <AdminPage
        tab={route.tab}
        monitorId={route.monitorId}
        searchQuery={route.searchQuery}
        user={user}
        onNavigate={navigate}
        onLogout={handleLogout}
        theme={theme}
        onToggleTheme={toggleTheme}
      />
    );
  }

  return (
    <PublicStatusPage
      slug={route.slug}
      onNavigate={navigate}
      theme={theme}
      onToggleTheme={toggleTheme}
    />
  );
}
