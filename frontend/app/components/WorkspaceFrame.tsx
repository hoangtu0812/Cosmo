'use client';

import {Suspense, useEffect, useState} from 'react';
import {usePathname, useRouter, useSearchParams} from 'next/navigation';
import {Archive, BarChart3, Bell, Bookmark, Bot, Building2, Check, Clock, FolderKanban, Library, MessageSquare, MoreHorizontal, Search, Settings, SquarePen, Trash2, UserPlus, UserRound, Workflow, Wrench, Zap} from 'lucide-react';
import {AlertDialog} from '@astryxdesign/core/AlertDialog';
import {AppShell} from '@astryxdesign/core/AppShell';
import {Avatar} from '@astryxdesign/core/Avatar';
import {DropdownMenu, DropdownMenuDivider, DropdownMenuItem} from '@astryxdesign/core/DropdownMenu';
import {EmptyState} from '@astryxdesign/core/EmptyState';
import {Icon} from '@astryxdesign/core/Icon';
import {HStack} from '@astryxdesign/core/Layout';
import {SideNav, SideNavHeading, SideNavItem, SideNavSection} from '@astryxdesign/core/SideNav';
import {api, Conversation, User, Workspace} from '../lib/api';
import {useTranslation} from '../lib/i18n';
import {ChatTargetFilters, ChatTargetList, useChatTargets} from './ChatTargetNav';
import {Token} from '@astryxdesign/core/Token';
import {UserProfileCard} from './UserProfileCard';

// The areas that live inside the workspace application. Anything outside it -
// signing in, accepting an invite - is its own page and gets no rail.
const FRAMED_ROUTES = ['/chat', '/knowledge', '/agents', '/projects', '/schedule', '/library', '/notifications', '/workflow', '/tools', '/skills', '/observability'];

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
  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [deleting, setDeleting] = useState<Conversation | null>(null);
  const [isDeleteBusy, setIsDeleteBusy] = useState(false);

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

  // Deleting the conversation being read leaves nothing to show, so the frame
  // moves to a new chat rather than a dead conversation id.
  async function deleteConversation() {
    if (!deleting) return;
    setIsDeleteBusy(true);
    try {
      await api.deleteConversation(deleting.id);
      setConversations((current) => current.filter((item) => item.id !== deleting.id));
      if (search.get('conversation') === deleting.id) goTo('/chat');
      setDeleting(null);
    } catch {
      setDeleting(null);
    } finally {
      setIsDeleteBusy(false);
    }
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
            footer={user ? <UserProfileCard user={user} /> : undefined}
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
            className="w-76"
            footer={isChatRoute || isLibraryRoute ? undefined : (
              <SideNavSection isHeaderHidden title={t('nav.operate')}>
                <SideNavItem icon={<Icon icon={BarChart3} size="sm" />} isSelected={pathname === '/observability'} label={t('nav.observability')} onClick={() => goTo('/observability')} />
                <SideNavItem icon={<Icon icon={Settings} size="sm" />} isSelected={pathname === '/settings'} label={t('nav.manageWorkspace')} onClick={() => goTo('/settings')} />
              </SideNavSection>
            )}
            topContent={isChatRoute ? <ChatTargetFilters t={t} targets={chatTargets} /> : undefined}
            header={isChatRoute || isLibraryRoute
              ? <SideNavHeading heading={isLibraryRoute ? t('nav.library') : t('nav.chat')} />
              : <SideNavHeading heading={workspace?.name ?? t('chat.loading')} icon={<Avatar name={workspace?.icon || workspace?.name || 'Cosmo'} size="sm" src={workspace?.has_icon_image ? api.workspaceIconURL(workspace.id) : undefined} />} menu={workspaceMenu} subheading={user?.email} />}
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
          {isChatRoute ? (<>
          <SideNavSection title={t('chat.recent')}>
            {conversations.map((item) => (
              <SideNavItem
                endContent={
                  <DropdownMenu
                    alignment="end"
                    button={{icon: <MoreHorizontal size={15} />, isIconOnly: true, label: t('conv.options'), size: 'sm', variant: 'ghost'}}
                    hasChevron={false}
                    items={[{icon: <Trash2 size={15} />, label: t('conv.delete'), onClick: () => setDeleting(item), variant: 'destructive'}]}
                  />
                }
                icon={<Icon icon={MessageSquare} size="sm" />}
                isSelected={pathname === '/chat' && search.get('conversation') === item.id}
                key={item.id}
                label={item.title}
                onClick={() => router.push(`/chat?workspace=${encodeURIComponent(workspace?.id ?? item.workspace_id)}&conversation=${encodeURIComponent(item.id)}`)}
              />
            ))}
          </SideNavSection>
          {conversations.length === 0 && <EmptyState description={t('chat.empty')} isCompact title="—" />}
          </>) : null}
          </SideNav>
          ) : null}
        </HStack>
      }
    >
      {children}
      <AlertDialog
        actionLabel={t('conv.delete')}
        cancelLabel={t('common.cancel')}
        description={t('conv.deleteBody')}
        isActionLoading={isDeleteBusy}
        isOpen={deleting !== null}
        onAction={() => void deleteConversation()}
        onOpenChange={(open) => { if (!open) setDeleting(null); }}
        title={t('conv.deleteTitle')}
      />
    </AppShell>
  );
}
