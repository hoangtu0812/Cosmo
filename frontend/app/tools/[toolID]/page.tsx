'use client';

import {Suspense, useCallback, useEffect, useState} from 'react';
import {useParams, useRouter, useSearchParams} from 'next/navigation';
import {ArrowLeft, History, Home, Play, Plus, RefreshCw, Search, ShieldCheck, Sparkles, Trash2, Zap} from 'lucide-react';
import {AlertDialog} from '@astryxdesign/core/AlertDialog';
import {Banner} from '@astryxdesign/core/Banner';
import {Button} from '@astryxdesign/core/Button';
import {Card} from '@astryxdesign/core/Card';
import {Dialog, DialogHeader} from '@astryxdesign/core/Dialog';
import {Switch} from '@astryxdesign/core/Switch';
import {EmptyState} from '@astryxdesign/core/EmptyState';
import {Icon} from '@astryxdesign/core/Icon';
import {IconButton} from '@astryxdesign/core/IconButton';
import {HStack, Layout, LayoutContent, LayoutFooter, LayoutHeader, LayoutPanel, VStack} from '@astryxdesign/core/Layout';
import {Item} from '@astryxdesign/core/Item';
import {List} from '@astryxdesign/core/List';
import {SelectableCard} from '@astryxdesign/core/SelectableCard';
import {Selector} from '@astryxdesign/core/Selector';
import {Text} from '@astryxdesign/core/Text';
import {TextArea} from '@astryxdesign/core/TextArea';
import {TextInput} from '@astryxdesign/core/TextInput';
import {useToast} from '@astryxdesign/core/Toast';
import {Toolbar} from '@astryxdesign/core/Toolbar';
import {Token} from '@astryxdesign/core/Token';
import {StatusLabel} from '../../components/StatusLabel';
import {api, APIError, Tool, ToolAction, ToolCallResult, ToolParameter, ToolVersion} from '../../lib/api';
import {useTranslation} from '../../lib/i18n';

export default function ToolDetailPage() {
  return (
    <Suspense fallback={null}>
      <ToolDetailScreen />
    </Suspense>
  );
}

function ToolDetailScreen() {
  const t = useTranslation();
  const router = useRouter();
  const search = useSearchParams();
  const params = useParams<{toolID: string}>();
  const toolID = params.toolID;
  const workspaceID = search.get('workspace') ?? '';

  const [tool, setTool] = useState<Tool | null>(null);
  const [actions, setActions] = useState<ToolAction[]>([]);
  const [selected, setSelected] = useState<ToolAction | null>(null);
  const [error, setError] = useState('');
  const [query, setQuery] = useState('');
  const [deleting, setDeleting] = useState<ToolAction | null>(null);
  const [isPublishOpen, setIsPublishOpen] = useState(false);
  const [isPublishing, setIsPublishing] = useState(false);
  const [changelog, setChangelog] = useState('');
  const [isHistoryOpen, setIsHistoryOpen] = useState(false);
  const [versions, setVersions] = useState<ToolVersion[]>([]);

  const load = useCallback(() => {
    if (!toolID) return;
    api.tool(toolID, workspaceID)
      .then((result) => {
        setTool(result.tool);
        setActions(result.actions);
        setSelected((current) => current ? result.actions.find((item) => item.id === current.id) ?? null : null);
      })
      .catch((caught) => {
        if (caught instanceof APIError && caught.status === 401) router.replace('/');
        else setError(caught instanceof Error ? caught.message : t('tool.loadFailed'));
      });
  }, [router, t, toolID, workspaceID]);

  useEffect(load, [load]);

  async function addAction() {
    setError('');
    try {
      const result = await api.saveToolAction(toolID, '', {
        name: `action_${actions.length + 1}`,
        description: '',
        method: 'GET',
        path: '/',
        parameters: [],
      }, workspaceID);
      setActions((current) => [...current, result.action]);
      setSelected(result.action);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : t('tool.saveFailed'));
    }
  }

  async function publish() {
    setIsPublishing(true);
    setError('');
    try {
      await api.publishTool(toolID, changelog.trim(), workspaceID);
      setChangelog('');
      setIsPublishOpen(false);
      // The header reads the tool's publish state, and publishing is exactly
      // what changes it.
      load();
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : t('tool.publishFailed'));
    } finally {
      setIsPublishing(false);
    }
  }

  async function removeAction() {
    if (!deleting) return;
    try {
      await api.deleteToolAction(toolID, deleting.id, workspaceID);
      setActions((current) => current.filter((item) => item.id !== deleting.id));
      setSelected((current) => current?.id === deleting.id ? null : current);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : t('tool.saveFailed'));
    } finally {
      setDeleting(null);
    }
  }

  const needle = query.trim().toLowerCase();
  const visible = actions.filter((item) => !needle || item.name.toLowerCase().includes(needle));

  return (
    <>
      <Layout
        height="fill"
        start={
          /* Actions get a column of their own, the way documents do in a
             knowledge base: choosing one changes what you are editing and
             nothing else on the screen moves. */
          <LayoutPanel hasDivider label={t('tool.actions')} padding={3} role="navigation" width={320}>
            <VStack gap={3} height="100%" width="100%">
              <HStack gap={2} vAlign="center" width="100%">
                <IconButton
                  icon={<ArrowLeft size={16} />}
                  label={t('tool.backToList')}
                  onClick={() => router.push(`/tools?workspace=${encodeURIComponent(workspaceID)}`)}
                  size="sm"
                  variant="ghost"
                />
                <Text maxLines={1} type="label">{tool?.name ?? ''}</Text>
              </HStack>

              <HStack gap={1} vAlign="center" width="100%">
                <TextInput
                  className="min-w-0"
                  isLabelHidden
                  label={t('tool.search')}
                  onChange={setQuery}
                  placeholder={t('tool.search')}
                  size="sm"
                  startIcon={<Icon icon={Search} size="sm" />}
                  value={query}
                  width="100%"
                />
                <IconButton
                  icon={<Plus size={16} />}
                  isDisabled={!tool?.is_editable}
                  label={t('tool.newAction')}
                  onClick={() => void addAction()}
                  size="sm"
                  variant="ghost"
                />
              </HStack>

              <VStack gap={1} isScrollable height="100%" width="100%">
                <SelectableCard
                  isSelected={selected === null}
                  label={t('tool.overview')}
                  onChange={() => setSelected(null)}
                  padding={2}
                  width="100%"
                >
                  <HStack gap={2} vAlign="center">
                    <Icon icon={Home} size="sm" />
                    <Text type="label">{t('tool.overview')}</Text>
                  </HStack>
                </SelectableCard>

                {visible.map((action) => (
                  <SelectableCard
                    isSelected={selected?.id === action.id}
                    key={action.id}
                    label={action.name}
                    onChange={() => setSelected(action)}
                    padding={2}
                    width="100%"
                  >
                    <HStack gap={2} vAlign="center" width="100%">
                      <Token label={action.method} size="sm" />
                      <Text maxLines={1}>{action.name}</Text>
                    </HStack>
                  </SelectableCard>
                ))}

                {actions.length === 0 ? (
                  <EmptyState description={t('tool.noActions')} isCompact title="—" />
                ) : null}
              </VStack>
            </VStack>
          </LayoutPanel>
        }
        header={
          <LayoutHeader hasDivider>
            <Toolbar
              endContent={
                tool && !tool.is_editable ? (
                  <StatusLabel label={t('agent.readOnly')} variant="warning" />
                ) : tool ? (
                  <HStack gap={3} vAlign="center">
                    {/* An agent published from now on calls the live version,
                        so the number belongs beside the button that moves it. */}
                    <Text color={tool.has_unpublished_changes ? 'accent' : 'secondary'} type="supporting">
                      {tool.has_unpublished_changes
                        ? t('tool.unpublished')
                        : t('tool.publishedVersion', {version: String(tool.published_version)})}
                    </Text>
                    <IconButton
                      icon={<History size={16} />}
                      label={t('tool.versions')}
                      onClick={() => {
                        setIsHistoryOpen(true);
                        api.toolVersions(toolID, workspaceID)
                          .then((result) => setVersions(result.versions))
                          .catch(() => setVersions([]));
                      }}
                      size="sm"
                      variant="ghost"
                    />
                    <Button
                      isDisabled={!tool.has_unpublished_changes || actions.length === 0}
                      isLoading={isPublishing}
                      label={t('tool.publish')}
                      onClick={() => setIsPublishOpen(true)}
                      size="sm"
                      variant="primary"
                    />
                  </HStack>
                ) : undefined
              }
              label={tool?.name ?? ''}
              startContent={
                <Text color="secondary" type="supporting">
                  {`${t('nav.tool')} · ${selected ? selected.name : t('tool.overview')}`}
                </Text>
              }
            />
          </LayoutHeader>
        }
        content={
          <LayoutContent padding={6}>
            <HStack hAlign="center" width="100%">
              <VStack gap={5} maxWidth={700} width="100%">
                {error ? <Banner isDismissable onDismiss={() => setError('')} status="error" title={error} /> : null}

                {tool === null ? null : selected ? (
                  <ActionEditor
                    action={selected}
                    key={selected.id}
                    isEditable={tool.is_editable}
                    onDelete={() => setDeleting(selected)}
                    onSaved={(saved) => {
                      setActions((current) => current.map((item) => item.id === saved.id ? saved : item));
                      setSelected(saved);
                    }}
                    t={t}
                    toolID={toolID}
                    workspaceID={workspaceID}
                  />
                ) : (
                  <ToolOverview actionCount={actions.length} onReload={load} onSaved={setTool} t={t} tool={tool} workspaceID={workspaceID} />
                )}
              </VStack>
            </HStack>
          </LayoutContent>
        }
      />

      <AlertDialog
        actionLabel={t('tool.deleteAction')}
        cancelLabel={t('common.cancel')}
        description={t('tool.deleteActionBody')}
        isOpen={deleting !== null}
        onAction={() => void removeAction()}
        onOpenChange={(open) => { if (!open) setDeleting(null); }}
        title={t('tool.deleteAction')}
      />

      <Dialog isOpen={isPublishOpen} onOpenChange={setIsPublishOpen} purpose="form">
        <Layout
          content={
            <LayoutContent>
              <VStack gap={3}>
                <Text color="secondary" type="supporting">
                  {tool && tool.published_version > 0
                    ? t('tool.publishFrom', {version: String(tool.published_version)})
                    : t('tool.publishFirst')}
                </Text>
                <TextArea
                  label={t('agent.changelog')}
                  maxLength={500}
                  onChange={setChangelog}
                  placeholder={t('agent.changelogPlaceholder')}
                  rows={4}
                  value={changelog}
                  width="100%"
                />
              </VStack>
            </LayoutContent>
          }
          footer={
            <LayoutFooter>
              <HStack gap={2} hAlign="end">
                <Button label={t('common.cancel')} onClick={() => setIsPublishOpen(false)} variant="secondary" />
                <Button isLoading={isPublishing} label={t('tool.publish')} onClick={() => void publish()} variant="primary" />
              </HStack>
            </LayoutFooter>
          }
          header={<DialogHeader onOpenChange={setIsPublishOpen} title={t('tool.publish')} />}
        />
      </Dialog>

      <Dialog isOpen={isHistoryOpen} onOpenChange={setIsHistoryOpen} purpose="info">
        <Layout
          content={
            <LayoutContent>
              {versions.length === 0 ? (
                <Text color="secondary" type="supporting">{t('tool.versionsEmpty')}</Text>
              ) : (
                <List>
                  {versions.map((version) => (
                    <Item
                      as="li"
                      description={`${version.changelog || t('agent.versionNoChangelog')} · ${t('tool.actionCount', {count: version.actions.length})} · ${new Date(version.created_at).toLocaleString()}`}
                      endContent={version.is_live
                        ? <StatusLabel label={t('agent.versionCurrent')} variant="success" />
                        : undefined}
                      key={version.id}
                      label={t('tool.publishedVersion', {version: String(version.version_number)})}
                    />
                  ))}
                </List>
              )}
            </LayoutContent>
          }
          header={<DialogHeader onOpenChange={setIsHistoryOpen} title={t('tool.versions')} />}
        />
      </Dialog>
    </>
  );
}

// The overview is where the tool as a whole is settled: where it points and
// how it authenticates. The credential is write-only by design - the server
// returns a hint, never the value - so the field is empty on every visit and
// says what is already stored beside it.
function ToolOverview({tool, actionCount, workspaceID, onSaved, onReload, t}: {
  tool: Tool;
  /* Counted from the list beside it rather than the row loaded with the page,
     so adding an action does not leave the overview claiming zero. */
  actionCount: number;
  workspaceID: string;
  onSaved: (tool: Tool) => void;
  /** Actions arrive in the column beside this one, so the page reloads both. */
  onReload: () => void;
  t: ReturnType<typeof useTranslation>;
}) {
  const [name, setName] = useState(tool.name);
  const [description, setDescription] = useState(tool.description);
  const [baseURL, setBaseURL] = useState(tool.base_url);
  const [authType, setAuthType] = useState(tool.auth_type);
  const [headerName, setHeaderName] = useState(tool.auth_header_name);
  const [secret, setSecret] = useState('');
  const [visibility, setVisibility] = useState(tool.visibility);
  const [isSaving, setIsSaving] = useState(false);
  const [failure, setFailure] = useState('');
  const [specURL, setSpecURL] = useState('');
  const [busy, setBusy] = useState('');
  // Renaming a tool changes the heading, but changing its purpose or who can
  // see it changes nothing on screen - the fields already show what was typed.
  // Without a word from the server, a successful save and a save that never
  // happened look identical.
  const toast = useToast();

  // All three routes end the same way - actions appear in the column beside
  // this one - so they share a handler and the caller only says which.
  async function fill(which: 'draft' | 'openapi' | 'discover') {
    setBusy(which);
    setFailure('');
    try {
      if (which === 'draft') {
        await api.draftToolActions(tool.id, description, workspaceID);
      } else if (which === 'openapi') {
        await api.importOpenAPI(tool.id, {url: specURL}, workspaceID);
      } else {
        await api.discoverMCPTools(tool.id, workspaceID);
      }
      onReload();
    } catch (caught) {
      setFailure(caught instanceof Error ? caught.message : t('tool.saveFailed'));
    } finally {
      setBusy('');
    }
  }

  async function save(extra: Record<string, unknown> = {}) {
    setIsSaving(true);
    setFailure('');
    try {
      const result = await api.updateTool(tool.id, {
        name,
        description,
        base_url: baseURL,
        auth_type: authType,
        auth_header_name: headerName,
        visibility,
        ...(secret ? {auth_secret: secret} : {}),
        ...extra,
      }, workspaceID);
      setSecret('');
      onSaved(result.tool);
      toast({body: t('tool.saved')});
    } catch (caught) {
      setFailure(caught instanceof Error ? caught.message : t('tool.saveFailed'));
    } finally {
      setIsSaving(false);
    }
  }

  return (
    <VStack gap={5} width="100%">
      {failure ? <Banner isDismissable onDismiss={() => setFailure('')} status="error" title={failure} /> : null}

      <HStack gap={3} vAlign="center">
        <Text type="display-3">{tool.icon || '🔌'}</Text>
        <VStack gap={0}>
          <Text size="xl" type="large">{tool.name}</Text>
          <Text color="secondary" type="supporting">{tool.base_url}</Text>
        </VStack>
      </HStack>

      <HStack gap={4} vAlign="center" wrap="wrap">
        <Text color="secondary" type="supporting">{actionCount === 1 ? t('tool.actionCountOne') : t('tool.actionCount', {count: actionCount})}</Text>
        <Text color="secondary" type="supporting">{tool.reference_count === 1 ? t('capability.referencesOne') : t('capability.references', {count: tool.reference_count})}</Text>
        {tool.has_secret ? <Token label={t('tool.authStored', {hint: tool.auth_hint})} size="sm" /> : null}
      </HStack>

      <VStack gap={4} width="100%">
        <TextInput isDisabled={!tool.is_editable} label={t('tool.name')} onChange={setName} value={name} width="100%" />
        <TextArea
          isDisabled={!tool.is_editable}
          label={t('tool.purpose')}
          maxLength={512}
          onChange={setDescription}
          rows={3}
          value={description}
          width="100%"
        />
        {/* A built-in reaches nothing, so there is no destination to name and
            no credential to store. Showing empty fields for both would suggest
            the tool is unfinished when it is complete. */}
        {tool.kind === 'builtin' ? null : (
          <TextInput isDisabled={!tool.is_editable} label={t('tool.baseURL')} onChange={setBaseURL} value={baseURL} width="100%" />
        )}
        <Selector
          isDisabled={!tool.is_editable}
          label={t('agent.visibility')}
          onChange={(value) => setVisibility(value as Tool['visibility'])}
          options={[
            {value: 'private', label: t('agent.visibilityPrivate')},
            {value: 'workspace', label: t('agent.visibilityWorkspace')},
          ]}
          value={visibility}
          width="100%"
        />
      </VStack>

      <VStack gap={3} hidden={tool.kind === 'builtin'} width="100%">
        <Text type="label">{t('tool.auth')}</Text>
        <Selector
          isDisabled={!tool.is_editable}
          label={t('tool.auth')}
          onChange={(value) => setAuthType(value as Tool['auth_type'])}
          options={[
            {value: 'none', label: t('tool.authNone')},
            {value: 'bearer', label: t('tool.authBearer')},
            {value: 'header', label: t('tool.authHeader')},
          ]}
          value={authType}
          width="100%"
        />
        {authType === 'header' ? (
          <TextInput
            isDisabled={!tool.is_editable}
            label={t('tool.authHeaderName')}
            onChange={setHeaderName}
            placeholder="X-Api-Key"
            value={headerName}
            width="100%"
          />
        ) : null}
        {authType !== 'none' ? (
          <VStack gap={2} width="100%">
            <TextInput
              isDisabled={!tool.is_editable}
              label={t('tool.authSecret')}
              onChange={setSecret}
              placeholder={tool.has_secret ? t('tool.authStored', {hint: tool.auth_hint}) : ''}
              type="password"
              value={secret}
              width="100%"
            />
            <HStack gap={2} vAlign="center">
              <Icon icon={ShieldCheck} size="sm" />
              <Text color="secondary" type="supporting">{t('tool.authSecretHint')}</Text>
            </HStack>
            {tool.has_secret ? (
              <HStack hAlign="start">
                <Button
                  isDisabled={!tool.is_editable || isSaving}
                  label={t('tool.clearSecret')}
                  onClick={() => void save({auth_secret: ''})}
                  size="sm"
                  variant="ghost"
                />
              </HStack>
            ) : null}
          </VStack>
        ) : null}
      </VStack>

      <HStack gap={2} hAlign="start">
        <Button
          isDisabled={!tool.is_editable || isSaving}
          isLoading={isSaving}
          label={t('tool.save')}
          onClick={() => void save()}
          variant="primary"
        />
      </HStack>

      {/* Three ways to fill a tool with actions, in order of how much they can
          be trusted: the server's own answer, the API's own description, then
          the model's recollection. A built-in's actions are fixed - there is
          nothing to discover, import or draft. */}
      <VStack gap={3} hidden={tool.kind === 'builtin'} width="100%">
        <Text type="label">{t('tool.fillActions')}</Text>
        {tool.kind === 'mcp' ? (
          <HStack gap={2} hAlign="start">
            <Button
              icon={<RefreshCw size={14} />}
              isDisabled={!tool.is_editable || busy !== ''}
              isLoading={busy === 'discover'}
              label={t('tool.rediscover')}
              onClick={() => void fill('discover')}
              size="sm"
              variant="secondary"
            />
          </HStack>
        ) : (
          <VStack gap={3} width="100%">
            <HStack gap={2} vAlign="end" width="100%">
              <TextInput
                className="min-w-0"
                label={t('tool.openapiURL')}
                onChange={setSpecURL}
                placeholder="https://api.example.com/openapi.json"
                value={specURL}
                width="100%"
              />
              <Button
                isDisabled={!tool.is_editable || !specURL.trim() || busy !== ''}
                isLoading={busy === 'openapi'}
                label={t('tool.importOpenAPI')}
                onClick={() => void fill('openapi')}
                size="sm"
                variant="secondary"
              />
            </HStack>
            <HStack gap={2} hAlign="start">
              <Button
                icon={<Sparkles size={14} />}
                isDisabled={!tool.is_editable || busy !== ''}
                isLoading={busy === 'draft'}
                label={t('tool.draftActions')}
                onClick={() => void fill('draft')}
                size="sm"
                variant="ghost"
              />
            </HStack>
          </VStack>
        )}
      </VStack>
    </VStack>
  );
}

// One action: what the model calls it, what it does, and where the request
// goes. The test panel underneath calls it once with values typed by hand, so
// the shape can be proved before an agent is pointed at it.
function ActionEditor({action, toolID, workspaceID, isEditable, onSaved, onDelete, t}: {
  action: ToolAction;
  toolID: string;
  workspaceID: string;
  isEditable: boolean;
  onSaved: (action: ToolAction) => void;
  onDelete: () => void;
  t: ReturnType<typeof useTranslation>;
}) {
  const [name, setName] = useState(action.name);
  const [description, setDescription] = useState(action.description);
  const [method, setMethod] = useState<ToolAction['method']>(action.method);
  const [path, setPath] = useState(action.path);
  const [parameters, setParameters] = useState<ToolParameter[]>(action.parameters);
  const [resultType, setResultType] = useState<NonNullable<ToolAction['result_type']>>(action.result_type ?? '');
  const [resultDescription, setResultDescription] = useState(action.result_description ?? '');
  const [isSaving, setIsSaving] = useState(false);
  const [failure, setFailure] = useState('');

  const [testValues, setTestValues] = useState<Record<string, string>>({});
  const [isTesting, setIsTesting] = useState(false);
  const [result, setResult] = useState<ToolCallResult | null>(null);

  async function save() {
    setIsSaving(true);
    setFailure('');
    try {
      const saved = await api.saveToolAction(toolID, action.id, {
        name, description, method, path, parameters,
        result_type: resultType, result_description: resultDescription,
      }, workspaceID);
      onSaved(saved.action);
    } catch (caught) {
      setFailure(caught instanceof Error ? caught.message : t('tool.saveFailed'));
    } finally {
      setIsSaving(false);
    }
  }

  async function test() {
    setIsTesting(true);
    setFailure('');
    try {
      const response = await api.testToolAction(toolID, action.id, testValues, workspaceID);
      setResult(response.result);
    } catch (caught) {
      setFailure(caught instanceof Error ? caught.message : t('tool.saveFailed'));
    } finally {
      setIsTesting(false);
    }
  }

  function updateParameter(index: number, changes: Partial<ToolParameter>) {
    setParameters((current) => current.map((item, position) => position === index ? {...item, ...changes} : item));
  }

  return (
    <VStack gap={5} width="100%">
      {failure ? <Banner isDismissable onDismiss={() => setFailure('')} status="error" title={failure} /> : null}

      <HStack gap={2} hAlign="between" vAlign="center" width="100%">
        <Text size="xl" type="large">{action.name}</Text>
        <IconButton icon={<Trash2 size={16} />} isDisabled={!isEditable} label={t('tool.deleteAction')} onClick={onDelete} size="sm" variant="ghost" />
      </HStack>

      <VStack gap={4} width="100%">
        <VStack gap={1} width="100%">
          <TextInput isDisabled={!isEditable} label={t('tool.actionName')} onChange={setName} value={name} width="100%" />
          <Text color="secondary" type="supporting">{t('tool.actionNameHint')}</Text>
        </VStack>
        <TextArea
          isDisabled={!isEditable}
          label={t('tool.actionDescription')}
          maxLength={512}
          onChange={setDescription}
          rows={2}
          value={description}
          width="100%"
        />
        <HStack gap={3} vAlign="end" width="100%">
          <Selector
            isDisabled={!isEditable}
            label={t('tool.method')}
            onChange={(value) => setMethod(value as ToolAction['method'])}
            options={['GET', 'POST', 'PUT', 'PATCH', 'DELETE'].map((verb) => ({value: verb, label: verb}))}
            value={method}
            width={160}
          />
          <TextInput className="min-w-0" isDisabled={!isEditable} label={t('tool.path')} onChange={setPath} placeholder="/customers/{id}" value={path} width="100%" />
        </HStack>
      </VStack>

      <VStack gap={3} width="100%">
        <HStack gap={2} hAlign="between" vAlign="center" width="100%">
          <Text type="label">{t('tool.parameters')}</Text>
          <Button
            icon={<Plus size={14} />}
            isDisabled={!isEditable}
            label={t('tool.addParameter')}
            onClick={() => setParameters((current) => [...current, {name: '', description: '', type: 'string', in: 'query', is_required: false, source: 'model', value: ''}])}
            size="sm"
            variant="ghost"
          />
        </HStack>

        {parameters.length === 0 ? (
          <Text color="secondary" type="supporting">{t('tool.noParameters')}</Text>
        ) : parameters.map((parameter, index) => (
          <Card key={index} padding={3} width="100%">
            <VStack gap={3} width="100%">
              <HStack gap={2} vAlign="end" width="100%">
                <TextInput
                  className="min-w-0"
                  isDisabled={!isEditable}
                  label={t('tool.paramName')}
                  onChange={(value) => updateParameter(index, {name: value})}
                  value={parameter.name}
                  width="100%"
                />
                <Selector
                  isDisabled={!isEditable}
                  label={t('tool.paramType')}
                  onChange={(value) => updateParameter(index, {type: value as ToolParameter['type']})}
                  options={[
                    {value: 'string', label: 'string'},
                    {value: 'number', label: 'number'},
                    {value: 'boolean', label: 'boolean'},
                  ]}
                  value={parameter.type}
                  width={140}
                />
                <Selector
                  isDisabled={!isEditable}
                  label={t('tool.paramIn')}
                  onChange={(value) => updateParameter(index, {in: value as ToolParameter['in']})}
                  options={[
                    {value: 'query', label: 'query'},
                    {value: 'path', label: 'path'},
                    {value: 'body', label: 'body'},
                  ]}
                  value={parameter.in}
                  width={140}
                />
                <Selector
                  isDisabled={!isEditable}
                  label={t('tool.paramSource')}
                  onChange={(value) => updateParameter(index, {
                    source: value as ToolParameter['source'],
                    // A fixed value is already supplied, so it cannot also be
                    // something the model is required to pass.
                    is_required: value === 'fixed' ? false : parameter.is_required,
                  })}
                  options={[
                    {value: 'model', label: t('tool.paramFromModel')},
                    {value: 'fixed', label: t('tool.paramFixed')},
                  ]}
                  value={parameter.source ?? 'model'}
                  width={170}
                />
                <IconButton
                  icon={<Trash2 size={16} />}
                  isDisabled={!isEditable}
                  label={t('tool.deleteAction')}
                  onClick={() => setParameters((current) => current.filter((_, position) => position !== index))}
                  size="sm"
                  variant="ghost"
                />
              </HStack>
              <TextInput
                isDisabled={!isEditable}
                label={t('tool.paramDescription')}
                onChange={(value) => updateParameter(index, {description: value})}
                value={parameter.description}
                width="100%"
              />
              {parameter.source === 'fixed' ? (
                <TextInput
                  isDisabled={!isEditable}
                  label={t('tool.paramValue')}
                  onChange={(value) => updateParameter(index, {value})}
                  value={parameter.value ?? ''}
                  width="100%"
                />
              ) : (
                <Switch
                  value={parameter.is_required}
                  isDisabled={!isEditable}
                  label={t('tool.paramRequired')}
                  onChange={(checked: boolean) => updateParameter(index, {is_required: checked})}
                />
              )}
            </VStack>
          </Card>
        ))}
      </VStack>

      <VStack gap={2} width="100%">
        <Text type="label">{t('tool.result')}</Text>
        {/* An action says what it takes; without this it says nothing about
            what comes back, and the model reads a wall of JSON and guesses. */}
        <Text color="secondary" type="supporting">{t('tool.resultHint')}</Text>
        <HStack gap={3} vAlign="end" width="100%">
          <Selector
            isDisabled={!isEditable}
            label={t('tool.resultType')}
            onChange={(value) => setResultType(value as NonNullable<ToolAction['result_type']>)}
            options={[
              {value: '', label: t('tool.resultUnsaid')},
              {value: 'object', label: 'object'},
              {value: 'array', label: 'array'},
              {value: 'string', label: 'string'},
              {value: 'number', label: 'number'},
              {value: 'boolean', label: 'boolean'},
            ]}
            value={resultType}
            width={170}
          />
          <TextInput
            isDisabled={!isEditable}
            label={t('tool.resultDescription')}
            onChange={setResultDescription}
            placeholder={t('tool.resultPlaceholder')}
            value={resultDescription}
            width="100%"
          />
        </HStack>
      </VStack>

      <HStack gap={2} hAlign="start">
        <Button isDisabled={!isEditable || isSaving} isLoading={isSaving} label={t('tool.save')} onClick={() => void save()} variant="primary" />
      </HStack>

      <VStack gap={3} width="100%">
        <HStack gap={2} vAlign="center">
          <Icon icon={Zap} size="sm" />
          <Text type="label">{t('tool.testTitle')}</Text>
        </HStack>
        {action.parameters.map((parameter) => (
          <TextInput
            key={parameter.name}
            label={parameter.name}
            onChange={(value) => setTestValues((current) => ({...current, [parameter.name]: value}))}
            placeholder={parameter.description}
            value={testValues[parameter.name] ?? ''}
            width="100%"
          />
        ))}
        <HStack gap={2} hAlign="start">
          <Button
            icon={<Play size={14} />}
            isDisabled={!isEditable || isTesting}
            isLoading={isTesting}
            label={t('tool.test')}
            onClick={() => void test()}
            size="sm"
            variant="secondary"
          />
        </HStack>

        {result ? (
          <Card padding={4} width="100%">
            <VStack gap={2} width="100%">
              <HStack gap={2} vAlign="center" wrap="wrap">
                <StatusLabel
                  label={String(result.status)}
                  variant={result.status >= 200 && result.status < 300 ? 'success' : 'error'}
                />
                <Text color="secondary" type="supporting">{`${result.duration_ms} ms`}</Text>
                {result.is_truncated ? <Text color="secondary" type="supporting">{t('tool.testTruncated')}</Text> : null}
              </HStack>
              <TextArea isReadOnly label={t('tool.testResult')} rows={10} value={result.body} width="100%" />
            </VStack>
          </Card>
        ) : (
          <Text color="secondary" type="supporting">{t('tool.testNever')}</Text>
        )}
      </VStack>
    </VStack>
  );
}
