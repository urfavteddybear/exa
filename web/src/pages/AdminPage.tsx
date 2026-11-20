import { useCallback, useEffect, useState } from 'preact/hooks';
import { api } from '../api';
import { CloseIcon, MenuIcon, MoonIcon, PulseLogoIcon, SunIcon } from '../components/Icons';
import type { User } from '../types';
import type { ThemeMode } from '../utils/theme';
import { IncidentsTab } from './admin/IncidentsTab';
import { MaintenanceTab } from './admin/MaintenanceTab';
import { MonitorDetailView } from './admin/MonitorDetailView';
import { MonitorsTab } from './admin/MonitorsTab';
import { NotificationsTab } from './admin/NotificationsTab';
import { OverviewTab } from './admin/OverviewTab';
import { SettingsTab } from './admin/SettingsTab';
import { StatusPagesTab } from './admin/StatusPagesTab';

export type AdminTabKey =
  | 'overview'
  | 'monitors'
  | 'monitor-detail'
  | 'incidents'
  | 'maintenance'
  | 'status-pages'
  | 'notifications'
  | 'settings';

interface AdminPageProps {
  tab: AdminTabKey;
  monitorId?: string;
  searchQuery?: string;
  user: User;
  onNavigate: (path: string) => void;
  onLogout: () => void;
  theme: ThemeMode;
  onToggleTheme: () => void;
}

export function AdminPage({
  tab,
  monitorId,
  searchQuery,
  user,
  onNavigate,
  onLogout,
  theme,
  onToggleTheme,
}: AdminPageProps) {
  const isAdmin = user.role === 'admin';
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [siteTitle, setSiteTitle] = useState('Exa');
  const [monitorCount, setMonitorCount] = useState<number | null>(null);
  const [openIncidentsCount, setOpenIncidentsCount] = useState<number | null>(null);

  const refreshCounts = useCallback(async () => {
    try {
      const ov = await api.overview();
      setMonitorCount(ov.total_monitors);
      setOpenIncidentsCount(ov.open_incidents_count);
      if (ov.default_page_title) setSiteTitle(ov.default_page_title);
    } catch {
      // Counts remain optional metadata in sidebar.
    }
  }, []);

  useEffect(() => {
    void refreshCounts();
  }, [refreshCounts]);

  useEffect(() => {
    if (!drawerOpen) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setDrawerOpen(false);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [drawerOpen]);

  const navItems: Array<{ key: AdminTabKey; path: string; label: string; badge?: number | null }> = [
    { key: 'overview', path: '/admin', label: 'Overview' },
    { key: 'monitors', path: '/admin/monitors', label: 'Monitors', badge: monitorCount },
    {
      key: 'incidents',
      path: '/admin/incidents',
      label: 'Incidents',
      badge: openIncidentsCount !== null && openIncidentsCount > 0 ? openIncidentsCount : null,
    },
    { key: 'status-pages', path: '/admin/status-pages', label: 'Status pages' },
    { key: 'maintenance', path: '/admin/maintenance', label: 'Maintenance' },
    { key: 'notifications', path: '/admin/notifications', label: 'Notifications' },
    { key: 'settings', path: '/admin/settings', label: 'Settings' },
  ];

  const isNavActive = (key: AdminTabKey) => {
    if (key === 'monitors' && tab === 'monitor-detail') return true;
    return tab === key;
  };

  const renderSidebarBody = (closeDrawer?: () => void) => (
    <>
      <div>
        <div class="sidebar-brand">
          <PulseLogoIcon size={28} />
          <div class="sidebar-brand-text">
            <div class="sidebar-brand-name">Exa</div>
            <div class="sidebar-brand-sub">{siteTitle}</div>
          </div>
        </div>

        <nav class="sidebar-nav" aria-label="Admin sections">
          {navItems.map((item) => (
            <a
              key={item.key}
              href={item.path}
              class="sidebar-nav-item"
              aria-current={isNavActive(item.key) ? 'page' : undefined}
              onClick={(e) => {
                e.preventDefault();
                closeDrawer?.();
                onNavigate(item.path);
              }}
            >
              <span>{item.label}</span>
              {item.badge !== null && item.badge !== undefined ? (
                <span class="sidebar-count">{item.badge}</span>
              ) : null}
            </a>
          ))}
        </nav>
      </div>

      <div class="sidebar-footer">
        <a
          href="/"
          class="sidebar-nav-item"
          onClick={(e) => {
            e.preventDefault();
            closeDrawer?.();
            onNavigate('/');
          }}
        >
          <span>Public status page</span>
        </a>
        <button
          type="button"
          class="sidebar-nav-item"
          onClick={onToggleTheme}
        >
          <span>{theme === 'dark' ? 'Light theme' : 'Dark theme'}</span>
          {theme === 'dark' ? <SunIcon size={15} /> : <MoonIcon size={15} />}
        </button>
        <button
          type="button"
          class="sidebar-nav-item"
          onClick={() => {
            closeDrawer?.();
            onLogout();
          }}
        >
          <span>Sign out ({user.username})</span>
        </button>
      </div>
    </>
  );

  return (
    <div class="admin-shell">
      <a href="#admin-main" class="skip-link">
        Skip to admin console
      </a>

      <aside class="admin-sidebar" aria-label="Operations navigation">
        {renderSidebarBody()}
      </aside>

      <div class="admin-main-wrap">
        <header class="admin-mobile-bar">
          <div class="brand-group">
            <PulseLogoIcon size={26} />
            <strong>Exa</strong>
          </div>
          <button
            type="button"
            class="btn"
            aria-expanded={drawerOpen}
            aria-label="Open navigation menu"
            onClick={() => setDrawerOpen(true)}
          >
            <MenuIcon size={16} />
            <span>Menu</span>
          </button>
        </header>

        <main id="admin-main" class="admin-content">
          {!isAdmin ? (
            <div class="alert-inline">
              Signed in as viewer ({user.username}). Read-only access is active; administrator role is required for changes.
            </div>
          ) : null}

          {tab === 'overview' ? (
            <OverviewTab
              isAdmin={isAdmin}
              onNavigate={onNavigate}
              onRefreshCounts={() => void refreshCounts()}
            />
          ) : null}
          {tab === 'monitors' ? (
            <MonitorsTab
              isAdmin={isAdmin}
              initialQuery={searchQuery}
              onNavigate={onNavigate}
              onRefreshCounts={() => void refreshCounts()}
            />
          ) : null}
          {tab === 'monitor-detail' && monitorId ? (
            <MonitorDetailView
              monitorId={monitorId}
              isAdmin={isAdmin}
              onNavigate={onNavigate}
              onRefreshCounts={() => void refreshCounts()}
            />
          ) : null}
          {tab === 'incidents' ? <IncidentsTab isAdmin={isAdmin} /> : null}
          {tab === 'maintenance' ? <MaintenanceTab isAdmin={isAdmin} /> : null}
          {tab === 'status-pages' ? (
            <StatusPagesTab isAdmin={isAdmin} onNavigate={onNavigate} />
          ) : null}
          {tab === 'notifications' ? <NotificationsTab isAdmin={isAdmin} /> : null}
          {tab === 'settings' ? <SettingsTab currentUser={user} /> : null}
        </main>
      </div>

      {drawerOpen ? (
        <div
          class="admin-drawer-backdrop"
          role="presentation"
          onClick={(e) => {
            if (e.target === e.currentTarget) setDrawerOpen(false);
          }}
        >
          <div class="admin-drawer-panel" role="dialog" aria-modal="true" aria-label="Navigation menu">
            <div style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: '8px' }}>
              <button
                type="button"
                class="btn btn-icon-only"
                aria-label="Close navigation menu"
                onClick={() => setDrawerOpen(false)}
              >
                <CloseIcon size={16} />
              </button>
            </div>
            {renderSidebarBody(() => setDrawerOpen(false))}
          </div>
        </div>
      ) : null}
    </div>
  );
}
