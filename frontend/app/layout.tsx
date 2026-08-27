import type {Metadata} from 'next';
import '@fontsource-variable/figtree';
import './globals.css';
import {Providers} from './providers';

export const metadata: Metadata = {
  title: 'Cosmo · Enterprise AI Platform',
  description: 'Nền tảng AI nội bộ an toàn và có kiểm soát cho doanh nghiệp.',
  icons: {icon: '/cosmo-logo.png'},
};

export default function RootLayout({children}: Readonly<{children: React.ReactNode}>) {
  return (
    <html data-astryx-theme="neutral" data-theme="light" lang="vi">
      <body><Providers>{children}</Providers></body>
    </html>
  );
}
