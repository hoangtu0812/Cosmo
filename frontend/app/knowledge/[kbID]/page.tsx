'use client';

import {useCallback, useEffect, useRef, useState} from 'react';
import {useParams, useRouter, useSearchParams} from 'next/navigation';
import {ArrowLeft, ExternalLink, FileText, FlaskConical, Home, Plus, ScrollText, Search, SlidersHorizontal, Trash2, Upload} from 'lucide-react';
import {AlertDialog} from '@astryxdesign/core/AlertDialog';
import {Banner} from '@astryxdesign/core/Banner';
import {Button} from '@astryxdesign/core/Button';
import {Card} from '@astryxdesign/core/Card';
import {Dialog, DialogHeader} from '@astryxdesign/core/Dialog';
import {EmptyState} from '@astryxdesign/core/EmptyState';
import {Grid} from '@astryxdesign/core/Grid';
import {Collapsible} from '@astryxdesign/core/Collapsible';
import {Icon} from '@astryxdesign/core/Icon';
import {IconButton} from '@astryxdesign/core/IconButton';
import {Item} from '@astryxdesign/core/Item';
import {HStack, Layout, LayoutContent, LayoutFooter, LayoutHeader, LayoutPanel, VStack} from '@astryxdesign/core/Layout';
import {List} from '@astryxdesign/core/List';
import {Section} from '@astryxdesign/core/Section';
import {Spinner} from '@astryxdesign/core/Spinner';
import {Step, Stepper} from '@astryxdesign/core/Stepper';
import {Selector} from '@astryxdesign/core/Selector';
import {Text} from '@astryxdesign/core/Text';
import {TextInput} from '@astryxdesign/core/TextInput';
import {Toolbar} from '@astryxdesign/core/Toolbar';
import {ProgressBar} from '@astryxdesign/core/ProgressBar';
import {NumberInput} from '@astryxdesign/core/NumberInput';
import {SegmentedControl, SegmentedControlItem} from '@astryxdesign/core/SegmentedControl';
import {SelectableCard} from '@astryxdesign/core/SelectableCard';
import {Skeleton} from '@astryxdesign/core/Skeleton';
import {Slider} from '@astryxdesign/core/Slider';
import {StatusLabel} from '../../components/StatusLabel';
import {api, APIError, DocumentEvent, GatewayModel, KnowledgeBase, KnowledgeDocument, KnowledgeDocumentDetail} from '../../lib/api';
import {useTranslation} from '../../lib/i18n';

// Ingestion is asynchronous, so a document that is still being parsed is
// re-checked until it settles. The poll stops as soon as nothing is in flight,
// rather than running for as long as the page is open.
const POLL_INTERVAL = 4000;


export default function KnowledgeDetailPage() {
  const t = useTranslation();
  const router = useRouter();
  const search = useSearchParams();
  const params = useParams<{kbID: string}>();
  const kbID = params.kbID;
  const workspaceID = search.get('workspace') ?? '';

  const [base, setBase] = useState<KnowledgeBase | null>(null);
	const [baseLoading, setBaseLoading] = useState(true);
  const [documents, setDocuments] = useState<KnowledgeDocument[]>([]);
  const [error, setError] = useState('');
  const [uploading, setUploading] = useState(false);
  const [retryBatch, setRetryBatch] = useState<{files: File[]; requestID: string} | null>(null);
  const [ingestion, setIngestion] = useState<{id: string; status: string; files: number; completed_documents: number; total_documents: number} | null>(null);
  const [deleting, setDeleting] = useState<KnowledgeDocument | null>(null);
  const [reindexing, setReindexing] = useState(false);
  const [publishing, setPublishing] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [pipelineDocument, setPipelineDocument] = useState<KnowledgeDocument | null>(null);
  const [selectedDocument, setSelectedDocument] = useState<KnowledgeDocument | null>(null);
  const [detail, setDetail] = useState<KnowledgeDocumentDetail | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [query, setQuery] = useState('');
  const fileRef = useRef<HTMLInputElement>(null);
  const uploadScope = useRef(0);

  useEffect(() => {
    return () => { uploadScope.current += 1; };
  }, [kbID]);

  useEffect(() => { setRetryBatch(null); setIngestion(null); }, [kbID]);

  const canEdit = base?.access === 'owner';

  const isSettling = documents.some((item) => item.status === 'processing' || item.status === 'pending');
	const processingCount = documents.filter((item) => item.status === 'processing' || item.status === 'pending').length;
	const failedCount = documents.filter((item) => item.status === 'failed').length;

  const loadDocuments = useCallback(
    async () => {
      const result = await api.knowledgeDocuments(kbID);
      setDocuments(result.documents);
      const bases = await api.knowledgeBases(workspaceID || undefined);
      setBase(bases.knowledge_bases.find((item) => item.id === kbID) ?? null);
      if (canEdit) {
        const result = await api.knowledgeIngestionJobs(kbID);
        setIngestion(result.jobs[0] ?? null);
      }
    },
    [kbID, canEdit, workspaceID],
  );

  // Stable across renders: the log subscribes on this callback, and a fresh
  // function each render would tear the stream down and reopen it every time.
  const handleSettled = useCallback(() => { void loadDocuments(); }, [loadDocuments]);

  const openDocument = useCallback(async (document: KnowledgeDocument) => {
    setSelectedDocument(document);
    setDetail(null);
    setDetailLoading(true);
    try {
      setDetail(await api.knowledgeDocumentDetail(kbID, document.id));
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : t('kb.docsFailed'));
    } finally {
      setDetailLoading(false);
    }
  }, [kbID, t]);

  useEffect(() => {
    api.knowledgeBases(workspaceID || undefined)
      .then((result) => {
        const found = result.knowledge_bases.find((item) => item.id === kbID);
        if (!found) router.replace(workspaceID ? `/knowledge?workspace=${encodeURIComponent(workspaceID)}` : '/knowledge');
        else setBase(found);
      })
      .catch((caught) => {
        if (caught instanceof APIError && caught.status === 401) router.replace('/');
		})
		.finally(() => setBaseLoading(false));
  }, [kbID, router, workspaceID]);

  useEffect(() => {
    loadDocuments().catch(() => setError(t('kb.docsFailed')));
  }, [loadDocuments, t]);

  useEffect(() => {
    if (!isSettling) return undefined;
    const timer = setInterval(() => { void loadDocuments().catch(() => undefined); }, POLL_INTERVAL);
    return () => clearInterval(timer);
  }, [isSettling, loadDocuments]);

  async function upload(files: File[], requestID = crypto.randomUUID()) {
    if (files.length === 0) return;
    if (files.length > 20 || files.reduce((size, file) => size + file.size, 0) > 64 * 1024 * 1024) {
      setError('Mỗi lô tối đa 20 tệp, tổng dung lượng 64 MB.');
      return;
    }
    setUploading(true);
    setError('');
    const scope = uploadScope.current;
    try {
      await api.uploadKnowledgeBatch(kbID, files, requestID);
      if (uploadScope.current !== scope) return;
      setRetryBatch(null);
      await loadDocuments();
    } catch (caught) {
      if (uploadScope.current !== scope) return;
      const uncertain = !(caught instanceof APIError) || caught.status >= 500;
      setRetryBatch(uncertain ? {files, requestID} : null);
      setError(caught instanceof Error ? caught.message : t('kb.uploadFailed'));
      void loadDocuments().catch(() => undefined);
    } finally {
      if (uploadScope.current === scope) setUploading(false);
    }
  }

  // Publishing captures indexed passages for snapshot releases. Live queries
  // continue reading the mutable index.
  async function reindex() {
    setReindexing(true);
    setError('');
    try { await api.reindexKnowledgeBase(kbID); await loadDocuments(); }
    catch (caught) { setError(caught instanceof Error ? caught.message : t('kb.saveFailed')); }
    finally { setReindexing(false); }
  }

  async function publish() {
    setPublishing(true);
    setError('');
    try {
      const result = await api.publishKnowledgeBase(kbID);
      setBase(result.knowledge_base);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : t('kb.publishFailed'));
    } finally {
      setPublishing(false);
    }
  }

  async function remove() {
    if (!deleting) return;
    try {
      await api.deleteKnowledgeDocument(kbID, deleting.id);
      setDocuments((current) => current.filter((item) => item.id !== deleting.id));
      setDeleting(null);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : t('kb.docDeleteFailed'));
    }
  }

  const needle = query.trim().toLowerCase();
  const visibleDocuments = documents.filter(
    (item) => !needle || (item.title || item.filename).toLowerCase().includes(needle),
  );

  return (
    <>
      <Layout
        height="fill"
        start={
          /* The reference gives documents a column of their own beside the
             workspace's, so opening one changes what you are reading and
             nothing else. This list replaces the table that used to fill the
             content area. */
          <LayoutPanel hasDivider label={t('kb.documents')} padding={3} role="navigation" width={320}>
            <VStack gap={3} height="100%" width="100%">
              <HStack gap={2} vAlign="center" width="100%">
                <IconButton
                  icon={<ArrowLeft size={16} />}
                  label={t('kb.backToList')}
                  onClick={() => router.push(`/knowledge?workspace=${encodeURIComponent(workspaceID)}`)}
                  size="sm"
                  variant="ghost"
                />
                <Text maxLines={1} type="label">{base?.name ?? ''}</Text>
              </HStack>

              <HStack gap={1} vAlign="center" width="100%">
                <TextInput
                  className="min-w-0"
                  isLabelHidden
                  label={t('kbd.searchDocuments')}
                  onChange={setQuery}
                  placeholder={t('kbd.searchDocuments')}
                  size="sm"
                  startIcon={<Icon icon={Search} size="sm" />}
                  value={query}
                  width="100%"
                />
                <IconButton
                  icon={<Plus size={16} />}
                  isDisabled={!canEdit || uploading || !base?.embedding_model}
                  label={t('kb.uploadMany')}
                  onClick={() => fileRef.current?.click()}
                  size="sm"
                  variant="ghost"
                />
              </HStack>

              <VStack gap={1} isScrollable height="100%" width="100%">
                <SelectableCard
                  isSelected={selectedDocument === null}
                  label={t('kb.overview')}
                  onChange={() => { setSelectedDocument(null); setDetail(null); }}
                  padding={2}
                  width="100%"
                >
                  <HStack gap={2} vAlign="center">
                    <Icon icon={Home} size="sm" />
                    <Text type="label">{t('kb.overview')}</Text>
                  </HStack>
                </SelectableCard>

                {visibleDocuments.map((item) => (
                  <SelectableCard
                    isSelected={selectedDocument?.id === item.id}
                    key={item.id}
                    label={item.title || item.filename}
                    onChange={() => openDocument(item)}
                    padding={2}
                    width="100%"
                  >
                    <HStack gap={2} vAlign="center" width="100%">
                      <Icon icon={FileText} size="sm" />
                      <Text maxLines={1}>{item.title || item.filename}</Text>
                    </HStack>
                  </SelectableCard>
                ))}

                {documents.length === 0 ? (
                  <EmptyState description={t('kb.noDocuments')} isCompact title="—" />
                ) : null}
              </VStack>

              {/* The reference closes this column with the base's own actions. */}
              <VStack gap={1} width="100%">
                <Button icon={<FlaskConical size={14} />} isDisabled label={t('kb.recallTest')} size="sm" variant="ghost" />
                <Button
                  icon={<SlidersHorizontal size={14} />}
                  isDisabled={!canEdit}
                  label={t('kb.settings')}
                  onClick={() => setSettingsOpen(true)}
                  size="sm"
                  variant="ghost"
                />
              </VStack>
            </VStack>
          </LayoutPanel>
        }
        header={
          <LayoutHeader hasDivider>
            <Toolbar
              endContent={
                  <HStack gap={2} vAlign="center">
                    {selectedDocument ? <Button icon={<ScrollText size={16} />} label={t('kb.viewLog')} onClick={() => setPipelineDocument(selectedDocument)} size="sm" variant="secondary" /> : null}
                    {canEdit ? <>
                    <StatusLabel
                      label={base && base.version > 0 ? t('kb.published', {version: base.version}) : t('kb.draft')}
                      variant={base && base.version > 0 ? 'neutral' : 'warning'}
                    />
                    <Button
                      isDisabled={publishing || base?.needs_reindex || documents.length === 0 || documents.some((item) => item.status !== 'ready')}
                      isLoading={publishing}
                      label={base && base.version > 0 ? t('kb.republish') : t('kb.publish')}
                      onClick={() => void publish()}
                      size="sm"
                      variant="primary"
                    />
                    </> : null}
                  </HStack>
              }
              label={base?.name ?? ''}
              startContent={
                <Text color="secondary" type="supporting">
                  {selectedDocument
                    ? `${base?.name ?? ''} · ${selectedDocument.title || selectedDocument.filename}`
                    : `${t('kb.title')} · ${t('kb.overview')}`}
                </Text>
              }
            />
          </LayoutHeader>
        }
        content={
          <LayoutContent padding={6}>
            {/* The reference centres this column rather than pinning it to the
                left edge, so the reading measure stays comfortable however
                wide the window is. */}
            <HStack hAlign="center" width="100%">
            <VStack gap={5} maxWidth={700} width="100%">
              {error && <Banner isDismissable onDismiss={() => setError('')} status="error" title={error} />}
              {canEdit && retryBatch && <Button label="Thử lại lô tải lên" variant="secondary" isDisabled={uploading} onClick={() => void upload(retryBatch.files, retryBatch.requestID)} />}
              {canEdit && ingestion && <Text>Lô gần nhất: {({uploading: 'Đang lưu tệp', queued: 'Đang chờ', running: 'Đang xử lý', succeeded: 'Hoàn tất', failed: 'Thất bại'} as Record<string, string>)[ingestion.status] ?? ingestion.status} · {ingestion.completed_documents}/{ingestion.total_documents} tài liệu</Text>}

              {canEdit && base?.needs_reindex ? <Banner status="warning" title={t('kb.needsReindex')} /> : null}
              {canEdit && selectedDocument && (base?.needs_reindex || failedCount > 0) ? <Button label={t('kb.reindex')} isDisabled={isSettling || reindexing} isLoading={reindexing} onClick={() => void reindex()} variant="secondary" /> : null}
              {base && !base.embedding_model ? <Banner status="warning" title={t('kbd.needEmbedding')} /> : null}

              <input
                accept=".txt,.md,.markdown,.csv,.json,.pdf,.docx,.pptx,.html,.htm"
                hidden
                multiple
                onChange={(event) => {
                  const files = Array.from(event.target.files ?? []);
                  if (files.length > 0) void upload(files);
                  event.target.value = '';
                }}
                ref={fileRef}
                type="file"
              />

              {selectedDocument ? (
                <DocumentReader
                  detail={detail}
                  isLoading={detailLoading}
                  kbID={kbID}
                  onDelete={canEdit ? () => setDeleting(selectedDocument) : undefined}
                  onSettled={handleSettled}
                  selectedDocument={selectedDocument}
                />
              ) : baseLoading ? (
                <Grid columns={{minWidth: 180, max: 3}} gap={3} width="100%">
                  {[0, 1, 2].map((index) => <Skeleton height={88} index={index} key={index} width="100%" />)}
                </Grid>
              ) : (
                <VStack gap={5} width="100%">
                  {/* The reference opens with what the base is before what is
                      in it: icon, name, description, then the counts. */}
                  <HStack gap={3} vAlign="center">
                    <Text type="display-3">{base?.icon || '📚'}</Text>
                    <VStack gap={0}>
                      <Text size="xl" type="large">{base?.name}</Text>
                      <Text color="secondary" type="supporting">
                        {base?.description || t('kb.noDescription')}
                      </Text>
                    </VStack>
                  </HStack>

                  <HStack gap={4} vAlign="center" wrap="wrap">
                    <Text color="secondary" type="supporting">
                      {t('kb.documentCount', {count: documents.length})}
                    </Text>
                    <Text color="secondary" type="supporting">
                      {t('kbd.chunkCount', {count: documents.reduce((total, item) => total + (item.chunk_count ?? 0), 0)})}
                    </Text>
                  </HStack>

                  <Grid columns={{minWidth: 180, max: 3}} gap={3} width="100%">
                    <MetricCard label={t('kbd.totalDocuments')} value={documents.length} />
                    <MetricCard isActive={processingCount > 0} label={t('kbd.processingCount')} value={processingCount} />
                    <MetricCard isError={failedCount > 0} label={t('kbd.failedCount')} value={failedCount} />
                  </Grid>

                  {/* The reference's three actions, in its order. Recall test
                      has no endpoint yet - see docs/ui_backlog.md. */}
                  <HStack gap={2} vAlign="center" wrap="wrap">
                    <Button
                      icon={<Upload size={14} />}
                      isDisabled={!canEdit || uploading || !base?.embedding_model}
                      isLoading={uploading}
                      label={t('kb.importData')}
                      onClick={() => fileRef.current?.click()}
                      size="sm"
                      variant="primary"
                    />
                    {canEdit && documents.length > 0 ? <Button label={t('kb.reindex')} isDisabled={isSettling || reindexing} isLoading={reindexing} onClick={() => void reindex()} size="sm" variant="secondary" /> : null}
                    <Button icon={<FlaskConical size={14} />} isDisabled label={t('kb.recallTest')} size="sm" variant="secondary" />
                    <Button
                      icon={<SlidersHorizontal size={14} />}
                      isDisabled={!canEdit}
                      label={t('kb.settings')}
                      onClick={() => setSettingsOpen(true)}
                      size="sm"
                      variant="secondary"
                    />
                  </HStack>
                </VStack>
              )}
            </VStack>
            </HStack>
          </LayoutContent>
        }
      />

      <AlertDialog
        actionLabel={t('kb.docDelete')}
        cancelLabel={t('common.cancel')}
        description={t('kb.docDeleteBody')}
        isOpen={deleting !== null}
        onAction={() => void remove()}
        onOpenChange={(open) => { if (!open) setDeleting(null); }}
        title={t('kb.docDeleteTitle')}
      />

      {pipelineDocument ? (
        <Dialog isOpen maxHeight="75dvh" position={{end: 'var(--spacing-4)'}} onOpenChange={() => setPipelineDocument(null)} width={440}>
          <Layout
            header={<DialogHeader title={t('kb.log')} subtitle={pipelineDocument.title || pipelineDocument.filename} onOpenChange={() => setPipelineDocument(null)} />}
            content={<LayoutContent><IngestionLog key={pipelineDocument.id} document={documents.find((item) => item.id === pipelineDocument.id) ?? pipelineDocument} kbID={kbID} onSettled={handleSettled} /></LayoutContent>}
          />
        </Dialog>
      ) : null}

      {settingsOpen && base ? (
        <LayoutDialog
          base={base}
          onClose={() => setSettingsOpen(false)}
          onError={setError}
          onSaved={(next) => { setBase(next); setSettingsOpen(false); void loadDocuments(); }}
					workspaceID={workspaceID || base.owner_workspace_id || ''}
        />
      ) : null}
    </>
  );
}

function MetricCard({label, value, isActive = false, isError = false}: {
	label: string;
	value: number;
	isActive?: boolean;
	isError?: boolean;
}) {
	return (
		<Card padding={4} width="100%">
			<VStack gap={2}>
				<HStack gap={2} vAlign="center">
					{isActive ? <StatusLabel isPulsing label={label} variant="accent" /> : null}
					{isError ? <StatusLabel label={label} variant="error" /> : null}
					<Text color="secondary" type="supporting">{label}</Text>
				</HStack>
				<Text type="large" weight="semibold">{value}</Text>
			</VStack>
		</Card>
	);
}

// Layout analysis is billed per page, so which documents are worth it belongs
// to the owner of the corpus rather than to the deployment. It applies to
// documents ingested from here on; what is already indexed is only re-read by
// a re-index.
function LayoutDialog({base, onClose, onError, onSaved, workspaceID}: {
  base: KnowledgeBase;
  onClose: () => void;
  onError: (value: string) => void;
  onSaved: (base: KnowledgeBase) => void;
	workspaceID: string;
}) {
  const t = useTranslation();
	const router = useRouter();
	const [layoutMode, setLayoutMode] = useState(base.layout_mode);
	const [retrievalMode, setRetrievalMode] = useState(base.retrieval_mode);
	const [embeddingModel, setEmbeddingModel] = useState(base.embedding_model);
	const [rerankerModel, setRerankerModel] = useState(base.reranker_model);
	const [rerankEnabled, setRerankEnabled] = useState(base.rerank_enabled);
	const [scoreThreshold, setScoreThreshold] = useState(base.score_threshold);
	const [topK, setTopK] = useState(base.retrieval_top_k);
	const [chunkSize, setChunkSize] = useState(base.chunk_size);
	const [chunkOverlap, setChunkOverlap] = useState(base.chunk_overlap);
	const [gatewayModels, setGatewayModels] = useState<GatewayModel[]>([]);
	const [gatewayConfigured, setGatewayConfigured] = useState(true);
	const [modelMessage, setModelMessage] = useState('');
	const [modelsLoading, setModelsLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [saveError, setSaveError] = useState('');
  const [layoutAvailable, setLayoutAvailable] = useState<boolean | null>(null);
  const [capabilityError, setCapabilityError] = useState('');
  useEffect(() => {
    api.knowledgeCapabilities(base.id).then((result) => setLayoutAvailable(result.layout_available))
      .catch((caught) => setCapabilityError(caught instanceof Error ? caught.message : t('kb.saveFailed')));
  }, [base.id, t]);
  const indexChanged = embeddingModel !== base.embedding_model || chunkSize !== base.chunk_size || chunkOverlap !== base.chunk_overlap || layoutMode !== base.layout_mode;
  const hasDocuments = base.document_count + base.processing_count + base.failed_count > 0;
  const invalid = !embeddingModel || (rerankEnabled && !rerankerModel) || chunkSize < 256 || chunkSize > 4096 || chunkOverlap < 0 || chunkOverlap > Math.min(2048, chunkSize - 1);
  const needsLayout = layoutMode !== 'off';
  const cannotSave = busy || !gatewayConfigured || invalid || base.processing_count > 0 || (layoutMode !== base.layout_mode && needsLayout && layoutAvailable !== true);


	useEffect(() => {
		api.workspaceKnowledgeModels(workspaceID)
			.then((result) => {
				setGatewayConfigured(result.configured);
				setGatewayModels(result.models);
				setModelMessage(result.message ?? '');
			})
			.catch((caught) => setModelMessage(caught instanceof Error ? caught.message : t('kbd.modelsFailed')))
			.finally(() => setModelsLoading(false));
	}, [workspaceID]);

	function modelOptions(kind: 'embedding' | 'rerank', current: string) {
		const matches = gatewayModels.filter((model) => {
			const mode = (model.mode ?? '').toLowerCase();
			return !mode || mode.includes(kind);
		});
		const ids = matches.map((model) => model.id);
		if (current && !ids.includes(current)) ids.unshift(current);
		return ids.map((id) => ({label: id, value: id}));
	}

	const embeddingOptions = modelOptions('embedding', embeddingModel);
	const rerankerOptions = modelOptions('rerank', rerankerModel);

  async function save(reindex = false) {
    setSaveError('');
    setBusy(true);
    try {
			const result = await api.updateKnowledgeBase(base.id, {
				layout_mode: layoutMode,
        reindex,
				retrieval_mode: retrievalMode,
				embedding_model: embeddingModel,
				reranker_model: rerankerModel,
				rerank_enabled: rerankEnabled,
				score_threshold: scoreThreshold,
				retrieval_top_k: topK,
				chunk_size: chunkSize,
				chunk_overlap: chunkOverlap,
			});
      onSaved(result.knowledge_base);
    } catch (caught) {
      setSaveError(caught instanceof Error ? caught.message : t('kb.saveFailed'));
    } finally {
      setBusy(false);
    }
  }

  return (
		<Dialog isOpen maxHeight="90dvh" onOpenChange={onClose} purpose="form" width={760}>
      <Layout
        content={
          <LayoutContent>
						<VStack gap={6}>
                {saveError ? <Banner status="error" title={saveError} /> : null}
                {hasDocuments && (indexChanged || base.needs_reindex) ? <Banner status="warning" title={t('kb.needsReindex')} /> : null}
							<VStack gap={3}>
								<VStack gap={1}>
									<Text type="label">Tìm nội dung</Text>
									<Text color="secondary" type="supporting">Áp dụng cho lượt tìm kiếm tiếp theo sau khi lưu.</Text>
								</VStack>
								<Grid columns={{minWidth: 180, max: 3}} gap={2} width="100%">
									<SelectableCard isSelected={retrievalMode === 'semantic'} label="Semantic Search" onChange={(selected) => { if (selected) setRetrievalMode('semantic'); }}>
										<VStack gap={2}><Search size={20} /><Text weight="semibold">Semantic Search</Text><Text color="secondary" type="supporting">Tìm theo ý nghĩa.</Text></VStack>
									</SelectableCard>
									<SelectableCard isSelected={retrievalMode === 'keyword'} label="Keyword Search" onChange={(selected) => { if (selected) setRetrievalMode('keyword'); }}>
										<VStack gap={2}><FileText size={20} /><Text weight="semibold">Keyword Search</Text><Text color="secondary" type="supporting">Tìm theo từ khóa chính xác.</Text></VStack>
									</SelectableCard>
									<SelectableCard isSelected={retrievalMode === 'hybrid'} label="Smart Search" onChange={(selected) => { if (selected) setRetrievalMode('hybrid'); }}>
										<VStack gap={2}><SlidersHorizontal size={20} /><Text weight="semibold">Smart Search</Text><Text color="secondary" type="supporting">Kết hợp semantic và keyword.</Text></VStack>
									</SelectableCard>
								</Grid>

								{!gatewayConfigured || modelMessage ? (
									<Banner
										status={gatewayConfigured ? 'warning' : 'error'}
										title={modelMessage || t('kbd.gatewayMissing')}
									/>
								) : null}
								{!gatewayConfigured ? <Button label={t('kbd.openWorkspaceSettings')} onClick={() => router.push('/settings?section=model')} variant="secondary" /> : null}

								<Slider
									description="Bỏ qua kết quả vector có độ tương đồng thấp hơn ngưỡng này."
									isDisabled={retrievalMode === 'keyword'}
									label={t('kbd.vectorThreshold')}
									max={1}
									min={0}
									onChange={(value: number) => setScoreThreshold(value)}
									step={0.05}
									value={scoreThreshold}
									valueDisplay="text"
									width="100%"
								/>
								<NumberInput isIntegerOnly label={t('kbd.topK')} max={50} min={1} onChange={setTopK} value={topK} width="100%" />
								<SegmentedControl label={t('kbd.rerankLabel')} layout="fill" onChange={(value) => setRerankEnabled(value === 'rerank')} value={rerankEnabled ? 'rerank' : 'none'}>
									<SegmentedControlItem label={t('kbd.noRerank')} value="none" />
									<SegmentedControlItem label={t('kbd.useRerank')} value="rerank" />
								</SegmentedControl>
								{rerankEnabled ? (
									<Selector
										isDisabled={modelsLoading || rerankerOptions.length === 0}
										label="Reranker model"
										onChange={setRerankerModel}
										options={rerankerOptions}
										placeholder={modelsLoading ? t('kbd.loadingModels') : t('kbd.pickReranker')}
										value={rerankerModel}
										width="100%"
									/>
								) : null}
							</VStack>

							<Section dividers={['top']} padding={0}>
								<VStack gap={3} padding={4}>
									<VStack gap={1}>
										<Text type="label">Tạo chỉ mục</Text>
										<Text color="secondary" type="supporting">Thay đổi cần re-index tài liệu đã có.</Text>
									</VStack>
									<Selector
									isDisabled={modelsLoading || embeddingOptions.length === 0}
									label="Embedding model"
									onChange={setEmbeddingModel}
									options={embeddingOptions}
									placeholder={modelsLoading ? t('kbd.loadingModels') : t('kbd.pickEmbedding')}
									value={embeddingModel}
									width="100%"
								/>
                  <Grid columns={{minWidth: 220, max: 2}} gap={3} width="100%">
										<NumberInput isIntegerOnly label="Chunk size" max={4096} min={256} onChange={setChunkSize} units="tokens" value={chunkSize} width="100%" />
										<NumberInput isIntegerOnly label="Chunk overlap" max={Math.max(0, Math.min(2048, chunkSize - 1))} min={0} onChange={setChunkOverlap} units="tokens" value={chunkOverlap} width="100%" />
									</Grid>
									<Selector
										label={t('kb.layoutMode')}
                    description={capabilityError || (layoutAvailable === false ? t('kb.layoutUnavailable') : layoutAvailable === null ? t('kb.checkingLayout') : undefined)}
										onChange={(value) => setLayoutMode(value as KnowledgeBase['layout_mode'])}
										options={[
											{value: 'auto', label: t('kb.layoutAuto'), disabled: layoutAvailable !== true},
											{value: 'always', label: t('kb.layoutAlways'), disabled: layoutAvailable !== true},
											{value: 'off', label: t('kb.layoutOff')},
										]}
										value={layoutMode}
										width="100%"
									/>
								</VStack>
							</Section>
						</VStack>
          </LayoutContent>
        }
        footer={
          <LayoutFooter>
            <HStack gap={2} hAlign="end">
              <Button label={t('common.cancel')} onClick={onClose} variant="secondary" />
						<Button isDisabled={cannotSave} isLoading={busy} label={t('common.save')} onClick={() => void save()} variant="secondary" />
              {hasDocuments ? <Button isDisabled={cannotSave || (needsLayout && layoutAvailable !== true)} isLoading={busy} label={t('kb.saveReindex')} onClick={() => void save(true)} variant="primary" /> : null}
            </HStack>
          </LayoutFooter>
        }
			header={<DialogHeader onOpenChange={onClose} subtitle={base.name} title="Knowledge settings" />}
      />
    </Dialog>
  );
}

// A document is read, not inspected: the reference gives it a title, a line of
// facts and then the text it managed to parse. What the pipeline did with it
// matters when something goes wrong, so it stays - folded away underneath.
function DocumentReader({
  detail,
  isLoading,
  kbID,
  onDelete,
  onSettled,
  selectedDocument,
}: {
  detail: KnowledgeDocumentDetail | null;
  isLoading: boolean;
  kbID: string;
  onDelete?: () => void;
  onSettled: () => void;
  selectedDocument: KnowledgeDocument;
}) {
  const t = useTranslation();
  const document = detail?.document ?? selectedDocument;
  const inspection = detail?.inspection;
  const status = document.status === 'ready'
    ? t('kb.statusReady')
    : document.status === 'failed' ? t('kb.statusFailed') : t('kb.statusProcessing');

  return (
    <VStack gap={4} width="100%">
      <HStack gap={2} hAlign="between" vAlign="start" width="100%">
        <Text size="xl" type="large">{document.title || document.filename}</Text>
        <HStack gap={1} vAlign="center">
          <IconButton
            icon={<ExternalLink size={16} />}
            label={t('kbd.openOriginal')}
            onClick={() => window.open(api.documentOriginalURL(kbID, document.id), '_blank', 'noopener,noreferrer')}
            size="sm"
            variant="ghost"
          />
          {onDelete ? (
            <IconButton icon={<Trash2 size={16} />} label={t('kb.docDelete')} onClick={onDelete} size="sm" variant="ghost" />
          ) : null}
        </HStack>
      </HStack>

      <HStack gap={2} vAlign="center" wrap="wrap">
        <StatusLabel
          label={status}
          variant={document.status === 'ready' ? 'success' : document.status === 'failed' ? 'error' : 'warning'}
        />
        <Text color="secondary" type="supporting">·</Text>
        <Text color="secondary" type="supporting">{t('kbd.chunkCount', {count: inspection?.total ?? 0})}</Text>
        <Text color="secondary" type="supporting">·</Text>
        <Text color="secondary" type="supporting">{formatSize(document.size_bytes)}</Text>
        <Text color="secondary" type="supporting">·</Text>
        <Text color="secondary" type="supporting">{document.content_type || t('kbd.unknownKind')}</Text>
      </HStack>

      {detail?.index_error ? <Banner status="error" title={detail.index_error} /> : null}
      {document.error ? <Banner status="error" title={document.error} /> : null}

      {isLoading ? (
        <VStack gap={2} width="100%">
          {[0, 1, 2].map((index) => <Skeleton height={72} index={index} key={index} width="100%" />)}
        </VStack>
      ) : inspection?.chunks?.length ? (
        <VStack gap={4} width="100%">
          {inspection.chunks.map((chunk) => (
            <VStack gap={1} key={chunk.chunk_index} width="100%">
              {chunk.section || chunk.page ? (
                <Text color="secondary" type="supporting">
                  {[chunk.section, chunk.page ? `${t('kbd.page')} ${chunk.page}` : ''].filter(Boolean).join(' · ')}
                </Text>
              ) : null}
              <Text>{chunk.text}</Text>
            </VStack>
          ))}
          {inspection.truncated ? (
            <Text color="secondary" type="supporting">{t('kbd.truncated')}</Text>
          ) : null}
        </VStack>
      ) : (
        <EmptyState description={t('kbd.noProcessed')} icon={<FileText size={48} strokeWidth={1} />} isCompact title="—" />
      )}

      <Collapsible trigger={<Text type="label">{t('kbd.pipelineDetail')}</Text>}>
        <VStack gap={3} width="100%">
          <List>
            <Item label={t('kbd.file')} description={document.filename} />
            <Item label={t('kbd.version')} description={`v${document.version}`} />
            <Item label={t('kbd.status')} description={inspection?.indexed ? t('kbd.indexed') : t('kbd.notIndexed')} />
            <Item label={t('kbd.chunksRead')} description={String(inspection?.total ?? 0)} />
          </List>
        </VStack>
      </Collapsible>
    </VStack>
  );
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

/**
 * Every stage recorded for one document, kept current while it is still being
 * processed.
 *
 * The backend replays everything recorded so far and then streams the rest, so
 * opening late still shows the whole story rather than only what happens next.
 */
function useIngestionEvents(kbID: string, document: KnowledgeDocument, onSettled: () => void) {
  const [events, setEvents] = useState<DocumentEvent[]>([]);
  const isLive = document.status === 'processing' || document.status === 'pending';

  useEffect(() => {
    if (!isLive) {
      api.documentEvents(kbID, document.id)
        .then((result) => setEvents(result.events))
        .catch(() => setEvents([]));
      return undefined;
    }

    const source = new EventSource(api.documentStreamURL(kbID, document.id), {withCredentials: true});
    source.addEventListener('stage', (message) => {
      const event = JSON.parse((message as MessageEvent<string>).data) as DocumentEvent;
      setEvents((current) => current.some((item) => item.id === event.id) ? current : [...current, event]);
      // The row still says "processing"; refreshing it is what turns the
      // badge green once the last stage lands.
      if (event.stage === 'done' || event.stage === 'error') onSettled();
    });
    return () => source.close();
  }, [document.id, isLive, kbID, onSettled]);

  return {events, isLive};
}

// The pipeline the service actually runs, in the order it runs it. Reported
// stages fold onto these steps rather than each becoming one: layout analysis
// is a route through reading a document, not a stage after it, and `done` is
// the index write finishing.
const PIPELINE_STEPS = [
  {key: 'received', stages: ['received']},
  {key: 'stored', stages: ['stored']},
  {key: 'parsing', stages: ['parsing', 'layout']},
  {key: 'chunked', stages: ['chunked']},
  {key: 'embedding', stages: ['embedding']},
  {key: 'indexing', stages: ['indexing', 'done']},
] as const;

/**
 * The ingestion pipeline for one document, drawn as the sequence it is.
 *
 * A log answers "what happened" only if you read it. The shape of the work is
 * the thing worth showing: which step it is on, how long each one took, and —
 * when a scan goes to layout analysis — that the minutes of silence are one
 * known step rather than a stall.
 */
function PipelineDialog({document, kbID, onClose, onSettled}: {
  document: KnowledgeDocument;
  kbID: string;
  onClose: () => void;
  onSettled: () => void;
}) {
  const t = useTranslation();
  const {events, isLive} = useIngestionEvents(kbID, document, onSettled);
  const [now, setNow] = useState(() => Date.now());

  // The step in progress shows time elapsed, which only reads as progress if
  // it moves. Nothing else on the page depends on this clock.
  useEffect(() => {
    if (!isLive) return undefined;
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [isLive]);

  const failure = events.find((event) => event.stage === 'error');
  const finished = events.some((event) => event.stage === 'done');
  const first = (stages: readonly string[]) => events.find((event) => stages.includes(event.stage));
  const last = (stages: readonly string[]) => events.filter((event) => stages.includes(event.stage)).at(-1);

  const starts = PIPELINE_STEPS.map((step) => first(step.stages)?.created_at ?? null);
  const terminal = (finished ? last(['done']) : failure)?.created_at ?? null;

  let active = 0;
  starts.forEach((start, index) => { if (start) active = index; });
  if (finished) active = PIPELINE_STEPS.length;

  const embedding = last(['embedding']);
  const progress = embedding && embedding.total > 0 ? Math.round((embedding.done / embedding.total) * 100) : null;

  return (
    <Dialog isOpen maxHeight={720} onOpenChange={onClose} purpose="info" width={680}>
      <Layout
        content={
          <LayoutContent>
            <VStack gap={4}>
              <Stepper activeStep={active} density="compact" label={t('kb.pipeline')} orientation="vertical">
                {PIPELINE_STEPS.map((step, index) => {
                  const start = starts[index];
                  const nextStart = starts.slice(index + 1).find(Boolean) ?? terminal;
                  const isCurrent = index === active && !finished;
                  // A step that has started but not handed over is still
                  // running, so it counts up to now rather than showing nothing.
                  const elapsed = start
                    ? (nextStart ? Date.parse(nextStart) : (isLive ? now : null))
                    : null;
                  return (
                    <Step
                      description={last(step.stages)?.message ?? undefined}
                      endContent={start && elapsed ? (
                        <Text color="secondary" type="supporting">{formatElapsed(elapsed - Date.parse(start))}</Text>
                      ) : undefined}
                      indicator={isCurrent && isLive ? <Spinner size="sm" /> : 'auto'}
                      key={step.key}
                      label={t(`kb.step.${step.key}` as Parameters<typeof t>[0])}
                      status={failure && index === active ? 'error' : undefined}
                      step={index}
                    >
                      {step.key === 'embedding' && progress !== null && isCurrent ? (
                        <ProgressBar isLabelHidden label={t('kb.step.embedding')} value={progress} />
                      ) : null}
                    </Step>
                  );
                })}
              </Stepper>

              {failure ? <Banner status="error" title={failure.message} /> : null}

              <Section dividers={['top']} padding={0}>
                <VStack gap={2}>
                  {events.map((event) => (
                    <HStack gap={3} key={event.id} vAlign="start">
                      <Text color="secondary" type="code">{formatTime(event.created_at)}</Text>
                      <Text type="code" weight="medium">{stageLabel(event.stage, t)}</Text>
                      <Text color="secondary" type="code">{event.message}</Text>
                    </HStack>
                  ))}
                </VStack>
              </Section>
            </VStack>
          </LayoutContent>
        }
        header={<DialogHeader onOpenChange={onClose} subtitle={document.title || document.filename} title={t('kb.pipeline')} />}
      />
    </Dialog>
  );
}

function formatElapsed(milliseconds: number): string {
  const seconds = Math.max(milliseconds, 0) / 1000;
  if (seconds < 60) return `${seconds.toFixed(1)}s`;
  return `${Math.floor(seconds / 60)}m ${String(Math.round(seconds % 60)).padStart(2, '0')}s`;
}

/**
 * The ingestion log for one document.
 *
 * Parsing and embedding a large manual takes minutes during which nothing
 * visible happens, which is indistinguishable from being stuck. The backend
 * replays every stage recorded so far and then streams the rest, so opening
 * this late still shows the whole story.
 */
function IngestionLog({document, kbID, onSettled}: {
  document: KnowledgeDocument;
  kbID: string;
  onSettled: () => void;
}) {
  const t = useTranslation();
  const {events} = useIngestionEvents(kbID, document, onSettled);
  const latest = events[events.length - 1];
  const progress = latest && latest.total > 0 ? Math.round((latest.done / latest.total) * 100) : null;

  return (
    <Section dividers={['top']} padding={4}>
      <VStack gap={2}>
        {progress !== null ? <ProgressBar isLabelHidden label={t('kb.log')} value={progress} /> : null}
        {events.length === 0 ? (
          <Text color="secondary" type="supporting">{t('kb.logEmpty')}</Text>
        ) : (
          [...events].reverse().map((event) => (
            <VStack gap={1} key={event.id} width="100%">
              <HStack gap={3}>
              <Text color="secondary" type="code">{formatTime(event.created_at)}</Text>
              <Text type="code" weight="medium">{stageLabel(event.stage, t)}</Text>
              </HStack>
              <Text className="break-words" color="secondary" type="code">{event.message}</Text>
            </VStack>
          ))
        )}
      </VStack>
    </Section>
  );
}

function formatTime(value: string): string {
  const date = new Date(value);
  return `${String(date.getHours()).padStart(2, '0')}:${String(date.getMinutes()).padStart(2, '0')}:${String(date.getSeconds()).padStart(2, '0')}`;
}

// Stages the service does not name yet fall back to their raw key rather than
// rendering an empty cell.
const STAGE_KEYS = new Set([
  'queued', 'received', 'stored', 'parsing', 'layout', 'chunked', 'embedding', 'indexing', 'done', 'error',
]);

function stageLabel(stage: string, t: ReturnType<typeof useTranslation>): string {
  return STAGE_KEYS.has(stage) ? t(`kb.stage.${stage}` as Parameters<typeof t>[0]) : stage;
}
