'use client';

import {Box, Building2, Check, ChevronsUpDown, Code, LogOut, Plus, SlidersHorizontal, UserRound} from 'lucide-react';

import {useState} from 'react';
import {useRouter} from 'next/navigation';
import {Avatar} from '@astryxdesign/core/Avatar';
import {Card} from '@astryxdesign/core/Card';
import {Dialog, DialogHeader} from '@astryxdesign/core/Dialog';
import {HStack, Layout, LayoutContent, VStack} from '@astryxdesign/core/Layout';
import {DropdownMenu, DropdownMenuDivider, DropdownMenuItem} from '@astryxdesign/core/DropdownMenu';
import {Selector} from '@astryxdesign/core/Selector';
import {Text} from '@astryxdesign/core/Text';
import {api, User, Workspace} from '../lib/api';
import {useTranslation} from '../lib/i18n';
import {usePreferences} from '../lib/preferences';

// The same account controls belong at the bottom of every application rail.
// Keeping the menu here prevents settings, chat and knowledge from drifting
// into separate sign-out and preference experiences.
//
// Which workspace you are in sits here too. It is the same kind of fact as who
// you are signed in as, it has to be readable from every screen - including
// chat, which has no second column to put it in - and the two questions are
// answered by one card rather than by two controls at opposite ends of the
// rail.
export function UserProfileCard({user, workspace = null, workspaces = [], onSwitchWorkspace, onCreateWorkspace}: {
  user: User;
  // Absent on the screens that run outside the workspace frame - the admin
  // console among them - where there is no workspace to be in and the section
  // is simply not drawn.
  workspace?: Workspace | null;
  workspaces?: Workspace[];
  onSwitchWorkspace?: (next: Workspace) => void;
  onCreateWorkspace?: () => void;
}) {
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
          {/* Your name identifies the card; the workspace under it is the one
              thing about your situation that changes as you work. The address
              moves into the menu, where it is read once. */}
          <VStack className="min-w-0" gap={0}>
            <Text maxLines={1} type="label">{user.name}</Text>
            <Text color="secondary" maxLines={1} type="supporting">{workspace?.name ?? user.email}</Text>
          </VStack>
          <DropdownMenu
            alignment="end"
            button={{icon: <ChevronsUpDown size={15} />, isIconOnly: true, label: t('profile.options'), size: 'sm', variant: 'ghost'}}
            hasChevron={false}
          >
            <HStack paddingBlock={1} paddingInline={2}>
              <Text color="secondary" maxLines={1} type="supporting">{user.email}</Text>
            </HStack>
            <DropdownMenuDivider />

            {workspaces.length > 0 ? (
              <>
                <HStack paddingBlock={1} paddingInline={2}>
                  <Text color="secondary" type="supporting">{t('menu.switchWorkspace')}</Text>
                </HStack>
                {workspaces.map((item) => (
                  <DropdownMenuItem
                    endContent={item.id === workspace?.id ? <Check size={14} /> : undefined}
                    icon={item.type === 'personal' ? <UserRound size={15} /> : <Building2 size={15} />}
                    key={item.id}
                    label={item.name}
                    onClick={() => onSwitchWorkspace?.(item)}
                  />
                ))}
                <DropdownMenuItem icon={<Plus size={15} />} label={t('menu.createWorkspace')} onClick={() => onCreateWorkspace?.()} />
                {/* Joining someone else's workspace needs an invite flow we
                    have not built - see docs/ui_backlog.md. */}
                <DropdownMenuItem icon={<Box size={15} />} isDisabled label={t('menu.joinWorkspace')} />
                <DropdownMenuDivider />
              </>
            ) : null}

            <DropdownMenuItem icon={<SlidersHorizontal size={15} />} label={t('settings.preferences')} onClick={() => setIsPreferencesOpen(true)} />
            {user.role === 'admin' ? (
              <DropdownMenuItem icon={<Building2 size={15} />} label={t('profile.admin')} onClick={() => router.push('/admin')} />
            ) : null}
            {/* The reference offers programmatic access from here. Cosmo has
                no key issuing yet - see docs/ui_backlog.md. */}
            <DropdownMenuItem icon={<Code size={15} />} isDisabled label={t('profile.apiAccess')} />
            <DropdownMenuDivider />
            <DropdownMenuItem icon={<LogOut size={15} />} label={t('menu.signOut')} onClick={() => void signOut()} variant="destructive" />
          </DropdownMenu>
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
