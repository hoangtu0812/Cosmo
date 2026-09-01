'use client';

import {Suspense, useCallback, useEffect, useState} from 'react';
import {useRouter, useSearchParams} from 'next/navigation';
import {Bot, Trash2} from 'lucide-react';
import {AlertDialog} from '@astryxdesign/core/AlertDialog';
import {Avatar} from '@astryxdesign/core/Avatar';
import {Badge} from '@astryxdesign/core/Badge';
import {Banner} from '@astryxdesign/core/Banner';
import {Button} from '@astryxdesign/core/Button';
import {Card} from '@astryxdesign/core/Card';
import {Dialog, DialogHeader} from '@astryxdesign/core/Dialog';
import {EmptyState} from '@astryxdesign/core/EmptyState';
import {Grid} from '@astryxdesign/core/Grid';
import {IconButton} from '@astryxdesign/core/IconButton';
import {HStack, Layout, LayoutContent, LayoutFooter, LayoutHeader, VStack} from '@astryxdesign/core/Layout';
import {Selector} from '@astryxdesign/core/Selector';
import {Text} from '@astryxdesign/core/Text';
import {TextArea} from '@astryxdesign/core/TextArea';
import {TextInput} from '@astryxdesign/core/TextInput';
import {Toolbar} from '@astryxdesign/core/Toolbar';
import {Agent, api, APIError} from '../lib/api';
import {useTranslation} from '../lib/i18n';

// Single-codepoint emoji only: Avatar renders the first character of `name`,
// and a sequence joined by ZWJ or a variation selector would be cut in half.
export const AGENT_AVATARS = ['🤖', '🧠', '📊', '🔍', '📚', '💡', '🧭', '🗂', '🔧', '🧪'];

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
  const [newAvatar, setNewAvatar] = useState(AGENT_AVATARS[0]);
  const [newVisibility, setNewVisibility] = useState<'private' | 'workspace'>('private');

  const load = useCallback(() => {
    api.agents(workspaceID)
      .then((result) => setAgents(result.agents))
      .catch((caught) => setError(caught instanceof APIError ? caught.message : ''));
  }, [workspaceID]);

  useEffect(load, [load]);

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
        tags: [],
        visibility: newVisibility,
        workspace_id: workspaceID,
      });
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
    setNewAvatar(AGENT_AVATARS[0]);
    setNewVisibility('private');
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

  return (
    <>
      <Layout
        contentWidth={1120}
        height="fill"
        header={
          <LayoutHeader hasDivider>
            <Toolbar
              endContent={<Button label={t('agent.new')} onClick={() => setIsCreating(true)} size="sm" variant="primary" />}
              label={t('agent.title')}
              startContent={<Text type="label" weight="semibold">{t('agent.title')}</Text>}
            />
          </LayoutHeader>
        }
        content={
          <LayoutContent padding={6}>
            <VStack gap={6}>
              {error ? <Banner isDismissable onDismiss={() => setError('')} status="error" title={error} /> : null}
              {agents.length === 0 ? (
                <EmptyState
                  description={t('agent.emptyBody')}
                  icon={<Bot size={64} strokeWidth={1} />}
                  actions={<Button label={t('agent.new')} onClick={() => setIsCreating(true)} variant="primary" />}
                  title={t('agent.emptyTitle')}
                />
              ) : (
                <Grid columns={{minWidth: 320, max: 3}} gap={4} width="100%">
                  {agents.map((agent) => (
                    <Card key={agent.id} onClick={() => openAgent(agent)} width="100%">
                      <VStack gap={3}>
                        <HStack gap={3} hAlign="between" vAlign="start">
                          <HStack gap={3} vAlign="center">
                            <Avatar name={agent.avatar || agent.name} size="md" />
                            <VStack gap={0}>
                              <Text type="label" weight="semibold">{agent.name}</Text>
                              <Text color="secondary" type="supporting">{agent.owner_name}</Text>
                            </VStack>
                          </HStack>
                          {agent.is_editable ? (
                            <IconButton
                              icon={<Trash2 size={14} />}
                              label={t('agent.deleteTitle')}
                              onClick={() => setDeleting(agent)}
                              size="sm"
                              variant="ghost"
                            />
                          ) : null}
                        </HStack>
                        {agent.introduction ? <Text color="secondary" type="supporting">{agent.introduction}</Text> : null}
                        <HStack gap={2} vAlign="center">
                          <Badge
                            label={agent.visibility === 'workspace' ? t('agent.visibilityWorkspace') : t('agent.visibilityPrivate')}
                            variant="neutral"
                          />
                          {agent.model ? <Badge label={agent.model} variant="neutral" /> : null}
                        </HStack>
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
                <Selector
                  label={t('agent.avatar')}
                  onChange={setNewAvatar}
                  options={AGENT_AVATARS.map((emoji) => ({label: emoji, value: emoji}))}
                  value={newAvatar}
                  width="100%"
                />
                <TextInput label={t('agent.name')} onChange={setNewName} value={newName} width="100%" />
                <TextArea
                  label={t('agent.introduction')}
                  maxLength={512}
                  onChange={setNewIntroduction}
                  placeholder={t('agent.introductionPlaceholder')}
                  rows={3}
                  value={newIntroduction}
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
