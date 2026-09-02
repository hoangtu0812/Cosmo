'use client';

import {Suspense, useCallback, useEffect, useState} from 'react';
import {useRouter, useSearchParams} from 'next/navigation';
import {Bot, Boxes, PackageMinus, Plug, Search, Settings2, Share2, ShieldCheck, Sparkles, Store, Trash2, Wrench, Zap} from 'lucide-react';
import {AlertDialog} from '@astryxdesign/core/AlertDialog';
import {Banner} from '@astryxdesign/core/Banner';
import {Button} from '@astryxdesign/core/Button';
import {Dialog, DialogHeader} from '@astryxdesign/core/Dialog';
import {DropdownMenu} from '@astryxdesign/core/DropdownMenu';
import {Grid} from '@astryxdesign/core/Grid';
import {Icon} from '@astryxdesign/core/Icon';
import {HStack, Layout, LayoutContent, LayoutFooter, VStack} from '@astryxdesign/core/Layout';
import {Skeleton} from '@astryxdesign/core/Skeleton';
import {Text} from '@astryxdesign/core/Text';
import {TextArea} from '@astryxdesign/core/TextArea';
import {TextInput} from '@astryxdesign/core/TextInput';
import {PageHeader} from '../components/PageHeader';
import {api, APIError, Tool, Workspace, WorkspaceRef} from '../lib/api';
import {ToolMarket} from './ToolMarket';
import {ToolCard} from './ToolCard';
import {ToolShareDialog} from './ToolShareDialog';
import {CapabilityHero} from '../components/CapabilityHero';
import {CardMenuItems} from '../components/CardMenu';
import {useTranslation} from '../lib/i18n';

export default function ToolsPage() {
  return (
    <Suspense fallback={null}>
      <ToolsScreen />
    </Suspense>
  );
}

function ToolsScreen() {
  const t = useTranslation();
  const router = useRouter();
  const search = useSearchParams();
  const workspaceID = search.get('workspace') ?? '';

  const [tools, setTools] = useState<Tool[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState('');
  const [query, setQuery] = useState('');
  const [deleting, setDeleting] = useState<Tool | null>(null);
  const [sharing, setSharing] = useState<Tool | null>(null);
  const [directory, setDirectory] = useState<WorkspaceRef[]>([]);
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  // The install and the switch are separate acts on the server, so they are
  // separate here too; `busy` names the tool a request is in flight for, so
  // one card's spinner does not freeze the rest.
  const [busy, setBusy] = useState('');

  // Named once, then read by both the ⋯ and right-click, so the two cannot
  // come to say different things about the same card.
  const toolActions = (tool: Tool): CardMenuItems => [
    ...(tool.is_editable ? [
      {icon: <Settings2 size={15} />, label: t('kb.configure'), onClick: () => router.push(`/tools/${tool.id}?workspace=${encodeURIComponent(workspaceID)}`)},
      {icon: <Share2 size={15} />, label: t('tool.share'), onClick: () => setSharing(tool)},
    ] : []),
    ...(tool.is_installed && canInstall ? [
      {icon: <PackageMinus size={15} />, label: t('tool.uninstall'), onClick: () => void uninstall(tool)},
    ] : []),
    ...(tool.is_editable ? [
      {type: 'divider' as const},
      {icon: <Trash2 size={15} />, label: t('kb.delete'), onClick: () => setDeleting(tool), variant: 'destructive' as const},
    ] : []),
  ];

  const [isCreating, setIsCreating] = useState(false);
  const [isSaving, setIsSaving] = useState(false);
  const [newName, setNewName] = useState('');
  const [newDescription, setNewDescription] = useState('');
  const [newBaseURL, setNewBaseURL] = useState('');
  // 'plain' asks for nothing more, 'ai' has the model describe the actions,
  // 'mcp' asks the server to describe itself.
  const [route, setRoute] = useState<'plain' | 'ai' | 'mcp'>('plain');
  const [isCatalogOpen, setIsCatalogOpen] = useState(false);

  const load = useCallback(() => {
    if (!workspaceID) return;
    api.tools(workspaceID)
      .then((result) => setTools(result.tools))
      .catch((caught) => {
        if (caught instanceof APIError && caught.status === 401) router.replace('/');
        else setError(caught instanceof Error ? caught.message : t('tool.loadFailed'));
      })
      .finally(() => setIsLoading(false));
  }, [router, t, workspaceID]);

  useEffect(load, [load]);

  // Loaded with the tools rather than when the dialog opens, so the dialog
  // never appears with an empty list of workspaces to offer to. The reader's
  // own workspaces come along for the role, which decides whether installing
  // is even offered.
  useEffect(() => {
    Promise.all([api.workspaces(), api.workspaceDirectory()])
      .then(([mine, all]) => {
        setWorkspaces(mine.workspaces);
        setDirectory(all.workspaces);
      })
      .catch(() => setDirectory([]));
  }, []);

  // Installing a tool into a workspace, and letting it answer questions there,
  // are the workspace's decisions rather than any member's. The server refuses
  // them for everyone else; the card stops offering them.
  const workspace = workspaces.find((item) => item.id === workspaceID);
  const canInstall = workspace?.role === 'owner' || workspace?.role === 'admin';

  async function create() {
    setIsSaving(true);
    setError('');
    try {
      const result = await api.createTool(
        {
          name: newName,
          description: newDescription,
          icon: route === 'mcp' ? '🧩' : '🔌',
          tags: [],
          base_url: newBaseURL,
          kind: route === 'mcp' ? 'mcp' : 'http',
        },
        workspaceID,
      );
      // Describing the actions is best effort: a tool with none is still a
      // tool, and landing in the editor beats an error over a draft.
      if (route === 'ai') {
        await api.draftToolActions(result.tool.id, newDescription, workspaceID).catch(() => undefined);
      } else if (route === 'mcp') {
        await api.discoverMCPTools(result.tool.id, workspaceID).catch(() => undefined);
      }
      closeCreate(true);
      router.push(`/tools/${result.tool.id}?workspace=${encodeURIComponent(workspaceID)}`);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : t('tool.createFailed'));
    } finally {
      setIsSaving(false);
    }
  }

  function closeCreate(force = false) {
    if (isSaving && !force) return;
    setIsCreating(false);
    setNewName('');
    setNewDescription('');
    setNewBaseURL('');
  }

  async function remove() {
    if (!deleting) return;
    try {
      await api.deleteTool(deleting.id, workspaceID);
      setTools((current) => current.filter((item) => item.id !== deleting.id));
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : t('tool.deleteFailed'));
    } finally {
      setDeleting(null);
    }
  }

  function patchTool(toolID: string, change: Partial<Tool>) {
    setTools((current) => current.map((item) => (item.id === toolID ? {...item, ...change} : item)));
  }

  async function install(tool: Tool) {
    setBusy(tool.id);
    setError('');
    try {
      await api.installWorkspaceTool(workspaceID, tool.id);
      // Installed, and deliberately not callable yet: that is a second
      // decision, taken at the switch beside this button.
      patchTool(tool.id, {is_installed: true, auto_call: false});
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : t('tool.saveFailed'));
    } finally {
      setBusy('');
    }
  }

  async function uninstall(tool: Tool) {
    setBusy(tool.id);
    setError('');
    try {
      await api.uninstallWorkspaceTool(workspaceID, tool.id);
      patchTool(tool.id, {is_installed: false, auto_call: false});
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : t('tool.saveFailed'));
    } finally {
      setBusy('');
    }
  }

  // The server refuses this for a tool holding a key, and says why. The message
  // is shown rather than swallowed: a switch that flicks back without a reason
  // is the worst of the outcomes available here.
  async function setAutoCall(tool: Tool, autoCall: boolean) {
    setBusy(tool.id);
    setError('');
    try {
      await api.setToolAutoCall(workspaceID, tool.id, autoCall);
      patchTool(tool.id, {auto_call: autoCall});
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : t('tool.saveFailed'));
    } finally {
      setBusy('');
    }
  }

  const needle = query.trim().toLowerCase();
  const visible = tools.filter((item) => !needle
    || item.name.toLowerCase().includes(needle)
    || item.description.toLowerCase().includes(needle));

  // The reference offers four routes to a new tool. Only one of them is built:
  // the rest are named so the shape of the area is visible - see
  // docs/ui_backlog.md.
  function openCreate(which: 'plain' | 'ai' | 'mcp') {
    setRoute(which);
    setIsCreating(true);
  }

  const addMenu = [
    {icon: <Sparkles size={15} />, label: t('tool.createWithAI'), onClick: () => openCreate('ai')},
    {icon: <Plug size={15} />, label: t('tool.addPlugin'), onClick: () => openCreate('plain')},
    {icon: <Boxes size={15} />, label: t('tool.addMCP'), onClick: () => openCreate('mcp')},
    {icon: <Store size={15} />, label: t('tool.marketplace'), onClick: () => setIsCatalogOpen(true)},
  ];

  return (
    <>
      <Layout
        content={
          <LayoutContent padding={6}>
            <VStack gap={6} width="100%">
              {error ? <Banner isDismissable onDismiss={() => setError('')} status="error" title={error} /> : null}

              {isLoading ? (
                <Grid columns={{minWidth: 220, max: 6}} gap={4} width="100%">
                  {[0, 1, 2].map((index) => <Skeleton height={180} index={index} key={index} width="100%" />)}
                </Grid>
              ) : tools.length === 0 ? (
                // The reference gives this area a proper opening rather than an
                // empty box, because a workspace with no tools is the normal
                // starting state, not a failure.
                <CapabilityHero
                  action={<DropdownMenu alignment="center" button={{label: t('tool.add'), variant: 'primary'}} items={addMenu} />}
                  description={t('tool.emptyBody')}
                  flow={[
                    {icon: Bot, label: t('agent.title')},
                    {icon: Wrench, label: t('nav.tool')},
                    {icon: Zap, label: t('tool.flowAct')},
                  ]}
                  points={[
                    {icon: Boxes, title: t('tool.wayMCP'), description: t('tool.wayMCPBody')},
                    {icon: Plug, title: t('tool.wayPlugin'), description: t('tool.wayPluginBody')},
                    {icon: Settings2, title: t('tool.wayCustom'), description: t('tool.wayCustomBody')},
                  ]}
                  title={t('tool.empty')}
                />
              ) : (
                <>
                  <HStack gap={3} vAlign="center" wrap="wrap">
                    <TextInput
                      isLabelHidden
                      label={t('tool.search')}
                      onChange={setQuery}
                      placeholder={t('tool.search')}
                      size="lg"
                      startIcon={<Icon icon={Search} size="sm" />}
                      value={query}
                      width={280}
                    />
                  </HStack>

                  <Grid columns={{minWidth: 220, max: 6}} gap={4} width="100%">
                    {visible.map((tool) => (
                      <ToolCard
                        actions={toolActions(tool)}
                        canInstall={canInstall}
                        isBusy={busy === tool.id}
                        key={tool.id}
                        onAutoCall={(autoCall) => void setAutoCall(tool, autoCall)}
                        onInstall={() => void install(tool)}
                        onOpen={() => router.push(`/tools/${tool.id}?workspace=${encodeURIComponent(workspaceID)}`)}
                        origin={tool.workspace_id === workspaceID ? '' : tool.workspace_name}
                        tool={tool}
                      />
                    ))}
                  </Grid>
                </>
              )}
            </VStack>
          </LayoutContent>
        }
        header={
          <PageHeader
            actions={<DropdownMenu alignment="end" button={{label: t('tool.add'), size: 'sm', variant: 'primary'}} items={addMenu} />}
            count={tools.length}
            description={t('tool.subtitle')}
            hasIntroduction
            title={t('nav.tool')}
          />
        }
        height="fill"
      />

      <Dialog isOpen={isCreating} onOpenChange={(open) => { if (!open) closeCreate(); }} purpose="form" width={520}>
        <Layout
          content={
            <LayoutContent>
              <VStack gap={4}>
                <TextInput
                  label={t('tool.name')}
                  onChange={setNewName}
                  placeholder={t('tool.namePlaceholder')}
                  value={newName}
                  width="100%"
                />
                <TextArea
                  label={t('tool.purpose')}
                  maxLength={512}
                  onChange={setNewDescription}
                  placeholder={t('tool.purposePlaceholder')}
                  rows={3}
                  value={newDescription}
                  width="100%"
                />
                <TextInput
                  label={route === 'mcp' ? t('tool.mcpURL') : t('tool.baseURL')}
                  onChange={setNewBaseURL}
                  onEnter={() => void create()}
                  placeholder={route === 'mcp' ? 'https://example.com/mcp' : 'https://api.example.com'}
                  value={newBaseURL}
                  width="100%"
                />
                {route === 'ai' ? (
                  <Text color="secondary" type="supporting">{t('tool.aiHint')}</Text>
                ) : null}
                {route === 'mcp' ? (
                  <Text color="secondary" type="supporting">{t('tool.mcpHint')}</Text>
                ) : null}
                {/* Said once, where it is decided, rather than after the fact in
                    an error: a tool reaches the internet and nothing else. */}
                <HStack gap={2} vAlign="center">
                  <Icon icon={ShieldCheck} size="sm" />
                  <Text color="secondary" type="supporting">{t('tool.egressNote')}</Text>
                </HStack>
              </VStack>
            </LayoutContent>
          }
          footer={
            <LayoutFooter>
              <HStack gap={2} hAlign="end">
                <Button label={t('common.cancel')} onClick={() => closeCreate()} variant="secondary" />
                <Button
                  isDisabled={!newName.trim() || !newBaseURL.trim() || isSaving}
                  isLoading={isSaving}
                  label={t('tool.create')}
                  onClick={() => void create()}
                  variant="primary"
                />
              </HStack>
            </LayoutFooter>
          }
          header={<DialogHeader onOpenChange={(open) => { if (!open) closeCreate(); }} title={route === 'mcp' ? t('tool.addMCP') : route === 'ai' ? t('tool.createWithAI') : t('tool.createTitle')} />}
        />
      </Dialog>

      <ToolMarket
        isOpen={isCatalogOpen}
        onOpen={(toolID) => {
          setIsCatalogOpen(false);
          router.push(`/tools/${toolID}?workspace=${encodeURIComponent(workspaceID)}`);
        }}
        onOpenChange={setIsCatalogOpen}
        workspaceID={workspaceID}
      />

      <AlertDialog
        actionLabel={t('kb.delete')}
        cancelLabel={t('common.cancel')}
        description={t('tool.deleteBody')}
        isOpen={deleting !== null}
        onAction={() => void remove()}
        onOpenChange={(open) => { if (!open) setDeleting(null); }}
        title={t('tool.deleteTitle')}
      />

      {sharing ? (
        <ToolShareDialog
          directory={directory}
          onClose={() => setSharing(null)}
          onError={setError}
          onSaved={(visibility) => { patchTool(sharing.id, {visibility}); setSharing(null); }}
          tool={sharing}
        />
      ) : null}
    </>
  );
}

