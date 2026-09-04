'use client';

import {useEffect, useState} from 'react';
import {useRouter} from 'next/navigation';
import {ArrowLeft, ClipboardList, LayoutDashboard, ServerCog, ShieldCheck, Users} from 'lucide-react';
import {AppShell} from '@astryxdesign/core/AppShell';
import {AlertDialog} from '@astryxdesign/core/AlertDialog';
import {Avatar} from '@astryxdesign/core/Avatar';
import {Badge} from '@astryxdesign/core/Badge';
import {Token} from '@astryxdesign/core/Token';
import {Banner} from '@astryxdesign/core/Banner';
import {Button} from '@astryxdesign/core/Button';
import {Card} from '@astryxdesign/core/Card';
import {Heading} from '@astryxdesign/core/Heading';
import {Icon} from '@astryxdesign/core/Icon';
import {Item} from '@astryxdesign/core/Item';
import {HStack, Layout, LayoutContent, LayoutHeader, VStack} from '@astryxdesign/core/Layout';
import {List} from '@astryxdesign/core/List';
import {ProgressBar} from '@astryxdesign/core/ProgressBar';
import {SideNav, SideNavHeading, SideNavItem, SideNavSection} from '@astryxdesign/core/SideNav';
import {Text} from '@astryxdesign/core/Text';
import {Toolbar} from '@astryxdesign/core/Toolbar';
import {StatusLabel} from '../components/StatusLabel';
import {AdminUser, api, APIError, KnowledgeIndexStatus, SystemStatus, User} from '../lib/api';
import {useTranslation} from '../lib/i18n';
import {UserProfileCard} from '../components/UserProfileCard';
import {AuditPanel} from './AuditPanel';
import {DashboardPanel} from './DashboardPanel';

type AdminSection = 'dashboard' | 'users' | 'audit' | 'system';

export default function AdminPage() {
  const t = useTranslation();
  const router = useRouter();
  const [user, setUser] = useState<User | null>(null);
  // The dashboard first: what the platform is doing is the question an
  // administrator opens this console with, and the lists answer the ones that
  // follow from it.
  const [section, setSection] = useState<AdminSection>('dashboard');
  const [users, setUsers] = useState<AdminUser[]>([]);
  const [system, setSystem] = useState<SystemStatus | null>(null);
  const [error, setError] = useState('');
  const [pendingUserID, setPendingUserID] = useState('');
  const [isReindexing, setIsReindexing] = useState(false);
  const [indexStatus, setIndexStatus] = useState<KnowledgeIndexStatus | null>(null);
  const [notice, setNotice] = useState('');

  useEffect(() => {
    api.me().then((result) => {
      if (result.user.role !== 'admin') {
        router.replace('/chat');
        return;
      }
      setUser(result.user);
      // The dashboard and the audit log load their own data when they are
      // opened: both are large, both take a range or a filter, and neither is
      // wanted by an administrator who came here to change one role.
      return Promise.all([api.adminUsers(), api.systemStatus(), api.knowledgeIndexStatus()])
        .then(([userResult, systemResult, indexResult]) => {
          setUsers(userResult.users);
          setSystem(systemResult);
          setIndexStatus(indexResult);
        });
    }).catch((caught) => {
      if (caught instanceof APIError && caught.status === 401) router.replace('/');
      else if (caught instanceof APIError && caught.status === 403) router.replace('/chat');
      else setError(caught instanceof Error ? caught.message : t('admin.loadFailed'));
    });
  }, [router, t]);

  // A re-index outlives the request that started it, so progress is read from
  // the document rows until nothing is left in flight. The poll stops itself
  // rather than running for as long as the console is open.
  useEffect(() => {
    if (!indexStatus?.running) return undefined;
    const timer = setInterval(() => {
      void api.knowledgeIndexStatus().then(setIndexStatus).catch(() => undefined);
    }, 3000);
    return () => clearInterval(timer);
  }, [indexStatus?.running]);

  async function updateRole(target: AdminUser) {
    const role = target.role === 'admin' ? 'user' : 'admin';
    setPendingUserID(target.id);
    setError('');
    try {
      await api.updateAdminUser(target.id, role);
      setUsers((current) => current.map((item) => item.id === target.id ? {...item, role} : item));
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : t('admin.loadFailed'));
    } finally {
      setPendingUserID('');
    }
  }


  async function reindexKnowledge() {
    setIsReindexing(true);
    setError('');
    setNotice('');
    try {
      const result = await api.reindexKnowledge();
      setNotice(t('admin.reindexQueued', {count: result.queued}));
      setIndexStatus(await api.knowledgeIndexStatus());
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
            <SideNavItem icon={<Icon icon={LayoutDashboard} size="sm" />} isSelected={section === 'dashboard'} label={t('admin.dashboard')} onClick={() => selectSection('dashboard')} />
            <SideNavItem icon={<Icon icon={Users} size="sm" />} isSelected={section === 'users'} label={t('admin.users')} onClick={() => selectSection('users')} />
            <SideNavItem icon={<Icon icon={ClipboardList} size="sm" />} isSelected={section === 'audit'} label={t('admin.audit')} onClick={() => selectSection('audit')} />
            <SideNavItem icon={<Icon icon={ServerCog} size="sm" />} isSelected={section === 'system'} label={t('admin.system')} onClick={() => selectSection('system')} />
          </SideNavSection>
        </SideNav>
      }
    >
      <Layout
        // Wide enough for a dashboard row of tiles and a table of workspaces
        // side by side; the settings forms inside it keep their own width.
        contentWidth={1200}
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
              {section === 'dashboard' && <DashboardPanel onError={setError} />}
              {section === 'users' && <UsersPanel pendingUserID={pendingUserID} t={t} users={users} onUpdateRole={updateRole} />}
              {section === 'audit' && <AuditPanel onError={setError} />}
              {section === 'system' && <SystemPanel indexStatus={indexStatus} isReindexing={isReindexing} system={system} t={t} onReindex={reindexKnowledge} />}
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
                  <Token label={item.role.toUpperCase()} size="sm" />
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

function SystemPanel({system, indexStatus, isReindexing, onReindex, t}: {system: SystemStatus | null; indexStatus: KnowledgeIndexStatus | null; isReindexing: boolean; onReindex: () => void; t: ReturnType<typeof useTranslation>}) {
  const [isReindexDialogOpen, setIsReindexDialogOpen] = useState(false);
  const rows: [string, boolean][] = system ? [
    [t('admin.entra'), system.entra_enabled],
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
            <Item as="li" endContent={<StatusLabel label={enabled ? t('admin.enabled') : t('admin.disabled')} variant={enabled ? 'success' : 'neutral'} />} key={label} label={label} />
          ))}
          {system && <Item as="li" description={system.configuration_source} label={t('admin.sessionTtl')} endContent={<Text type="label">{system.session_ttl}</Text>} />}
          {system && <Item as="li" label={t('admin.adminEmails')} endContent={<Text type="label">{String(system.admin_email_count)}</Text>} />}
        </List>
      </Card>
      {system && <Card width="100%">
        <VStack gap={3}>
          <HStack hAlign="between" vAlign="center">
            <Text type="label">{t('admin.reindex')}</Text>
            <Button isDisabled={isReindexing || indexStatus?.running || !system.knowledge_enabled} isLoading={isReindexing} label={t('admin.reindexAction')} onClick={() => setIsReindexDialogOpen(true)} variant="secondary" />
          </HStack>
          {indexStatus && indexStatus.total > 0 ? (
            <VStack gap={2}>
              {indexStatus.running ? (
                <ProgressBar isLabelHidden label={t('admin.reindex')} value={Math.round(((indexStatus.total - indexStatus.pending) / indexStatus.total) * 100)} />
              ) : null}
              <HStack gap={3}>
                <Text color="secondary" type="supporting">{t('admin.indexProgress', {ready: indexStatus.ready, total: indexStatus.total})}</Text>
                {indexStatus.failed > 0 ? <Badge label={t('admin.indexFailed', {failed: indexStatus.failed})} variant="error" /> : null}
              </HStack>
            </VStack>
          ) : null}
        </VStack>
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
