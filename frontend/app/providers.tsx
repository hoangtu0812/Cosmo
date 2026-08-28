'use client';

import Link from 'next/link';
import {Theme} from '@astryxdesign/core/theme';
import {LinkProvider} from '@astryxdesign/core/Link';
import {InternationalizationProvider} from '@astryxdesign/core/i18n';
import viVN from '@astryxdesign/core/locales/vi-VN.json';
import {cosmoTheme} from './theme/cosmo';
import {PreferencesProvider, usePreferences, useResolvedTheme} from './lib/preferences';

/**
 * Theme mode and Astryx's own locale both follow the stored preference, so the
 * component strings (Required, Select…, Search…) match the app strings.
 */
function ThemedApp({children}: {children: React.ReactNode}) {
  const {preferences} = usePreferences();
  const mode = useResolvedTheme();
  const locale = preferences.locale === 'vi' ? 'vi-VN' : 'en';

  return (
    <InternationalizationProvider locale={locale} messages={{'vi-VN': viVN}}>
      <Theme mode={mode} theme={cosmoTheme}>
        <LinkProvider component={Link}>{children}</LinkProvider>
      </Theme>
    </InternationalizationProvider>
  );
}

export function Providers({children}: {children: React.ReactNode}) {
  return (
    <PreferencesProvider>
      <ThemedApp>{children}</ThemedApp>
    </PreferencesProvider>
  );
}
