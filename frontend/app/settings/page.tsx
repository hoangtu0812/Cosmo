'use client';

import {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import {useRouter} from 'next/navigation';
import {ArrowLeft, Building2, Copy, KeyRound, Trash2, Users} from 'lucide-react';
import {AppShell} from '@astryxdesign/core/AppShell';
import {Avatar} from '@astryxdesign/core/Avatar';
import {Badge} from '@astryxdesign/core/Badge';
import {Banner} from '@astryxdesign/core/Banner';
import {Button} from '@astryxdesign/core/Button';
import {Card} from '@astryxdesign/core/Card';
import {EmptyState} from '@astryxdesign/core/EmptyState';
import {Heading} from '@astryxdesign/core/Heading';
import {HStack, Layout, LayoutContent, LayoutHeader, VStack} from '@astryxdesign/core/Layout';
import {Icon} from '@astryxdesign/core/Icon';
import {IconButton} from '@astryxdesign/core/IconButton';
import {Item} from '@astryxdesign/core/Item';
import {List} from '@astryxdesign/core/List';
import {Section} from '@astryxdesign/core/Section';
import {Selector} from '@astryxdesign/core/Selector';
import {SideNav, SideNavHeading, SideNavItem, SideNavSection} from '@astryxdesign/core/SideNav';
import {Text} from '@astryxdesign/core/Text';
import {TextInput} from '@astryxdesign/core/TextInput';
import {Toolbar} from '@astryxdesign/core/Toolbar';
import {api, APIError, Invitation, LLMSettings, Member, User, Workspace} from '../lib/api';
import {useTranslation} from '../lib/i18n';

type SectionKey = 'model' | 'members' | 'workspace';

const WORKSPACE_SECTIONS: {key: SectionKey; labelKey: 'settings.model' | 'settings.members' | 'settings.workspace'; icon: typeof KeyRound}[] = [
  {key: 'model', labelKey: 'settings.model', icon: KeyRound},
  {key: 'members', labelKey: 'settings.members', icon: Users},
  {key: 'workspace', labelKey: 'settings.workspace', icon: Building2},
];
const ALL_SECTIONS: SectionKey[] = ['model', 'members', 'workspace'];

export default function SettingsPage() {
  const t = useTranslation();
  const router = useRouter();
  const [user, setUser] = useState<User | null>(null);
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [workspaceID, setWorkspaceID] = useState('');
  // Deep links from the workspace menu land straight on a section.
  const [section, setSection] = useState<SectionKey>(() => {
    if (typeof window === 'undefined') return 'model';
    const requested = new URLSearchParams(window.location.search).get('section');
    return ALL_SECTIONS.includes(requested as SectionKey) ? (requested as SectionKey) : 'model';
  });
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');

  const workspace = useMemo(() => workspaces.find((item) => item.id === workspaceID), [workspaces, workspaceID]);
  const canAdmin = user?.role === 'admin' || workspace?.role === 'owner' || workspace?.role === 'admin';

  useEffect(() => {
    Promise.all([api.me(), api.workspaces()]).then(([me, result]) => {
      setUser(me.user);
      setWorkspaces(result.workspaces);
      // Settings is always scoped to the workspace currently selected in the
      // app. It deliberately ignores a workspace URL parameter, so opening
      // settings cannot become an alternate workspace switcher.
      const target = me.user.last_workspace_id ?? result.workspaces[0]?.id ?? '';
      setWorkspaceID(target);
    }).catch((caught) => {
      if (caught instanceof APIError && caught.status === 401) router.replace('/');
      else setError(caught instanceof Error ? caught.message : t('settings.loadFailed'));
    });
  }, [router, t]);

  function backToApp() {
    router.push(workspaceID ? `/chat?workspace=${encodeURIComponent(workspaceID)}` : '/chat');
  }

  return (
    <AppShell
      contentPadding={0}
      sideNav={
        <SideNav
          footer={
            user ? (
              <Card padding={3} width="100%">
                <HStack gap={2} vAlign="center">
                  <Avatar name={user.name} size="md" src={user.has_avatar ? api.userAvatarURL() : undefined} />
                  <VStack gap={0} minWidth={0}>
                    <Text truncate type="label" weight="semibold">{user.name}</Text>
                    <Text color="secondary" truncate type="supporting">{user.email}</Text>
                  </VStack>
                </HStack>
              </Card>
            ) : undefined
          }
          header={
            <SideNavHeading
              heading={t('settings.title')}
              icon={<Icon icon={ArrowLeft} size="sm" />}
              onClick={backToApp}
              subheading={workspace?.name}
            />
          }
        >
          <SideNavSection title={t('settings.workspaceGroup')}>
            {WORKSPACE_SECTIONS.map((item) => (
              <SideNavItem
                icon={<Icon icon={item.icon} size="sm" />}
                isSelected={section === item.key}
                key={item.key}
                label={t(item.labelKey)}
                onClick={() => { setSection(item.key); setNotice(''); setError(''); }}
              />
            ))}
          </SideNavSection>
        </SideNav>
      }
    >
      <Layout
        contentWidth={720}
        height="fill"
        header={
          <LayoutHeader hasDivider>
            <Toolbar
              label={t('settings.nav')}
              startContent={<Button icon={<ArrowLeft size={15} />} label={t('settings.back')} onClick={backToApp} size="sm" variant="ghost" />}
            />
          </LayoutHeader>
        }
        content={
          <LayoutContent padding={6}>
            <VStack gap={6}>
              {error && <Banner isDismissable onDismiss={() => setError('')} status="error" title={error} />}
              {notice && <Banner isDismissable onDismiss={() => setNotice('')} status="success" title={notice} />}

              {!workspaceID && !error && <EmptyState description={t('settings.empty')} title="—" />}

              {workspaceID && section === 'model' && (
                <ModelSettings canAdmin={!!canAdmin} onError={setError} onNotice={setNotice} workspaceID={workspaceID} />
              )}
              {workspaceID && section === 'members' && (
                <MemberSettings canAdmin={!!canAdmin} onError={setError} onNotice={setNotice} workspaceID={workspaceID} />
              )}
              {workspaceID && section === 'workspace' && (
                <WorkspaceSettings
                  onError={setError}
                  onNotice={setNotice}
                  onUpdated={(updated) => setWorkspaces((current) => current.map((item) => item.id === updated.id ? {...item, ...updated} : item))}
                  workspace={workspace}
                />
              )}
            </VStack>
          </LayoutContent>
        }
      />
    </AppShell>
  );
}

function ModelSettings({canAdmin, onError, onNotice, workspaceID}: {canAdmin: boolean; onError: (value: string) => void; onNotice: (value: string) => void; workspaceID: string}) {
  const t = useTranslation();
  const [settings, setSettings] = useState<LLMSettings | null>(null);
  const [baseURL, setBaseURL] = useState('');
  const [model, setModel] = useState('');
  const [apiKey, setApiKey] = useState('');
  const [probe, setProbe] = useState<{models: string[]; state: 'loading' | 'ready' | 'failed'; message: string}>({models: [], state: 'loading', message: ''});
  const [saving, setSaving] = useState(false);

  // A model list can only be fetched once there is somewhere to fetch it from
  // and something to authenticate with. Derived during render so the effect
  // below never has to reset state synchronously.
  const canProbe = baseURL.trim() !== '' && (apiKey !== '' || !!settings?.has_api_key);
  const models = canProbe ? probe.models : [];
  const modelState = canProbe ? probe.state : 'idle';
  const modelError = canProbe && probe.state === 'failed' ? probe.message : '';

  const load = useCallback(() => {
    api.llmSettings(workspaceID).then((result) => {
      setSettings(result);
      setBaseURL(result.base_url);
      setModel(result.model);
    }).catch((caught) => onError(caught instanceof Error ? caught.message : t('model.readFailed')));
  }, [onError, t, workspaceID]);

  useEffect(load, [load]);

  // The model list comes from the gateway itself, so the operator picks a real
  // model instead of typing an identifier.
  useEffect(() => {
    if (!canProbe) return;
    let cancelled = false;
    const timer = setTimeout(() => {
      if (cancelled) return;
      setProbe((current) => ({...current, state: 'loading'}));
      api.gatewayModels(workspaceID, {base_url: baseURL.trim(), api_key: apiKey || undefined})
        .then((result) => {
          if (cancelled) return;
          setProbe({
            models: result.models,
            state: result.ok ? 'ready' : 'failed',
            message: result.ok ? '' : (result.message ?? t('model.listFailed')),
          });
        })
        .catch((caught) => {
          if (cancelled) return;
          setProbe({models: [], state: 'failed', message: caught instanceof Error ? caught.message : t('model.listFailed')});
        });
    }, 600);
    return () => { cancelled = true; clearTimeout(timer); };
  }, [apiKey, baseURL, canProbe, t, workspaceID]);

  async function save() {
    setSaving(true);
    onError('');
    try {
      // An untouched key field means "keep what is stored"; the server treats
      // null that way and an empty string as "clear it".
      const result = await api.saveLLMSettings(workspaceID, {base_url: baseURL, model, api_key: apiKey === '' ? null : apiKey});
      setSettings(result);
      setApiKey('');
      onNotice(t('model.saved'));
    } catch (caught) {
      onError(caught instanceof Error ? caught.message : t('model.saveFailed'));
    } finally {
      setSaving(false);
    }
  }

  if (!canAdmin) {
    return <Banner status="info" title={t('settings.needAdmin')} />;
  }

  return (
    <VStack gap={4}>
      <Heading level={1} type="display-3">{t('settings.model')}</Heading>

      <Card padding={0} width="100%">
        <Section dividers={['bottom']} padding={4}>
          <VStack gap={3}>
            <TextInput
              label={t('model.baseUrl')}
              onChange={setBaseURL}
              placeholder="https://api.openai.com/v1"
              value={baseURL}
              width="100%"
            />
            <TextInput
              description={settings?.has_api_key ? t('model.storedKey', {hint: settings.api_key_hint ?? ''}) : undefined}
              label={t('model.apiKey')}
              onChange={setApiKey}
              placeholder="sk-..."
              type="password"
              value={apiKey}
              width="100%"
            />
          </VStack>
        </Section>

        <Section dividers={['bottom']} padding={4}>
          <VStack gap={2}>
            <Selector
              hasSearch
              isDisabled={modelState !== 'ready' || models.length === 0}
              isLoading={modelState === 'loading'}
              label={t('model.model')}
              onChange={setModel}
              options={models}
              value={model}
              width="100%"
            />
            {modelError && <Text color="secondary" display="block" type="supporting">{modelError}</Text>}
          </VStack>
        </Section>

        <Section padding={4}>
          <HStack hAlign="end">
            <Button isDisabled={saving} isLoading={saving} label={t('model.save')} onClick={() => void save()} variant="primary" />
          </HStack>
        </Section>
      </Card>
    </VStack>
  );
}

function MemberSettings({canAdmin, onError, onNotice, workspaceID}: {canAdmin: boolean; onError: (value: string) => void; onNotice: (value: string) => void; workspaceID: string}) {
  const t = useTranslation();
  const [members, setMembers] = useState<Member[]>([]);
  const [invitations, setInvitations] = useState<Invitation[]>([]);
  const [email, setEmail] = useState('');
  const [role, setRole] = useState('member');
  const [inviteURL, setInviteURL] = useState('');
  const [sending, setSending] = useState(false);

  const load = useCallback(() => {
    api.members(workspaceID).then((result) => setMembers(result.members)).catch(() => undefined);
    if (canAdmin) {
      api.invitations(workspaceID).then((result) => setInvitations(result.invitations)).catch(() => undefined);
    }
  }, [canAdmin, workspaceID]);

  useEffect(load, [load]);

  async function invite() {
    setSending(true);
    onError('');
    setInviteURL('');
    try {
      const result = await api.createInvitation(workspaceID, email, role);
      setInviteURL(result.invitation.invite_url ?? '');
      setEmail('');
      onNotice(t('members.created'));
      load();
    } catch (caught) {
      onError(caught instanceof Error ? caught.message : t('members.createFailed'));
    } finally {
      setSending(false);
    }
  }

  async function revoke(invitationID: string) {
    try {
      await api.revokeInvitation(workspaceID, invitationID);
      load();
    } catch (caught) {
      onError(caught instanceof Error ? caught.message : t('members.revokeFailed'));
    }
  }

  return (
    <VStack gap={4}>
      <Heading level={1} type="display-3">{t('settings.members')}</Heading>

      {canAdmin && (
        <Card padding={4} width="100%">
          <VStack gap={3}>
            <HStack gap={2} vAlign="end">
              <TextInput label={t('members.inviteEmail')} onChange={setEmail} placeholder="name@example.com" type="email" value={email} width="100%" />
              <Selector
                label={t('members.role')}
                onChange={setRole}
                options={[{value: 'member', label: t('members.roleMember')}, {value: 'admin', label: t('members.roleAdmin')}]}
                value={role}
              />
              <Button isDisabled={!email.trim() || sending} isLoading={sending} label={t('members.create')} onClick={() => void invite()} variant="primary" />
            </HStack>
            {inviteURL && (
              <Banner
                description={inviteURL}
                endContent={
                  <Button
                    icon={<Copy size={14} />}
                    label={t('members.copy')}
                    onClick={() => void navigator.clipboard.writeText(inviteURL)}
                    size="sm"
                    variant="secondary"
                  />
                }
                status="success"
                title={t('members.inviteLink')}
              />
            )}
          </VStack>
        </Card>
      )}

      <Card padding={0} width="100%">
        <Section dividers={['bottom']} padding={4}>
          <Text type="label" weight="semibold">{t('members.list', {count: members.length})}</Text>
        </Section>
        <List>
          {members.map((member) => (
            <Item
              as="li"
              description={member.email}
              endContent={<Badge label={member.role.toUpperCase()} variant="neutral" />}
              key={member.user_id}
              label={member.name}
              startContent={<Avatar name={member.name} size="sm" />}
            />
          ))}
        </List>
      </Card>

      {canAdmin && invitations.length > 0 && (
        <Card padding={0} width="100%">
          <Section dividers={['bottom']} padding={4}>
            <Text type="label" weight="semibold">{t('members.pending', {count: invitations.length})}</Text>
          </Section>
          <List>
            {invitations.map((invitation) => (
              <Item
                as="li"
                description={t('members.expires', {date: new Date(invitation.expires_at).toLocaleDateString()})}
                endContent={
                  <IconButton
                    icon={<Trash2 size={14} />}
                    label={t('members.revoke', {email: invitation.email})}
                    onClick={() => void revoke(invitation.id)}
                    size="sm"
                    variant="ghost"
                  />
                }
                key={invitation.id}
                label={invitation.email}
              />
            ))}
          </List>
        </Card>
      )}
    </VStack>
  );
}

// Downscales an upload to a small square before it reaches the API, so the
// stored icon stays well under the server's 256 KB cap whatever the source is.
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
  const buffer = new Uint8Array(await blob.arrayBuffer());
  let binary = '';
  buffer.forEach((byte) => { binary += String.fromCharCode(byte); });
  return {mime: 'image/png', data: btoa(binary)};
}

function WorkspaceSettings({onError, onNotice, onUpdated, workspace}: {
  onError: (value: string) => void;
  onNotice: (value: string) => void;
  onUpdated: (workspace: Workspace) => void;
  workspace?: Workspace;
}) {
  const t = useTranslation();
  const [identityName, setIdentityName] = useState(workspace?.name ?? '');
  const [identityDescription, setIdentityDescription] = useState(workspace?.description ?? '');
  const [identityIcon, setIdentityIcon] = useState(workspace?.icon ?? '');
  const [savingIdentity, setSavingIdentity] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);
  const canEdit = workspace?.role === 'owner' || workspace?.role === 'admin';

  async function saveIdentity() {
    if (!workspace) return;
    setSavingIdentity(true);
    onError('');
    try {
      const result = await api.updateWorkspace(workspace.id, {name: identityName, description: identityDescription, icon: identityIcon});
      onUpdated(result.workspace);
      onNotice(t('workspace.identitySaved'));
    } catch (caught) {
      onError(caught instanceof Error ? caught.message : t('workspace.identityFailed'));
    } finally {
      setSavingIdentity(false);
    }
  }

  async function uploadIcon(file: File) {
    if (!workspace) return;
    onError('');
    try {
      const {mime, data} = await resizeToSquare(file);
      await api.uploadWorkspaceIcon(workspace.id, mime, data);
      onUpdated({...workspace, has_icon_image: true});
      onNotice(t('workspace.identitySaved'));
    } catch (caught) {
      onError(caught instanceof Error ? caught.message : t('workspace.identityFailed'));
    }
  }

  async function removeIcon() {
    if (!workspace) return;
    try {
      await api.deleteWorkspaceIcon(workspace.id);
      onUpdated({...workspace, has_icon_image: false});
    } catch (caught) {
      onError(caught instanceof Error ? caught.message : t('workspace.identityFailed'));
    }
  }

  return (
    <VStack gap={4}>
      <Heading level={1} type="display-3">{t('settings.workspace')}</Heading>

      {workspace && canEdit && (
        <Card padding={4} width="100%">
          <VStack gap={3}>
            <Text type="label" weight="semibold">{t('workspace.identity')}</Text>
            <HStack gap={3} vAlign="end">
              <Avatar
                name={identityIcon || workspace.name}
                size="lg"
                src={workspace.has_icon_image ? api.workspaceIconURL(workspace.id) : undefined}
              />
              <TextInput label={t('workspace.name')} onChange={setIdentityName} value={identityName} width="100%" />
              <TextInput label={t('workspace.icon')} onChange={setIdentityIcon} value={identityIcon} width={96} />
            </HStack>
            <TextInput label={t('workspace.description')} onChange={setIdentityDescription} value={identityDescription} width="100%" />
            <HStack gap={2} hAlign="end">
              <input
                accept="image/png,image/jpeg,image/webp,image/gif"
                hidden
                onChange={(event) => { const file = event.target.files?.[0]; if (file) void uploadIcon(file); event.target.value = ''; }}
                ref={fileRef}
                type="file"
              />
              {workspace.has_icon_image && (
                <Button label={t('workspace.removeImage')} onClick={() => void removeIcon()} variant="ghost" />
              )}
              <Button label={t('workspace.uploadImage')} onClick={() => fileRef.current?.click()} variant="secondary" />
              <Button isDisabled={!identityName.trim() || savingIdentity} isLoading={savingIdentity} label={t('workspace.saveIdentity')} onClick={() => void saveIdentity()} variant="primary" />
            </HStack>
          </VStack>
        </Card>
      )}

    </VStack>
  );
}
