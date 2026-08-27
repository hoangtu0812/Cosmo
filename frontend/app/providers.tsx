'use client';

import Link from 'next/link';
import {Theme} from '@astryxdesign/core/theme';
import {LinkProvider} from '@astryxdesign/core/Link';
import {InternationalizationProvider} from '@astryxdesign/core/i18n';
import {neutralTheme} from '@astryxdesign/theme-neutral/built';
import viVN from '@astryxdesign/core/locales/vi-VN.json';

export function Providers({children}: {children: React.ReactNode}) {
  return (
    <InternationalizationProvider locale="vi-VN" messages={{'vi-VN': viVN}}>
      <Theme mode="light" theme={neutralTheme}>
        <LinkProvider component={Link}>{children}</LinkProvider>
      </Theme>
    </InternationalizationProvider>
  );
}
