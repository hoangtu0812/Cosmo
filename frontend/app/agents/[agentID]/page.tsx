'use client';

import {Suspense, useCallback, useEffect, useRef, useState} from 'react';
import {useParams, useRouter, useSearchParams} from 'next/navigation';
import {ArrowLeft, Braces, History, MoreHorizontal, Pencil, Plus, SlidersHorizontal, Trash2, Wrench} from 'lucide-react';
import {Avatar} from '@astryxdesign/core/Avatar';
import {Banner} from '@astryxdesign/core/Banner';
import {Button} from '@astryxdesign/core/Button';
import {CheckboxList, CheckboxListItem} from '@astryxdesign/core/CheckboxList';
import {IconButton} from '@astryxdesign/core/IconButton';
import {Dialog, DialogHeader} from '@astryxdesign/core/Dialog';
import {HStack, Layout, LayoutContent, LayoutFooter, LayoutHeader, VStack} from '@astryxdesign/core/Layout';
import {Selector} from '@astryxdesign/core/Selector';
import {Switch} from '@astryxdesign/core/Switch';
import {SegmentedControl, SegmentedControlItem} from '@astryxdesign/core/SegmentedControl';
import {Tab, TabList} from '@astryxdesign/core/TabList';
import {Text} from '@astryxdesign/core/Text';
import {Card} from '@astryxdesign/core/Card';
import {ChatComposer} from '@astryxdesign/core/Chat';
import {Popover} from '@astryxdesign/core/Popover';
import {Token} from '@astryxdesign/core/Token';
import {DropdownMenu} from '@astryxdesign/core/DropdownMenu';
import {EmptyState} from '@astryxdesign/core/EmptyState';
import {Item} from '@astryxdesign/core/Item';
import {List} from '@astryxdesign/core/List';
import {useEntryAnimation, useStreamingText} from '@astryxdesign/core/hooks';
import {Divider} from '@astryxdesign/core/Divider';
import {Markdown} from '@astryxdesign/core/Markdown';
import {TextArea} from '@astryxdesign/core/TextArea';
import {TextInput} from '@astryxdesign/core/TextInput';
import {Toolbar} from '@astryxdesign/core/Toolbar';
import {StatusLabel} from '../../components/StatusLabel';
import {AnswerWithToolCalls} from '../../components/AnswerWithToolCalls';
import {CopyButton} from '../../components/CopyButton';
import {APIError, Agent, AgentVersion, Conversation, KnowledgeBase, Message, MessageToolCall, RunStep, Tool, api, streamChat} from '../../lib/api';
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
        .then((result) => setModels(result.models.map((item) => item.id)))
        .catch(() => setModels([]));
    }
  }, [agentID, workspaceID]);

  // The editor edits a local copy and saves explicitly, so a slow request
  // never fights what is being typed.
  const patch = useCallback((changes: Partial<Agent>) => {
    setAgent((current) => (current ? {...current, ...changes} : current));
    setIsDirty(true);
  }, []);

  // The draft is saved for the reader rather than by them, so `dirty` tracks
  // whether anything is still unsent and the timer restarts on every edit.
  const [isDirty, setIsDirty] = useState(false);
  const [isPublishing, setIsPublishing] = useState(false);
  const [isPublishOpen, setIsPublishOpen] = useState(false);
  const [workspaceTools, setWorkspaceTools] = useState<Tool[]>([]);
  const [attachedToolIDs, setAttachedToolIDs] = useState<string[]>([]);
  const [changelog, setChangelog] = useState('');
  const [isHistoryOpen, setIsHistoryOpen] = useState(false);
  const [versions, setVersions] = useState<AgentVersion[]>([]);
  const [isDetailsOpen, setIsDetailsOpen] = useState(false);
  const [isStale, setIsStale] = useState(false);
  const latest = useRef<Agent | null>(null);
  const savingDraft = useRef(false);
  latest.current = agent;

  const saveDraft = useCallback(async () => {
    const current = latest.current;
    if (!current || !current.is_editable || savingDraft.current) return;
    savingDraft.current = true;
    setIsSaving(true);
    setError('');
    try {
      const result = await api.updateAgent(current.id, {
        name: current.name,
        introduction: current.introduction,
        visibility: current.visibility,
        model: current.model,
        system_prompt: current.system_prompt,
        opening_line: current.opening_line,
        preset_questions: current.preset_questions,
        has_suggested_questions: current.has_suggested_questions,
        is_memory_enabled: current.is_memory_enabled,
        knowledge_base_ids: current.knowledge_base_ids,
        draft_revision: current.draft_revision,
      }, workspaceID);
      // Only the fields the server owns are taken back. Replacing the whole
      // agent would overwrite whatever is being typed while the save is in
      // flight, which is exactly what an autosave must never do.
      const editedDuringSave = latest.current !== current;
      if (latest.current) latest.current = {...latest.current, draft_revision: result.agent.draft_revision};
      setAgent((live) => live ? {
        ...live,
        draft_revision: result.agent.draft_revision,
        published_version: result.agent.published_version,
        published_version_id: result.agent.published_version_id,
        has_unpublished_changes: result.agent.has_unpublished_changes,
        updated_at: result.agent.updated_at,
      } : live);
      setIsDirty(editedDuringSave);
      setSavedAt(Date.now());
    } catch (caught) {
      // A conflict means someone else saved; the draft on screen is no longer
      // built on what is stored, so saving again would clobber their work.
      if (caught instanceof APIError && caught.status === 409) setIsStale(true);
      setError(caught instanceof Error ? caught.message : t('agent.saveFailed'));
    } finally {
      savingDraft.current = false;
      setIsSaving(false);
    }
  }, [t, workspaceID]);

  // The tool list and the attachment are read together: the tab needs both to
  // say anything, and neither changes while the editor is open unless this
  // screen changes it.
  useEffect(() => {
    if (!agentID) return;
    let cancelled = false;
    Promise.all([api.tools(workspaceID), api.agentTools(agentID, workspaceID)])
      .then(([toolResult, attachedResult]) => {
        if (cancelled) return;
        setWorkspaceTools(toolResult.tools);
        setAttachedToolIDs(attachedResult.tool_ids);
      })
      .catch(() => undefined);
    return () => { cancelled = true; };
  }, [agentID, workspaceID]);

  // Attaching is one fact, not a form: it saves when the switch moves, and the
  // list is set from what the server confirms rather than from what was asked.
  async function setToolAttached(toolID: string, isAttached: boolean) {
    const current = latest.current;
    if (!current || isStale || savingDraft.current) return;
    savingDraft.current = true;
    setIsSaving(true);
    const next = isAttached
      ? [...attachedToolIDs, toolID]
      : attachedToolIDs.filter((item) => item !== toolID);
    setAttachedToolIDs(next);
    try {
      const result = await api.setAgentTools(agentID, next, current.draft_revision, workspaceID);
      setAttachedToolIDs(result.tool_ids);
      if (latest.current) latest.current = {...latest.current, draft_revision: result.draft_revision};
      setAgent((live) => live ? {...live, draft_revision: result.draft_revision, has_unpublished_changes: true} : live);
    } catch (caught) {
      setAttachedToolIDs(attachedToolIDs);
      if (caught instanceof APIError && caught.status === 409) setIsStale(true);
      setError(caught instanceof Error ? caught.message : t('tool.saveFailed'));
    } finally {
      savingDraft.current = false;
      setIsSaving(false);
    }
  }

  useEffect(() => {
    if (!isDirty || isStale || isSaving) return;
    const timer = setTimeout(() => void saveDraft(), 1200);
    return () => clearTimeout(timer);
  }, [agent, isDirty, isStale, isSaving, saveDraft]);

  async function publish() {
    if (!agent) return;
    setIsPublishing(true);
    setError('');
    try {
      // Anything unsent is written first, so what gets frozen is what is on
      // screen rather than the last autosave.
      if (isDirty) await saveDraft();
      const result = await api.publishAgent(agent.id, changelog.trim(), workspaceID);
      setAgent((live) => live ? {
        ...live,
        published_version: result.agent.published_version,
        published_version_id: result.agent.published_version_id,
        has_unpublished_changes: result.agent.has_unpublished_changes,
      } : live);
      setIsPublishOpen(false);
      setChangelog('');
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : t('agent.publishFailed'));
    } finally {
      setIsPublishing(false);
    }
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
  // Three states, in the order a reader meets them: saving now, saved a moment
  // ago, or nothing written yet this session.
  const draftStatus = isSaving
    ? t('agent.saving')
    : savedAt > 0
      ? t('agent.autoSaved')
      : '';
  const modelOptions = models.map((model) => ({label: model, value: model}));
  const isAtKnowledgeLimit = agent.knowledge_base_ids.length >= MAX_KNOWLEDGE_BASES;

  // The header belongs to the configuration column, not to the window: the
  // debug panel is the other half of the screen and runs the full height, as
  // the reference has it.
  const header = (
        <LayoutHeader hasDivider>
          <Toolbar
            endContent={
              isReadOnly ? null : (
                <HStack gap={3} vAlign="center">
                  <Text color="secondary" type="supporting">{draftStatus}</Text>
                  <Text color={agent.has_unpublished_changes ? 'accent' : 'secondary'} type="supporting">
                    {agent.has_unpublished_changes
                      ? t('agent.unpublished')
                      : t('agent.publishedVersion', {version: String(agent.published_version)})}
                  </Text>
                  <Button
                    isDisabled={!agent.has_unpublished_changes || isStale}
                    isLoading={isPublishing}
                    label={t('agent.publish')}
                    onClick={() => setIsPublishOpen(true)}
                    size="sm"
                    variant="primary"
                  />
                </HStack>
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
                <Avatar
                  name={agent.avatar || agent.name}
                  size="sm"
                  src={agent.has_avatar_image ? api.agentAvatarURL(agent.id, workspaceID, agent.updated_at) : undefined}
                />
                {/* Name and introduction belong to the agent's identity, so they
                    sit in the header and leave the Prompt tab to the prompt. */}
                <VStack gap={0}>
                  <Text type="label">{agent.name}</Text>
                  {agent.introduction ? (
                    <Text color="secondary" maxLines={1} type="supporting">{agent.introduction}</Text>
                  ) : null}
                </VStack>
                {isReadOnly ? null : (
                  <DropdownMenu
                    alignment="start"
                    button={{icon: <MoreHorizontal size={15} />, isIconOnly: true, label: t('agent.moreActions'), size: 'sm', variant: 'ghost'}}
                    hasChevron={false}
                    items={[
                      {icon: <Pencil size={15} />, label: t('agent.editDetails'), onClick: () => setIsDetailsOpen(true)},
                      {icon: <History size={15} />, label: t('agent.versions'), onClick: () => {
                        setIsHistoryOpen(true);
                        api.agentVersions(agent.id, workspaceID)
                          .then((result) => setVersions(result.versions))
                          .catch(() => setVersions([]));
                      }},
                    ]}
                  />
                )}
              </HStack>
            }
          />
        </LayoutHeader>
  );

  return (
    <>
    <Layout
      end={<AgentChatPanel agent={agent} t={t} workspaceID={workspaceID} />}
      height="fill"
      content={
        <Layout header={header} height="fill" content={
        <LayoutContent padding={6}>
          <VStack gap={5}>
            {isStale ? (
              <Banner
                endContent={<Button label={t('agent.reload')} onClick={() => window.location.reload()} size="sm" variant="secondary" />}
                status="warning"
                title={t('agent.staleDraft')}
              />
            ) : null}
            {error && !isStale ? <Banner isDismissable onDismiss={() => setError('')} status="error" title={error} /> : null}
            {isReadOnly ? <Banner status="info" title={t('agent.readOnly')} /> : null}

            <HStack gap={4} vAlign="center" width="100%">
              <Selector
                isDisabled={isReadOnly || modelOptions.length === 0}
                isLabelHidden
                label={t('agent.model')}
                onChange={(value) => patch({model: value})}
                options={modelOptions}
                placeholder={t('agent.modelUnset')}
                size="sm"
                value={agent.model}
                width={200}
              />
              {/* Capabilities is the reference's fourth tab. It needs Tool and
                  Skill, which Cosmo has not built. Tab takes no disabled prop,
                  so the panel behind it says so plainly instead: the shape is
                  visible and nothing pretends to work. */}
              <TabList hasDivider onChange={setTab} value={tab}>
                <Tab label={t('agent.tabPrompt')} value="prompt" />
                <Tab label={t('agent.tabCapabilities')} value="capabilities" />
                <Tab label={t('agent.tabKnowledge')} value="knowledge" />
                <Tab label={t('agent.tabExperience')} value="experience" />
              </TabList>
            </HStack>

            {tab === 'prompt' ? (
              <VStack gap={1} maxWidth={960} width="100%">
                {/* A prompt is written, not filled in. The reference gives it a
                    borderless surface for that reason; the border and rounding
                    made ours read as a form field in a box. Utilities rather
                    than component props because TextArea has no ghost variant.
                    The className lands on the wrapper, which is what draws the
                    border, and the grip belongs to the textarea inside it -
                    hence the descendant variant for that one rule. */}
                <HStack hAlign="start" width="100%">
                  {/* Variables are the reference's one affordance above the
                      prompt. Cosmo has no variable substitution yet, so the
                      button says what is coming and does nothing. */}
                  <Button icon={<Braces size={14} />} isDisabled label={t('agent.variables')} size="sm" variant="ghost" />
                </HStack>
                <TextArea
                  className="border-0! bg-transparent! px-0! [&_textarea]:resize-none"
                  isDisabled={isReadOnly}
                  isLabelHidden
                  label={t('agent.systemPrompt')}
                  onChange={(value) => patch({system_prompt: value})}
                  placeholder={t('agent.systemPromptPlaceholder')}
                  rows={34}
                  value={agent.system_prompt}
                  width="100%"
                />
                <HStack hAlign="end" width="100%">
                  <Text color="secondary" type="supporting">
                    {t('agent.promptChars', {count: String(agent.system_prompt.length)})}
                  </Text>
                </HStack>
              </VStack>
            ) : null}

            {tab === 'capabilities' ? (
              workspaceTools.length === 0 ? (
                <EmptyState
                  actions={<Button label={t('tool.add')} onClick={() => router.push(`/tools?workspace=${encodeURIComponent(workspaceID)}`)} variant="secondary" />}
                  description={t('agent.noToolsBody')}
                  icon={<Wrench size={48} strokeWidth={1} />}
                  title={t('agent.noTools')}
                />
              ) : (
                /* Attaching a tool is what makes it callable during a turn, so
                   the switch saves immediately rather than waiting for a form
                   to be submitted somewhere else. */
                <VStack gap={3} width="100%">
                  <Text color="secondary" type="supporting">{t('agent.toolsHint')}</Text>
                  {workspaceTools.map((item) => (
                    <Card key={item.id} padding={4} width="100%">
                      <HStack gap={3} hAlign="between" vAlign="center" width="100%">
                        <HStack gap={3} vAlign="center">
                          <Text type="display-3">{item.icon || '🔌'}</Text>
                          <VStack gap={0}>
                            <Text type="label">{item.name}</Text>
                            <Text color="secondary" type="supporting">
                              {item.description || item.base_url}
                            </Text>
                            <Text color="secondary" type="supporting">
                              {t('tool.actionCount', {count: item.action_count})}
                            </Text>
                          </VStack>
                        </HStack>
                        <Switch
                          isDisabled={isReadOnly || isSaving || isStale || item.action_count === 0}
                          isLabelHidden
                          label={item.name}
                          onChange={(checked: boolean) => void setToolAttached(item.id, checked)}
                          value={attachedToolIDs.includes(item.id)}
                        />
                      </HStack>
                    </Card>
                  ))}
                </VStack>
              )
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
                    <Text type="label">{t('agent.knowledgeBases')}</Text>
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
                    <Text type="label">{t('agent.presetQuestions')}</Text>
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
        } />
      }
    />

    <Dialog isOpen={isDetailsOpen} onOpenChange={setIsDetailsOpen} purpose="form">
      <Layout
        content={
          <LayoutContent>
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
                rows={3}
                value={agent.introduction}
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
          </LayoutContent>
        }
        footer={
          <LayoutFooter>
            <HStack hAlign="end">
              <Button label={t('common.done')} onClick={() => setIsDetailsOpen(false)} variant="primary" />
            </HStack>
          </LayoutFooter>
        }
        header={<DialogHeader onOpenChange={setIsDetailsOpen} title={t('agent.editDetails')} />}
      />
    </Dialog>

    <Dialog isOpen={isHistoryOpen} onOpenChange={setIsHistoryOpen} purpose="info">
      <Layout
        content={
          <LayoutContent>
            {versions.length === 0 ? (
              <Text color="secondary" type="supporting">{t('agent.versionsEmpty')}</Text>
            ) : (
              <List>
                {versions.map((version) => (
                  <Item
                    as="li"
                    description={`${version.changelog || t('agent.versionNoChangelog')} · ${new Date(version.created_at).toLocaleString()}`}
                    endContent={version.id === agent.published_version_id
                      ? <StatusLabel label={t('agent.versionCurrent')} variant="success" />
                      : undefined}
                    key={version.id}
                    label={t('agent.publishedVersion', {version: String(version.version_number)})}
                  />
                ))}
              </List>
            )}
          </LayoutContent>
        }
        header={<DialogHeader onOpenChange={setIsHistoryOpen} title={t('agent.versions')} />}
      />
    </Dialog>

    <Dialog isOpen={isPublishOpen} onOpenChange={setIsPublishOpen} purpose="form">
      <Layout
        content={
          <LayoutContent>
            <VStack gap={3}>
              <Text color="secondary" type="supporting">
                {agent.published_version > 0
                  ? t('agent.publishFrom', {version: String(agent.published_version)})
                  : t('agent.publishFirst')}
              </Text>
              <TextArea
                label={t('agent.changelog')}
                maxLength={500}
                onChange={setChangelog}
                placeholder={t('agent.changelogPlaceholder')}
                rows={4}
                value={changelog}
                width="100%"
              />
            </VStack>
          </LayoutContent>
        }
        footer={
          <LayoutFooter>
            <HStack gap={2} hAlign="end">
              <Button label={t('common.cancel')} onClick={() => setIsPublishOpen(false)} variant="secondary" />
              <Button isLoading={isPublishing} label={t('agent.publish')} onClick={() => void publish()} variant="primary" />
            </HStack>
          </LayoutFooter>
        }
        header={<DialogHeader onOpenChange={setIsPublishOpen} title={t('agent.publish')} />}
      />
    </Dialog>
    </>
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



// stepDetail says what a step did, using only what the run actually records.
// Duration comes from the step's own timestamps; token counts are absent
// because nothing measures them yet.
function stepDetail(step: RunStep, t: ReturnType<typeof useTranslation>): string {
  const parts: string[] = [];
  if (step.started_at && step.finished_at) {
    const ms = new Date(step.finished_at).getTime() - new Date(step.started_at).getTime();
    parts.push(t('agent.stepDuration', {ms: String(ms)}));
  }
  const output = step.output ?? {};
  if (typeof output.passage_count === 'number') parts.push(t('agent.stepPassages', {count: String(output.passage_count)}));
  if (typeof output.citation_count === 'number') parts.push(t('agent.stepCitations', {count: String(output.citation_count)}));
  if (typeof output.model === 'string') parts.push(output.model);
  if (step.error_code) parts.push(step.error_code);
  return parts.join(' · ');
}

// The agent's own chat. It writes conversations stamped with the agent id, and
// reaches the model through the very endpoint the general chat uses, so a
// reply here goes through the same retrieval, citations and streaming.
function AgentChatPanel({agent, t, workspaceID}: {agent: Agent; t: ReturnType<typeof useTranslation>; workspaceID: string}) {
  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [conversationID, setConversationID] = useState('');
  const [messages, setMessages] = useState<Message[]>([]);
  const [draft, setDraft] = useState('');
  const [streamed, setStreamed] = useState('');
  // Calls arrive twice - running, then settled - so they are held by id and
  // the second arrival replaces the first rather than adding a row.
  const [liveToolCalls, setLiveToolCalls] = useState<MessageToolCall[]>([]);

  const [suggestions, setSuggestions] = useState<string[]>([]);
  // What the last turn actually did, read back from the run the chat recorded.
  const [steps, setSteps] = useState<RunStep[]>([]);
  const [isInspectorOpen, setIsInspectorOpen] = useState(false);
  const [isSending, setIsSending] = useState(false);
  const [chatError, setChatError] = useState('');
  const runID = useRef('');
  // Deltas arrive in bursts, which reads as stuttering. This decouples the
  // display rate from the arrival rate and advances on word and syntax
  // boundaries, so the markdown renderer never sees a half-written token.
  const revealed = useStreamingText(streamed, isSending);
  const entry = useEntryAnimation('slideUp');

  useEffect(() => {
    api.agentConversations(agent.id, workspaceID)
      .then((result) => {
        setConversations(result.conversations);
        // Only a conversation that follows the draft belongs here. Picking the
        // newest of any kind meant the panel could open one pinned to a
        // published version - so Debug would be running the published agent,
        // which is the one thing this panel is not for. A pinned conversation
        // is still reachable from the history menu, where choosing it is the
        // reader's decision rather than an accident of ordering.
        const draft = result.conversations.find((item) => !item.agent_version_id);
        if (!draft) return;
        setConversationID(draft.id);
        return api.messages(draft.id).then((loaded) => setMessages(loaded.messages));
      })
      .catch(() => undefined);
  }, [agent.id, workspaceID]);

  // Switching conversation replaces the transcript wholesale, and drops any
  // half-streamed reply so text from the previous one cannot bleed into it.
  async function openConversation(id: string) {
    if (!id || id === conversationID) return;
    setConversationID(id);
    setStreamed('');
    setSuggestions([]);
    setChatError('');
    try {
      const loaded = await api.messages(id);
      setMessages(loaded.messages);
    } catch (caught) {
      setChatError(caught instanceof Error ? caught.message : t('agent.chatFailed'));
    }
  }

  async function startNew() {
    setChatError('');
    try {
      const result = await api.startAgentConversation(agent.id, 'draft', workspaceID);
      setConversations((current) => [result.conversation, ...current]);
      setConversationID(result.conversation.id);
      setMessages([]);
      setStreamed('');
      setSuggestions([]);
    } catch (caught) {
      setChatError(caught instanceof Error ? caught.message : t('agent.chatFailed'));
    }
  }

  // Takes the question rather than reading the box, so a suggestion can be
  // sent without being typed into it first.
  async function send(question?: string) {
    const content = (question ?? draft).trim();
    if (!content || isSending) return;
    setChatError('');
    setIsSending(true);
    setDraft('');
    setStreamed('');
    setSuggestions([]);
    let target = conversationID;
    try {
      if (!target) {
        const started = await api.startAgentConversation(agent.id, 'draft', workspaceID);
        target = started.conversation.id;
        setConversations((current) => [started.conversation, ...current]);
        setConversationID(target);
      }
      setMessages((current) => [...current, {id: `local_${Date.now()}`, conversation_id: target, role: 'user', content, created_at: new Date().toISOString()} as Message]);
      setLiveToolCalls([]);
      await streamChat(target, content, {}, {
        onToolCall: (call) => setLiveToolCalls((current) => {
          const index = current.findIndex((item) => item.id === call.id);
          if (index < 0) return [...current, call];
          const next = [...current];
          next[index] = call;
          return next;
        }),
        onDelta: (delta) => setStreamed((current) => current + delta),
        onMeta: (data) => { if (data.run_id) runID.current = data.run_id; },
        onSuggestions: (data) => setSuggestions(data.questions),
        onDone: (data) => {
          setMessages((current) => [...current, data.message]);
          setStreamed('');
          // The saved message carries the calls now, so the live copy would
          // only draw them a second time.
          setLiveToolCalls([]);
        },
      });
      const transcript = await api.messages(target).catch(() => null);
      if (transcript) setMessages(transcript.messages);
      // Read after the reply, so inspecting never delays the answer.
      if (runID.current) {
        try {
          const result = await api.runSteps(runID.current);
          setSteps(result.steps);
        } catch {
          // The inspector is a convenience; failing to load it is not an error
          // worth putting in front of someone who just got their answer.
        }
      }
    } catch (caught) {
      setChatError(caught instanceof Error ? caught.message : t('agent.chatFailed'));
      setDraft(content);
      if (target) {
        const transcript = await api.messages(target).catch(() => null);
        if (transcript) setMessages(transcript.messages);
      }
    } finally {
      setIsSending(false);
    }
  }

  const composer = (
    <ChatComposer
      isDisabled={isSending || !agent.model}
      onChange={setDraft}
      onSubmit={() => void send()}
      placeholder={t('agent.chatPlaceholder')}
      value={draft}
    />
  );

  const isEmpty = messages.length === 0 && !streamed && liveToolCalls.length === 0;

  // The panel is a surface of its own rather than the other half of the same
  // sheet: a different ground and a rounded inner edge, so what is being
  // configured and what is being tried are told apart at a glance.
  return (
    <VStack className="rounded-l-2xl bg-[var(--color-background-body)]" gap={0} height="100%" width={768}>
      <HStack gap={2} hAlign="between" padding={3} vAlign="center" width="100%">
        <HStack gap={2} vAlign="center">
          {/* The reference names the panel's modes here. Only the first has
              anything behind it - see docs/ui_backlog.md. */}
          <SegmentedControl label={t('agent.panelMode')} onChange={() => undefined} size="sm" value="debug">
            <SegmentedControlItem label={t('agent.panelDebug')} value="debug" />
            <SegmentedControlItem isDisabled label={t('agent.panelOptimise')} value="optimise" />
          </SegmentedControl>
          {/* Not a mode but a reminder: what happens here is a draft, and
              nobody else sees it until the agent is published. */}
          <Token label={t('agent.debugIsDraft')} size="sm" />
        </HStack>
        <HStack gap={1} vAlign="center">
          <IconButton
            icon={<Plus size={15} />}
            label={t('agent.chatNew')}
            onClick={() => void startNew()}
            size="sm"
            variant="ghost"
          />
          <DropdownMenu
            alignment="end"
            button={{icon: <History size={15} />, isIconOnly: true, label: t('agent.chatHistory'), size: 'sm', variant: 'ghost'}}
            hasChevron={false}
            items={conversations.length > 0
              ? conversations.map((item) => ({
                // A pinned conversation runs the version it was pinned to, so
                // the menu says which, rather than leaving the reader to
                // wonder why an answer ignores a change they just made.
                label: item.agent_version_id
                  ? `${item.title} · v${item.version_number ?? 0}`
                  : item.title,
                onClick: () => void openConversation(item.id),
              }))
              : [{isDisabled: true, label: t('agent.chatNoHistory')}]}
          />
          <Divider orientation="vertical" />
          <Popover
            content={
              <VStack gap={3} padding={3} width={280}>
                <Text type="label">{t('agent.debugSettings')}</Text>
                <HStack gap={2} hAlign="between" vAlign="center" width="100%">
                  <Text type="body">{t('agent.memory')}</Text>
                  <Token label={agent.is_memory_enabled ? t('agent.memoryOn') : t('agent.memoryOff')} size="sm" />
                </HStack>
                <Text color="secondary" type="supporting">{t('agent.debugSettingsHint')}</Text>
              </VStack>
            }
          >
            <Button icon={<SlidersHorizontal size={15} />} label={t('agent.debugSettings')} size="sm" variant="ghost" />
          </Popover>
        </HStack>
      </HStack>

      {!agent.model || chatError ? (
        <VStack gap={2} padding={3} width="100%">
          {!agent.model ? <Banner status="warning" title={t('agent.chatNoModel')} /> : null}
          {chatError ? <Banner isDismissable onDismiss={() => setChatError('')} status="error" title={chatError} /> : null}
        </VStack>
      ) : null}

      {isEmpty ? (
        // Nothing has been asked yet, so the greeting and the box to ask in
        // are one centred block - not a heading marooned at the top with the
        // composer pinned to the floor.
        <VStack gap={5} hAlign="center" height="100%" padding={6} vAlign="center" width="100%">
          <VStack gap={2} hAlign="center">
            <Avatar
              name={agent.avatar || agent.name}
              size="lg"
              src={agent.has_avatar_image ? api.agentAvatarURL(agent.id, workspaceID, agent.updated_at) : undefined}
            />
            <Text type="large">{agent.name}</Text>
            {agent.opening_line ? (
              <Text color="secondary" type="body">{agent.opening_line}</Text>
            ) : (
              <Text color="secondary" type="supporting">{t('agent.chatEmpty')}</Text>
            )}
          </VStack>
          <VStack gap={2} maxWidth={520} width="100%">
            {composer}
            {agent.preset_questions.map((question, index) => (
              <Button
                key={index}
                label={question}
                onClick={() => setDraft(question)}
                size="sm"
                variant="secondary"
                width="100%"
              />
            ))}
          </VStack>
        </VStack>
      ) : (
        <>
          <VStack gap={3} height="100%" isScrollable padding={4} width="100%">
            {messages.map((message) => (
              <Card key={message.id} padding={3} width="100%" xstyle={entry}>
                <VStack gap={1}>
                  <Text color="secondary" type="supporting">{message.role === 'user' ? agent.owner_name || 'You' : agent.name}</Text>
                  {message.role === 'assistant'
                    ? <AnswerWithToolCalls calls={message.tool_calls ?? []}>{message.content}</AnswerWithToolCalls>
                    : <Text type="body">{message.content}</Text>}
                  {message.role === 'assistant' ? (
                    <HStack gap={2} hAlign="end" width="100%">
                      <CopyButton text={message.content} />
                    </HStack>
                  ) : null}
                </VStack>
              </Card>
            ))}
            {liveToolCalls.length > 0 || streamed ? (
              <Card padding={3} width="100%">
                <VStack gap={2}>
                  <Text color="secondary" type="supporting">{agent.name}</Text>
                  <AnswerWithToolCalls calls={liveToolCalls} isStreaming>{revealed}</AnswerWithToolCalls>
                </VStack>
              </Card>
            ) : null}
            {suggestions.length > 0 && !isSending ? (
              /* Taking one sends it. Filling the box instead made a suggestion
                 a draft to edit, which is a step nobody wanted - and the chips
                 hug their text, so three of them read as three choices rather
                 than a stack of buttons. */
              <HStack gap={2} vAlign="center" wrap="wrap">
                {suggestions.map((question) => (
                  <Button key={question} label={question} onClick={() => void send(question)} size="sm" variant="secondary" />
                ))}
              </HStack>
            ) : null}
          </VStack>
          <VStack gap={2} padding={3} width="100%">
            {steps.length > 0 ? (
              <VStack gap={2} width="100%">
                <HStack gap={2} hAlign="between" vAlign="center">
                  <Text color="secondary" type="supporting">{t('agent.lastTurn')}</Text>
                  <Button
                    label={isInspectorOpen ? t('agent.hideDetail') : t('agent.showDetail')}
                    onClick={() => setIsInspectorOpen(!isInspectorOpen)}
                    size="sm"
                    variant="ghost"
                  />
                </HStack>
                {isInspectorOpen ? (
                  <List>
                    {steps.map((step) => (
                      <Item
                        as="li"
                        description={stepDetail(step, t)}
                        endContent={<StatusLabel label={step.status} variant={step.status === 'succeeded' ? 'success' : step.status === 'failed' ? 'error' : 'neutral'} />}
                        key={step.id}
                        label={step.name || step.type}
                      />
                    ))}
                  </List>
                ) : null}
              </VStack>
            ) : null}
            {composer}
          </VStack>
        </>
      )}
    </VStack>
  );
}
