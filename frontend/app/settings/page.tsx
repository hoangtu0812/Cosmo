'use client';

import {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import {useRouter} from 'next/navigation';
import {Copy, Trash2} from 'lucide-react';
import {Avatar} from '@astryxdesign/core/Avatar';
import {Token} from '@astryxdesign/core/Token';
import {AlertDialog} from '@astryxdesign/core/AlertDialog';
import {Banner} from '@astryxdesign/core/Banner';
import {Button} from '@astryxdesign/core/Button';
import {Card} from '@astryxdesign/core/Card';
import {EmptyState} from '@astryxdesign/core/EmptyState';
import {HStack, Layout, LayoutContent, VStack} from '@astryxdesign/core/Layout';
import {IconButton} from '@astryxdesign/core/IconButton';
import {Item} from '@astryxdesign/core/Item';
import {List} from '@astryxdesign/core/List';
import {Section} from '@astryxdesign/core/Section';
import {Switch} from '@astryxdesign/core/Switch';
import {Selector} from '@astryxdesign/core/Selector';
import {Text} from '@astryxdesign/core/Text';
import {TextArea} from '@astryxdesign/core/TextArea';
import {TextInput} from '@astryxdesign/core/TextInput';
import {api, APIError, Invitation, LLMSettings, Member, Tool, User, Workspace} from '../lib/api';
import {PageHeader} from '../components/PageHeader';
import {resizeToSquare} from '../lib/image';
import {useTranslation} from '../lib/i18n';



export default function SettingsPage() {
  const t = useTranslation();
  const router = useRouter();
  const [user, setUser] = useState<User | null>(null);
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [workspaceID, setWorkspaceID] = useState('');
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [isDeleteOpen, setIsDeleteOpen] = useState(false);
  const [isDeleting, setIsDeleting] = useState(false);

  const workspace = useMemo(() => workspaces.find((item) => item.id === workspaceID), [workspaces, workspaceID]);
  const canAdmin = user?.role === 'admin' || workspace?.role === 'owner' || workspace?.role === 'admin';
  // The workspace made for one person at sign-in. It cannot be given away, or
  // given a face other than the account's own, or thrown away - there would be
  // nowhere to land at the next sign-in.
  const isPersonal = workspace?.type === 'personal';

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

  async function removeWorkspace() {
    if (!workspaceID) return;
    setIsDeleting(true);
    try {
      await api.deleteWorkspace(workspaceID);
      // The workspace this page was about is gone, so the page cannot stay on
      // it. Chat resolves whichever workspace is left.
      router.replace('/chat');
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : t('workspace.deleteFailed'));
      setIsDeleting(false);
      setIsDeleteOpen(false);
    }
  }

  return (
    <>
      {/* Settings keeps the rail, as the reference does: it is a place in the
          workspace, not a way out of it. Its parts stack on one page rather
          than hiding behind a sidebar of their own. */}
      <Layout
        contentWidth={720}
        height="fill"
        header={<PageHeader description={t('settings.subtitle')} title={t('settings.title')} />}
        content={
          <LayoutContent padding={6}>
            <VStack gap={6}>
              {error && <Banner isDismissable onDismiss={() => setError('')} status="error" title={error} />}
              {notice && <Banner isDismissable onDismiss={() => setNotice('')} status="success" title={notice} />}

              {!workspaceID && !error && <EmptyState description={t('settings.empty')} title="—" />}

              {workspaceID ? (
                <>
                  <WorkspaceSettings
                    canChooseIcon={!isPersonal}
                    onError={setError}
                    onNotice={setNotice}
                    onUpdated={(updated) => setWorkspaces((current) => current.map((item) => item.id === updated.id ? {...item, ...updated} : item))}
                    workspace={workspace}
                  />
                  {/* Only where there is something to delete: the personal
                      workspace is where a person lands at sign-in, so it stays
                      whether they like it or not, and the section that would
                      say otherwise is not shown at all. */}
                  {!isPersonal && workspace?.role === 'owner' ? (
                    <Card width="100%">
                      <VStack gap={3}>
                        <Text type="label">{t('workspace.deleteTitle')}</Text>
                        <Text color="secondary" type="supporting">{t('workspace.deleteBody')}</Text>
                        <HStack hAlign="start">
                          <Button
                            icon={<Trash2 size={14} />}
                            label={t('workspace.deleteAction')}
                            onClick={() => setIsDeleteOpen(true)}
                            variant="secondary"
                          />
                        </HStack>
                      </VStack>
                    </Card>
                  ) : null}
                  <ModelSettings canAdmin={!!canAdmin} onError={setError} onNotice={setNotice} workspaceID={workspaceID} />
                  <InstalledToolSettings canAdmin={!!canAdmin} onError={setError} workspaceID={workspaceID} />
                  {isPersonal ? null : (
                    <MemberSettings canAdmin={!!canAdmin} onError={setError} onNotice={setNotice} workspaceID={workspaceID} />
                  )}
                </>
              ) : null}
            </VStack>
          </LayoutContent>
        }
      />

      <AlertDialog
        actionLabel={t('workspace.deleteAction')}
        cancelLabel={t('common.cancel')}
        description={t('workspace.deleteConfirm', {name: workspace?.name ?? ''})}
        actionVariant="destructive"
        isActionLoading={isDeleting}
        isOpen={isDeleteOpen}
        onAction={() => void removeWorkspace()}
        onOpenChange={(open) => { if (!open && !isDeleting) setIsDeleteOpen(false); }}
        title={t('workspace.deleteTitle')}
      />
    </>
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
  const canProbe = baseURL.trim() !== '';
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

  // The model list comes from the gateway itself. This setting is optional: it
  // merely supplies the default used when a member has not picked one in chat.
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
      <Text size="lg" type="large">{t('settings.model')}</Text>

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
              isDisabled={modelState !== 'ready' && models.length === 0}
              isLoading={modelState === 'loading'}
              label={t('model.defaultModel')}
              onChange={setModel}
              options={[{value: '', label: t('model.noDefault')}, ...models.map((item) => ({value: item, label: item}))]}
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
      <Text size="lg" type="large">{t('settings.members')}</Text>

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
          <Text type="label">{t('members.list', {count: members.length})}</Text>
        </Section>
        <List>
          {members.map((member) => (
            <Item
              as="li"
              description={member.email}
              endContent={<Token label={member.role.toUpperCase()} size="sm" />}
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
            <Text type="label">{t('members.pending', {count: invitations.length})}</Text>
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

function WorkspaceSettings({canChooseIcon, onError, onNotice, onUpdated, workspace}: {
  canChooseIcon: boolean;
  onError: (value: string) => void;
  onNotice: (value: string) => void;
  onUpdated: (workspace: Workspace) => void;
  workspace?: Workspace;
}) {
  const t = useTranslation();
  const [identityName, setIdentityName] = useState(workspace?.name ?? '');
  const [identityDescription, setIdentityDescription] = useState(workspace?.description ?? '');
  const [identityIcon, setIdentityIcon] = useState(workspace?.icon ?? '');
  const [identityContext, setIdentityContext] = useState(workspace?.context ?? '');
  const [savingIdentity, setSavingIdentity] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);
  const canEdit = workspace?.role === 'owner' || workspace?.role === 'admin';

  async function saveIdentity() {
    if (!workspace) return;
    setSavingIdentity(true);
    onError('');
    try {
      const result = await api.updateWorkspace(workspace.id, {
        name: identityName,
        description: identityDescription,
        context: identityContext,
        // A personal workspace wears the account's picture, and the server
        // refuses an icon for it, so the field is not sent where it is not
        // shown either.
        ...(canChooseIcon ? {icon: identityIcon} : {}),
      });
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
      <Text size="lg" type="large">{t('settings.workspace')}</Text>

      {workspace && canEdit && (
        <Card padding={4} width="100%">
          <VStack gap={3}>
            <Text type="label">{t('workspace.identity')}</Text>
            <HStack gap={3} vAlign="end">
              <Avatar
                name={identityIcon || workspace.name}
                size="lg"
                src={workspace.has_icon_image ? api.workspaceIconURL(workspace.id) : undefined}
              />
              <TextInput label={t('workspace.name')} onChange={setIdentityName} value={identityName} width="100%" />
              {canChooseIcon ? (
                <TextInput label={t('workspace.icon')} onChange={setIdentityIcon} value={identityIcon} width={96} />
              ) : null}
            </HStack>
            <TextInput label={t('workspace.description')} onChange={setIdentityDescription} value={identityDescription} width="100%" />
            {/* Read by the model on every turn in this workspace, which is why
                it says so rather than leaving people to guess what it is for. */}
            <TextArea
              description={t('workspace.contextHint')}
              label={t('workspace.context')}
              maxLength={2000}
              onChange={setIdentityContext}
              placeholder={t('workspace.contextPlaceholder')}
              rows={4}
              value={identityContext}
              width="100%"
            />
            <HStack gap={2} hAlign="end">
              <input
                accept="image/png,image/jpeg,image/webp,image/gif"
                hidden
                onChange={(event) => { const file = event.target.files?.[0]; if (file) void uploadIcon(file); event.target.value = ''; }}
                ref={fileRef}
                type="file"
              />
              {canChooseIcon && workspace.has_icon_image && (
                <Button label={t('workspace.removeImage')} onClick={() => void removeIcon()} variant="ghost" />
              )}
              {canChooseIcon ? (
                <Button label={t('workspace.uploadImage')} onClick={() => fileRef.current?.click()} variant="secondary" />
              ) : (
                <Text color="secondary" type="supporting">{t('workspace.personalIcon')}</Text>
              )}
              <Button isDisabled={!identityName.trim() || savingIdentity} isLoading={savingIdentity} label={t('workspace.saveIdentity')} onClick={() => void saveIdentity()} variant="primary" />
            </HStack>
          </VStack>
        </Card>
      )}

    </VStack>
  );
}

type Install = {tool: Tool; auto_call: boolean; is_blocked_by_key: boolean};

/**
 * The tools this workspace has installed, and which of them a plain question
 * may reach.
 *
 * The same switch as on the tool card, gathered where the workspace is
 * administered - because "what can this workspace call on its own" is a
 * question about the workspace, and reading it card by card is not an answer.
 */
function InstalledToolSettings({canAdmin, onError, workspaceID}: {canAdmin: boolean; onError: (value: string) => void; workspaceID: string}) {
  const t = useTranslation();
  const [installs, setInstalls] = useState<Install[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [busy, setBusy] = useState('');

  const load = useCallback(() => {
    api.workspaceTools(workspaceID)
      .then((result) => setInstalls(result.installs))
      .catch((caught) => onError(caught instanceof Error ? caught.message : t('tool.loadFailed')))
      .finally(() => setIsLoading(false));
  }, [onError, t, workspaceID]);

  useEffect(load, [load]);

  async function setAutoCall(install: Install, autoCall: boolean) {
    setBusy(install.tool.id);
    try {
      await api.setToolAutoCall(workspaceID, install.tool.id, autoCall);
      setInstalls((current) => current.map((item) => item.tool.id === install.tool.id
        ? {...item, auto_call: autoCall, is_blocked_by_key: autoCall && item.tool.has_secret}
        : item));
    } catch (caught) {
      onError(caught instanceof Error ? caught.message : t('tool.saveFailed'));
    } finally {
      setBusy('');
    }
  }

  const blocked = installs.filter((item) => item.is_blocked_by_key);

  return (
    <VStack gap={4}>
      <Text size="lg" type="large">{t('settings.installedTools')}</Text>

      {/* Raised here rather than at the switch: the switch reads as on, and
          the tool is not being called, and nothing on the card can say so. */}
      {blocked.length > 0 ? (
        <Banner
          description={blocked.map((item) => item.tool.name).join(', ')}
          status="warning"
          title={t('tool.blockedByKey')}
        />
      ) : null}

      <Card padding={0} width="100%">
        {isLoading ? (
          <Section padding={4}>
            <Text color="secondary" type="supporting">{t('chat.loading')}</Text>
          </Section>
        ) : installs.length === 0 ? (
          <Section padding={4}>
            <Text color="secondary" type="supporting">{t('tool.noneInstalled')}</Text>
          </Section>
        ) : (
          installs.map((install, index) => (
            <Section dividers={index < installs.length - 1 ? ['bottom'] : []} key={install.tool.id} padding={4}>
              <HStack gap={3} hAlign="between" vAlign="center" width="100%">
                <VStack gap={1}>
                  <Text type="label">{install.tool.name}</Text>
                  <Text color="secondary" type="supporting">
                    {install.tool.workspace_id === workspaceID
                      ? install.tool.description || install.tool.base_url
                      : t('tool.from', {workspace: install.tool.workspace_name})}
                  </Text>
                </VStack>
                <Switch
                  isDisabled={!canAdmin || busy === install.tool.id || install.tool.has_secret}
                  label={t('tool.autoCall')}
                  onChange={(checked: boolean) => void setAutoCall(install, checked)}
                  size="sm"
                  value={install.auto_call}
                />
              </HStack>
            </Section>
          ))
        )}
      </Card>
    </VStack>
  );
}
