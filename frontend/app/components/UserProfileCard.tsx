'use client';

import {Building2, Code, LogOut, SlidersHorizontal} from 'lucide-react';

import {useState} from 'react';
import {useRouter} from 'next/navigation';
import {Avatar} from '@astryxdesign/core/Avatar';
import {Card} from '@astryxdesign/core/Card';
import {Dialog, DialogHeader} from '@astryxdesign/core/Dialog';
import {HStack, Layout, LayoutContent, VStack} from '@astryxdesign/core/Layout';
import {MoreMenu} from '@astryxdesign/core/MoreMenu';
import {Selector} from '@astryxdesign/core/Selector';
import {Text} from '@astryxdesign/core/Text';
import {api, User} from '../lib/api';
import {useTranslation} from '../lib/i18n';
import {usePreferences} from '../lib/preferences';

// The same account controls belong at the bottom of every application rail.
// Keeping the menu here prevents settings, chat and knowledge from drifting
// into separate sign-out and preference experiences.
export function UserProfileCard({user}: {user: User}) {
  const t = useTranslation();
  const router = useRouter();
  const {preferences, setLocale, setTheme} = usePreferences();
  const [isPreferencesOpen, setIsPreferencesOpen] = useState(false);

  async function signOut() {
    await api.signOut();
    router.replace('/');
  }

  return (
    <>
      <Card padding={3} width="100%">
        <HStack gap={2} hAlign="between" vAlign="center">
          <Avatar name={user.name} size="md" src={user.has_avatar ? api.userAvatarURL() : undefined} />
          <VStack className="min-w-0" gap={0}>
            <Text maxLines={1} type="label">{user.name}</Text>
            <Text color="secondary" maxLines={1} type="supporting">{user.email}</Text>
          </VStack>
          <MoreMenu
            items={[
              {icon: <SlidersHorizontal size={15} />, label: t('settings.preferences'), onClick: () => setIsPreferencesOpen(true)},
              ...(user.role === 'admin' ? [{icon: <Building2 size={15} />, label: t('profile.admin'), onClick: () => router.push('/admin')}] : []),
              /* The reference offers programmatic access from here. Cosmo has
                 no key issuing yet - see docs/ui_backlog.md. */
              {icon: <Code size={15} />, isDisabled: true, label: t('profile.apiAccess')},
              {type: 'divider' as const},
              {icon: <LogOut size={15} />, label: t('menu.signOut'), onClick: () => void signOut(), variant: 'destructive'},
            ]}
            label={t('profile.options')}
            size="sm"
          />
        </HStack>
      </Card>

      <Dialog isOpen={isPreferencesOpen} onOpenChange={setIsPreferencesOpen} purpose="form">
        <Layout
          content={
            <LayoutContent>
              <VStack gap={4}>
                <HStack hAlign="between" vAlign="center">
                  <Text type="label">{t('prefs.theme')}</Text>
                  <Selector
                    isLabelHidden
                    label={t('prefs.theme')}
                    onChange={(value) => setTheme(value as 'light' | 'dark' | 'system')}
                    options={[
                      {value: 'light', label: t('prefs.themeLight')},
                      {value: 'dark', label: t('prefs.themeDark')},
                      {value: 'system', label: t('prefs.themeSystem')},
                    ]}
                    value={preferences.theme}
                  />
                </HStack>
                <HStack hAlign="between" vAlign="center">
                  <Text type="label">{t('prefs.language')}</Text>
                  <Selector
                    isLabelHidden
                    label={t('prefs.language')}
                    onChange={(value) => setLocale(value as 'en' | 'vi')}
                    options={[
                      {value: 'en', label: t('prefs.languageEn')},
                      {value: 'vi', label: t('prefs.languageVi')},
                    ]}
                    value={preferences.locale}
                  />
                </HStack>
              </VStack>
            </LayoutContent>
          }
          header={<DialogHeader onOpenChange={setIsPreferencesOpen} title={t('settings.preferences')} />}
        />
      </Dialog>
    </>
  );
}
