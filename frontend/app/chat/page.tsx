'use client';

import {useEffect, useMemo, useRef, useState} from 'react';
import {useRouter, useSearchParams} from 'next/navigation';
import {ThinkingOrb, type OrbState} from 'thinking-orbs';
import {Bot, Brain, Cloud, Cpu, FolderOpen, Gem, History, MessageSquare, MoreHorizontal, Pencil, Plus, Sparkles, SquarePen, Trash2, X} from 'lucide-react';
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
import {Collapsible} from '@astryxdesign/core/Collapsible';
import {DropdownMenu} from '@astryxdesign/core/DropdownMenu';
import type {DropdownMenuOption} from '@astryxdesign/core/DropdownMenu';
import {EmptyState} from '@astryxdesign/core/EmptyState';
import {Icon} from '@astryxdesign/core/Icon';
import {IconButton} from '@astryxdesign/core/IconButton';
import {Item} from '@astryxdesign/core/Item';
import {HStack, Layout, LayoutContent, LayoutFooter, LayoutHeader, LayoutPanel, VStack} from '@astryxdesign/core/Layout';
import {Link} from '@astryxdesign/core/Link';
import {List} from '@astryxdesign/core/List';
import {Token} from '@astryxdesign/core/Token';
import {Text} from '@astryxdesign/core/Text';
import {Timestamp} from '@astryxdesign/core/Timestamp';
import {Toolbar} from '@astryxdesign/core/Toolbar';
import {Agent, api, APIError, Citation, Conversation, GatewayModel, Message, MessageToolCall, streamChat, User, Workspace} from '../lib/api';
import {ToolCallTrail} from '../components/ToolCallTrail';
import {AlertDialog} from '@astryxdesign/core/AlertDialog';
import {Dialog, DialogHeader} from '@astryxdesign/core/Dialog';
import {Markdown} from '@astryxdesign/core/Markdown';
import {TextInput} from '@astryxdesign/core/TextInput';
import {Selector} from '@astryxdesign/core/Selector';
import {StatusLabel} from '../components/StatusLabel';
import {useTranslation} from '../lib/i18n';


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
  const requestedTarget = search.get('target') ?? '';
  const suggestions = [t('chat.suggestion1'), t('chat.suggestion2'), t('chat.suggestion3'), t('chat.suggestion4')];
  const [user, setUser] = useState<User | null>(null);
  const [workspace, setWorkspace] = useState<Workspace | null>(null);
  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [conversationID, setConversationID] = useState('');
  const [messages, setMessages] = useState<Message[]>([]);
  const [draft, setDraft] = useState('');
  const [streaming, setStreaming] = useState(false);
  const [liveToolCalls, setLiveToolCalls] = useState<MessageToolCall[]>([]);
  const [status, setStatus] = useState('');
  const [orbState, setOrbState] = useState<OrbState>('working');
  const [error, setError] = useState('');
  // Per-conversation overrides for the composer pickers. Empty model means the
  // workspace default; empty effort omits the parameter, since models that do
  // not reason reject it on some providers.
  const [availableModels, setAvailableModels] = useState<GatewayModel[]>([]);
  const [defaultModel, setDefaultModel] = useState('');
  const [agents, setAgents] = useState<Agent[]>([]);
  const [chatTarget, setChatTarget] = useState('model:');
  const [reasoningEffort, setReasoningEffort] = useState('');
  // Conversations whose messages are already in state. Without this the fetch
  // below races the optimistic bubbles: sending the first message of a new chat
  // sets conversationID, the effect refetches, and the refetch overwrites the
  // streaming placeholder so deltas land on an id that no longer exists.
  const hydratedRef = useRef('');
  const [renaming, setRenaming] = useState<Conversation | null>(null);
  const [isRecentOpen, setIsRecentOpen] = useState(false);
  const [renameTitle, setRenameTitle] = useState('');
  const [deleting, setDeleting] = useState<Conversation | null>(null);
  const [busy, setBusy] = useState(false);

  // Below 768px the rail must start collapsed. Tracked as a subscription
  // rather than an effect so a resize stays in sync.

  const activeConversation = useMemo(() => conversations.find((item) => item.id === conversationID), [conversations, conversationID]);
  const selectedAgentID = chatTarget.startsWith('agent:') ? chatTarget.slice(6) : '';
  const selectedAgent = useMemo(() => agents.find((item) => item.id === selectedAgentID), [agents, selectedAgentID]);
  const selectedModelID = chatTarget.startsWith('model:') ? chatTarget.slice(6) : '';
  const effectiveModelID = selectedAgent?.model || selectedModelID || defaultModel;
  // The agent answers as itself; otherwise the model does, and its name is
  // what a reader needs to see.
  const chatTargetLabel = selectedAgent?.name || effectiveModelID || workspace?.model_alias || '';
  const effectiveModel = useMemo(() => availableModels.find((item) => item.id === effectiveModelID), [availableModels, effectiveModelID]);
  const reasoningEfforts = useMemo(() => effectiveModel?.reasoning_efforts ?? [], [effectiveModel]);
  const reasoningLabels: Record<string, string> = {
    none: t('composer.reasoningNone'),
    minimal: t('composer.reasoningMinimal'),
    low: t('composer.reasoningLow'),
    medium: t('composer.reasoningMedium'),
    high: t('composer.reasoningHigh'),
    xhigh: t('composer.reasoningXHigh'),
    max: t('composer.reasoningMax'),
  };

  useEffect(() => {
    Promise.all([api.me(), api.workspaces()]).then(async ([me, workspaceResult]) => {
      const workspaceID = requestedWorkspaceID ?? me.user.last_workspace_id ?? workspaceResult.workspaces[0]?.id;
      const selected = workspaceResult.workspaces.find((item) => item.id === workspaceID);
      setUser(me.user);
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
        // The sidebar picks who to talk to by changing the URL, so the target
        // has to come back out of it rather than resetting to the default.
        if (requestedTarget) setChatTarget(requestedTarget);
        return;
      }
      const selectedConversation = conversationResult.conversations.find((item) => item.id === requestedConversationID) ?? conversationResult.conversations[0];
      if (selectedConversation) {
        setConversationID(selectedConversation.id);
        setChatTarget(selectedConversation.agent_id ? `agent:${selectedConversation.agent_id}` : 'model:');
      }
    }).catch((caught) => {
      if (caught instanceof APIError && caught.status === 401) router.replace('/');
      else setError(caught instanceof Error ? caught.message : t('chat.loadFailed'));
    });
  }, [requestedConversationID, requestedTarget, requestedWorkspaceID, router, t]);

  useEffect(() => {
    if (!workspace) return;
    let cancelled = false;
    Promise.all([api.workspaceModels(workspace.id), api.agents(workspace.id)])
      .then(([modelResult, agentResult]) => {
        if (cancelled) return;
        setAvailableModels(modelResult.models);
        setDefaultModel(modelResult.default);
        setAgents(agentResult.agents);
      })
      .catch(() => undefined);
    return () => { cancelled = true; };
  }, [workspace]);

  useEffect(() => {
    if (!conversationID) return;
    if (hydratedRef.current === conversationID) return;
    hydratedRef.current = conversationID;
    api.messages(conversationID).then((result) => setMessages(result.messages)).catch((caught) => setError(caught instanceof Error ? caught.message : t('chat.historyFailed')));
  }, [conversationID, t]);

  function openConversation(conversation: Conversation) {
    setConversationID(conversation.id);
    setChatTarget(conversation.agent_id ? `agent:${conversation.agent_id}` : 'model:');
    setReasoningEffort('');
    if (workspace) router.replace(`/chat?workspace=${encodeURIComponent(workspace.id)}&conversation=${encodeURIComponent(conversation.id)}`);
  }

  function startNewChat() {
    setConversationID('');
    setMessages([]);
    setError('');
    setChatTarget('model:');
    setReasoningEffort('');
    if (workspace) router.replace(`/chat?workspace=${encodeURIComponent(workspace.id)}&conversation=new`);
  }

  function changeChatTarget(value: string) {
    if (value === chatTarget) return;
    setChatTarget(value);
    setReasoningEffort('');
    setConversationID('');
    setMessages([]);
    setError('');
    hydratedRef.current = '';
    if (workspace) router.replace(`/chat?workspace=${encodeURIComponent(workspace.id)}&conversation=new&target=${encodeURIComponent(value)}`);
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

  async function submit(content: string) {
    const trimmed = content.trim();
    if (!trimmed || streaming || !workspace) return;
    const supportedReasoningEffort = reasoningEfforts.includes(reasoningEffort) ? reasoningEffort : '';
    setDraft('');
    setError('');
    let targetID = conversationID;
    if (!targetID) {
      const result = selectedAgentID
        ? await api.startAgentConversation(selectedAgentID, 'published', workspace.id)
        : await api.createConversation(workspace.id, trimmed.slice(0, 100));
      targetID = result.conversation.id;
      // The optimistic bubbles below are the freshest view of this
      // conversation, so suppress the history fetch this id would trigger.
      hydratedRef.current = targetID;
      setConversationID(targetID);
      setConversations((current) => [result.conversation, ...current.filter((item) => item.id !== result.conversation.id)]);
      router.replace(`/chat?workspace=${encodeURIComponent(workspace.id)}&conversation=${encodeURIComponent(targetID)}`);
    }
    const optimisticUser: Message = {id: `local-${crypto.randomUUID()}`, conversation_id: targetID, role: 'user', content: trimmed, created_at: new Date().toISOString()};
    const optimisticAssistant: Message = {id: `stream-${crypto.randomUUID()}`, conversation_id: targetID, role: 'assistant', content: '', created_at: new Date().toISOString()};
    setMessages((current) => [...current, optimisticUser, optimisticAssistant]);
    setStreaming(true);
    setLiveToolCalls([]);
    setStatus(t('chat.thinking'));
    setOrbState('working');
    try {
      await streamChat(targetID, trimmed, {model: selectedAgentID ? '' : selectedModelID, reasoningEffort: supportedReasoningEffort}, {
        onMeta: () => {
          setStatus(t('chat.writing'));
          setOrbState('composing');
        },
        // Calls arrive twice - running, then settled - so they are held by id
        // and the second arrival replaces the first rather than adding a row.
        onToolCall: (call) => setLiveToolCalls((current) => {
          const index = current.findIndex((item) => item.id === call.id);
          if (index < 0) return [...current, call];
          const next = [...current];
          next[index] = call;
          return next;
        }),
        onStatus: ({stage, message}) => {
          setStatus(message);
          setOrbState(activityOrb(stage));
          if (stage === 'retrieval_failed') setError(message);
        },
        onDelta: (delta) => {
          setOrbState('composing');
          setMessages((current) => current.map((item) => item.id === optimisticAssistant.id ? {...item, content: item.content + delta} : item));
        },
        onDone: ({message}) => {
          setMessages((current) => current.map((item) => item.id === optimisticAssistant.id ? message : item));
          // The saved message carries the calls now; the live copy would only
          // draw them a second time.
          setLiveToolCalls([]);
        },
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

  // Workspace switching lives in the SideNav heading's popover, the way the
  // reference shell puts it, rather than on a separate picker page.
  const conversationMenu: DropdownMenuOption[] = [
    {id: 'new', label: t('chat.newChat'), icon: <Plus size={15} />, onClick: startNewChat},
    ...(conversations.length > 0 ? [{type: 'divider' as const}] : []),
    ...conversations.map((item) => ({
      id: item.id,
      label: item.title,
      icon: item.agent_id ? <Bot size={15} /> : <MessageSquare size={15} />,
      onClick: () => openConversation(item),
    })),
  ];

  const modelTargetOptions = [
    ...(defaultModel ? [{
      value: 'model:',
      label: defaultModel,
      description: `${providerName(availableModels.find((item) => item.id === defaultModel)?.provider)} · ${t('composer.modelDefault')}`,
      icon: providerIcon(availableModels.find((item) => item.id === defaultModel)?.provider),
    }] : []),
    ...availableModels
      .filter((item) => item.id !== defaultModel)
      .map((item) => ({
        value: `model:${item.id}`,
        label: item.id,
        description: providerName(item.provider),
        icon: providerIcon(item.provider),
      })),
  ];
  const agentTargetOptions = agents.map((item) => ({
    value: `agent:${item.id}`,
    label: item.name,
    description: item.model ? `${t('composer.agent')} · ${item.model}` : t('composer.agentNoModel'),
    disabled: !item.model,
    icon: <Avatar name={item.name} size="xsm" src={item.has_avatar_image && workspace ? api.agentAvatarURL(item.id, workspace.id) : undefined} tooltip={false} />,
  }));
  const chatTargetOptions = [
    ...(modelTargetOptions.length ? [{type: 'section' as const, title: t('composer.modelsSection'), options: modelTargetOptions}] : []),
    ...(agentTargetOptions.length ? [{type: 'section' as const, title: t('composer.agentsSection'), options: agentTargetOptions}] : []),
  ];
  const promptSuggestions = selectedAgent?.preset_questions.length ? selectedAgent.preset_questions : suggestions;

  // An empty conversation greets you and puts the question where your eyes
  // already are, rather than at the bottom edge - the reference's arrangement.
  const emptyState = (
    <VStack gap={6} hAlign="center" width="100%">
      {selectedAgent ? (
        <EmptyState
          description={selectedAgent.introduction || t('chat.greetingBody')}
          headingLevel={1}
          icon={<Avatar name={selectedAgent.name} size="xl" src={selectedAgent.has_avatar_image && workspace ? api.agentAvatarURL(selectedAgent.id, workspace.id) : undefined} tooltip={false} />}
          title={selectedAgent.name}
        />
      ) : (
        <VStack gap={1} hAlign="center">
          {user?.name ? <Text color="secondary" type="supporting">{t('chat.hello', {name: user.name})}</Text> : null}
          <Text type="large">{t('chat.greeting')}</Text>
        </VStack>
      )}
    </VStack>
  );

  const promptCards = (
      <VStack gap={2} width="100%">
        {promptSuggestions.map((suggestion) => (
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
  );

  const composer = (
    <VStack gap={2}>
      {error && <Banner isDismissable onDismiss={() => setError('')} status="error" title={error} />}
      <ChatComposer
        footerActions={
          <HStack gap={2} vAlign="center">
            <StatusLabel label={workspace?.model_configured ? t('chat.modelReady') : t('chat.modelMissing')} variant={workspace?.model_configured ? 'success' : 'warning'} />
            {chatTargetOptions.length > 0 ? (
              <Selector
                hasSearch
                isLabelHidden
                label={t('composer.target')}
                onChange={changeChatTarget}
                options={chatTargetOptions}
                size="sm"
                value={chatTarget}
                variant="ghost"
              />
            ) : (
              <Text color="secondary" type="supporting">{workspace?.model_alias || t('composer.model')}</Text>
            )}
            {reasoningEfforts.length > 0 ? (
              <Selector
                isLabelHidden
                label={t('composer.reasoning')}
                onChange={setReasoningEffort}
                options={[
                  {value: '', label: t('composer.reasoningAuto')},
                  ...reasoningEfforts.map((effort) => ({value: effort, label: reasoningLabels[effort] ?? effort})),
                ]}
                size="sm"
                value={reasoningEffort}
                variant="ghost"
              />
            ) : null}
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
        end={isRecentOpen ? (
          <LayoutPanel hasDivider label={t('chat.recentChats')} padding={4} role="complementary" width={340}>
            <VStack gap={3} width="100%">
              <HStack hAlign="between" vAlign="center" width="100%">
                <Text type="label">{t('chat.recentChats')}</Text>
                <IconButton icon={<X size={16} />} label={t('conv.closeRecent')} onClick={() => setIsRecentOpen(false)} size="sm" variant="ghost" />
              </HStack>
              {conversations.length === 0 ? (
                <EmptyState description={t('chat.empty')} isCompact title="—" />
              ) : (
                <VStack gap={1} width="100%">
                  {conversations.map((item) => (
                    <HStack gap={1} key={item.id} vAlign="center" width="100%">
                      <ClickableCard
                        /* The title can be long; without this the row grows
                           past the panel and drags a scrollbar with it. */
                        className="min-w-0"
                        label={item.title}
                        onClick={() => openConversation(item)}
                        padding={3}
                        width="100%"
                      >
                        <VStack gap={0}>
                          <Text maxLines={1} type="label">{item.title}</Text>
                          <Text color="secondary" type="supporting">
                            <Timestamp format="relative" value={item.created_at} />
                          </Text>
                        </VStack>
                      </ClickableCard>
                      <DropdownMenu
                        alignment="end"
                        button={{icon: <MoreHorizontal size={15} />, isIconOnly: true, label: t('conv.options'), size: 'sm', variant: 'ghost'}}
                        hasChevron={false}
                        items={[
                          {icon: <Pencil size={15} />, label: t('conv.rename'), onClick: () => setRenaming(item)},
                          {icon: <Trash2 size={15} />, label: t('conv.delete'), onClick: () => setDeleting(item), variant: 'destructive' as const},
                        ]}
                      />
                    </HStack>
                  ))}
                </VStack>
              )}
            </VStack>
          </LayoutPanel>
        ) : undefined}
        height="fill"
        header={
          <LayoutHeader hasDivider>
            <Toolbar
              label={t('chat.toolbar')}
              startContent={
                <HStack gap={2} vAlign="center">
                  <DropdownMenu
                    alignment="start"
                    button={{label: activeConversation?.title ?? t('chat.newChat'), size: 'sm', variant: 'ghost'}}
                    hasChevron
                    items={conversationMenu}
                    menuWidth={264}
                  />
                  {/* What you are talking to belongs where you can see it. It
                      was only in the composer footer, so the answer's author
                      was out of view for the whole conversation. */}
                  {chatTargetLabel ? <Token label={chatTargetLabel} size="sm" /> : null}
                </HStack>
              }
              endContent={
                /* Starting a new conversation is an action on the transcript,
                   so it sits with the transcript rather than in the column
                   that lists who to talk to. Files and recent chats are shells
                   for now - see docs/ui_backlog.md. */
                <HStack gap={1} vAlign="center">
                  <Button icon={<Icon icon={SquarePen} size="sm" />} label={t('chat.newChat')} onClick={startNewChat} size="sm" variant="ghost" />
                  <Button icon={<Icon icon={FolderOpen} size="sm" />} isDisabled isIconOnly label={t('chat.files')} size="sm" variant="ghost" />
                  <Button icon={<Icon icon={History} size="sm" />} isIconOnly label={t('chat.recentChats')} onClick={() => setIsRecentOpen((open) => !open)} size="sm" variant="ghost" />
                </HStack>
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
              {messages.length === 0 ? (
                <VStack gap={6} hAlign="center" height="100%" padding={6} vAlign="center" width="100%">
                  {emptyState}
                  <VStack gap={6} width="100%">
                    {composer}
                    {promptCards}
                  </VStack>
                </VStack>
              ) : (
              <ChatLayout composer={composer}>
                {(
                  <ChatMessageList isStreaming={streaming}>
                    {messages.map((message) => message.role === 'user' ? (
                      <ChatMessage key={message.id} sender="user">
                        <ChatMessageBubble metadata={<ChatMessageMetadata timestamp={<Timestamp format="time" value={message.created_at} />} />}>
                          {message.content}
                        </ChatMessageBubble>
                      </ChatMessage>
                    ) : (() => {
                      const isActiveStream = streaming && message.id.startsWith('stream-');
                      return (
                      <ChatMessage
                        avatar={<Avatar
                          name={selectedAgent?.name || workspace?.icon || workspace?.name || 'Cosmo'}
                          size="md"
                          src={selectedAgent?.has_avatar_image && workspace
                            ? api.agentAvatarURL(selectedAgent.id, workspace.id)
                            : workspace?.has_icon_image ? api.workspaceIconURL(workspace.id) : undefined}
                        />}
                        key={message.id}
                        sender="assistant"
                      >
                        <ChatMessageBubble
                          metadata={message.content ? <ChatMessageMetadata footer={message.model || workspace?.model_alias} timestamp={<Timestamp format="time" value={message.created_at} />} /> : undefined}
                          name={selectedAgent?.name || 'Cosmo'}
                          variant="ghost"
                        >
                          {message.content
                            ? <VStack gap={3}>
                              <ToolCallTrail calls={isActiveStream ? liveToolCalls : message.tool_calls ?? []} />
                              <Markdown density="compact" isStreaming={isActiveStream} headingLevelStart={3}>
                                {answerForDisplay(message.content, message.citations ?? [], isActiveStream)}
                              </Markdown>
                              {isActiveStream ? null : <CitationList citations={message.citations ?? []} showEmpty />}
                            </VStack>
                            : (streaming ? <VStack gap={3}>
                              <ToolCallTrail calls={liveToolCalls} />
                              <HStack gap={2} vAlign="center"><ThinkingOrb size={20} state={orbState} /><Text color="secondary" type="supporting">{status}</Text></HStack>
                              <CitationList citations={message.citations ?? []} />
                            </VStack> : '')}
                        </ChatMessageBubble>
                      </ChatMessage>
                      );
                    })())}
                  </ChatMessageList>
                )}
              </ChatLayout>
              )}
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

    </>
  );
}

function providerIcon(provider?: string) {
  switch ((provider ?? '').toLowerCase()) {
    case 'openai': return <Sparkles size={16} />;
    case 'anthropic': return <Brain size={16} />;
    case 'google':
    case 'gemini': return <Gem size={16} />;
    case 'azure': return <Cloud size={16} />;
    default: return <Cpu size={16} />;
  }
}

function providerName(provider?: string) {
  if (!provider) return 'Model Gateway';
  const names: Record<string, string> = {
    openai: 'OpenAI',
    anthropic: 'Anthropic',
    google: 'Google',
    gemini: 'Google Gemini',
    azure: 'Azure AI',
    cohere: 'Cohere',
  };
  return names[provider.toLowerCase()] ?? provider;
}

function CitationList({citations, showEmpty = false}: {citations: Citation[]; showEmpty?: boolean}) {
  if (citations.length === 0) {
    return showEmpty ? <Text color="secondary" type="supporting">Không dùng tài liệu workspace cho câu trả lời này.</Text> : null;
  }
  const groups = groupCitations(citations);
  const list = (
      <List density="compact">
        {groups.map((group) => (
          <Item
            align="start"
            density="compact"
            description={group.description}
            key={`${group.kbID}:${group.documentID}`}
            label={
              <Link href={api.documentOriginalURL(group.kbID, group.documentID)} isExternalLink isStandalone weight="medium">
                {group.title}
              </Link>
            }
          />
        ))}
      </List>
  );
  if (groups.length <= 3) {
    return <VStack gap={1}><Text color="secondary" type="label">Tài liệu tham khảo ({groups.length})</Text>{list}</VStack>;
  }
  return <Collapsible defaultIsOpen={false} trigger={<Text color="secondary" type="label">Tài liệu tham khảo ({groups.length})</Text>}>{list}</Collapsible>;
}

function groupCitations(citations: Citation[]) {
  const groups = new Map<string, {kbID: string; documentID: string; title: string; pages: string[]; sections: string[]; source: string}>();
  citations.forEach((citation) => {
    const key = `${citation.kb_id}:${citation.document_id}`;
    const group = groups.get(key) ?? {
      kbID: citation.kb_id,
      documentID: citation.document_id,
      title: citation.title || citation.source,
      pages: [],
      sections: [],
      source: citation.source,
    };
    if (citation.page && !group.pages.includes(citation.page)) group.pages.push(citation.page);
    if (citation.section && !group.sections.includes(citation.section)) group.sections.push(citation.section);
    groups.set(key, group);
  });
  return [...groups.values()].map((group) => ({
    ...group,
    description: [
      summarizeValues(group.sections, 2, 'mục'),
      group.pages.length ? `Trang ${summarizeValues([...group.pages].sort(naturalOrder), 4, 'trang')}` : '',
      !group.sections.length && !group.pages.length ? group.source : '',
    ].filter(Boolean).join(' · '),
  }));
}

function naturalOrder(left: string, right: string) {
  return left.localeCompare(right, 'vi', {numeric: true});
}

function summarizeValues(values: string[], visibleCount: number, noun: string) {
  if (values.length <= visibleCount) return values.join(', ');
  return `${values.slice(0, visibleCount).join(', ')} và ${values.length - visibleCount} ${noun} khác`;
}

// Retrieval indexes such as [3] identify internal passages, not pages or list
// items. Keep them in the stored answer so the backend can audit which
// passages were used, but remove them from the reader-facing copy. The compact
// document links below are the stable, understandable citation surface.
function answerForDisplay(content: string, citations: Citation[], hideAllIndexes = false) {
  const indexes = new Set(citations.map((citation) => citation.index));
  if (indexes.size === 0 && !hideAllIndexes) return content;
  return content
    .replace(/\[(\d+)]/g, (marker, value: string) => hideAllIndexes || indexes.has(Number(value)) ? '' : marker)
    .replace(/[ \t]+$/gm, '')
    .replace(/[ \t]+([.,;:!?])/g, '$1');
}

