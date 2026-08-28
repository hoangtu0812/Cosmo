'use client';

import {useCallback, useEffect, useState} from 'react';
import {useRouter} from 'next/navigation';
import {ArrowLeft, Building2, Library, Trash2, UserRound} from 'lucide-react';
import {AlertDialog} from '@astryxdesign/core/AlertDialog';
import {AppShell} from '@astryxdesign/core/AppShell';
import {Badge} from '@astryxdesign/core/Badge';
import {Banner} from '@astryxdesign/core/Banner';
import {Button} from '@astryxdesign/core/Button';
import {Card} from '@astryxdesign/core/Card';
import {Dialog, DialogHeader} from '@astryxdesign/core/Dialog';
import {EmptyState} from '@astryxdesign/core/EmptyState';
import {Icon} from '@astryxdesign/core/Icon';
import {IconButton} from '@astryxdesign/core/IconButton';
import {Item} from '@astryxdesign/core/Item';
import {HStack, Layout, LayoutContent, LayoutHeader, VStack} from '@astryxdesign/core/Layout';
import {List} from '@astryxdesign/core/List';
import {Section} from '@astryxdesign/core/Section';
import {Selector} from '@astryxdesign/core/Selector';
import {SideNav, SideNavHeading, SideNavItem, SideNavSection} from '@astryxdesign/core/SideNav';
import {Text} from '@astryxdesign/core/Text';
import {TextInput} from '@astryxdesign/core/TextInput';
import {Toolbar} from '@astryxdesign/core/Toolbar';
import {api, APIError, KnowledgeBase, KnowledgeGrant, User, Workspace} from '../lib/api';
import {useTranslation} from '../lib/i18n';

export default function KnowledgePage() {
  const t = useTranslation();
  const router = useRouter();
  const [user, setUser] = useState<User | null>(null);
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [bases, setBases] = useState<KnowledgeBase[]>([]);
  const [selected, setSelected] = useState<KnowledgeBase | null>(null);
  const [error, setError] = useState('');
  const [creating, setCreating] = useState(false);
  const [newName, setNewName] = useState('');
  const [newDescription, setNewDescription] = useState('');
  const [deleting, setDeleting] = useState<KnowledgeBase | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(() => {
    api.knowledgeBases()
      .then((result) => setBases(result.knowledge_bases))
      .catch((caught) => {
        if (caught instanceof APIError && caught.status === 401) router.replace('/');
        else setError(caught instanceof Error ? caught.message : t('kb.loadFailed'));
      });
  }, [router, t]);

  useEffect(() => {
    Promise.all([api.me(), api.workspaces()])
      .then(([me, result]) => { setUser(me.user); setWorkspaces(result.workspaces); })
      .catch((caught) => {
        if (caught instanceof APIError && caught.status === 401) router.replace('/');
      });
  }, [router]);

  useEffect(load, [load]);

  async function create() {
    setBusy(true);
    setError('');
    try {
      const result = await api.createKnowledgeBase(newName, newDescription);
      setBases((current) => [result.knowledge_base, ...current]);
      setNewName('');
      setNewDescription('');
      setCreating(false);
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
      if (selected?.id === deleting.id) setSelected(null);
      setDeleting(null);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : t('kb.saveFailed'));
    } finally {
      setBusy(false);
    }
  }

  async function setVisibility(base: KnowledgeBase, visibility: string) {
    try {
      const result = await api.updateKnowledgeBase(base.id, {visibility});
      setBases((current) => current.map((item) => item.id === base.id ? {...item, ...result.knowledge_base} : item));
      setSelected((current) => current && current.id === base.id ? {...current, ...result.knowledge_base} : current);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : t('kb.saveFailed'));
    }
  }

  return (
    <AppShell
      contentPadding={0}
      sideNav={
        <SideNav
          header={
            <SideNavHeading
              heading={t('kb.title')}
              icon={<Icon icon={Library} size="sm" />}
              subheading={user?.email}
            />
          }
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
        contentWidth={880}
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
            <VStack gap={4}>
              {error && <Banner isDismissable onDismiss={() => setError('')} status="error" title={error} />}

              {bases.length === 0 ? (
                <EmptyState description={t('kb.empty')} icon={<Library size={64} strokeWidth={1} />} title={t('kb.title')} />
              ) : (
                <Card padding={0} width="100%">
                  <List>
                    {bases.map((base) => (
                      <Item
                        as="li"
                        description={`${base.description || ''}${base.description ? ' · ' : ''}${t('kb.owner')}: ${base.owner_user_id === user?.id ? t('kb.you') : base.owner_name}`}
                        endContent={
                          <HStack gap={2} vAlign="center">
                            <Badge label={base.visibility === 'organization' ? t('kb.organization') : t('kb.private')} variant="neutral" />
                            {base.access === 'owner' && (
                              <>
                                <Button label={t('kb.share')} onClick={() => setSelected(base)} size="sm" variant="secondary" />
                                <IconButton icon={<Trash2 size={14} />} label={t('kb.delete')} onClick={() => setDeleting(base)} size="sm" variant="ghost" />
                              </>
                            )}
                          </HStack>
                        }
                        key={base.id}
                        label={base.name}
                        startContent={<Icon icon={Library} size="sm" />}
                      />
                    ))}
                  </List>
                </Card>
              )}
            </VStack>
          </LayoutContent>
        }
      />

      <Dialog isOpen={creating} onOpenChange={setCreating} padding={0} purpose="form">
        <DialogHeader onOpenChange={setCreating} title={t('kb.create')} />
        <VStack gap={4} padding={4}>
          <TextInput label={t('kb.name')} onChange={setNewName} value={newName} width="100%" />
          <TextInput label={t('kb.description')} onChange={setNewDescription} value={newDescription} width="100%" />
          <HStack gap={2} hAlign="end">
            <Button label={t('common.cancel')} onClick={() => setCreating(false)} variant="secondary" />
            <Button isDisabled={!newName.trim() || busy} isLoading={busy} label={t('common.save')} onClick={() => void create()} variant="primary" />
          </HStack>
        </VStack>
      </Dialog>

      {selected && (
        <ShareDialog
          base={selected}
          onClose={() => setSelected(null)}
          onError={setError}
          onVisibility={(visibility) => void setVisibility(selected, visibility)}
          workspaces={workspaces}
        />
      )}

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

function ShareDialog({base, onClose, onError, onVisibility, workspaces}: {
  base: KnowledgeBase;
  onClose: () => void;
  onError: (value: string) => void;
  onVisibility: (visibility: string) => void;
  workspaces: Workspace[];
}) {
  const t = useTranslation();
  const [grants, setGrants] = useState<KnowledgeGrant[]>([]);
  const [subjectType, setSubjectType] = useState('user');
  const [email, setEmail] = useState('');
  const [workspaceID, setWorkspaceID] = useState(workspaces[0]?.id ?? '');
  const [role, setRole] = useState('viewer');
  const [busy, setBusy] = useState(false);

  const load = useCallback(() => {
    api.knowledgeGrants(base.id).then((result) => setGrants(result.grants)).catch(() => undefined);
  }, [base.id]);

  useEffect(load, [load]);

  async function share() {
    setBusy(true);
    try {
      const result = await api.createKnowledgeGrant(base.id, {
        subject_type: subjectType,
        email: subjectType === 'user' ? email : undefined,
        workspace_id: subjectType === 'workspace' ? workspaceID : undefined,
        role,
      });
      setGrants(result.grants);
      setEmail('');
    } catch (caught) {
      onError(caught instanceof Error ? caught.message : t('kb.shareFailed'));
    } finally {
      setBusy(false);
    }
  }

  async function revoke(grant: KnowledgeGrant) {
    try {
      await api.deleteKnowledgeGrant(base.id, grant.subject_type, grant.subject_id);
      load();
    } catch (caught) {
      onError(caught instanceof Error ? caught.message : t('kb.shareFailed'));
    }
  }

  return (
    <Dialog isOpen onOpenChange={onClose} padding={0} purpose="form" width={560}>
      <DialogHeader onOpenChange={onClose} subtitle={base.name} title={t('kb.share')} />
      <VStack gap={4} padding={4}>
        <Selector
          label={t('kb.visibility')}
          onChange={onVisibility}
          options={[
            {value: 'private', label: t('kb.private')},
            {value: 'organization', label: t('kb.organization')},
          ]}
          value={base.visibility}
          width="100%"
        />

        <Section dividers={['top']} padding={0}>
          <VStack gap={3} paddingBlock={4}>
            <Selector
              label={t('kb.share')}
              onChange={setSubjectType}
              options={[
                {value: 'user', label: t('kb.shareUser')},
                {value: 'workspace', label: t('kb.shareWorkspace')},
              ]}
              value={subjectType}
              width="100%"
            />
            <HStack gap={2} vAlign="end">
              {subjectType === 'user' ? (
                <TextInput label={t('kb.shareEmail')} onChange={setEmail} placeholder="name@example.com" type="email" value={email} width="100%" />
              ) : (
                <Selector
                  label={t('settings.workspace')}
                  onChange={setWorkspaceID}
                  options={workspaces.map((item) => ({value: item.id, label: item.name}))}
                  value={workspaceID}
                  width="100%"
                />
              )}
              <Selector
                label={t('kb.role')}
                onChange={setRole}
                options={[{value: 'viewer', label: t('kb.viewer')}, {value: 'editor', label: t('kb.editor')}]}
                value={role}
              />
              <Button
                isDisabled={busy || (subjectType === 'user' ? !email.trim() : !workspaceID)}
                isLoading={busy}
                label={t('kb.share')}
                onClick={() => void share()}
                variant="primary"
              />
            </HStack>
          </VStack>
        </Section>

        {grants.length > 0 && (
          <Card padding={0} width="100%">
            <Section dividers={['bottom']} padding={4}>
              <Text type="label" weight="semibold">{t('kb.sharedWith', {count: grants.length})}</Text>
            </Section>
            <List>
              {grants.map((grant) => (
                <Item
                  as="li"
                  description={grant.role === 'editor' ? t('kb.editor') : t('kb.viewer')}
                  endContent={
                    <IconButton
                      icon={<Trash2 size={14} />}
                      label={t('kb.revoke', {subject: grant.subject_name || grant.subject_id})}
                      onClick={() => void revoke(grant)}
                      size="sm"
                      variant="ghost"
                    />
                  }
                  key={`${grant.subject_type}:${grant.subject_id}`}
                  label={grant.subject_name || grant.subject_id}
                  startContent={<Icon icon={grant.subject_type === 'user' ? UserRound : Building2} size="sm" />}
                />
              ))}
            </List>
          </Card>
        )}
      </VStack>
    </Dialog>
  );
}
