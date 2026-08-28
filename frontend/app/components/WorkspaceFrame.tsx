'use client';

import {useEffect, useState} from 'react';
import {usePathname, useRouter, useSearchParams} from 'next/navigation';
import {Building2, Check, Library, MessageSquare, Settings, SquarePen, UserPlus, UserRound} from 'lucide-react';
import {AppShell} from '@astryxdesign/core/AppShell';
import {Avatar} from '@astryxdesign/core/Avatar';
import {DropdownMenuDivider, DropdownMenuItem} from '@astryxdesign/core/DropdownMenu';
import {EmptyState} from '@astryxdesign/core/EmptyState';
import {Icon} from '@astryxdesign/core/Icon';
import {SideNav, SideNavHeading, SideNavItem, SideNavSection} from '@astryxdesign/core/SideNav';
import {api, Conversation, User, Workspace} from '../lib/api';
import {useTranslation} from '../lib/i18n';
import {UserProfileCard} from './UserProfileCard';

// Chat and Knowledge are one workspace application. Keeping the frame above
// their route content lets navigation replace only that content; the workspace
// context, conversation list and profile card never turn into a second page.
export function WorkspaceFrame({children}: {children: React.ReactNode}) {
  const pathname = usePathname();
  if (pathname !== '/chat' && !pathname.startsWith('/knowledge')) return <>{children}</>;
  return <WorkspaceShell>{children}</WorkspaceShell>;
}

function WorkspaceShell({children}: {children: React.ReactNode}) {
  const t = useTranslation();
  const pathname = usePathname();
  const router = useRouter();
  const search = useSearchParams();
  const requestedWorkspaceID = search.get('workspace') ?? '';
  const [user, setUser] = useState<User | null>(null);
  const [workspace, setWorkspace] = useState<Workspace | null>(null);
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [conversations, setConversations] = useState<Conversation[]>([]);

  useEffect(() => {
    let cancelled = false;
    Promise.all([api.me(), api.workspaces()]).then(async ([me, result]) => {
      const workspaceID = requestedWorkspaceID || me.user.last_workspace_id || result.workspaces[0]?.id || '';
      const selected = result.workspaces.find((item) => item.id === workspaceID) ?? null;
      if (!selected) return;
      await api.selectWorkspace(selected.id);
      const history = await api.conversations(selected.id);
      if (cancelled) return;
      setUser(me.user);
      setWorkspaces(result.workspaces);
      setWorkspace(selected);
      setConversations(history.conversations);
    }).catch(() => undefined);
    return () => { cancelled = true; };
  }, [requestedWorkspaceID]);

  function goTo(path: string) {
    const workspaceQuery = workspace ? `?workspace=${encodeURIComponent(workspace.id)}` : '';
    const newConversation = path === '/chat' ? `${workspaceQuery ? '&' : '?'}conversation=new` : '';
    router.push(`${path}${workspaceQuery}${newConversation}`);
  }

  async function switchWorkspace(next: Workspace) {
    if (next.id === workspace?.id) return;
    await api.selectWorkspace(next.id);
    const target = pathname.startsWith('/knowledge') ? '/knowledge' : '/chat';
    router.push(`${target}?workspace=${encodeURIComponent(next.id)}`);
  }

  const workspaceMenu = (
    <>
      {workspaces.map((item) => (
        <DropdownMenuItem
          endContent={item.id === workspace?.id ? <Check size={14} /> : undefined}
          icon={item.type === 'personal' ? <UserRound size={15} /> : <Building2 size={15} />}
          key={item.id}
          label={item.name}
          onClick={() => void switchWorkspace(item)}
        />
      ))}
      <DropdownMenuDivider />
      <DropdownMenuItem icon={<Settings size={15} />} label={t('menu.settings')} onClick={() => router.push('/settings')} />
      <DropdownMenuItem icon={<UserPlus size={15} />} label={t('menu.invite')} onClick={() => router.push('/settings?section=members')} />
    </>
  );

  return (
    <AppShell
      contentPadding={0}
      variant="elevated"
      sideNav={
        <SideNav
          footer={user ? <UserProfileCard user={user} /> : undefined}
          header={<SideNavHeading heading={workspace?.name ?? t('chat.loading')} icon={<Avatar name={workspace?.icon || workspace?.name || 'Cosmo'} size="sm" src={workspace?.has_icon_image ? api.workspaceIconURL(workspace.id) : undefined} />} menu={workspaceMenu} subheading={user?.email} />}
        >
          <SideNavSection isHeaderHidden title={t('chat.actions')}>
            <SideNavItem icon={<Icon icon={SquarePen} size="sm" />} isSelected={pathname === '/chat' && search.get('conversation') === 'new'} label={t('chat.newChat')} onClick={() => goTo('/chat')} />
            <SideNavItem icon={<Icon icon={Library} size="sm" />} isSelected={pathname.startsWith('/knowledge')} label={t('kb.title')} onClick={() => goTo('/knowledge')} />
          </SideNavSection>
          <SideNavSection title={t('chat.recent')}>
            {conversations.map((item) => (
              <SideNavItem
                icon={<Icon icon={MessageSquare} size="sm" />}
                isSelected={pathname === '/chat' && search.get('conversation') === item.id}
                key={item.id}
                label={item.title}
                onClick={() => router.push(`/chat?workspace=${encodeURIComponent(workspace?.id ?? item.workspace_id)}&conversation=${encodeURIComponent(item.id)}`)}
              />
            ))}
          </SideNavSection>
          {conversations.length === 0 && <EmptyState description={t('chat.empty')} isCompact title="—" />}
        </SideNav>
      }
    >
      {children}
    </AppShell>
  );
}
