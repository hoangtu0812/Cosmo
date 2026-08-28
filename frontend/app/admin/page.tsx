'use client';

import {useEffect, useState} from 'react';
import {useRouter} from 'next/navigation';
import {ArrowLeft, ClipboardList, ServerCog, ShieldCheck, Users} from 'lucide-react';
import {AppShell} from '@astryxdesign/core/AppShell';
import {AlertDialog} from '@astryxdesign/core/AlertDialog';
import {Avatar} from '@astryxdesign/core/Avatar';
import {Badge} from '@astryxdesign/core/Badge';
import {Banner} from '@astryxdesign/core/Banner';
import {Button} from '@astryxdesign/core/Button';
import {Card} from '@astryxdesign/core/Card';
import {EmptyState} from '@astryxdesign/core/EmptyState';
import {Heading} from '@astryxdesign/core/Heading';
import {Icon} from '@astryxdesign/core/Icon';
import {Item} from '@astryxdesign/core/Item';
import {HStack, Layout, LayoutContent, LayoutHeader, VStack} from '@astryxdesign/core/Layout';
import {List} from '@astryxdesign/core/List';
import {Selector} from '@astryxdesign/core/Selector';
import {SideNav, SideNavHeading, SideNavItem, SideNavSection} from '@astryxdesign/core/SideNav';
import {Text} from '@astryxdesign/core/Text';
import {TextInput} from '@astryxdesign/core/TextInput';
import {Timestamp} from '@astryxdesign/core/Timestamp';
import {Toolbar} from '@astryxdesign/core/Toolbar';
import {AdminUser, api, APIError, AuditEvent, SystemStatus, User} from '../lib/api';
import {useTranslation} from '../lib/i18n';
import {UserProfileCard} from '../components/UserProfileCard';

type AdminSection = 'users' | 'audit' | 'system';

export default function AdminPage() {
  const t = useTranslation();
  const router = useRouter();
  const [user, setUser] = useState<User | null>(null);
  const [section, setSection] = useState<AdminSection>('users');
  const [users, setUsers] = useState<AdminUser[]>([]);
  const [events, setEvents] = useState<AuditEvent[]>([]);
  const [system, setSystem] = useState<SystemStatus | null>(null);
  const [error, setError] = useState('');
  const [pendingUserID, setPendingUserID] = useState('');
  const [isSavingSystem, setIsSavingSystem] = useState(false);
  const [isReindexing, setIsReindexing] = useState(false);
  const [notice, setNotice] = useState('');

  useEffect(() => {
    api.me().then((result) => {
      if (result.user.role !== 'admin') {
        router.replace('/chat');
        return;
      }
      setUser(result.user);
      return Promise.all([api.adminUsers(), api.auditEvents(), api.systemStatus()]).then(([userResult, auditResult, systemResult]) => {
        setUsers(userResult.users);
        setEvents(auditResult.events);
        setSystem(systemResult);
      });
    }).catch((caught) => {
      if (caught instanceof APIError && caught.status === 401) router.replace('/');
      else if (caught instanceof APIError && caught.status === 403) router.replace('/chat');
      else setError(caught instanceof Error ? caught.message : t('admin.loadFailed'));
    });
  }, [router, t]);

  async function updateRole(target: AdminUser) {
    const role = target.role === 'admin' ? 'user' : 'admin';
    setPendingUserID(target.id);
    setError('');
    try {
      await api.updateAdminUser(target.id, role);
      setUsers((current) => current.map((item) => item.id === target.id ? {...item, role} : item));
      const audit = await api.auditEvents();
      setEvents(audit.events);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : t('admin.loadFailed'));
    } finally {
      setPendingUserID('');
    }
  }

  async function updateSystemModels(settings: {embeddingModel: string; rerankerModel: string; gatewayBaseURL: string; gatewayAPIKey: string}) {
    setIsSavingSystem(true);
    setError('');
    try {
      setSystem(await api.updateSystemSettings({
        embedding_model: settings.embeddingModel,
        reranker_model: settings.rerankerModel,
        gateway_base_url: settings.gatewayBaseURL,
        ...(settings.gatewayAPIKey ? {gateway_api_key: settings.gatewayAPIKey} : {}),
      }));
      const audit = await api.auditEvents();
      setEvents(audit.events);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : t('admin.loadFailed'));
    } finally {
      setIsSavingSystem(false);
    }
  }

  async function reindexKnowledge() {
    setIsReindexing(true);
    setError('');
    setNotice('');
    try {
      const result = await api.reindexKnowledge();
      setNotice(t('admin.reindexQueued', {count: result.queued}));
      const audit = await api.auditEvents();
      setEvents(audit.events);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : t('admin.loadFailed'));
    } finally {
      setIsReindexing(false);
    }
  }

  function selectSection(next: AdminSection) {
    setSection(next);
    setError('');
  }

  return (
    <AppShell
      contentPadding={0}
      sideNav={
        <SideNav
          footer={user ? <UserProfileCard user={user} /> : undefined}
          header={<SideNavHeading heading={t('admin.title')} icon={<Icon icon={ShieldCheck} size="sm" />} onClick={() => router.push('/chat')} subheading={user?.email} />}
        >
          <SideNavSection title={t('admin.title')}>
            <SideNavItem icon={<Icon icon={Users} size="sm" />} isSelected={section === 'users'} label={t('admin.users')} onClick={() => selectSection('users')} />
            <SideNavItem icon={<Icon icon={ClipboardList} size="sm" />} isSelected={section === 'audit'} label={t('admin.audit')} onClick={() => selectSection('audit')} />
            <SideNavItem icon={<Icon icon={ServerCog} size="sm" />} isSelected={section === 'system'} label={t('admin.system')} onClick={() => selectSection('system')} />
          </SideNavSection>
        </SideNav>
      }
    >
      <Layout
        contentWidth={960}
        height="fill"
        header={
          <LayoutHeader hasDivider>
            <Toolbar label={t('admin.title')} startContent={<Button icon={<ArrowLeft size={15} />} label={t('admin.back')} onClick={() => router.push('/chat')} size="sm" variant="ghost" />} />
          </LayoutHeader>
        }
        content={
          <LayoutContent padding={6}>
            <VStack gap={5}>
              {error && <Banner isDismissable onDismiss={() => setError('')} status="error" title={error} />}
              {notice && <Banner isDismissable onDismiss={() => setNotice('')} status="success" title={notice} />}
              {section === 'users' && <UsersPanel pendingUserID={pendingUserID} t={t} users={users} onUpdateRole={updateRole} />}
              {section === 'audit' && <AuditPanel events={events} t={t} />}
              {section === 'system' && <SystemPanel isReindexing={isReindexing} isSaving={isSavingSystem} system={system} t={t} onReindex={reindexKnowledge} onSave={updateSystemModels} />}
            </VStack>
          </LayoutContent>
        }
      />
    </AppShell>
  );
}

function UsersPanel({users, pendingUserID, onUpdateRole, t}: {users: AdminUser[]; pendingUserID: string; onUpdateRole: (user: AdminUser) => void; t: ReturnType<typeof useTranslation>}) {
  return (
    <VStack gap={4}>
      <VStack gap={1}>
        <Heading level={1} type="display-3">{t('admin.usersTitle')}</Heading>
        <Text color="secondary" type="body">{t('admin.usersBody')}</Text>
      </VStack>
      <Card padding={0} width="100%">
        <List>
          {users.map((item) => (
            <Item
              as="li"
              description={`${item.email} · ${item.provider === 'entra' ? t('admin.providerEntra') : t('admin.providerLocal')} · ${t('admin.workspaces', {count: item.workspace_count})}`}
              endContent={
                <HStack gap={2} vAlign="center">
                  <Badge label={item.role.toUpperCase()} variant={item.role === 'admin' ? 'info' : 'neutral'} />
                  <Button
                    isDisabled={pendingUserID === item.id}
                    isLoading={pendingUserID === item.id}
                    label={item.role === 'admin' ? t('admin.removeAdmin') : t('admin.makeAdmin')}
                    onClick={() => onUpdateRole(item)}
                    size="sm"
                    variant={item.role === 'admin' ? 'secondary' : 'primary'}
                  />
                </HStack>
              }
              key={item.id}
              label={item.name}
              startContent={<Avatar name={item.name} size="sm" />}
            />
          ))}
        </List>
      </Card>
    </VStack>
  );
}

function AuditPanel({events, t}: {events: AuditEvent[]; t: ReturnType<typeof useTranslation>}) {
  return (
    <VStack gap={4}>
      <Heading level={1} type="display-3">{t('admin.audit')}</Heading>
      {events.length === 0 ? <EmptyState description={t('admin.auditEmpty')} title="—" /> : (
        <Card padding={0} width="100%">
          <List>
            {events.map((event) => (
              <Item
                as="li"
                description={`${event.actor_name || event.actor_email || 'System'} · ${event.target_type || 'system'}${event.target_id ? ` · ${event.target_id}` : ''}`}
                endContent={<Timestamp format="date_time" value={event.created_at} />}
                key={event.id}
                label={event.action}
              />
            ))}
          </List>
        </Card>
      )}
    </VStack>
  );
}

function SystemPanel({system, isSaving, isReindexing, onSave, onReindex, t}: {system: SystemStatus | null; isSaving: boolean; isReindexing: boolean; onSave: (settings: {embeddingModel: string; rerankerModel: string; gatewayBaseURL: string; gatewayAPIKey: string}) => void; onReindex: () => void; t: ReturnType<typeof useTranslation>}) {
  const [embeddingModel, setEmbeddingModel] = useState('');
  const [rerankerModel, setRerankerModel] = useState('');
  const [gatewayBaseURL, setGatewayBaseURL] = useState('');
  const [gatewayAPIKey, setGatewayAPIKey] = useState('');
  const [gatewayModels, setGatewayModels] = useState<string[]>([]);
  const [isLoadingModels, setIsLoadingModels] = useState(false);
  const [isReindexDialogOpen, setIsReindexDialogOpen] = useState(false);
  useEffect(() => {
    setEmbeddingModel(system?.embedding_model ?? '');
    setRerankerModel(system?.reranker_model ?? '');
    setGatewayBaseURL(system?.system_gateway.base_url ?? '');
    setGatewayAPIKey('');
  }, [system?.embedding_model, system?.reranker_model, system?.system_gateway.base_url]);
  const rows = system ? [
    [t('admin.entra'), system.entra_enabled],
    [t('admin.gateway'), system.system_gateway.configured],
    [t('admin.knowledge'), system.knowledge_enabled],
    [t('admin.cookieSecure'), system.cookie_secure],
  ] : [];
  return (
    <VStack gap={4}>
      <VStack gap={1}>
        <Heading level={1} type="display-3">{t('admin.system')}</Heading>
        <Text color="secondary" type="body">{t('admin.systemBody')}</Text>
      </VStack>
      <Card padding={0} width="100%">
        <List>
          {rows.map(([label, enabled]) => (
            <Item as="li" endContent={<Badge label={enabled ? t('admin.enabled') : t('admin.disabled')} variant={enabled ? 'success' : 'neutral'} />} key={label} label={label} />
          ))}
          {system && <Item as="li" description={system.configuration_source} label={t('admin.sessionTtl')} endContent={<Text type="label">{system.session_ttl}</Text>} />}
          {system && <Item as="li" label={t('admin.adminEmails')} endContent={<Text type="label">{String(system.admin_email_count)}</Text>} />}
        </List>
      </Card>
      {system && <Card width="100%">
        <VStack gap={4}>
          <Text type="label" weight="semibold">{t('admin.gateway')}</Text>
          <TextInput label={t('admin.gatewayBaseURL')} onChange={setGatewayBaseURL} value={gatewayBaseURL} />
          <TextInput label={system.system_gateway.has_api_key ? t('admin.gatewayKeyStored', {hint: system.system_gateway.api_key_hint ?? ''}) : t('admin.gatewayApiKey')} onChange={setGatewayAPIKey} placeholder="sk-..." type="password" value={gatewayAPIKey} />
          <HStack hAlign="end">
            <Button
              isDisabled={!gatewayBaseURL.trim() || isLoadingModels}
              isLoading={isLoadingModels}
              label={t('admin.loadModels')}
              onClick={() => {
                setIsLoadingModels(true);
                api.systemGatewayModels({base_url: gatewayBaseURL.trim(), ...(gatewayAPIKey.trim() ? {api_key: gatewayAPIKey.trim()} : {})})
                  .then((result) => { if (result.ok) setGatewayModels(result.models); })
                  .catch(() => setGatewayModels([]))
                  .finally(() => setIsLoadingModels(false));
              }}
              variant="secondary"
            />
          </HStack>
        </VStack>
      </Card>}
      {system && <Card width="100%">
        <VStack gap={4}>
          {gatewayModels.length > 0 ? <Selector label={t('admin.embeddingModel')} onChange={setEmbeddingModel} options={gatewayModels.map((model) => ({label: model, value: model}))} value={embeddingModel} width="100%" /> : <TextInput label={t('admin.embeddingModel')} onChange={setEmbeddingModel} value={embeddingModel} />}
          {gatewayModels.length > 0 ? <Selector label={t('admin.rerankerModel')} onChange={setRerankerModel} options={gatewayModels.map((model) => ({label: model, value: model}))} value={rerankerModel} width="100%" /> : <TextInput label={t('admin.rerankerModel')} onChange={setRerankerModel} value={rerankerModel} />}
          <HStack hAlign="end">
            <Button isDisabled={!embeddingModel.trim() || !rerankerModel.trim()} isLoading={isSaving} label={t('admin.saveSystem')} onClick={() => onSave({embeddingModel: embeddingModel.trim(), rerankerModel: rerankerModel.trim(), gatewayBaseURL: gatewayBaseURL.trim(), gatewayAPIKey: gatewayAPIKey.trim()})} variant="primary" />
          </HStack>
        </VStack>
      </Card>}
      {system && <Card width="100%">
        <HStack hAlign="between" vAlign="center">
          <Text type="label" weight="semibold">{t('admin.reindex')}</Text>
          <Button isDisabled={isReindexing || !system.system_gateway.configured} isLoading={isReindexing} label={t('admin.reindexAction')} onClick={() => setIsReindexDialogOpen(true)} variant="secondary" />
        </HStack>
      </Card>}
      <AlertDialog
        actionLabel={t('admin.reindexConfirm')}
        cancelLabel={t('common.cancel')}
        description={t('admin.reindexBody')}
        isActionLoading={isReindexing}
        isOpen={isReindexDialogOpen}
        onAction={() => { setIsReindexDialogOpen(false); void onReindex(); }}
        onOpenChange={setIsReindexDialogOpen}
        title={t('admin.reindexTitle')}
      />
    </VStack>
  );
}
