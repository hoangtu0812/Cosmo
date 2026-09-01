'use client';

import {useCallback, useEffect, useMemo, useState} from 'react';
import {useRouter, useSearchParams} from 'next/navigation';
import {BookOpen, Search, Trash2} from 'lucide-react';
import {AlertDialog} from '@astryxdesign/core/AlertDialog';
import {Token} from '@astryxdesign/core/Token';
import {Banner} from '@astryxdesign/core/Banner';
import {Button} from '@astryxdesign/core/Button';
import {Card} from '@astryxdesign/core/Card';
import {CheckboxList, CheckboxListItem} from '@astryxdesign/core/CheckboxList';
import {Dialog, DialogHeader} from '@astryxdesign/core/Dialog';
import {EmptyState} from '@astryxdesign/core/EmptyState';
import {Grid} from '@astryxdesign/core/Grid';
import {IconButton} from '@astryxdesign/core/IconButton';
import {HStack, Layout, LayoutContent, LayoutFooter, VStack} from '@astryxdesign/core/Layout';
import {Link} from '@astryxdesign/core/Link';
import {Section} from '@astryxdesign/core/Section';
import {Selector} from '@astryxdesign/core/Selector';
import {Skeleton} from '@astryxdesign/core/Skeleton';
import {Text} from '@astryxdesign/core/Text';
import {TextArea} from '@astryxdesign/core/TextArea';
import {TextInput} from '@astryxdesign/core/TextInput';
import {Icon} from '@astryxdesign/core/Icon';
import {PageHeader} from '../components/PageHeader';
import {StatusLabel} from '../components/StatusLabel';
import {api, APIError, KnowledgeBase, Workspace, WorkspaceRef} from '../lib/api';
import {useTranslation} from '../lib/i18n';
import {KnowledgeIconPicker} from './KnowledgeIconPicker';

type Translate = ReturnType<typeof useTranslation>;

export default function KnowledgePage() {
  const t = useTranslation();
  const router = useRouter();
  const search = useSearchParams();
  const requestedWorkspaceID = search.get('workspace') ?? '';
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [directory, setDirectory] = useState<WorkspaceRef[]>([]);
  const [workspaceID, setWorkspaceID] = useState('');
  const [bases, setBases] = useState<KnowledgeBase[]>([]);
  const [sharing, setSharing] = useState<KnowledgeBase | null>(null);
  const [error, setError] = useState('');
  const [creating, setCreating] = useState(false);
  const [newName, setNewName] = useState('');
  const [newDescription, setNewDescription] = useState('');
	const [newIcon, setNewIcon] = useState('📚');
	const [newTags, setNewTags] = useState('');
  const [deleting, setDeleting] = useState<KnowledgeBase | null>(null);
  const [pending, setPending] = useState('');
  const [busy, setBusy] = useState(false);
	const [loading, setLoading] = useState(true);
	const [query, setQuery] = useState('');
	const [scope, setScope] = useState('all');

  // The owner sees its KBs in a dedicated management area. Only published
  // bases shared from another workspace appear as something to install.
  // A draft is never mountable, even in the workspace that owns it.
  const workspace = workspaces.find((item) => item.id === workspaceID);
  const canInstall = workspace?.role === 'owner' || workspace?.role === 'admin';
	const visibleBases = useMemo(() => {
		const needle = query.trim().toLocaleLowerCase();
		return bases.filter((base) => {
			if (scope === 'owned' && base.access !== 'owner') return false;
			if (scope === 'installed' && !base.is_mounted) return false;
			if (scope === 'shared' && base.access === 'owner') return false;
			return !needle || `${base.name} ${base.description} ${base.tags.join(' ')}`.toLocaleLowerCase().includes(needle);
		});
	}, [bases, query, scope]);

  const load = useCallback((targetWorkspace: string) => {
		setLoading(true);
    api.knowledgeBases(targetWorkspace || undefined)
      .then((result) => setBases(result.knowledge_bases))
      .catch((caught) => {
        if (caught instanceof APIError && caught.status === 401) router.replace('/');
        else setError(caught instanceof Error ? caught.message : t('kb.loadFailed'));
		})
		.finally(() => setLoading(false));
  }, [router, t]);

  useEffect(() => {
    Promise.all([api.me(), api.workspaces(), api.workspaceDirectory()])
      .then(([me, mine, all]) => {
        setWorkspaces(mine.workspaces);
        setDirectory(all.workspaces);
        setWorkspaceID(requestedWorkspaceID || me.user.last_workspace_id || mine.workspaces[0]?.id || '');
      })
      .catch((caught) => {
        if (caught instanceof APIError && caught.status === 401) router.replace('/');
      });
  }, [requestedWorkspaceID, router]);

  useEffect(() => {
    const loadTimer = window.setTimeout(() => load(workspaceID), 0);
    return () => window.clearTimeout(loadTimer);
  }, [load, workspaceID]);

  async function create() {
    setBusy(true);
    setError('');
    try {
		const result = await api.createKnowledgeBase({
			name: newName,
			description: newDescription,
			workspace_id: workspaceID,
			icon: newIcon,
			tags: newTags.split(',').map((tag) => tag.trim()).filter(Boolean),
		});
      setBases((current) => [result.knowledge_base, ...current]);
      setNewName('');
      setNewDescription('');
		setNewIcon('📚');
		setNewTags('');
      setCreating(false);
		router.push(`/knowledge/${result.knowledge_base.id}?workspace=${encodeURIComponent(workspaceID)}`);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : t('kb.saveFailed'));
    } finally {
      setBusy(false);
    }
  }

  async function remove() {
    if (!deleting) return;
    setBusy(true);
    try {
      await api.deleteKnowledgeBase(deleting.id);
      setBases((current) => current.filter((item) => item.id !== deleting.id));
      setDeleting(null);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : t('kb.saveFailed'));
    } finally {
      setBusy(false);
    }
  }

  // Install, update and uninstall are the same write from the workspace's side:
  // it is choosing which version of a base it runs on, or none at all.
  async function setInstalled(base: KnowledgeBase, installed: boolean) {
    if (!workspaceID) return;
    setPending(base.id);
    setError('');
    try {
      if (installed) await api.mountKnowledge(workspaceID, base.id);
      else await api.unmountKnowledge(workspaceID, base.id);
      load(workspaceID);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : t('kb.mountFailed'));
    } finally {
      setPending('');
    }
  }

  function installationAction(base: KnowledgeBase) {
    if (base.version === 0) return null;
    if (!canInstall) return base.is_mounted ? <StatusLabel label={t('kb.installed')} variant="success" /> : null;
    return (
      <Button
        isDisabled={pending === base.id || !workspaceID}
        isLoading={pending === base.id}
        label={
          base.update_available ? t('kb.update')
            : base.is_mounted ? t('kb.uninstall')
              : t('kb.install')
        }
        onClick={() => void setInstalled(base, !base.is_mounted || base.update_available)}
        size="sm"
        variant={base.is_mounted && !base.update_available ? 'ghost' : 'primary'}
      />
    );
  }

  function managementActions(base: KnowledgeBase) {
    return (
      <HStack gap={1} vAlign="center">
        <Button label={t('kb.share')} onClick={() => setSharing(base)} size="sm" variant="secondary" />
        <IconButton
          icon={<Trash2 size={14} />}
          label={t('kb.delete')}
          onClick={() => setDeleting(base)}
          size="sm"
          variant="ghost"
        />
      </HStack>
    );
  }

  return (
    <>
      <Layout
        height="fill"
        header={
          <PageHeader
            actions={<Button label={t('kb.create')} onClick={() => setCreating(true)} size="sm" variant="primary" />}
            count={bases.length}
            description={t('kb.subtitle')}
            title={t('kb.title')}
          />
        }
        content={
          <LayoutContent padding={6}>
            <VStack gap={4}>
              {error ? <Banner isDismissable onDismiss={() => setError('')} status="error" title={error} /> : null}

              <HStack gap={3} vAlign="center" wrap="wrap">
                <Selector
                  isLabelHidden
                  label={t('kb.scope')}
                  onChange={setScope}
                  options={[
                    {value: 'all', label: t('kb.scopeAll')},
                    {value: 'owned', label: t('kb.scopeOwned')},
                    {value: 'installed', label: t('kb.scopeInstalled')},
                    {value: 'shared', label: t('kb.scopeShared')},
                  ]}
                  value={scope}
                  width={220}
                />
                <TextInput
                  isLabelHidden
                  label={t('kb.search')}
                  onChange={setQuery}
                  placeholder={t('kb.search')}
                  size="lg"
                  startIcon={<Icon icon={Search} size="sm" />}
                  value={query}
                  width={280}
                />
              </HStack>

				{loading ? (
					<Grid columns={{minWidth: 280, max: 3}} gap={4} width="100%">
						{[0, 1, 2].map((index) => (
							<Card key={index} padding={4} width="100%">
								<VStack gap={3}>
									<Skeleton height={44} index={index} radius="rounded" width={44} />
									<Skeleton height={18} index={index + 1} width="55%" />
									<Skeleton height={14} index={index + 2} width="85%" />
									<Skeleton height={14} index={index + 3} width="45%" />
								</VStack>
							</Card>
						))}
					</Grid>
				) : visibleBases.length === 0 ? (
					<EmptyState
						actions={<Button label={t('kb.create')} onClick={() => setCreating(true)} variant="primary" />}
						description={query ? 'Không có knowledge base phù hợp.' : t('kb.managedEmpty')}
						icon={<BookOpen size={56} strokeWidth={1} />}
						title={query ? 'Không tìm thấy kết quả' : t('kb.title')}
					/>
				) : (
					<Grid columns={{minWidth: 280, max: 3}} gap={4} width="100%">
						{visibleBases.map((base) => (
							<KnowledgeCard
								base={base}
								key={base.id}
								primary={installationAction(base)}
								secondary={base.access === 'owner' ? managementActions(base) : undefined}
								t={t}
								workspaceID={workspaceID}
							/>
						))}
					</Grid>
				)}
            </VStack>
          </LayoutContent>
        }
      />

		<Dialog isOpen={creating} onOpenChange={setCreating} purpose="form" width={520}>
        <Layout
          content={
            <LayoutContent>
              <VStack gap={4}>
						<HStack gap={2} vAlign="end">
							<KnowledgeIconPicker onChange={setNewIcon} value={newIcon} />
							<TextInput label={t('kb.name')} onChange={setNewName} value={newName} width="100%" />
						</HStack>
						<TextArea label={t('kb.description')} maxLength={500} onChange={setNewDescription} rows={4} value={newDescription} width="100%" />
						<TextInput label="Tags" onChange={setNewTags} placeholder="quy trình, vận hành, an toàn" value={newTags} width="100%" />
              </VStack>
            </LayoutContent>
          }
          footer={
            <LayoutFooter>
              <HStack gap={2} hAlign="end">
                <Button label={t('common.cancel')} onClick={() => setCreating(false)} variant="secondary" />
                <Button isDisabled={!newName.trim() || busy} isLoading={busy} label={t('common.save')} onClick={() => void create()} variant="primary" />
              </HStack>
            </LayoutFooter>
          }
          header={<DialogHeader onOpenChange={setCreating} title={t('kb.create')} />}
        />
      </Dialog>

      {sharing ? (
        <ShareDialog
          base={sharing}
          directory={directory}
          onClose={() => setSharing(null)}
          onError={setError}
          onSaved={() => { setSharing(null); load(workspaceID); }}
        />
      ) : null}

      <AlertDialog
        actionLabel={t('kb.delete')}
        cancelLabel={t('common.cancel')}
        description={t('kb.deleteBody')}
        isActionLoading={busy}
        isOpen={deleting !== null}
        onAction={() => void remove()}
        onOpenChange={(open) => { if (!open) setDeleting(null); }}
        title={t('kb.deleteTitle')}
      />
    </>
  );
}

/**
 * One knowledge base as a card: what it is, how big it is, which release it is
 * on, and the single action that matters for it here.
 */
function KnowledgeCard({base, primary, secondary, t, workspaceID}: {
  base: KnowledgeBase;
  primary: React.ReactNode;
  secondary?: React.ReactNode;
  t: Translate;
	workspaceID: string;
}) {
  return (
    <Card padding={0} width="100%">
      <VStack gap={0} height="100%">
        <Section padding={4}>
					<VStack gap={3}>
						<HStack gap={3} vAlign="center">
							<Text type="large">{base.icon || '📚'}</Text>
							<VStack gap={1}>
								<Link href={`/knowledge/${base.id}?workspace=${encodeURIComponent(workspaceID)}`}>
									<Text type="body" weight="semibold">{base.name}</Text>
								</Link>
								{base.processing_count > 0 ? (
									<HStack gap={1} vAlign="center">
										<StatusLabel isPulsing label={`${base.processing_count} đang xử lý`} variant="accent" />
									</HStack>
								) : null}
							</VStack>
						</HStack>
            <Text color="secondary" type="supporting">
              {base.description || t('kb.noDescription')}
            </Text>
						{base.tags.length > 0 ? (
							<HStack gap={1} wrap="wrap">
								{base.tags.slice(0, 3).map((tag) => <Token key={tag} label={tag} size="sm" />)}
							</HStack>
						) : null}
          </VStack>
        </Section>

        <Section dividers={['top']} padding={4}>
          <HStack gap={2} hAlign="between" vAlign="center" wrap="wrap">
            <HStack gap={2} vAlign="center" wrap="wrap">
              <Text color="secondary" type="supporting">
                {base.document_count === 1 ? t('kb.docCountOne') : t('kb.docCount', {count: base.document_count})}
              </Text>
							<Text color="secondary" type="supporting">{base.shared_count} chia sẻ</Text>
              <StatusLabel
                label={base.version === 0 ? t('kb.draft') : t('kb.version', {version: base.version})}
                variant={base.version === 0 ? 'warning' : 'neutral'}
              />
              {base.update_available ? (
                <StatusLabel label={t('kb.updateAvailable', {version: base.version})} variant="accent" />
              ) : null}
              {base.access === 'owner' && base.has_unpublished_changes ? (
                <StatusLabel label={t('kb.unpublished')} variant="warning" />
              ) : null}
            </HStack>
            <HStack gap={1} vAlign="center">
              {primary}
              {secondary}
            </HStack>
          </HStack>
        </Section>
      </VStack>
    </Card>
  );
}

/**
 * Reach, chosen by the owner.
 *
 * Sharing is workspace to workspace: everyone signs in to a workspace already,
 * so naming a person as well would only restate that. Choosing specific
 * workspaces lists the whole organisation, because the point is to reach teams
 * you are not part of.
 */
function ShareDialog({base, directory, onClose, onError, onSaved}: {
  base: KnowledgeBase;
  directory: WorkspaceRef[];
  onClose: () => void;
  onError: (value: string) => void;
  onSaved: () => void;
}) {
  const t = useTranslation();
  const [visibility, setVisibility] = useState(base.visibility);
  const [selected, setSelected] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api.knowledgeShares(base.id)
      .then((result) => setSelected(result.shares.map((item) => item.workspace_id)))
      .catch(() => setSelected([]));
  }, [base.id]);

  async function save() {
    setBusy(true);
    try {
      await api.updateKnowledgeBase(base.id, {visibility, workspaces: selected});
      onSaved();
    } catch (caught) {
      onError(caught instanceof Error ? caught.message : t('kb.saveFailed'));
    } finally {
      setBusy(false);
    }
  }

  // The owning workspace always has the base; showing it as a choice would
  // suggest it could be taken away here.
  const others = directory.filter((item) => item.id !== base.owner_workspace_id);

  return (
    <Dialog isOpen onOpenChange={onClose} purpose="form" width={520}>
      <Layout
        content={
          <LayoutContent>
            <VStack gap={4}>
              <Selector
                label={t('kb.visibility')}
                onChange={(value) => setVisibility(value as KnowledgeBase['visibility'])}
                options={[
                  {value: 'workspace', label: t('kb.visWorkspace')},
                  {value: 'selected', label: t('kb.visSelected')},
                  {value: 'everyone', label: t('kb.visEveryone')},
                ]}
                value={visibility}
                width="100%"
              />

              {visibility === 'selected' ? (
                <Card padding={0} width="100%">
                  <Section dividers={['bottom']} padding={3}>
                    <Text type="label">{t('kb.sharedWorkspaces')}</Text>
                  </Section>
                  <Section padding={3}>
                    <CheckboxList
                      isLabelHidden
                      label={t('kb.sharedWorkspaces')}
                      onChange={setSelected}
                      value={selected}
                      width="100%"
                    >
                      {others.map((item) => (
                        <CheckboxListItem key={item.id} label={item.name} value={item.id} />
                      ))}
                    </CheckboxList>
                  </Section>
                </Card>
              ) : null}
            </VStack>
          </LayoutContent>
        }
        footer={
          <LayoutFooter>
            <HStack gap={2} hAlign="end">
              <Button label={t('common.cancel')} onClick={onClose} variant="secondary" />
              <Button isDisabled={busy} isLoading={busy} label={t('common.save')} onClick={() => void save()} variant="primary" />
            </HStack>
          </LayoutFooter>
        }
        header={<DialogHeader onOpenChange={onClose} subtitle={base.name} title={t('kb.share')} />}
      />
    </Dialog>
  );
}
