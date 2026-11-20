import { useEffect, useState } from 'preact/hooks';

export type ThemeMode = 'light' | 'dark';
export type ThemeSurface = 'public' | 'admin';

function readStoredTheme(surface: ThemeSurface): ThemeMode {
  const key = surface === 'admin' ? 'exa_theme_admin' : 'exa_theme_public';
  const fallback: ThemeMode = surface === 'admin' ? 'dark' : 'light';
  try {
    const saved = localStorage.getItem(key);
    if (saved === 'dark' || saved === 'light') return saved;
  } catch {
    // Storage may be unavailable in restricted contexts.
  }
  return fallback;
}

export function useTheme(surface: ThemeSurface): [ThemeMode, () => void] {
  const [theme, setTheme] = useState<ThemeMode>(() => readStoredTheme(surface));

  useEffect(() => {
    setTheme(readStoredTheme(surface));
  }, [surface]);

  useEffect(() => {
    document.documentElement.setAttribute('data-theme', theme);
    const key = surface === 'admin' ? 'exa_theme_admin' : 'exa_theme_public';
    try {
      localStorage.setItem(key, theme);
    } catch {
      // Storage may be unavailable in restricted contexts.
    }
  }, [theme, surface]);

  const toggle = () => setTheme((t) => (t === 'dark' ? 'light' : 'dark'));
  return [theme, toggle];
}
