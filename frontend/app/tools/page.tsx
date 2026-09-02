'use client';

import {Suspense, useCallback, useEffect, useState} from 'react';
import {useRouter, useSearchParams} from 'next/navigation';
import {Boxes, MoreHorizontal, Plug, Search, Settings2, ShieldCheck, Sparkles, Store, Trash2, Wrench} from 'lucide-react';
import {AlertDialog} from '@astryxdesign/core/AlertDialog';
import {Banner} from '@astryxdesign/core/Banner';
import {Button} from '@astryxdesign/core/Button';
import {Card} from '@astryxdesign/core/Card';
import {Dialog, DialogHeader} from '@astryxdesign/core/Dialog';
import {DropdownMenu} from '@astryxdesign/core/DropdownMenu';
import {EmptyState} from '@astryxdesign/core/EmptyState';
import {Grid} from '@astryxdesign/core/Grid';
import {Icon} from '@astryxdesign/core/Icon';
import {HStack, Layout, LayoutContent, LayoutFooter, VStack} from '@astryxdesign/core/Layout';
import {Section} from '@astryxdesign/core/Section';
import {Skeleton} from '@astryxdesign/core/Skeleton';
import {Text} from '@astryxdesign/core/Text';
import {TextArea} from '@astryxdesign/core/TextArea';
import {TextInput} from '@astryxdesign/core/TextInput';
import {Token} from '@astryxdesign/core/Token';
import {PageHeader} from '../components/PageHeader';
import {StatusLabel} from '../components/StatusLabel';
import {api, APIError, Tool, ToolCatalogEntry} from '../lib/api';
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

  const [isCreating, setIsCreating] = useState(false);
  const [isSaving, setIsSaving] = useState(false);
  const [newName, setNewName] = useState('');
  const [newDescription, setNewDescription] = useState('');
  const [newBaseURL, setNewBaseURL] = useState('');
  // 'plain' asks for nothing more, 'ai' has the model describe the actions,
  // 'mcp' asks the server to describe itself.
  const [route, setRoute] = useState<'plain' | 'ai' | 'mcp'>('plain');
  const [isCatalogOpen, setIsCatalogOpen] = useState(false);
  const [catalog, setCatalog] = useState<ToolCatalogEntry[]>([]);
  const [installing, setInstalling] = useState('');

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

  function openCatalog() {
    setIsCatalogOpen(true);
    api.toolCatalog().then((result) => setCatalog(result.entries)).catch(() => setCatalog([]));
  }

  async function install(entry: ToolCatalogEntry) {
    setInstalling(entry.id);
    setError('');
    try {
      const result = await api.installCatalogTool(entry.id, workspaceID);
      setIsCatalogOpen(false);
      router.push(`/tools/${result.tool.id}?workspace=${encodeURIComponent(workspaceID)}`);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : t('tool.createFailed'));
    } finally {
      setInstalling('');
    }
  }

  const addMenu = [
    {icon: <Sparkles size={15} />, label: t('tool.createWithAI'), onClick: () => openCreate('ai')},
    {icon: <Plug size={15} />, label: t('tool.addPlugin'), onClick: () => openCreate('plain')},
    {icon: <Boxes size={15} />, label: t('tool.addMCP'), onClick: () => openCreate('mcp')},
    {icon: <Store size={15} />, label: t('tool.marketplace'), onClick: openCatalog},
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
                <VStack gap={6} hAlign="center" padding={6} width="100%">
                  <EmptyState
                    actions={<DropdownMenu alignment="center" button={{label: t('tool.add'), variant: 'primary'}} items={addMenu} />}
                    description={t('tool.emptyBody')}
                    icon={<Wrench size={64} strokeWidth={1} />}
                    title={t('tool.empty')}
                  />
                  <Grid columns={{minWidth: 200, max: 3}} gap={4} maxWidth={760} width="100%">
                    {[
                      {key: 'mcp', icon: Boxes, title: t('tool.wayMCP'), body: t('tool.wayMCPBody')},
                      {key: 'plugin', icon: Plug, title: t('tool.wayPlugin'), body: t('tool.wayPluginBody')},
                      {key: 'custom', icon: Settings2, title: t('tool.wayCustom'), body: t('tool.wayCustomBody')},
                    ].map((way) => (
                      <Card key={way.key} padding={4}>
                        <VStack gap={2}>
                          <Icon icon={way.icon} size="md" />
                          <Text type="label">{way.title}</Text>
                          <Text color="secondary" type="supporting">{way.body}</Text>
                        </VStack>
                      </Card>
                    ))}
                  </Grid>
                </VStack>
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
                      <Card key={tool.id} onClick={() => router.push(`/tools/${tool.id}?workspace=${encodeURIComponent(workspaceID)}`)} padding={0} width="100%">
                        <VStack gap={0} height="100%">
                          <Section padding={5} variant="muted">
                            <HStack hAlign="center" width="100%">
                              <Card padding={3}>
                                <Text type="display-3">{tool.icon || '🔌'}</Text>
                              </Card>
                            </HStack>
                          </Section>
                          <Section padding={4}>
                            <VStack gap={2}>
                              <HStack gap={2} hAlign="between" vAlign="center" width="100%">
                                <Text maxLines={1} type="label">{tool.name}</Text>
                                {tool.is_editable ? (
                                  <DropdownMenu
                                    alignment="end"
                                    button={{icon: <MoreHorizontal size={15} />, isIconOnly: true, label: t('kb.manage'), size: 'sm', variant: 'ghost'}}
                                    hasChevron={false}
                                    items={[
                                      {icon: <Settings2 size={15} />, label: t('kb.configure'), onClick: () => router.push(`/tools/${tool.id}?workspace=${encodeURIComponent(workspaceID)}`)},
                                      {type: 'divider' as const},
                                      {icon: <Trash2 size={15} />, label: t('kb.delete'), onClick: () => setDeleting(tool), variant: 'destructive' as const},
                                    ]}
                                  />
                                ) : null}
                              </HStack>
                              <Text color="secondary" maxLines={2} type="supporting">
                                {tool.description || tool.base_url}
                              </Text>
                              <HStack gap={2} vAlign="center" wrap="wrap">
                                <Text color="secondary" type="supporting">
                                  {t('tool.actionCount', {count: tool.action_count})}
                                </Text>
                                {tool.has_secret ? <Token label={t('tool.keySet', {hint: tool.auth_hint})} size="sm" /> : null}
                                <StatusLabel
                                  label={tool.visibility === 'workspace' ? t('agent.visibilityWorkspace') : t('agent.visibilityPrivate')}
                                  variant="neutral"
                                />
                              </HStack>
                            </VStack>
                          </Section>
                        </VStack>
                      </Card>
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

      <Dialog isOpen={isCatalogOpen} onOpenChange={setIsCatalogOpen} purpose="form" width={640}>
        <Layout
          content={
            <LayoutContent>
              <VStack gap={3} width="100%">
                <Text color="secondary" type="supporting">{t('tool.catalogHint')}</Text>
                {catalog.map((entry) => (
                  <Card key={entry.id} padding={4} width="100%">
                    <HStack gap={3} hAlign="between" vAlign="center" width="100%">
                      <HStack gap={3} vAlign="center">
                        <Text type="display-3">{entry.icon}</Text>
                        <VStack gap={0}>
                          <Text type="label">{entry.name}</Text>
                          <Text color="secondary" type="supporting">{entry.description}</Text>
                          <Text color="secondary" type="supporting">
                            {t('tool.actionCount', {count: entry.actions.length})}
                          </Text>
                        </VStack>
                      </HStack>
                      <Button
                        isDisabled={installing !== ''}
                        isLoading={installing === entry.id}
                        label={t('tool.install')}
                        onClick={() => void install(entry)}
                        size="sm"
                        variant="secondary"
                      />
                    </HStack>
                  </Card>
                ))}
              </VStack>
            </LayoutContent>
          }
          header={<DialogHeader onOpenChange={setIsCatalogOpen} title={t('tool.marketplace')} />}
        />
      </Dialog>

      <AlertDialog
        actionLabel={t('kb.delete')}
        cancelLabel={t('common.cancel')}
        description={t('tool.deleteBody')}
        isOpen={deleting !== null}
        onAction={() => void remove()}
        onOpenChange={(open) => { if (!open) setDeleting(null); }}
        title={t('tool.deleteTitle')}
      />
    </>
  );
}
