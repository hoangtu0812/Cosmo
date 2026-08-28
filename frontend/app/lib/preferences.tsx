'use client';

import {createContext, useCallback, useContext, useMemo, useState, useSyncExternalStore} from 'react';

export type Locale = 'en' | 'vi';
export type ThemeMode = 'light' | 'dark' | 'system';

const STORAGE_KEY = 'cosmo.preferences';

type Preferences = {locale: Locale; theme: ThemeMode};

// English is the default so the product reads the same for every reader until
// someone opts into Vietnamese.
const DEFAULTS: Preferences = {locale: 'en', theme: 'light'};

function read(): Preferences {
  if (typeof window === 'undefined') return DEFAULTS;
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    if (!raw) return DEFAULTS;
    const parsed = JSON.parse(raw) as Partial<Preferences>;
    return {
      locale: parsed.locale === 'vi' ? 'vi' : 'en',
      theme: parsed.theme === 'dark' || parsed.theme === 'system' ? parsed.theme : 'light',
    };
  } catch {
    return DEFAULTS;
  }
}

const PreferencesContext = createContext<{
  preferences: Preferences;
  setLocale: (locale: Locale) => void;
  setTheme: (theme: ThemeMode) => void;
}>({preferences: DEFAULTS, setLocale: () => undefined, setTheme: () => undefined});

export function PreferencesProvider({children}: {children: React.ReactNode}) {
  // Read once during render rather than syncing state inside an effect. The
  // server render always sees the defaults, which is why the html element
  // carries data-theme="light" in the root layout.
  const [preferences, setPreferences] = useState<Preferences>(read);

  const persist = useCallback((next: Preferences) => {
    setPreferences(next);
    try {
      window.localStorage.setItem(STORAGE_KEY, JSON.stringify(next));
    } catch {
      // A browser with storage disabled still gets the change for this session.
    }
    // Astryx drives browser chrome (scrollbars, native controls) from the
    // documentElement attribute, so keep it in step with the provider.
    const resolved = next.theme === 'system'
      ? (window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light')
      : next.theme;
    document.documentElement.setAttribute('data-theme', resolved);
  }, []);

  const value = useMemo(() => ({
    preferences,
    setLocale: (locale: Locale) => persist({...preferences, locale}),
    setTheme: (theme: ThemeMode) => persist({...preferences, theme}),
  }), [persist, preferences]);

  return <PreferencesContext.Provider value={value}>{children}</PreferencesContext.Provider>;
}

export function usePreferences() {
  return useContext(PreferencesContext);
}

function subscribeSystemTheme(onChange: () => void) {
  const query = window.matchMedia('(prefers-color-scheme: dark)');
  query.addEventListener('change', onChange);
  return () => query.removeEventListener('change', onChange);
}

/** The concrete light/dark value to hand Astryx's Theme. */
export function useResolvedTheme(): 'light' | 'dark' {
  const {preferences} = usePreferences();
  const systemPrefersDark = useSyncExternalStore(
    subscribeSystemTheme,
    () => window.matchMedia('(prefers-color-scheme: dark)').matches,
    () => false,
  );
  if (preferences.theme === 'system') return systemPrefersDark ? 'dark' : 'light';
  return preferences.theme;
}
