'use client';

import {BookOpen, Layers, PencilLine, Repeat, Rocket, Zap} from 'lucide-react';
import {Button} from '@astryxdesign/core/Button';
import {Layout, LayoutContent} from '@astryxdesign/core/Layout';
import {CapabilityHero} from '../components/CapabilityHero';
import {PageHeader} from '../components/PageHeader';
import {useTranslation} from '../lib/i18n';

// Shell only - see docs/ui_backlog.md. Skills sit on top of tools, so they
// wait for the same groundwork.
export default function SkillsPage() {
  const t = useTranslation();

  return (
    <Layout
      content={
        <LayoutContent padding={0}>
          <CapabilityHero
            action={<Button isDisabled label={t('skill.add')} size="sm" variant="primary" />}
            description={t('skill.heroBody')}
            flow={[
              {icon: PencilLine, label: t('skill.flowDefine')},
              {icon: Zap, label: t('nav.skill')},
              {icon: Rocket, label: t('skill.flowDeploy')},
            ]}
            points={[
              {icon: BookOpen, title: t('skill.written'), description: t('skill.writtenBody')},
              {icon: Layers, title: t('skill.composed'), description: t('skill.composedBody')},
              {icon: Repeat, title: t('skill.reused'), description: t('skill.reusedBody')},
            ]}
            title={t('skill.hero')}
          />
        </LayoutContent>
      }
      header={<PageHeader description={t('skill.subtitle')} title={t('nav.skill')} />}
      height="fill"
    />
  );
}
