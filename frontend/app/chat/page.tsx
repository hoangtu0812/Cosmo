'use client';

import {useEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {useRouter, useSearchParams} from 'next/navigation';
import {ThinkingOrb, type OrbState} from 'thinking-orbs';
import {Bot, Building2, Check, MessageSquare, Plus, Settings, UserPlus, UserRound} from 'lucide-react';
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
import {Item} from '@astryxdesign/core/Item';
import {HStack, Layout, LayoutContent, LayoutFooter, LayoutHeader, VStack} from '@astryxdesign/core/Layout';
import {Link} from '@astryxdesign/core/Link';
import {List} from '@astryxdesign/core/List';
import {StatusDot} from '@astryxdesign/core/StatusDot';
import {Text} from '@astryxdesign/core/Text';
import {Timestamp} from '@astryxdesign/core/Timestamp';
import {Toolbar} from '@astryxdesign/core/Toolbar';
import {api, APIError, Citation, Conversation, Message, streamChat, User, Workspace} from '../lib/api';
import {AlertDialog} from '@astryxdesign/core/AlertDialog';
import {Dialog, DialogHeader} from '@astryxdesign/core/Dialog';
import {Markdown} from '@astryxdesign/core/Markdown';
import {MoreMenu} from '@astryxdesign/core/MoreMenu';
import {TextInput} from '@astryxdesign/core/TextInput';
import {Selector} from '@astryxdesign/core/Selector';
import {useTranslation} from '../lib/i18n';
import {UserProfileCard} from '../components/UserProfileCard';

const MOBILE_QUERY = '(max-width: 768px)';

function subscribeMobile(onChange: () => void) {
  const query = window.matchMedia(MOBILE_QUERY);
  query.addEventListener('change', onChange);
  return () => query.removeEventListener('change', onChange);
}

function activityOrb(stage: string): OrbState {
  switch (stage) {
    case 'retrieving': return 'searching';
    case 'writing': return 'composing';
    default: return 'working';
  }
}

export default function ChatPage() {
  const t = useTranslation();
  const router = useRouter();
  const search = useSearchParams();
  const requestedWorkspaceID = search.get('workspace');
  const requestedConversationID = search.get('conversation');
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
  const [orbState, setOrbState] = useState<OrbState>('working');
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
  const [isCreateWorkspaceOpen, setIsCreateWorkspaceOpen] = useState(false);
  const [workspaceName, setWorkspaceName] = useState('');
  const [workspaceDescription, setWorkspaceDescription] = useState('');
  const [workspaceLogo, setWorkspaceLogo] = useState<File | null>(null);
  const [isCreatingWorkspace, setIsCreatingWorkspace] = useState(false);
  const workspaceLogoInput = useRef<HTMLInputElement>(null);

  // Below 768px the rail must start collapsed. Tracked as a subscription
  // rather than an effect so a resize stays in sync.
  const isMobile = useSyncExternalStore(subscribeMobile, () => window.matchMedia(MOBILE_QUERY).matches, () => false);
  const [sidebarOverride, setSidebarOverride] = useState<boolean | null>(null);
  const sidebarOpen = sidebarOverride ?? !isMobile;

  const activeConversation = useMemo(() => conversations.find((item) => item.id === conversationID), [conversations, conversationID]);

  useEffect(() => {
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
      if (requestedConversationID === 'new') {
        // New chat is a real route state. Clear the former transcript before
        // rendering the composer so navigation from the shared sidebar cannot
        // leave the previous conversation visible behind the active action.
        hydratedRef.current = '';
        setConversationID('');
        setMessages([]);
        return;
      }
      const selectedConversation = conversationResult.conversations.find((item) => item.id === requestedConversationID) ?? conversationResult.conversations[0];
      if (selectedConversation) setConversationID(selectedConversation.id);
    }).catch((caught) => {
      if (caught instanceof APIError && caught.status === 401) router.replace('/');
      else setError(caught instanceof Error ? caught.message : t('chat.loadFailed'));
    });
  }, [requestedConversationID, requestedWorkspaceID, router, t]);

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
    setError('');
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
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : t('workspace.createFailed'));
    } finally {
      setIsCreatingWorkspace(false);
    }
  }

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
    setOrbState('working');
    try {
      await streamChat(targetID, trimmed, {model, reasoningEffort}, {
        onMeta: ({citations}) => {
          setStatus(t('chat.writing'));
          setOrbState('composing');
          setMessages((current) => current.map((item) => item.id === optimisticAssistant.id ? {...item, citations} : item));
        },
        onSources: ({citations}) => setMessages((current) => current.map((item) => item.id === optimisticAssistant.id ? {...item, citations} : item)),
        onStatus: ({stage, message}) => {
          setStatus(message);
          setOrbState(activityOrb(stage));
          if (stage === 'retrieval_failed') setError(message);
        },
        onDelta: (delta) => {
          setOrbState('composing');
          setMessages((current) => current.map((item) => item.id === optimisticAssistant.id ? {...item, content: item.content + delta} : item));
        },
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
      setOrbState('working');
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
        onClick={() => router.push('/settings')}
      />
      <DropdownMenuItem
        icon={<UserPlus size={15} />}
        label={t('menu.invite')}
        onClick={() => router.push('/settings?section=members')}
      />
      <DropdownMenuItem
        icon={<Plus size={15} />}
        label={t('menu.createWorkspace')}
        onClick={() => setIsCreateWorkspaceOpen(true)}
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
              <Text color="secondary" type="supporting">{workspace?.model_alias || t('composer.model')}</Text>
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
    <>
      <Layout
        height="fill"
        header={
          <LayoutHeader hasDivider>
            <Toolbar
              label={t('chat.toolbar')}
              startContent={
                <>
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
          <Layout
            contentWidth={960}
            height="fill"
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
                      onClick={() => router.push('/settings')}
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
                            ? <VStack gap={3}>
                              <Markdown isStreaming={streaming} headingLevelStart={3}>{message.content}</Markdown>
                              <CitationList citations={message.citations ?? []} showEmpty={Boolean(message.content)} />
                            </VStack>
                            : (streaming ? <VStack gap={3}>
                              <HStack gap={2} vAlign="center"><ThinkingOrb size={20} state={orbState} /><Text color="secondary" type="supporting">{status}</Text></HStack>
                              <CitationList citations={message.citations ?? []} />
                            </VStack> : '')}
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

      <Dialog
        isOpen={isCreateWorkspaceOpen}
        onOpenChange={(open) => { if (!open) closeCreateWorkspace(); }}
        purpose="form"
      >
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
                    {workspaceLogo && <Button label={t('workspace.removeImage')} onClick={() => setWorkspaceLogo(null)} variant="ghost" />}
                  </HStack>
                </VStack>
              </VStack>
            </LayoutContent>
          }
          footer={
            <LayoutFooter>
              <HStack gap={2} hAlign="end">
                <Button label={t('common.cancel')} onClick={closeCreateWorkspace} variant="secondary" />
                <Button isDisabled={!workspaceName.trim() || isCreatingWorkspace} isLoading={isCreatingWorkspace} label={t('workspace.create')} onClick={() => void createWorkspace()} variant="primary" />
              </HStack>
            </LayoutFooter>
          }
          header={<DialogHeader onOpenChange={(open) => { if (!open) closeCreateWorkspace(); }} title={t('workspace.createTitle')} />}
        />
      </Dialog>

    </>
  );
}

function CitationList({citations, showEmpty = false}: {citations: Citation[]; showEmpty?: boolean}) {
  if (citations.length === 0) {
    return showEmpty ? <Text color="secondary" type="supporting">Không tìm thấy tài liệu Knowledge Base liên quan.</Text> : null;
  }
  return (
    <VStack gap={2}>
      <Text type="label" weight="semibold">Nguồn Knowledge Base</Text>
      <List>
        {citations.map((citation) => (
          <Item
            description={citation.section ? `${citation.section}${citation.page ? ` · Trang ${citation.page}` : ''}` : citation.page ? `Trang ${citation.page}` : citation.source}
            endContent={
              <Link href={api.documentOriginalURL(citation.kb_id, citation.document_id)} isExternalLink>
                Mở tài liệu
              </Link>
            }
            key={`${citation.kb_id}:${citation.document_id}:${citation.index}`}
            label={`[${citation.index}] ${citation.title || citation.source}`}
          />
        ))}
      </List>
    </VStack>
  );
}

async function resizeToSquare(file: File, size = 128): Promise<{mime: string; data: string}> {
  const bitmap = await createImageBitmap(file);
  const canvas = document.createElement('canvas');
  canvas.width = size;
  canvas.height = size;
  const context = canvas.getContext('2d');
  if (!context) throw new Error('Canvas unavailable');
  const side = Math.min(bitmap.width, bitmap.height);
  context.drawImage(bitmap, (bitmap.width - side) / 2, (bitmap.height - side) / 2, side, side, 0, 0, size, size);
  bitmap.close();
  const blob: Blob = await new Promise((resolve, reject) => {
    canvas.toBlob((result) => (result ? resolve(result) : reject(new Error('Encode failed'))), 'image/png');
  });
  const bytes = new Uint8Array(await blob.arrayBuffer());
  let binary = '';
  bytes.forEach((byte) => { binary += String.fromCharCode(byte); });
  return {mime: 'image/png', data: btoa(binary)};
}
