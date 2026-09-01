'use client';

import {Suspense, useCallback, useEffect, useState} from 'react';
import {useParams, useRouter, useSearchParams} from 'next/navigation';
import {ArrowLeft, Plus, SendHorizontal, Trash2} from 'lucide-react';
import {Avatar} from '@astryxdesign/core/Avatar';
import {Banner} from '@astryxdesign/core/Banner';
import {Button} from '@astryxdesign/core/Button';
import {CheckboxList, CheckboxListItem} from '@astryxdesign/core/CheckboxList';
import {IconButton} from '@astryxdesign/core/IconButton';
import {HStack, Layout, LayoutContent, LayoutHeader, VStack} from '@astryxdesign/core/Layout';
import {Selector} from '@astryxdesign/core/Selector';
import {Switch} from '@astryxdesign/core/Switch';
import {Tab, TabList} from '@astryxdesign/core/TabList';
import {Text} from '@astryxdesign/core/Text';
import {Card} from '@astryxdesign/core/Card';
import {Divider} from '@astryxdesign/core/Divider';
import {TextArea} from '@astryxdesign/core/TextArea';
import {TextInput} from '@astryxdesign/core/TextInput';
import {Toolbar} from '@astryxdesign/core/Toolbar';
import {Agent, AgentUpdate, api, KnowledgeBase, Message, streamChat} from '../../lib/api';
import {useTranslation} from '../../lib/i18n';

const MAX_KNOWLEDGE_BASES = 5;

function AgentEditorView() {
  const t = useTranslation();
  const router = useRouter();
  const params = useParams<{agentID: string}>();
  const search = useSearchParams();
  const agentID = params.agentID;
  const workspaceID = search.get('workspace') ?? '';

  const [agent, setAgent] = useState<Agent | null>(null);
  const [bases, setBases] = useState<KnowledgeBase[]>([]);
  const [models, setModels] = useState<string[]>([]);
  const [tab, setTab] = useState('prompt');
  const [error, setError] = useState('');
  const [isSaving, setIsSaving] = useState(false);
  const [savedAt, setSavedAt] = useState(0);

  useEffect(() => {
    api.agent(agentID, workspaceID)
      .then((result) => setAgent(result.agent))
      .catch((caught) => setError(caught instanceof Error ? caught.message : ''));
    // Only what the workspace has installed: installing is what puts a base at
    // the workspace's disposal, and an agent draws on that same pool.
    api.knowledgeBases(workspaceID)
      .then((result) => setBases(result.knowledge_bases.filter((base) => base.is_mounted)))
      .catch(() => setBases([]));
    // The agent picks from the same list the composer offers, so a model it
    // is saved with is one the workspace can actually run.
    if (workspaceID) {
      api.workspaceModels(workspaceID)
        .then((result) => setModels(result.models))
        .catch(() => setModels([]));
    }
  }, [agentID, workspaceID]);

  // The editor edits a local copy and saves explicitly, so a slow request
  // never fights what is being typed.
  const patch = useCallback((changes: Partial<Agent>) => {
    setAgent((current) => (current ? {...current, ...changes} : current));
  }, []);

  async function save(changes: AgentUpdate) {
    if (!agent) return;
    setIsSaving(true);
    setError('');
    try {
      const result = await api.updateAgent(agent.id, changes, workspaceID);
      setAgent(result.agent);
      setSavedAt(Date.now());
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : t('agent.saveFailed'));
    } finally {
      setIsSaving(false);
    }
  }

  function saveAll() {
    if (!agent) return;
    void save({
      name: agent.name,
      introduction: agent.introduction,
      visibility: agent.visibility,
      model: agent.model,
      system_prompt: agent.system_prompt,
      opening_line: agent.opening_line,
      preset_questions: agent.preset_questions,
      has_suggested_questions: agent.has_suggested_questions,
      is_memory_enabled: agent.is_memory_enabled,
      knowledge_base_ids: agent.knowledge_base_ids,
    });
  }

  function setPresetQuestion(index: number, value: string) {
    if (!agent) return;
    const next = [...agent.preset_questions];
    next[index] = value;
    patch({preset_questions: next});
  }

  if (!agent) {
    return (
      <Layout
        content={<LayoutContent padding={6}>{error ? <Banner status="error" title={error} /> : null}</LayoutContent>}
        height="fill"
      />
    );
  }

  const isReadOnly = !agent.is_editable;
  const modelOptions = models.map((model) => ({label: model, value: model}));
  const isAtKnowledgeLimit = agent.knowledge_base_ids.length >= MAX_KNOWLEDGE_BASES;

  return (
    <Layout
      contentWidth={960}
      header={
        <LayoutHeader hasDivider>
          <Toolbar
            endContent={
              isReadOnly ? null : (
                <Button isLoading={isSaving} label={t('common.save')} onClick={saveAll} size="sm" variant="primary" />
              )
            }
            label={agent.name}
            startContent={
              <HStack gap={2} vAlign="center">
                <IconButton
                  icon={<ArrowLeft size={16} />}
                  label={t('agent.title')}
                  onClick={() => router.push(`/agents${workspaceID ? `?workspace=${encodeURIComponent(workspaceID)}` : ''}`)}
                  size="sm"
                  variant="ghost"
                />
                <Avatar name={agent.avatar || agent.name} size="sm" />
                <Text type="label" weight="semibold">{agent.name}</Text>
                {savedAt > 0 && !isSaving ? <Text color="secondary" type="supporting">{t('agent.saved')}</Text> : null}
              </HStack>
            }
          />
        </LayoutHeader>
      }
      end={<AgentChatPanel agent={agent} t={t} workspaceID={workspaceID} />}
      height="fill"
      content={
        <LayoutContent padding={6}>
          <VStack gap={5}>
            {error ? <Banner isDismissable onDismiss={() => setError('')} status="error" title={error} /> : null}
            {isReadOnly ? <Banner status="info" title={t('agent.readOnly')} /> : null}

            <TabList hasDivider onChange={setTab} value={tab}>
              <Tab label={t('agent.tabPrompt')} value="prompt" />
              <Tab label={t('agent.tabKnowledge')} value="knowledge" />
              <Tab label={t('agent.tabExperience')} value="experience" />
            </TabList>

            {tab === 'prompt' ? (
              <VStack gap={4}>
                <TextInput
                  isDisabled={isReadOnly}
                  label={t('agent.name')}
                  onChange={(value) => patch({name: value})}
                  value={agent.name}
                  width="100%"
                />
                <TextArea
                  isDisabled={isReadOnly}
                  label={t('agent.introduction')}
                  maxLength={512}
                  onChange={(value) => patch({introduction: value})}
                  rows={2}
                  value={agent.introduction}
                  width="100%"
                />
                <Selector
                  isDisabled={isReadOnly || modelOptions.length === 0}
                  label={t('agent.model')}
                  onChange={(value) => patch({model: value})}
                  options={modelOptions}
                  placeholder={t('agent.modelUnset')}
                  value={agent.model}
                  width="100%"
                />
                <TextArea
                  isDisabled={isReadOnly}
                  label={t('agent.systemPrompt')}
                  onChange={(value) => patch({system_prompt: value})}
                  placeholder={t('agent.systemPromptPlaceholder')}
                  rows={12}
                  value={agent.system_prompt}
                  width="100%"
                />
                <Selector
                  isDisabled={isReadOnly}
                  label={t('agent.visibility')}
                  onChange={(value) => patch({visibility: value === 'workspace' ? 'workspace' : 'private'})}
                  options={[
                    {label: t('agent.visibilityPrivate'), value: 'private'},
                    {label: t('agent.visibilityWorkspace'), value: 'workspace'},
                  ]}
                  value={agent.visibility}
                  width="100%"
                />
              </VStack>
            ) : null}

            {tab === 'knowledge' ? (
              <VStack gap={4}>
                <Switch
                  isDisabled={isReadOnly}
                  label={t('agent.memory')}
                  onChange={(checked) => patch({is_memory_enabled: checked})}
                  value={agent.is_memory_enabled}
                />
                <VStack gap={2}>
                  <HStack gap={2} hAlign="between" vAlign="center">
                    <Text type="label" weight="semibold">{t('agent.knowledgeBases')}</Text>
                    <Text color="secondary" type="supporting">
                      {`${agent.knowledge_base_ids.length}/${MAX_KNOWLEDGE_BASES}`}
                    </Text>
                  </HStack>
                  {bases.length === 0 ? (
                    <Text color="secondary" type="supporting">{t('agent.knowledgeNone')}</Text>
                  ) : (
                    <CheckboxList
                      isLabelHidden
                      label={t('agent.knowledgeBases')}
                      onChange={(values) => patch({knowledge_base_ids: values.slice(0, MAX_KNOWLEDGE_BASES)})}
                      value={agent.knowledge_base_ids}
                      width="100%"
                    >
                      {bases.map((base) => (
                        <CheckboxListItem
                          description={base.description}
                          isDisabled={isReadOnly || (!agent.knowledge_base_ids.includes(base.id) && isAtKnowledgeLimit)}
                          key={base.id}
                          label={base.name}
                          value={base.id}
                        />
                      ))}
                    </CheckboxList>
                  )}
                </VStack>
              </VStack>
            ) : null}

            {tab === 'experience' ? (
              <VStack gap={4}>
                <TextArea
                  isDisabled={isReadOnly}
                  label={t('agent.openingLine')}
                  onChange={(value) => patch({opening_line: value})}
                  placeholder={t('agent.openingLinePlaceholder')}
                  rows={4}
                  value={agent.opening_line}
                  width="100%"
                />
                <VStack gap={2}>
                  <HStack gap={2} hAlign="between" vAlign="center">
                    <Text type="label" weight="semibold">{t('agent.presetQuestions')}</Text>
                    <Text color="secondary" type="supporting">{`${agent.preset_questions.length}/10`}</Text>
                  </HStack>
                  {agent.preset_questions.map((question, index) => (
                    <HStack gap={2} key={index} vAlign="center" width="100%">
                      <TextInput
                        isDisabled={isReadOnly}
                        isLabelHidden
                        label={`${t('agent.presetQuestions')} ${index + 1}`}
                        onChange={(value) => setPresetQuestion(index, value)}
                        value={question}
                        width="100%"
                      />
                      <IconButton
                        icon={<Trash2 size={14} />}
                        isDisabled={isReadOnly}
                        label={t('conv.delete')}
                        onClick={() => patch({preset_questions: agent.preset_questions.filter((_, at) => at !== index)})}
                        size="sm"
                        variant="ghost"
                      />
                    </HStack>
                  ))}
                  <HStack hAlign="start">
                    <Button
                      isDisabled={isReadOnly || agent.preset_questions.length >= 10}
                      label={t('agent.presetAdd')}
                      onClick={() => patch({preset_questions: [...agent.preset_questions, '']})}
                      size="sm"
                      variant="secondary"
                    />
                  </HStack>
                </VStack>
                <Switch
                  isDisabled={isReadOnly}
                  label={t('agent.suggestedQuestions')}
                  onChange={(checked) => patch({has_suggested_questions: checked})}
                  value={agent.has_suggested_questions}
                />
              </VStack>
            ) : null}
          </VStack>
        </LayoutContent>
      }
    />
  );
}

// useSearchParams reads the request URL, so the route cannot be prerendered
// without a boundary to fall back to.
export default function AgentEditorPage() {
  return (
    <Suspense fallback={null}>
      <AgentEditorView />
    </Suspense>
  );
}


// The agent's own chat. It writes conversations stamped with the agent id, and
// reaches the model through the very endpoint the general chat uses, so a
// reply here goes through the same retrieval, citations and streaming.
function AgentChatPanel({agent, t, workspaceID}: {agent: Agent; t: ReturnType<typeof useTranslation>; workspaceID: string}) {
  const [conversationID, setConversationID] = useState('');
  const [messages, setMessages] = useState<Message[]>([]);
  const [draft, setDraft] = useState('');
  const [streamed, setStreamed] = useState('');
  const [isSending, setIsSending] = useState(false);
  const [chatError, setChatError] = useState('');

  useEffect(() => {
    api.agentConversations(agent.id, workspaceID)
      .then((result) => {
        const latest = result.conversations[0];
        if (!latest) return;
        setConversationID(latest.id);
        return api.messages(latest.id).then((loaded) => setMessages(loaded.messages));
      })
      .catch(() => undefined);
  }, [agent.id, workspaceID]);

  async function startNew() {
    setChatError('');
    try {
      const result = await api.startAgentConversation(agent.id, workspaceID);
      setConversationID(result.conversation.id);
      setMessages([]);
    } catch (caught) {
      setChatError(caught instanceof Error ? caught.message : t('agent.chatFailed'));
    }
  }

  async function send() {
    const content = draft.trim();
    if (!content || isSending) return;
    setChatError('');
    setIsSending(true);
    setDraft('');
    setStreamed('');
    try {
      let target = conversationID;
      if (!target) {
        const started = await api.startAgentConversation(agent.id, workspaceID);
        target = started.conversation.id;
        setConversationID(target);
      }
      setMessages((current) => [...current, {id: `local_${Date.now()}`, conversation_id: target, role: 'user', content, created_at: new Date().toISOString()} as Message]);
      await streamChat(target, content, {}, {
        onDelta: (delta) => setStreamed((current) => current + delta),
        onDone: (data) => {
          setMessages((current) => [...current, data.message]);
          setStreamed('');
        },
      });
    } catch (caught) {
      setChatError(caught instanceof Error ? caught.message : t('agent.chatFailed'));
    } finally {
      setIsSending(false);
    }
  }

  return (
    <VStack gap={3} height="100%" padding={4} width={420}>
      <HStack gap={2} hAlign="between" vAlign="center">
        <Text type="label" weight="semibold">{t('agent.chat')}</Text>
        <Button icon={<Plus size={14} />} label={t('agent.chatNew')} onClick={() => void startNew()} size="sm" variant="ghost" />
      </HStack>
      <Divider />
      {!agent.model ? <Banner status="warning" title={t('agent.chatNoModel')} /> : null}
      {chatError ? <Banner isDismissable onDismiss={() => setChatError('')} status="error" title={chatError} /> : null}
      <VStack gap={3} isScrollable height="100%" width="100%">
        {agent.opening_line && messages.length === 0 ? (
          <Card padding={3} width="100%"><Text type="body">{agent.opening_line}</Text></Card>
        ) : null}
        {messages.map((message) => (
          <Card key={message.id} padding={3} width="100%">
            <VStack gap={1}>
              <Text color="secondary" type="supporting">{message.role === 'user' ? agent.owner_name || 'You' : agent.name}</Text>
              <Text type="body">{message.content}</Text>
            </VStack>
          </Card>
        ))}
        {streamed ? (
          <Card padding={3} width="100%">
            <VStack gap={1}>
              <Text color="secondary" type="supporting">{agent.name}</Text>
              <Text type="body">{streamed}</Text>
            </VStack>
          </Card>
        ) : null}
        {agent.preset_questions.length > 0 && messages.length === 0 ? (
          <VStack gap={2} width="100%">
            {agent.preset_questions.map((question, index) => (
              <Button key={index} label={question} onClick={() => setDraft(question)} size="sm" variant="secondary" />
            ))}
          </VStack>
        ) : null}
      </VStack>
      <HStack gap={2} vAlign="end" width="100%">
        <TextArea
          isLabelHidden
          label={t('agent.chatPlaceholder')}
          onChange={setDraft}
          placeholder={t('agent.chatPlaceholder')}
          rows={3}
          value={draft}
          width="100%"
        />
        <IconButton
          icon={<SendHorizontal size={16} />}
          isDisabled={!draft.trim() || isSending || !agent.model}
          isLoading={isSending}
          label={t('agent.chatSend')}
          onClick={() => void send()}
          variant="primary"
        />
      </HStack>
    </VStack>
  );
}
