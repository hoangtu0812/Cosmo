'use client';

import {Suspense, useEffect, useState} from 'react';
import {usePathname, useRouter, useSearchParams} from 'next/navigation';
import {Archive, BarChart3, Bell, Bookmark, Bot, Box, Building2, Check, Clock, FolderKanban, Library, MessageSquare, Search, Settings, SquarePen, UserPlus, UserRound, Workflow, Wrench, Zap} from 'lucide-react';
import {AppShell} from '@astryxdesign/core/AppShell';
import {Avatar} from '@astryxdesign/core/Avatar';
import {DropdownMenuDivider, DropdownMenuItem} from '@astryxdesign/core/DropdownMenu';
import {Icon} from '@astryxdesign/core/Icon';
import {HStack} from '@astryxdesign/core/Layout';
import {SideNav, SideNavHeading, SideNavItem, SideNavSection} from '@astryxdesign/core/SideNav';
import {api, User, Workspace} from '../lib/api';
import {useTranslation} from '../lib/i18n';
import {ChatTargetFilters, ChatTargetList, useChatTargets} from './ChatTargetNav';
import {Token} from '@astryxdesign/core/Token';
import {UserProfileCard} from './UserProfileCard';

// The areas that live inside the workspace application. Anything outside it -
// signing in, accepting an invite - is its own page and gets no rail.
// The areas the second column lists when you are inside the workspace.
const WORKSPACE_ROUTES = ['/agents', '/knowledge', '/workflow', '/tools', '/skills', '/observability', '/settings'];

const FRAMED_ROUTES = ['/chat', '/knowledge', '/agents', '/projects', '/schedule', '/library', '/notifications', '/workflow', '/tools', '/skills', '/observability', '/settings'];

// Chat and Knowledge are one workspace application. Keeping the frame above
// their route content lets navigation replace only that content; the workspace
// context, conversation list and profile card never turn into a second page.
export function WorkspaceFrame({children}: {children: React.ReactNode}) {
  const pathname = usePathname();
  if (!FRAMED_ROUTES.some((route) => pathname === route || pathname.startsWith(`${route}/`))) return <>{children}</>;
  // The shell and every page it frames read the request URL through
  // useSearchParams, which a prerender has no answer for. One boundary here
  // covers the shell and its children together.
  return (
    <Suspense fallback={null}>
      <WorkspaceShell>{children}</WorkspaceShell>
    </Suspense>
  );
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

  useEffect(() => {
    let cancelled = false;
    // Who you are and which workspace you are in is what the whole frame is
    // drawn from, so it is set as soon as it is known. The conversation list
    // follows separately: it is one section of one column, and it failing
    // used to leave the entire shell saying "loading" forever.
    Promise.all([api.me(), api.workspaces()]).then(async ([me, result]) => {
      if (cancelled) return;
      const workspaceID = requestedWorkspaceID || me.user.last_workspace_id || result.workspaces[0]?.id || '';
      const selected = result.workspaces.find((item) => item.id === workspaceID) ?? null;
      setUser(me.user);
      setWorkspaces(result.workspaces);
      setWorkspace(selected);
      if (!selected) return;
      await api.selectWorkspace(selected.id).catch(() => undefined);
    }).catch(() => undefined);
    return () => { cancelled = true; };
  }, [requestedWorkspaceID]);

  // Every framed page reads its workspace from the URL, but a direct load has
  // no such parameter and the frame is what resolves it. Writing it back means
  // a page that loaded without one refetches against the right workspace
  // instead of sitting on an empty list it never retries.
  useEffect(() => {
    if (!workspace || search.get('workspace') === workspace.id) return;
    const params = new URLSearchParams(search.toString());
    params.set('workspace', workspace.id);
    router.replace(`${pathname}?${params.toString()}`);
  }, [workspace, search, pathname, router]);

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

  // The agent editor is a focused surface, so it runs without the rail while
  // keeping everything else the frame does - resolving the workspace above all.
  const isChatRoute = pathname === '/chat';
  const isLibraryRoute = pathname === '/library';
  const hasSecondColumn = !['/projects', '/schedule', '/notifications'].includes(pathname);
  const chatTargets = useChatTargets(workspace);
  const isFocusedRoute = /^\/agents\/[^/]+$/.test(pathname);

  return (
    <AppShell
      contentPadding={0}
      variant="elevated"
      sideNav={isFocusedRoute ? undefined :
        /* Two columns, as the reference has them: the outer rail is the
           product's top-level areas and stays the same wherever you are; the
           inner one is what the current workspace holds. Built from two
           SideNavs side by side rather than hand-rolled layout. */
        <HStack gap={0} height="100%">
          <SideNav
            className="w-60"
            footer={
              <>
                {/* The workspace's own areas live in the second column, which
                    chat replaces with its list of things to talk to. Without a
                    way back in, agents and knowledge became unreachable from
                    chat - the reference keeps this entry at the foot of the
                    rail for exactly that reason. */}
                <SideNavSection isHeaderHidden title={t('nav.workspaceArea')}>
                  <SideNavItem
                    icon={<Icon icon={Box} size="sm" />}
                    isSelected={WORKSPACE_ROUTES.some((route) => pathname.startsWith(route))}
                    label={t('nav.workspaceArea')}
                    onClick={() => goTo('/agents')}
                    size="md"
                  />
                </SideNavSection>
                {user ? <UserProfileCard user={user} /> : null}
              </>
            }
            header={<SideNavHeading heading="Cosmo" icon={<Icon icon={Bot} size="sm" />} />}
          >
            <SideNavSection isHeaderHidden title={t('nav.product')}>
              <SideNavItem icon={<Icon icon={MessageSquare} size="sm" />} isSelected={pathname === '/chat'} label={t('nav.chat')} size="md" onClick={() => goTo('/chat')} />
              <SideNavItem icon={<Icon icon={FolderKanban} size="sm" />} isSelected={pathname === '/projects'} label={t('nav.projects')} size="md" onClick={() => goTo('/projects')} />
              <SideNavItem icon={<Icon icon={Clock} size="sm" />} isSelected={pathname === '/schedule'} label={t('nav.schedule')} size="md" onClick={() => goTo('/schedule')} />
              <SideNavItem icon={<Icon icon={Archive} size="sm" />} isSelected={pathname === '/library'} label={t('nav.library')} size="md" onClick={() => goTo('/library')} />
              <SideNavItem icon={<Icon icon={Bell} size="sm" />} isSelected={pathname === '/notifications'} label={t('nav.notification')} size="md" onClick={() => goTo('/notifications')} />
            </SideNavSection>
          </SideNav>

          {hasSecondColumn ? (
          <SideNav
            /* The column is wider where it lists things to choose between and
               narrower where it is only a menu, as the reference has it. */
            className={isChatRoute || isLibraryRoute ? 'w-76' : 'w-50'}
            footer={isChatRoute || isLibraryRoute ? undefined : (
              <SideNavSection isHeaderHidden title={t('nav.operate')}>
                <SideNavItem icon={<Icon icon={BarChart3} size="sm" />} isSelected={pathname === '/observability'} label={t('nav.observability')} onClick={() => goTo('/observability')} />
                <SideNavItem icon={<Icon icon={Settings} size="sm" />} isSelected={pathname === '/settings'} label={t('nav.manageWorkspace')} onClick={() => goTo('/settings')} />
              </SideNavSection>
            )}
            topContent={isChatRoute ? <ChatTargetFilters t={t} targets={chatTargets} /> : undefined}
            header={isChatRoute || isLibraryRoute
              ? <SideNavHeading heading={isLibraryRoute ? t('nav.library') : t('nav.chat')} />
              : <SideNavHeading heading={workspace?.name ?? t('chat.loading')} icon={<Avatar name={workspace?.icon || workspace?.name || 'Cosmo'} size="sm" src={workspace?.has_icon_image ? api.workspaceIconURL(workspace.id) : undefined} />} menu={workspaceMenu} />}
          >
          {isChatRoute || isLibraryRoute ? null : (
            <SideNavSection isHeaderHidden title={t('chat.actions')}>
              <SideNavItem icon={<Icon icon={SquarePen} size="sm" />} isSelected={pathname === '/chat' && search.get('conversation') === 'new'} label={t('chat.newChat')} onClick={() => goTo('/chat')} />
            </SideNavSection>
          )}
          {/* Chat asks a different question of this column: not where to go,
              but who to ask. The workspace sections step aside for the list of
              agents and models, which is how the reference arranges it. */}
          {isLibraryRoute ? (
            /* The library's own sections. Shells for now - see
               docs/ui_backlog.md. */
            <SideNavSection isHeaderHidden title={t('nav.library')}>
              <SideNavItem icon={<Icon icon={Search} size="sm" />} isDisabled label={t('library.search')} size="lg" />
              <SideNavItem endContent={<Token label={t('library.internal')} size="sm" />} icon={<Icon icon={Archive} size="sm" />} isDisabled label={t('library.shared')} size="lg" />
              <SideNavItem icon={<Icon icon={Bookmark} size="sm" />} isDisabled label={t('library.collection')} size="lg" />
            </SideNavSection>
          ) : isChatRoute ? (
            <ChatTargetList
              activeTarget={search.get('target') ?? ''}
              onPick={(target) => router.push(`/chat?workspace=${encodeURIComponent(workspace?.id ?? '')}&conversation=new&target=${encodeURIComponent(target)}`)}
              t={t}
              targets={chatTargets}
              workspace={workspace}
            />
          ) : (<>
          {/* The sections mirror the reference's information architecture, so
              the shape of the product is visible before every part of it
              exists. An item with no feature behind it is disabled rather than
              hidden: it says what is coming without pretending to work.
              What is stubbed is tracked in docs/ui_backlog.md. */}
          <SideNavSection isHeaderHidden title={t('nav.workspace')}>
            <SideNavItem icon={<Icon icon={Bot} size="sm" />} isSelected={pathname.startsWith('/agents')} label={t('agent.title')} onClick={() => goTo('/agents')} />
            <SideNavItem icon={<Icon icon={Workflow} size="sm" />} isSelected={pathname === '/workflow'} label={t('nav.workflow')} onClick={() => goTo('/workflow')} />
          </SideNavSection>
          <SideNavSection title={t('nav.capabilities')}>
            <SideNavItem icon={<Icon icon={Wrench} size="sm" />} isSelected={pathname === '/tools'} label={t('nav.tool')} onClick={() => goTo('/tools')} />
            <SideNavItem icon={<Icon icon={Zap} size="sm" />} isSelected={pathname === '/skills'} label={t('nav.skill')} onClick={() => goTo('/skills')} />
          </SideNavSection>
          <SideNavSection title={t('nav.data')}>
            <SideNavItem icon={<Icon icon={Library} size="sm" />} isSelected={pathname.startsWith('/knowledge')} label={t('kb.title')} onClick={() => goTo('/knowledge')} />
          </SideNavSection>
          </>)}
          </SideNav>
          ) : null}
        </HStack>
      }
    >
      {children}
    </AppShell>
  );
}
