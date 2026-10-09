export type Theme = 'light' | 'dark';

const storageKey = 'yingxu-theme';
const listeners = new Set<() => void>();
let preference: Theme | null = null;
let currentTheme: Theme = 'light';
let initialized = false;
let systemTheme: MediaQueryList | undefined;

function applyTheme() {
  const next = preference || (systemTheme?.matches ? 'dark' : 'light');
  document.documentElement.dataset.theme = next;
  document.documentElement.style.colorScheme = next;
  if (next !== currentTheme) {
    currentTheme = next;
    listeners.forEach(listener => listener());
  }
}

export function initializeTheme() {
  if (initialized || typeof window === 'undefined') return;
  initialized = true;
  try {
    const stored = window.localStorage.getItem(storageKey);
    if (stored === 'light' || stored === 'dark') preference = stored;
  } catch {
    preference = null;
  }
  systemTheme = window.matchMedia('(prefers-color-scheme: dark)');
  systemTheme.addEventListener('change', applyTheme);
  window.addEventListener('storage', event => {
    if (event.key !== storageKey && event.key !== null) return;
    preference = event.newValue === 'light' || event.newValue === 'dark' ? event.newValue : null;
    applyTheme();
  });
  applyTheme();
}

export function getTheme(): Theme {
  initializeTheme();
  return currentTheme;
}

export function setTheme(theme: Theme) {
  initializeTheme();
  preference = theme;
  try {
    window.localStorage.setItem(storageKey, theme);
  } catch {
    preference = theme;
  }
  applyTheme();
}

export function subscribeTheme(listener: () => void) {
  initializeTheme();
  listeners.add(listener);
  return () => listeners.delete(listener);
}

initializeTheme();
