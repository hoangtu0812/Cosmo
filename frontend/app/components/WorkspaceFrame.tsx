'use client';

import {Suspense, useEffect, useRef, useState} from 'react';
import {usePathname, useRouter, useSearchParams} from 'next/navigation';
import {Archive, BarChart3, Bell, Bookmark, Bot, Box, Clock, FolderKanban, Library, MessageSquare, Search, Settings, SquarePen, Workflow, Wrench, Zap} from 'lucide-react';
import {AppShell} from '@astryxdesign/core/AppShell';
import {Avatar} from '@astryxdesign/core/Avatar';
import {Button} from '@astryxdesign/core/Button';
import {Dialog, DialogHeader} from '@astryxdesign/core/Dialog';
import {Icon} from '@astryxdesign/core/Icon';
import {HStack, Layout, LayoutContent, LayoutFooter, VStack} from '@astryxdesign/core/Layout';
import {Text} from '@astryxdesign/core/Text';
import {TextInput} from '@astryxdesign/core/TextInput';
import {SideNav, SideNavHeading, SideNavItem, SideNavSection} from '@astryxdesign/core/SideNav';
import {api, User, Workspace} from '../lib/api';
import {resizeToSquare} from '../lib/image';
import {useTranslation} from '../lib/i18n';
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
// A canvas is the whole job while it is open, so the workflow editor takes the
// window: the rail and the second column would be a third of the room to draw
// in, spent on navigation nobody uses mid-edit. Its own header carries the way
// back out.
const UNFRAMED_ROUTES = [/^\/workflow\/[^/]+$/];

export function WorkspaceFrame({children}: {children: React.ReactNode}) {
  const pathname = usePathname();
  if (UNFRAMED_ROUTES.some((route) => route.test(pathname))) return <>{children}</>;
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
  const [isCreateWorkspaceOpen, setIsCreateWorkspaceOpen] = useState(false);
  const [isCreatingWorkspace, setIsCreatingWorkspace] = useState(false);
  const [workspaceName, setWorkspaceName] = useState('');
  const [workspaceDescription, setWorkspaceDescription] = useState('');
  const [workspaceLogo, setWorkspaceLogo] = useState<File | null>(null);
  const workspaceLogoInput = useRef<HTMLInputElement>(null);

  useEffect(() => {
    let cancelled = false;
    // Who you are and which workspace you are in is what the whole frame is
    // drawn from, so it is set as soon as it is known. The conversation list
    // follows separately: it is one section of one column, and it failing
    // used to leave the entire shell saying "loading" forever.
    Promise.all([api.me(), api.workspaces()]).then(async ([me, result]) => {
      if (cancelled) return;
      const workspaceID = requestedWorkspaceID || me.user.last_workspace_id || result.workspaces[0]?.id || '';
      // A URL can name a workspace this account has lost access to, or one
      // that no longer exists. Falling back leaves somewhere to stand instead
      // of an empty frame.
      const selected = result.workspaces.find((item) => item.id === workspaceID)
        ?? result.workspaces.find((item) => item.id === me.user.last_workspace_id)
        ?? result.workspaces[0]
        ?? null;
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
  //
  // Only then, though. The URL is the request, and a URL that already names a
  // workspace has to be answered rather than overwritten: switching writes the
  // new id there, and this effect - still holding the one being left - used to
  // put it straight back, so the switch bounced.
  useEffect(() => {
    if (!workspace) return;
    const named = search.get('workspace') ?? '';
    const isKnown = named !== '' && workspaces.some((item) => item.id === named);
    if (isKnown) return;
    const params = new URLSearchParams(search.toString());
    params.set('workspace', workspace.id);
    router.replace(`${pathname}?${params.toString()}`);
  }, [workspace, workspaces, search, pathname, router]);

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

  function closeCreateWorkspace(force = false) {
    if (isCreatingWorkspace && !force) return;
    setIsCreateWorkspaceOpen(false);
    setWorkspaceName('');
    setWorkspaceDescription('');
    setWorkspaceLogo(null);
  }

  async function createWorkspace() {
    const name = workspaceName.trim();
    if (!name) return;
    setIsCreatingWorkspace(true);
    try {
      const result = await api.createWorkspace(name, workspaceDescription.trim());
      let created = result.workspace;
      if (workspaceLogo) {
        const {mime, data} = await resizeToSquare(workspaceLogo);
        await api.uploadWorkspaceIcon(created.id, mime, data);
        created = {...created, has_icon_image: true};
      }
      setWorkspaces((current) => [...current, created]);
      closeCreateWorkspace(true);
      await switchWorkspace(created);
    } catch {
      closeCreateWorkspace(true);
    } finally {
      setIsCreatingWorkspace(false);
    }
  }

  // The agent editor is a focused surface, so it runs without the rail while
  // keeping everything else the frame does - resolving the workspace above all.
  const isLibraryRoute = pathname === '/library';
  // Chat joins the routes that run on the outer rail alone: who to talk to is
  // chosen in the composer, where your hand already is, so a column repeating
  // that choice only narrowed the conversation.
  const hasSecondColumn = !['/chat', '/projects', '/schedule', '/notifications'].includes(pathname);
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
                    chat does without entirely. Without a way back in, agents
                    and knowledge would be unreachable from chat - the
                    reference keeps this entry at the foot of the rail for
                    exactly that reason. */}
                <SideNavSection isHeaderHidden title={t('nav.workspaceArea')}>
                  <SideNavItem
                    icon={<Icon icon={Box} size="sm" />}
                    isSelected={WORKSPACE_ROUTES.some((route) => pathname.startsWith(route))}
                    label={t('nav.workspaceArea')}
                    onClick={() => goTo('/agents')}
                    size="md"
                  />
                </SideNavSection>
                {user ? (
                  <UserProfileCard
                    onCreateWorkspace={() => setIsCreateWorkspaceOpen(true)}
                    onSwitchWorkspace={(next) => void switchWorkspace(next)}
                    user={user}
                    workspace={workspace}
                    workspaces={workspaces}
                  />
                ) : null}
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
            className={isLibraryRoute ? 'w-76' : 'w-50'}
            footer={isLibraryRoute ? undefined : (
              <SideNavSection isHeaderHidden title={t('nav.operate')}>
                <SideNavItem icon={<Icon icon={BarChart3} size="sm" />} isSelected={pathname === '/observability'} label={t('nav.observability')} onClick={() => goTo('/observability')} />
                <SideNavItem icon={<Icon icon={Settings} size="sm" />} isSelected={pathname === '/settings'} label={t('nav.manageWorkspace')} onClick={() => goTo('/settings')} />
              </SideNavSection>
            )}
            header={isLibraryRoute
              ? <SideNavHeading heading={t('nav.library')} />
              : <SideNavHeading heading={workspace?.name ?? t('chat.loading')} icon={<Avatar name={workspace?.icon || workspace?.name || 'Cosmo'} size="sm" src={workspace?.has_icon_image ? api.workspaceIconURL(workspace.id) : undefined} />} />}
          >
          {isLibraryRoute ? null : (
            <SideNavSection isHeaderHidden title={t('chat.actions')}>
              <SideNavItem icon={<Icon icon={SquarePen} size="sm" />} isSelected={pathname === '/chat' && search.get('conversation') === 'new'} label={t('chat.newChat')} onClick={() => goTo('/chat')} />
            </SideNavSection>
          )}
          {isLibraryRoute ? (
            /* The library's own sections. Shells for now - see
               docs/ui_backlog.md. */
            <SideNavSection isHeaderHidden title={t('nav.library')}>
              <SideNavItem icon={<Icon icon={Search} size="sm" />} isDisabled label={t('library.search')} size="lg" />
              <SideNavItem endContent={<Token label={t('library.internal')} size="sm" />} icon={<Icon icon={Archive} size="sm" />} isDisabled label={t('library.shared')} size="lg" />
              <SideNavItem icon={<Icon icon={Bookmark} size="sm" />} isDisabled label={t('library.collection')} size="lg" />
            </SideNavSection>
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
            <SideNavItem icon={<Icon icon={Wrench} size="sm" />} isSelected={pathname.startsWith('/tools')} label={t('nav.tool')} onClick={() => goTo('/tools')} />
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

      <Dialog isOpen={isCreateWorkspaceOpen} onOpenChange={(open) => { if (!open) closeCreateWorkspace(); }} purpose="form">
        <Layout
          content={
            <LayoutContent>
              <VStack gap={4}>
                <TextInput label={t('workspace.name')} onChange={setWorkspaceName} onEnter={() => void createWorkspace()} value={workspaceName} width="100%" />
                <TextInput label={t('workspace.description')} onChange={setWorkspaceDescription} value={workspaceDescription} width="100%" />
                <VStack gap={2}>
                  <Text type="label">{t('workspace.logo')}</Text>
                  <HStack gap={2} vAlign="center">
                    <Avatar name={workspaceName || 'Workspace'} size="lg" src={workspaceLogo ? URL.createObjectURL(workspaceLogo) : undefined} />
                    <input
                      accept="image/png,image/jpeg,image/webp,image/gif"
                      hidden
                      onChange={(event) => { setWorkspaceLogo(event.target.files?.[0] ?? null); event.target.value = ''; }}
                      ref={workspaceLogoInput}
                      type="file"
                    />
                    <Button label={t('workspace.uploadImage')} onClick={() => workspaceLogoInput.current?.click()} variant="secondary" />
                    {workspaceLogo ? <Button label={t('workspace.removeImage')} onClick={() => setWorkspaceLogo(null)} variant="ghost" /> : null}
                  </HStack>
                </VStack>
              </VStack>
            </LayoutContent>
          }
          footer={
            <LayoutFooter>
              <HStack gap={2} hAlign="end">
                <Button label={t('common.cancel')} onClick={() => closeCreateWorkspace()} variant="secondary" />
                <Button isDisabled={!workspaceName.trim() || isCreatingWorkspace} isLoading={isCreatingWorkspace} label={t('workspace.create')} onClick={() => void createWorkspace()} variant="primary" />
              </HStack>
            </LayoutFooter>
          }
          header={<DialogHeader onOpenChange={(open) => { if (!open) closeCreateWorkspace(); }} title={t('workspace.createTitle')} />}
        />
      </Dialog>
    </AppShell>
  );
}
