import type {Metadata} from 'next';
import '@fontsource/be-vietnam-pro/400.css';
import '@fontsource/be-vietnam-pro/500.css';
import '@fontsource/be-vietnam-pro/600.css';
import '@fontsource/be-vietnam-pro/700.css';
import './globals.css';
import {Providers} from './providers';
import {WorkspaceFrame} from './components/WorkspaceFrame';

export const metadata: Metadata = {
  title: 'Cosmo · Enterprise AI Platform',
  description: 'Nền tảng AI nội bộ an toàn và có kiểm soát cho doanh nghiệp.',
  icons: {icon: '/cosmo-logo.png'},
};

export default function RootLayout({children}: Readonly<{children: React.ReactNode}>) {
  return (
    <html data-astryx-theme="cosmo" data-theme="light" lang="vi">
      <body><Providers><WorkspaceFrame>{children}</WorkspaceFrame></Providers></body>
    </html>
  );
}
