'use client';

import {Suspense, useCallback, useEffect, useState} from 'react';
import {useRouter, useSearchParams} from 'next/navigation';
import {BarChart3, Bot, Copy, ExternalLink, ImageUp, KeyRound, MoreHorizontal, Pencil, Search, Settings2, ShieldCheck, Sparkles, Trash2, Workflow} from 'lucide-react';
import {AlertDialog} from '@astryxdesign/core/AlertDialog';
import {Avatar, AvatarStatusDot} from '@astryxdesign/core/Avatar';
import {Token} from '@astryxdesign/core/Token';
import {Banner} from '@astryxdesign/core/Banner';
import {Button} from '@astryxdesign/core/Button';
import {Card} from '@astryxdesign/core/Card';
import {useEntryAnimation} from '@astryxdesign/core/hooks';
import {cardHover, coverFrame, coverZoom} from '../components/motion';
import {Dialog, DialogHeader} from '@astryxdesign/core/Dialog';
import {DropdownMenu} from '@astryxdesign/core/DropdownMenu';
import {EmptyState} from '@astryxdesign/core/EmptyState';
import {Skeleton} from '@astryxdesign/core/Skeleton';
import {Grid} from '@astryxdesign/core/Grid';
import {IconButton} from '@astryxdesign/core/IconButton';
import {HStack, Layout, LayoutContent, LayoutFooter, VStack} from '@astryxdesign/core/Layout';
import {Popover} from '@astryxdesign/core/Popover';
import {SegmentedControl, SegmentedControlItem} from '@astryxdesign/core/SegmentedControl';
import {SelectableCard} from '@astryxdesign/core/SelectableCard';
import {Section} from '@astryxdesign/core/Section';
import {Selector} from '@astryxdesign/core/Selector';
import {Text} from '@astryxdesign/core/Text';
import {TextArea} from '@astryxdesign/core/TextArea';
import {TextInput} from '@astryxdesign/core/TextInput';
import {PageHeader} from '../components/PageHeader';
import {StatusLabel} from '../components/StatusLabel';
import {Agent, api, APIError} from '../lib/api';
import {useTranslation} from '../lib/i18n';
import {AgentAvatarPicker} from './AgentAvatarPicker';

const DEFAULT_AGENT_AVATAR = '🤖';

// toBase64 strips the data: prefix the reader adds, because the upload
// endpoint stores raw base64 and sniffs the bytes to check the declared type.
function toBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result).split(',')[1] ?? '');
    reader.onerror = () => reject(reader.error);
    reader.readAsDataURL(file);
  });
}

function AgentsView() {
  const t = useTranslation();
  const router = useRouter();
  const search = useSearchParams();
  const workspaceID = search.get('workspace') ?? '';
  const [agents, setAgents] = useState<Agent[]>([]);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [isCreating, setIsCreating] = useState(false);
  const [deleting, setDeleting] = useState<Agent | null>(null);
  const [newName, setNewName] = useState('');
  const [newIntroduction, setNewIntroduction] = useState('');
  const [newAvatar, setNewAvatar] = useState(DEFAULT_AGENT_AVATAR);
  const [newVisibility, setNewVisibility] = useState<'private' | 'workspace'>('private');
  const [newTags, setNewTags] = useState('');
  // Held until the agent exists: the upload endpoint needs an id to attach to.
  const [newAvatarFile, setNewAvatarFile] = useState<File | null>(null);
  const [query, setQuery] = useState('');
  // The reference filters by agent type. Cosmo has one type, so the division
  // that actually means something here is whose agent it is.
  const [scope, setScope] = useState('all');
  // Needed to tell "mine" from "shared with me"; the list itself only says who
  // owns each agent.
  const [ownerID, setOwnerID] = useState('');
  // Only animates cards inserted after the first paint, so arriving on the
  // page stays still and a newly created agent is the thing that moves.
  const entry = useEntryAnimation('scaleIn');
  // "No agents yet" is a claim about the workspace. Until the first read
  // lands, the page does not know enough to make it.
  const [isLoading, setIsLoading] = useState(true);

  const load = useCallback(() => {
    api.agents(workspaceID)
      .then((result) => setAgents(result.agents))
      .catch((caught) => setError(caught instanceof APIError ? caught.message : ''))
      .finally(() => setIsLoading(false));
  }, [workspaceID]);

  useEffect(load, [load]);

  useEffect(() => {
    api.me().then((result) => setOwnerID(result.user.id)).catch(() => undefined);
  }, []);

  function openAgent(agent: Agent) {
    const query = workspaceID ? `?workspace=${encodeURIComponent(workspaceID)}` : '';
    router.push(`/agents/${encodeURIComponent(agent.id)}${query}`);
  }

  async function create() {
    setBusy(true);
    setError('');
    try {
      const result = await api.createAgent({
        name: newName.trim(),
        introduction: newIntroduction.trim(),
        avatar: newAvatar,
        tags: newTags.split(',').map((tag) => tag.trim()).filter(Boolean),
        visibility: newVisibility,
        workspace_id: workspaceID,
      });
      // The picture can only be attached once the agent has an id. A failure
      // here leaves a created agent with its emoji, which is worth more than
      // failing the whole creation over a picture.
      if (newAvatarFile) {
        try {
          await api.uploadAgentAvatar(result.agent.id, newAvatarFile.type, await toBase64(newAvatarFile), workspaceID);
        } catch {
          // The editor offers the same picker, so it can be set again there.
        }
      }
      closeCreate();
      openAgent(result.agent);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : t('agent.createFailed'));
    } finally {
      setBusy(false);
    }
  }

  function closeCreate() {
    setIsCreating(false);
    setNewName('');
    setNewIntroduction('');
    setNewAvatar(DEFAULT_AGENT_AVATAR);
    setNewVisibility('private');
    setNewTags('');
    setNewAvatarFile(null);
  }

  async function remove() {
    if (!deleting) return;
    setBusy(true);
    try {
      await api.deleteAgent(deleting.id, workspaceID);
      setAgents((current) => current.filter((item) => item.id !== deleting.id));
      setDeleting(null);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : t('agent.deleteFailed'));
    } finally {
      setBusy(false);
    }
  }

  const visible = agents.filter((agent) => {
    if (scope === 'mine' && agent.owner_user_id !== ownerID) return false;
    if (scope === 'shared' && agent.visibility !== 'workspace') return false;
    const needle = query.trim().toLowerCase();
    if (!needle) return true;
    return [agent.name, agent.introduction, ...agent.tags]
      .some((field) => (field || '').toLowerCase().includes(needle));
  });

  return (
    <>
      <Layout
        height="fill"
        header={
          <PageHeader
            actions={
              /* Two routes in the reference. Building one from a description
                 needs a model pass we have not written - see
                 docs/ui_backlog.md. */
              <DropdownMenu
                alignment="end"
                button={{label: t('agent.new'), size: 'sm', variant: 'primary'}}
                items={[
                  {icon: <Sparkles size={15} />, isDisabled: true, label: t('agent.createWithAI')},
                  {icon: <Settings2 size={15} />, label: t('agent.createManually'), onClick: () => setIsCreating(true)},
                ]}
              />
            }
            count={agents.length}
            hasIntroduction
            description={t('agent.subtitle')}
            title={t('agent.title')}
          />
        }
        content={
          <LayoutContent padding={6}>
            <VStack gap={6}>
              {error ? <Banner isDismissable onDismiss={() => setError('')} status="error" title={error} /> : null}
              {agents.length > 0 ? (
                <HStack gap={3} vAlign="center" width="100%">
                  <SegmentedControl label={t('agent.scope')} onChange={setScope} size="md" value={scope}>
                    <SegmentedControlItem label={t('agent.scopeAll')} value="all" />
                    <SegmentedControlItem label={t('agent.scopeMine')} value="mine" />
                    <SegmentedControlItem label={t('agent.scopeShared')} value="shared" />
                  </SegmentedControl>
                  <TextInput
                    isLabelHidden
                    label={t('agent.search')}
                    onChange={setQuery}
                    placeholder={t('agent.search')}
                    size="lg"
                    startIcon={<Search size={15} />}
                    value={query}
                    width={280}
                  />
                </HStack>
              ) : null}
              {isLoading ? (
                <Grid columns={{minWidth: 220, max: 6}} gap={4} width="100%">
                  {[0, 1, 2].map((index) => <Skeleton height={290} index={index} key={index} width="100%" />)}
                </Grid>
              ) : agents.length === 0 ? (
                <EmptyState
                  description={t('agent.emptyBody')}
                  icon={<Bot size={64} strokeWidth={1} />}
                  actions={<Button label={t('agent.new')} onClick={() => setIsCreating(true)} variant="primary" />}
                  title={t('agent.emptyTitle')}
                />
              ) : visible.length === 0 ? (
                <Text color="secondary" type="supporting">{t('agent.noMatch')}</Text>
              ) : (
                <Grid columns={{minWidth: 220, max: 6}} gap={4} width="100%">
                  {visible.map((agent) => (
                    // Portrait card, as the reference has it: the face on a band
                    // of its own, then the name, then what the agent is for.
                    <Card className={cardHover} key={agent.id} onClick={() => openAgent(agent)} padding={0} width="100%" xstyle={entry}>
                      <VStack gap={0} height="100%">
                        {/* The face sits on a tinted band, as the reference
                            has it, so a wall of cards reads as faces first. */}
                        <Section className={coverFrame} padding={3} variant="muted">
                          <HStack gap={2} hAlign="between" vAlign="start">
                            {/* The face zooms inside its band rather than
                                moving with the card, as the reference does. */}
                            {/* The reference marks an unpublished agent with a
                                dot on its face, which is where the eye already
                                is on a wall of cards. Astryx offers success,
                                neutral and error; unpublished is a state, not
                                a fault. */}
                            <Avatar
                              className={coverZoom}
                              name={agent.avatar || agent.name}
                              size="xl"
                              status={agent.has_unpublished_changes
                                ? <AvatarStatusDot label={t('agent.unpublished')} variant="neutral" />
                                : undefined}
                            />
                            {agent.is_editable ? (
                              /* The reference offers this set from the card.
                                 What Cosmo cannot do yet is disabled rather
                                 than absent, so the shape is visible and
                                 nothing pretends to work. */
                              <DropdownMenu
                                alignment="end"
                                button={{icon: <MoreHorizontal size={15} />, isIconOnly: true, label: t('agent.moreActions'), size: 'sm', variant: 'ghost'}}
                                hasChevron={false}
                                items={[
                                  {icon: <Settings2 size={15} />, label: t('agent.configure'), onClick: () => openAgent(agent)},
                                  {icon: <Pencil size={15} />, isDisabled: true, label: t('agent.edit')},
                                  {icon: <Copy size={15} />, isDisabled: true, label: t('agent.copy')},
                                  {icon: <ExternalLink size={15} />, isDisabled: true, label: t('agent.access')},
                                  {icon: <KeyRound size={15} />, isDisabled: true, label: t('agent.apiKeys')},
                                  {icon: <ShieldCheck size={15} />, isDisabled: true, label: t('agent.accessControl')},
                                  {icon: <BarChart3 size={15} />, isDisabled: true, label: t('agent.observability')},
                                  {type: 'divider' as const},
                                  {icon: <Trash2 size={15} />, label: t('conv.delete'), onClick: () => setDeleting(agent), variant: 'destructive' as const},
                                ]}
                              />
                            ) : null}
                          </HStack>
                        </Section>
                        <Section padding={3}>
                          <VStack gap={1}>
                            <Text maxLines={1} type="label">{agent.name}</Text>
                            <Text color="secondary" maxLines={2} type="supporting">
                              {agent.introduction || agent.owner_name}
                            </Text>
                            <HStack gap={2} vAlign="center" wrap="wrap">
                              <StatusLabel
                                label={agent.visibility === 'workspace' ? t('agent.visibilityWorkspace') : t('agent.visibilityPrivate')}
                                variant="neutral"
                              />
                              {agent.model ? <Token label={agent.model} size="sm" /> : null}
                            </HStack>
                          </VStack>
                        </Section>
                      </VStack>
                    </Card>
                  ))}
                </Grid>
              )}
            </VStack>
          </LayoutContent>
        }
      />

      <Dialog isOpen={isCreating} onOpenChange={(open) => { if (!open) closeCreate(); }} purpose="form">
        <Layout
          content={
            <LayoutContent>
              <VStack gap={4}>
                <VStack gap={2}>
                  <Text type="label">{t('agent.type')}</Text>
                  <HStack gap={3} width="100%">
                    <SelectableCard isSelected label={t('agent.typePrompt')} onChange={() => undefined} width="100%">
                      {/* What each type is, at the moment of choosing between
                          them, because the names alone do not say. */}
                      <VStack gap={1}>
                        <HStack gap={2} vAlign="center">
                          <Bot size={18} />
                          <Text type="label">{t('agent.typePrompt')}</Text>
                        </HStack>
                        <Text color="secondary" type="supporting">{t('agent.typePromptBody')}</Text>
                      </VStack>
                    </SelectableCard>
                    {/* Flow needs a workflow engine Cosmo does not have yet. It
                        is shown, disabled, so the choice it will one day offer
                        is visible rather than sprung on the reader later. */}
                    <SelectableCard
                      isDisabled
                      isSelected={false}
                      label={t('agent.typeFlow')}
                      onChange={() => undefined}
                      width="100%"
                    >
                      <VStack gap={1}>
                        <HStack gap={2} vAlign="center">
                          <Workflow size={18} />
                          <Text type="label">{t('agent.typeFlow')}</Text>
                        </HStack>
                        <Text color="secondary" type="supporting">{t('agent.typeFlowBody')}</Text>
                        <Text color="secondary" type="supporting">{t('agent.typeFlowUnavailable')}</Text>
                      </VStack>
                    </SelectableCard>
                  </HStack>
                </VStack>
                <HStack gap={3} vAlign="end" width="100%">
                  <AgentAvatarPicker
                    file={newAvatarFile}
                    onChangeEmoji={(emoji) => { setNewAvatar(emoji); setNewAvatarFile(null); }}
                    onChangeFile={(file) => setNewAvatarFile(file)}
                    t={t}
                    value={newAvatar}
                  />
                  <TextInput label={t('agent.name')} onChange={setNewName} value={newName} width="100%" />
                </HStack>
                <TextArea
                  label={t('agent.introduction')}
                  maxLength={512}
                  onChange={setNewIntroduction}
                  placeholder={t('agent.introductionPlaceholder')}
                  rows={3}
                  value={newIntroduction}
                  width="100%"
                />
                <TextInput
                  label={t('agent.tags')}
                  onChange={setNewTags}
                  placeholder={t('agent.tagsPlaceholder')}
                  value={newTags}
                  width="100%"
                />
                <Selector
                  label={t('agent.visibility')}
                  onChange={(value) => setNewVisibility(value === 'workspace' ? 'workspace' : 'private')}
                  options={[
                    {label: t('agent.visibilityPrivate'), value: 'private'},
                    {label: t('agent.visibilityWorkspace'), value: 'workspace'},
                  ]}
                  value={newVisibility}
                  width="100%"
                />
              </VStack>
            </LayoutContent>
          }
          footer={
            <LayoutFooter>
              <HStack gap={2} hAlign="end">
                <Button label={t('common.cancel')} onClick={closeCreate} variant="secondary" />
                <Button isDisabled={!newName.trim() || busy} isLoading={busy} label={t('agent.create')} onClick={() => void create()} variant="primary" />
              </HStack>
            </LayoutFooter>
          }
          header={<DialogHeader onOpenChange={(open) => { if (!open) closeCreate(); }} title={t('agent.new')} />}
        />
      </Dialog>

      <AlertDialog
        actionLabel={t('conv.delete')}
        cancelLabel={t('common.cancel')}
        description={t('agent.deleteBody')}
        isActionLoading={busy}
        isOpen={deleting !== null}
        onAction={() => void remove()}
        onOpenChange={(open) => { if (!open) setDeleting(null); }}
        title={t('agent.deleteTitle')}
      />
    </>
  );
}

// useSearchParams reads the request URL, so the route cannot be prerendered
// without a boundary to fall back to.
export default function AgentsPage() {
  return (
    <Suspense fallback={null}>
      <AgentsView />
    </Suspense>
  );
}
