import { useEffect, useState } from 'preact/hooks';
import type { PageSummary } from '../types';
import type { ThemeMode } from '../utils/theme';
import { BellIcon, CloseIcon, MenuIcon, MoonIcon, PulseLogoIcon, SunIcon } from './Icons';

interface HeaderProps {
  title?: string;
  currentSlug?: string;
  otherPages?: PageSummary[];
  onNavigate: (path: string) => void;
  onOpenSubscribe?: () => void;
  theme: ThemeMode;
  onToggleTheme: () => void;
  showAdminLink?: boolean;
}

export function Header({
  title,
  currentSlug,
  otherPages = [],
  onNavigate,
  onOpenSubscribe,
  theme,
  onToggleTheme,
  showAdminLink = false,
}: HeaderProps) {
  const [menuOpen, setMenuOpen] = useState(false);

  const allPages =
    otherPages.length > 0 && currentSlug
      ? [{ slug: currentSlug, title: title || currentSlug }, ...otherPages]
      : [];

  useEffect(() => {
    if (!menuOpen) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setMenuOpen(false);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [menuOpen]);

  return (
    <header class="public-header">
      <div class="container-public">
        <div class="public-header-inner">
          <div class="brand-group">
            <a
              href="/"
              class="brand-wordmark"
              onClick={(e) => {
                e.preventDefault();
                onNavigate('/');
              }}
            >
              <PulseLogoIcon size={28} />
              <span class="brand-page-title">{title || 'Exa Status'}</span>
            </a>
          </div>

          <div class="header-actions header-actions--desktop">
            {allPages.length > 1 ? (
              <select
                class="page-switcher"
                aria-label="Switch status page"
                value={currentSlug}
                onChange={(e) => {
                  const slug = (e.currentTarget as HTMLSelectElement).value;
                  onNavigate(`/s/${encodeURIComponent(slug)}`);
                }}
              >
                {allPages.map((p) => (
                  <option key={p.slug} value={p.slug}>
                    {p.title}
                  </option>
                ))}
              </select>
            ) : null}

            {onOpenSubscribe ? (
              <button type="button" class="btn btn-primary btn-subscribe" onClick={onOpenSubscribe}>
                Subscribe to updates
              </button>
            ) : null}

            {showAdminLink ? (
              <>
                <button
                  type="button"
                  class="btn btn-icon-only"
                  onClick={onToggleTheme}
                  aria-label={theme === 'dark' ? 'Switch to light theme' : 'Switch to dark theme'}
                >
                  {theme === 'dark' ? <SunIcon size={16} /> : <MoonIcon size={16} />}
                </button>
                <a
                  href="/admin"
                  class="btn"
                  onClick={(e) => {
                    e.preventDefault();
                    onNavigate('/admin');
                  }}
                >
                  Admin Console
                </a>
              </>
            ) : null}
          </div>

          <div class="header-actions mobile-menu-toggle">
            <button
              type="button"
              class="btn"
              aria-expanded={menuOpen}
              aria-controls="mobile-nav-panel"
              onClick={() => setMenuOpen((o) => !o)}
            >
              {menuOpen ? <CloseIcon size={16} /> : <MenuIcon size={16} />}
              <span>Menu</span>
            </button>
          </div>
        </div>

        {menuOpen ? (
          <div id="mobile-nav-panel" class="mobile-menu-panel">
            {allPages.length > 1 ? (
              <label class="form-field">
                <span class="form-label">Status page</span>
                <select
                  class="select"
                  value={currentSlug}
                  onChange={(e) => {
                    const slug = (e.currentTarget as HTMLSelectElement).value;
                    setMenuOpen(false);
                    onNavigate(`/s/${encodeURIComponent(slug)}`);
                  }}
                >
                  {allPages.map((p) => (
                    <option key={p.slug} value={p.slug}>
                      {p.title}
                    </option>
                  ))}
                </select>
              </label>
            ) : null}

            {onOpenSubscribe ? (
              <button
                type="button"
                class="btn btn-primary btn-subscribe"
                onClick={() => {
                  setMenuOpen(false);
                  onOpenSubscribe();
                }}
              >
                <BellIcon size={15} />
                <span>Subscribe to updates</span>
              </button>
            ) : null}

            <button type="button" class="btn" onClick={onToggleTheme}>
              {theme === 'dark' ? <SunIcon size={16} /> : <MoonIcon size={16} />}
              <span>{theme === 'dark' ? 'Use light theme' : 'Use dark theme'}</span>
            </button>

            <a
              href="/admin"
              class="btn"
              onClick={(e) => {
                e.preventDefault();
                setMenuOpen(false);
                onNavigate('/admin');
              }}
            >
              Admin Console
            </a>
          </div>
        ) : null}
      </div>
    </header>
  );
}
