'use client';

import {useEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {useRouter} from 'next/navigation';
import {ThinkingOrb} from 'thinking-orbs';
import {Bot, Building2, Check, Library, LogOut, MessageSquare, Plus, Settings, SquarePen, UserPlus, UserRound} from 'lucide-react';
import {AppShell} from '@astryxdesign/core/AppShell';
import {Avatar} from '@astryxdesign/core/Avatar';
import {Banner} from '@astryxdesign/core/Banner';
import {Button} from '@astryxdesign/core/Button';
import {
  ChatComposer,
  ChatLayout,
  ChatMessage,
  ChatMessageBubble,
  ChatMessageList,
  ChatMessageMetadata,
} from '@astryxdesign/core/Chat';
import {ClickableCard} from '@astryxdesign/core/ClickableCard';
import {DropdownMenu, DropdownMenuDivider, DropdownMenuItem} from '@astryxdesign/core/DropdownMenu';
import type {DropdownMenuOption} from '@astryxdesign/core/DropdownMenu';
import {EmptyState} from '@astryxdesign/core/EmptyState';
import {Icon} from '@astryxdesign/core/Icon';
import {IconButton} from '@astryxdesign/core/IconButton';
import {HStack, Layout, LayoutContent, LayoutFooter, LayoutHeader, VStack} from '@astryxdesign/core/Layout';
import {SideNav, SideNavCollapseButton, SideNavHeading, SideNavItem, SideNavSection} from '@astryxdesign/core/SideNav';
import {StatusDot} from '@astryxdesign/core/StatusDot';
import {Text} from '@astryxdesign/core/Text';
import {Timestamp} from '@astryxdesign/core/Timestamp';
import {Toolbar} from '@astryxdesign/core/Toolbar';
import {api, APIError, Conversation, Message, streamChat, User, Workspace} from '../lib/api';
import {AlertDialog} from '@astryxdesign/core/AlertDialog';
import {Dialog, DialogHeader} from '@astryxdesign/core/Dialog';
import {Markdown} from '@astryxdesign/core/Markdown';
import {MoreMenu} from '@astryxdesign/core/MoreMenu';
import {TextInput} from '@astryxdesign/core/TextInput';
import {Selector} from '@astryxdesign/core/Selector';
import {useTranslation} from '../lib/i18n';

const MOBILE_QUERY = '(max-width: 768px)';

function subscribeMobile(onChange: () => void) {
  const query = window.matchMedia(MOBILE_QUERY);
  query.addEventListener('change', onChange);
  return () => query.removeEventListener('change', onChange);
}

export default function ChatPage() {
  const t = useTranslation();
  const router = useRouter();
  const suggestions = [t('chat.suggestion1'), t('chat.suggestion2'), t('chat.suggestion3'), t('chat.suggestion4')];
  const [user, setUser] = useState<User | null>(null);
  const [workspace, setWorkspace] = useState<Workspace | null>(null);
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [conversationID, setConversationID] = useState('');
  const [messages, setMessages] = useState<Message[]>([]);
  const [draft, setDraft] = useState('');
  const [streaming, setStreaming] = useState(false);
  const [status, setStatus] = useState('');
  const [error, setError] = useState('');
  // Per-conversation overrides for the composer pickers. Empty model means the
  // workspace default; empty effort omits the parameter, since models that do
  // not reason reject it on some providers.
  const [availableModels, setAvailableModels] = useState<string[]>([]);
  const [model, setModel] = useState('');
  const [reasoningEffort, setReasoningEffort] = useState('');
  // Conversations whose messages are already in state. Without this the fetch
  // below races the optimistic bubbles: sending the first message of a new chat
  // sets conversationID, the effect refetches, and the refetch overwrites the
  // streaming placeholder so deltas land on an id that no longer exists.
  const hydratedRef = useRef('');
  const [renaming, setRenaming] = useState<Conversation | null>(null);
  const [renameTitle, setRenameTitle] = useState('');
  const [deleting, setDeleting] = useState<Conversation | null>(null);
  const [busy, setBusy] = useState(false);

  // Below 768px the rail must start collapsed. Tracked as a subscription
  // rather than an effect so a resize stays in sync.
  const isMobile = useSyncExternalStore(subscribeMobile, () => window.matchMedia(MOBILE_QUERY).matches, () => false);
  const [sidebarOverride, setSidebarOverride] = useState<boolean | null>(null);
  const sidebarOpen = sidebarOverride ?? !isMobile;

  const activeConversation = useMemo(() => conversations.find((item) => item.id === conversationID), [conversations, conversationID]);

  useEffect(() => {
    const requestedWorkspaceID = new URLSearchParams(window.location.search).get('workspace');
    Promise.all([api.me(), api.workspaces()]).then(async ([me, workspaceResult]) => {
      const workspaceID = requestedWorkspaceID ?? me.user.last_workspace_id ?? workspaceResult.workspaces[0]?.id;
      const selected = workspaceResult.workspaces.find((item) => item.id === workspaceID);
      setUser(me.user);
      setWorkspaces(workspaceResult.workspaces);
      if (!selected) {
        setError(t('chat.noWorkspace'));
        return;
      }
      if (!requestedWorkspaceID) router.replace(`/chat?workspace=${encodeURIComponent(selected.id)}`);
      await api.selectWorkspace(selected.id);
      const conversationResult = await api.conversations(selected.id);
      setWorkspace(selected);
      setConversations(conversationResult.conversations);
      if (conversationResult.conversations[0]) setConversationID(conversationResult.conversations[0].id);
    }).catch((caught) => {
      if (caught instanceof APIError && caught.status === 401) router.replace('/');
      else setError(caught instanceof Error ? caught.message : t('chat.loadFailed'));
    });
  }, [router, t]);

  useEffect(() => {
    if (!workspace) return;
    let cancelled = false;
    api.workspaceModels(workspace.id)
      .then((result) => { if (!cancelled) setAvailableModels(result.models); })
      .catch(() => undefined);
    return () => { cancelled = true; };
  }, [workspace]);

  useEffect(() => {
    if (!conversationID) return;
    if (hydratedRef.current === conversationID) return;
    hydratedRef.current = conversationID;
    api.messages(conversationID).then((result) => setMessages(result.messages)).catch((caught) => setError(caught instanceof Error ? caught.message : t('chat.historyFailed')));
  }, [conversationID, t]);

  function openConversation(id: string) {
    setConversationID(id);
    if (isMobile) setSidebarOverride(false);
  }

  function startNewChat() {
    setConversationID('');
    setMessages([]);
    setError('');
    if (isMobile) setSidebarOverride(false);
  }

  async function switchWorkspace(next: Workspace) {
    if (next.id === workspace?.id) return;
    setError('');
    try {
      await api.selectWorkspace(next.id);
      const result = await api.conversations(next.id);
      setWorkspace(next);
      setConversations(result.conversations);
      setConversationID(result.conversations[0]?.id ?? '');
      setMessages([]);
      router.replace(`/chat?workspace=${encodeURIComponent(next.id)}`);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : t('chat.switchFailed'));
    }
  }

  async function renameConversation() {
    if (!renaming) return;
    setBusy(true);
    try {
      const title = renameTitle.trim();
      await api.renameConversation(renaming.id, title);
      setConversations((current) => current.map((item) => item.id === renaming.id ? {...item, title} : item));
      setRenaming(null);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : t('conv.renameFailed'));
    } finally {
      setBusy(false);
    }
  }

  async function deleteConversation() {
    if (!deleting) return;
    setBusy(true);
    try {
      await api.deleteConversation(deleting.id);
      setConversations((current) => current.filter((item) => item.id !== deleting.id));
      if (deleting.id === conversationID) startNewChat();
      setDeleting(null);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : t('conv.deleteFailed'));
    } finally {
      setBusy(false);
    }
  }

  async function signOut() { await api.signOut(); router.replace('/'); }

  async function submit(content: string) {
    const trimmed = content.trim();
    if (!trimmed || streaming || !workspace) return;
    setDraft('');
    setError('');
    let targetID = conversationID;
    if (!targetID) {
      const result = await api.createConversation(workspace.id, trimmed.slice(0, 100));
      targetID = result.conversation.id;
      // The optimistic bubbles below are the freshest view of this
      // conversation, so suppress the history fetch this id would trigger.
      hydratedRef.current = targetID;
      setConversationID(targetID);
      setConversations((current) => [result.conversation, ...current]);
    }
    const optimisticUser: Message = {id: `local-${crypto.randomUUID()}`, conversation_id: targetID, role: 'user', content: trimmed, created_at: new Date().toISOString()};
    const optimisticAssistant: Message = {id: `stream-${crypto.randomUUID()}`, conversation_id: targetID, role: 'assistant', content: '', created_at: new Date().toISOString()};
    setMessages((current) => [...current, optimisticUser, optimisticAssistant]);
    setStreaming(true);
    setStatus(t('chat.thinking'));
    try {
      await streamChat(targetID, trimmed, {model, reasoningEffort}, {
        onMeta: () => setStatus(t('chat.writing')),
        onDelta: (delta) => setMessages((current) => current.map((item) => item.id === optimisticAssistant.id ? {...item, content: item.content + delta} : item)),
        onDone: ({message}) => setMessages((current) => current.map((item) => item.id === optimisticAssistant.id ? message : item)),
      });
      const refreshed = await api.conversations(workspace.id);
      setConversations(refreshed.conversations);
    } catch (caught) {
      setMessages((current) => current.filter((item) => item.id !== optimisticAssistant.id));
      setError(caught instanceof Error ? caught.message : t('chat.replyFailed'));
    } finally {
      setStreaming(false);
      setStatus('');
    }
  }

  const collapsible = {
    isCollapsed: !sidebarOpen,
    onCollapsedChange: (isCollapsed: boolean) => setSidebarOverride(!isCollapsed),
    hasButton: false,
  };

  // Workspace switching lives in the SideNav heading's popover, the way the
  // reference shell puts it, rather than on a separate picker page.
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
      <DropdownMenuItem
        icon={<Settings size={15} />}
        label={t('menu.settings')}
        onClick={() => router.push(`/settings?workspace=${encodeURIComponent(workspace?.id ?? '')}`)}
      />
      <DropdownMenuItem
        icon={<UserPlus size={15} />}
        label={t('menu.invite')}
        onClick={() => router.push(`/settings?workspace=${encodeURIComponent(workspace?.id ?? '')}&section=members`)}
      />
      <DropdownMenuItem
        icon={<Plus size={15} />}
        label={t('menu.createWorkspace')}
        onClick={() => router.push('/settings?section=workspace')}
      />
      <DropdownMenuDivider />
      <DropdownMenuItem
        description={user?.email}
        icon={<LogOut size={15} />}
        label={t('menu.signOut')}
        onClick={() => void signOut()}
      />
    </>
  );

  const conversationMenu: DropdownMenuOption[] = [
    {id: 'new', label: t('chat.newChat'), icon: <Plus size={15} />, onClick: startNewChat},
    ...(conversations.length > 0 ? [{type: 'divider' as const}] : []),
    ...conversations.map((item) => ({id: item.id, label: item.title, icon: <MessageSquare size={15} />, onClick: () => openConversation(item.id)})),
  ];

  const emptyState = (
    <VStack gap={6} hAlign="center" width="100%">
      <EmptyState
        description={t('chat.greetingBody')}
        headingLevel={1}
        icon={<Bot size={72} strokeWidth={1} />}
        title={t('chat.greeting')}
      />
      <VStack gap={2} width="100%">
        {suggestions.map((suggestion) => (
          <ClickableCard
            isDisabled={streaming || !workspace}
            key={suggestion}
            label={suggestion}
            onClick={() => void submit(suggestion)}
            padding={3}
          >
            {suggestion}
          </ClickableCard>
        ))}
      </VStack>
    </VStack>
  );

  const composer = (
    <VStack gap={2}>
      {error && <Banner isDismissable onDismiss={() => setError('')} status="error" title={error} />}
      <ChatComposer
        footerActions={
          <HStack gap={2} vAlign="center">
            <StatusDot label={workspace?.model_configured ? t('chat.modelReady') : t('chat.modelMissing')} variant={workspace?.model_configured ? 'success' : 'warning'} />
            {availableModels.length > 0 ? (
              <Selector
                hasSearch
                isLabelHidden
                label={t('composer.model')}
                onChange={setModel}
                options={[{value: '', label: workspace?.model_alias ?? t('composer.model')}, ...availableModels.map((item) => ({value: item, label: item}))]}
                size="sm"
                value={model}
                variant="ghost"
              />
            ) : (
              <Text color="secondary" type="supporting">{workspace?.model_alias ?? t('chat.modelMissing')}</Text>
            )}
            <Selector
              isLabelHidden
              label={t('composer.reasoning')}
              onChange={setReasoningEffort}
              options={[
                {value: '', label: t('composer.reasoningAuto')},
                {value: 'minimal', label: t('composer.reasoningMinimal')},
                {value: 'low', label: t('composer.reasoningLow')},
                {value: 'medium', label: t('composer.reasoningMedium')},
                {value: 'high', label: t('composer.reasoningHigh')},
              ]}
              size="sm"
              value={reasoningEffort}
              variant="ghost"
            />
          </HStack>
        }
        isDisabled={streaming || !workspace}
        onChange={setDraft}
        onSubmit={(value) => void submit(value)}
        placeholder={t('chat.placeholder')}
        value={draft}
      />
      <Text color="secondary" display="block" type="supporting">
        {t('chat.disclaimer')}
      </Text>
    </VStack>
  );

  return (
    <AppShell
      contentPadding={0}
      variant="elevated"
      sideNav={
        <SideNav
          collapsible={collapsible}
          header={
            <SideNavHeading
              heading={workspace?.name ?? t('chat.loading')}
              icon={<Avatar name={workspace?.icon || workspace?.name || 'Cosmo'} size="sm" src={workspace?.has_icon_image ? api.workspaceIconURL(workspace.id) : undefined} />}
              menu={workspaceMenu}
              subheading={user?.email}
            />
          }
        >
          <SideNavSection isHeaderHidden title={t('chat.actions')}>
            <SideNavItem
              icon={<Icon icon={SquarePen} size="sm" />}
              isSelected={conversationID === ''}
              label={t('chat.newChat')}
              onClick={startNewChat}
            />
            <SideNavItem
              icon={<Icon icon={Library} size="sm" />}
              label={t('kb.title')}
              onClick={() => router.push('/knowledge')}
            />
          </SideNavSection>

          <SideNavSection title={t('chat.recent')}>
            {conversations.map((item) => (
              <SideNavItem
                endContent={
                  <MoreMenu
                    items={[
                      {label: t('conv.rename'), onClick: () => { setRenaming(item); setRenameTitle(item.title); }},
                      {label: t('conv.delete'), variant: 'destructive', onClick: () => setDeleting(item)},
                    ]}
                    label={t('conv.options')}
                    size="sm"
                  />
                }
                icon={<Icon icon={MessageSquare} size="sm" />}
                isSelected={item.id === conversationID}
                key={item.id}
                label={item.title}
                onClick={() => openConversation(item.id)}
              />
            ))}
          </SideNavSection>
          {conversations.length === 0 && <EmptyState description={t('chat.empty')} isCompact title="—" />}
        </SideNav>
      }
    >
      <Layout
        contentWidth={960}
        height="fill"
        header={
          <LayoutHeader hasDivider>
            <Toolbar
              endContent={<IconButton icon={<Plus size={16} />} label={t('chat.newChatLong')} onClick={startNewChat} size="sm" variant="ghost" />}
              label={t('chat.toolbar')}
              startContent={
                <>
                  <SideNavCollapseButton collapsible={collapsible} label={t('chat.sidebar')} size="sm" />
                  <DropdownMenu
                    alignment="start"
                    button={{label: activeConversation?.title ?? t('chat.newChat'), size: 'sm', variant: 'ghost'}}
                    hasChevron
                    items={conversationMenu}
                    menuWidth={264}
                  />
                </>
              }
            />
          </LayoutHeader>
        }
        content={
          <LayoutContent padding={0}>
            <VStack height="100%">
              {workspace && !workspace.model_configured && (
                <Banner
                  container="section"
                  description={t('chat.modelMissingBody')}
                  endContent={
                    <Button
                      label={t('chat.openSettings')}
                      onClick={() => router.push(`/settings?workspace=${encodeURIComponent(workspace.id)}`)}
                      size="sm"
                      variant="secondary"
                    />
                  }
                  status="warning"
                  title={t('chat.modelMissingTitle')}
                />
              )}
              <ChatLayout composer={composer} emptyState={emptyState}>
                {messages.length === 0 ? null : (
                  <ChatMessageList isStreaming={streaming}>
                    {messages.map((message) => message.role === 'user' ? (
                      <ChatMessage key={message.id} sender="user">
                        <ChatMessageBubble metadata={<ChatMessageMetadata timestamp={<Timestamp format="time" value={message.created_at} />} />}>
                          {message.content}
                        </ChatMessageBubble>
                      </ChatMessage>
                    ) : (
                      <ChatMessage avatar={<Avatar name={workspace?.icon || workspace?.name || 'Cosmo'} size="md" src={workspace?.has_icon_image ? api.workspaceIconURL(workspace.id) : undefined} />} key={message.id} sender="assistant">
                        <ChatMessageBubble
                          metadata={message.content ? <ChatMessageMetadata footer={message.model || workspace?.model_alias} timestamp={<Timestamp format="time" value={message.created_at} />} /> : undefined}
                          name="Cosmo"
                          variant="ghost"
                        >
                          {message.content
                            ? <Markdown isStreaming={streaming} headingLevelStart={3}>{message.content}</Markdown>
                            : (streaming ? <HStack gap={2} vAlign="center"><ThinkingOrb size={20} state="composing" /><Text color="secondary" type="supporting">{status}</Text></HStack> : '')}
                        </ChatMessageBubble>
                      </ChatMessage>
                    ))}
                  </ChatMessageList>
                )}
              </ChatLayout>
            </VStack>
          </LayoutContent>
        }
      />

      <Dialog
        isOpen={renaming !== null}
        onOpenChange={(open) => { if (!open) setRenaming(null); }}
        purpose="form"
      >
        <Layout
          content={
            <LayoutContent>
              <TextInput label={t('conv.title')} onChange={setRenameTitle} onEnter={() => void renameConversation()} value={renameTitle} width="100%" />
            </LayoutContent>
          }
          footer={
            <LayoutFooter>
              <HStack gap={2} hAlign="end">
                <Button label={t('common.cancel')} onClick={() => setRenaming(null)} variant="secondary" />
                <Button isDisabled={!renameTitle.trim() || busy} isLoading={busy} label={t('common.save')} onClick={() => void renameConversation()} variant="primary" />
              </HStack>
            </LayoutFooter>
          }
          header={<DialogHeader onOpenChange={(open) => { if (!open) setRenaming(null); }} title={t('conv.renameTitle')} />}
        />
      </Dialog>

      <AlertDialog
        actionLabel={t('conv.delete')}
        cancelLabel={t('common.cancel')}
        description={t('conv.deleteBody')}
        isActionLoading={busy}
        isOpen={deleting !== null}
        onAction={() => void deleteConversation()}
        onOpenChange={(open) => { if (!open) setDeleting(null); }}
        title={t('conv.deleteTitle')}
      />
    </AppShell>
  );
}
