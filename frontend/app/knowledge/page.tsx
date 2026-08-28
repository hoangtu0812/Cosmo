'use client';

import {useCallback, useEffect, useMemo, useState} from 'react';
import {useRouter} from 'next/navigation';
import {ArrowLeft, Library, Trash2} from 'lucide-react';
import {AlertDialog} from '@astryxdesign/core/AlertDialog';
import {AppShell} from '@astryxdesign/core/AppShell';
import {Badge} from '@astryxdesign/core/Badge';
import {Banner} from '@astryxdesign/core/Banner';
import {Button} from '@astryxdesign/core/Button';
import {Card} from '@astryxdesign/core/Card';
import {CheckboxList, CheckboxListItem} from '@astryxdesign/core/CheckboxList';
import {Dialog, DialogHeader} from '@astryxdesign/core/Dialog';
import {Grid} from '@astryxdesign/core/Grid';
import {Icon} from '@astryxdesign/core/Icon';
import {IconButton} from '@astryxdesign/core/IconButton';
import {HStack, Layout, LayoutContent, LayoutFooter, LayoutHeader, VStack} from '@astryxdesign/core/Layout';
import {Link} from '@astryxdesign/core/Link';
import {Section} from '@astryxdesign/core/Section';
import {Selector} from '@astryxdesign/core/Selector';
import {SideNav, SideNavHeading, SideNavItem, SideNavSection} from '@astryxdesign/core/SideNav';
import {Text} from '@astryxdesign/core/Text';
import {TextInput} from '@astryxdesign/core/TextInput';
import {Toolbar} from '@astryxdesign/core/Toolbar';
import {api, APIError, KnowledgeBase, User, Workspace, WorkspaceRef} from '../lib/api';
import {useTranslation} from '../lib/i18n';

type Translate = ReturnType<typeof useTranslation>;

export default function KnowledgePage() {
  const t = useTranslation();
  const router = useRouter();
  const [user, setUser] = useState<User | null>(null);
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [directory, setDirectory] = useState<WorkspaceRef[]>([]);
  const [workspaceID, setWorkspaceID] = useState('');
  const [bases, setBases] = useState<KnowledgeBase[]>([]);
  const [sharing, setSharing] = useState<KnowledgeBase | null>(null);
  const [error, setError] = useState('');
  const [creating, setCreating] = useState(false);
  const [newName, setNewName] = useState('');
  const [newDescription, setNewDescription] = useState('');
  const [deleting, setDeleting] = useState<KnowledgeBase | null>(null);
  const [pending, setPending] = useState('');
  const [busy, setBusy] = useState(false);

  // Every visible KB is available to install, including one owned by this
  // workspace. Management and installation are separate actions: ownership
  // must never cause a KB to be silently enabled for chat.
  const available = useMemo(() => bases, [bases]);
  const workspace = workspaces.find((item) => item.id === workspaceID);
  const canInstall = workspace?.role === 'owner' || workspace?.role === 'admin';

  const load = useCallback((targetWorkspace: string) => {
    api.knowledgeBases(targetWorkspace || undefined)
      .then((result) => setBases(result.knowledge_bases))
      .catch((caught) => {
        if (caught instanceof APIError && caught.status === 401) router.replace('/');
        else setError(caught instanceof Error ? caught.message : t('kb.loadFailed'));
      });
  }, [router, t]);

  useEffect(() => {
    Promise.all([api.me(), api.workspaces(), api.workspaceDirectory()])
      .then(([me, mine, all]) => {
        setUser(me.user);
        setWorkspaces(mine.workspaces);
        setDirectory(all.workspaces);
        setWorkspaceID(me.user.last_workspace_id || mine.workspaces[0]?.id || '');
      })
      .catch((caught) => {
        if (caught instanceof APIError && caught.status === 401) router.replace('/');
      });
  }, [router]);

  useEffect(() => { load(workspaceID); }, [load, workspaceID]);

  async function create() {
    setBusy(true);
    setError('');
    try {
      const result = await api.createKnowledgeBase(newName, newDescription, workspaceID);
      setBases((current) => [result.knowledge_base, ...current]);
      setNewName('');
      setNewDescription('');
      setCreating(false);
      router.push(`/knowledge/${result.knowledge_base.id}`);
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

  async function selectWorkspace(id: string) {
    setWorkspaceID(id);
    setError('');
    try {
      // The detail page is addressed by KB id, so keep the server-side
      // workspace context in sync before navigating into one.
      await api.selectWorkspace(id);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : t('kb.loadFailed'));
    }
  }

  return (
    <AppShell
      contentPadding={0}
      sideNav={
        <SideNav
          header={<SideNavHeading heading={t('kb.title')} icon={<Icon icon={Library} size="sm" />} subheading={user?.email} />}
        >
          <SideNavSection isHeaderHidden title={t('kb.title')}>
            <SideNavItem
              icon={<Icon icon={ArrowLeft} size="sm" />}
              label={t('settings.back')}
              onClick={() => router.push('/chat')}
            />
          </SideNavSection>
        </SideNav>
      }
    >
      <Layout
        contentWidth={1120}
        height="fill"
        header={
          <LayoutHeader hasDivider>
            <Toolbar
              endContent={<Button label={t('kb.create')} onClick={() => setCreating(true)} size="sm" variant="primary" />}
              label={t('kb.title')}
              startContent={<Text type="label" weight="semibold">{t('kb.title')}</Text>}
            />
          </LayoutHeader>
        }
        content={
          <LayoutContent padding={6}>
            <VStack gap={6}>
              {error ? <Banner isDismissable onDismiss={() => setError('')} status="error" title={error} /> : null}

              <VStack gap={3}>
                <HStack gap={3} hAlign="between" vAlign="center">
                  <Text type="label" weight="semibold">{t('kb.available')}</Text>
                  {workspaces.length > 1 ? (
                    <Selector
                      isLabelHidden
                      label={t('kb.installTarget')}
                      onChange={(id) => void selectWorkspace(id)}
                      options={workspaces.map((item) => ({value: item.id, label: item.name}))}
                      size="sm"
                      value={workspaceID}
                      width={240}
                    />
                  ) : null}
                </HStack>
                {available.length === 0 ? (
                  <Text color="secondary" type="supporting">{t('kb.availableEmpty')}</Text>
                ) : (
                  <Grid columns={{minWidth: 320, max: 3}} gap={4} width="100%">
                    {available.map((base) => (
                      <KnowledgeCard
                        base={base}
                        key={base.id}
                        primary={
                          canInstall ? (
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
                          ) : base.is_mounted ? (
                            <Badge label={t('kb.installed')} variant="neutral" />
                          ) : null
                        }
                        secondary={
                          base.access === 'owner' ? (
                            <HStack gap={1} vAlign="center">
                              <Button
                                label={t('kb.share')}
                                onClick={() => setSharing(base)}
                                size="sm"
                                variant="secondary"
                              />
                              <IconButton
                                icon={<Trash2 size={14} />}
                                label={t('kb.delete')}
                                onClick={() => setDeleting(base)}
                                size="sm"
                                variant="ghost"
                              />
                            </HStack>
                          ) : undefined
                        }
                        t={t}
                      />
                    ))}
                  </Grid>
                )}
              </VStack>
            </VStack>
          </LayoutContent>
        }
      />

      <Dialog isOpen={creating} onOpenChange={setCreating} purpose="form">
        <Layout
          content={
            <LayoutContent>
              <VStack gap={4}>
                <TextInput label={t('kb.name')} onChange={setNewName} value={newName} width="100%" />
                <TextInput label={t('kb.description')} onChange={setNewDescription} value={newDescription} width="100%" />
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
    </AppShell>
  );
}

/**
 * One knowledge base as a card: what it is, how big it is, which release it is
 * on, and the single action that matters for it here.
 */
function KnowledgeCard({base, primary, secondary, t}: {
  base: KnowledgeBase;
  primary: React.ReactNode;
  secondary?: React.ReactNode;
  t: Translate;
}) {
  return (
    <Card padding={0} width="100%">
      <VStack gap={0} height="100%">
        <Section padding={4}>
          <VStack gap={2}>
            <Link href={`/knowledge/${base.id}`}>
              <Text type="body" weight="semibold">{base.name}</Text>
            </Link>
            <Text color="secondary" type="supporting">
              {base.description || t('kb.noDescription')}
            </Text>
          </VStack>
        </Section>

        <Section dividers={['top']} padding={4}>
          <HStack gap={2} hAlign="between" vAlign="center" wrap="wrap">
            <HStack gap={2} vAlign="center" wrap="wrap">
              <Text color="secondary" type="supporting">
                {base.document_count === 1 ? t('kb.docCountOne') : t('kb.docCount', {count: base.document_count})}
              </Text>
              <Badge
                label={base.version === 0 ? t('kb.draft') : t('kb.version', {version: base.version})}
                variant={base.version === 0 ? 'warning' : 'neutral'}
              />
              {base.update_available ? (
                <Badge label={t('kb.updateAvailable', {version: base.version})} variant="info" />
              ) : null}
              {base.access === 'owner' && base.has_unpublished_changes ? (
                <Badge label={t('kb.unpublished')} variant="warning" />
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
                    <Text type="label" weight="semibold">{t('kb.sharedWorkspaces')}</Text>
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
