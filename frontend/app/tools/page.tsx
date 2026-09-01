'use client';

import {Blocks, Code2, Plug, Wrench} from 'lucide-react';
import {Button} from '@astryxdesign/core/Button';
import {Layout, LayoutContent} from '@astryxdesign/core/Layout';
import {CapabilityHero} from '../components/CapabilityHero';
import {PageHeader} from '../components/PageHeader';
import {useTranslation} from '../lib/i18n';

// Shell only - see docs/ui_backlog.md. Tools need the credential vault and the
// egress allowlist before anything here can call out.
export default function ToolsPage() {
  const t = useTranslation();

  return (
    <Layout
      content={
        <LayoutContent padding={0}>
          <CapabilityHero
            action={<Button isDisabled label={t('tool.add')} size="sm" variant="primary" />}
            description={t('tool.heroBody')}
            icon={Wrench}
            points={[
              {icon: Plug, title: t('tool.mcp'), description: t('tool.mcpBody')},
              {icon: Blocks, title: t('tool.catalogue'), description: t('tool.catalogueBody')},
              {icon: Code2, title: t('tool.custom'), description: t('tool.customBody')},
            ]}
            title={t('tool.hero')}
          />
        </LayoutContent>
      }
      header={<PageHeader description={t('tool.subtitle')} title={t('nav.tool')} />}
      height="fill"
    />
  );
}
