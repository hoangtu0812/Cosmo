'use client';

import {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import {useParams, useRouter, useSearchParams} from 'next/navigation';
import {ExternalLink, FileText, FlaskConical, Search, SlidersHorizontal, Trash2, Upload, X} from 'lucide-react';
import {AlertDialog} from '@astryxdesign/core/AlertDialog';
import {Banner} from '@astryxdesign/core/Banner';
import {Button} from '@astryxdesign/core/Button';
import {Card} from '@astryxdesign/core/Card';
import {Dialog, DialogHeader} from '@astryxdesign/core/Dialog';
import {EmptyState} from '@astryxdesign/core/EmptyState';
import {Grid} from '@astryxdesign/core/Grid';
import {IconButton} from '@astryxdesign/core/IconButton';
import {Item} from '@astryxdesign/core/Item';
import {HStack, Layout, LayoutContent, LayoutFooter, LayoutHeader, LayoutPanel, VStack} from '@astryxdesign/core/Layout';
import {List} from '@astryxdesign/core/List';
import {PowerSearch, PowerSearchFilter, usePowerSearchConfig} from '@astryxdesign/core/PowerSearch';
import {Section} from '@astryxdesign/core/Section';
import {Spinner} from '@astryxdesign/core/Spinner';
import {Step, Stepper} from '@astryxdesign/core/Stepper';
import {Selector} from '@astryxdesign/core/Selector';
import {proportional, Table, TableColumn, toSearchFilters, useTableFiltering, useTableFilterState} from '@astryxdesign/core/Table';
import {Text} from '@astryxdesign/core/Text';
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

// Built from the string table, so the filter reads in whatever language the
// rest of the screen does. A function rather than a constant because the
// table is only reachable from inside a component.
function documentSearchFields(t: ReturnType<typeof useTranslation>) {
  return [
    {key: 'name', type: 'string', label: t('kbd.docName')},
    {
      key: 'status', type: 'enum', label: t('kbd.status'), enumValues: [
        {value: 'pending', label: t('kbd.pending')},
        {value: 'processing', label: t('kbd.processing')},
        {value: 'ready', label: t('kbd.ready')},
        {value: 'failed', label: t('kbd.failed')},
      ],
    },
  ] as const;
}

type DocumentRow = KnowledgeDocument & Record<string, unknown> & {name: string};

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
  const [deleting, setDeleting] = useState<KnowledgeDocument | null>(null);
  const [publishing, setPublishing] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [pipelineDocument, setPipelineDocument] = useState<KnowledgeDocument | null>(null);
  const [selectedDocument, setSelectedDocument] = useState<KnowledgeDocument | null>(null);
  const [detail, setDetail] = useState<KnowledgeDocumentDetail | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [searchFilters, setSearchFilters] = useState<ReadonlyArray<PowerSearchFilter>>([]);
  const fileRef = useRef<HTMLInputElement>(null);
  const searchFields = useMemo(() => documentSearchFields(t), [t]);
  const {config: searchConfig, applyFilters} = usePowerSearchConfig(searchFields, 'KnowledgeDocuments');
  const {filters: tableFilters, onFilterChange} = useTableFilterState();

  const canEdit = base?.access === 'owner';

  const statusLabel = useCallback((status: KnowledgeDocument['status']) => {
    if (status === 'ready') return t('kb.statusReady');
    if (status === 'failed') return t('kb.statusFailed');
    return t('kb.statusProcessing');
  }, [t]);
  const isSettling = documents.some((item) => item.status === 'processing' || item.status === 'pending');
	const processingCount = documents.filter((item) => item.status === 'processing' || item.status === 'pending').length;
	const failedCount = documents.filter((item) => item.status === 'failed').length;

  const rows = useMemo<DocumentRow[]>(
    () => documents.map((document) => ({...document, name: document.title || document.filename})),
    [documents],
  );

  const loadDocuments = useCallback(
    () => api.knowledgeDocuments(kbID).then((result) => setDocuments(result.documents)),
    [kbID],
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

  const columns = useMemo<TableColumn<DocumentRow>[]>(
    () => [
      {
        key: 'name', header: t('kbd.docName'), width: proportional(3), filter: 'name',
        renderCell: (document) => (
          <Button label={document.name} onClick={() => void openDocument(document)} size="sm" variant="ghost" />
        ),
      },
      {key: 'size_bytes', header: t('kbd.size'), width: proportional(1), renderCell: (document) => <Text>{formatSize(document.size_bytes)}</Text>},
      {key: 'chunk_count', header: 'Chunks', align: 'end', width: proportional(1), renderCell: (document) => <Text>{document.chunk_count}</Text>},
      {
        key: 'pipeline', header: t('kbd.pipeline'), width: proportional(1),
        renderCell: (document) => (
          <Button label={t('kb.pipelineView')} onClick={() => setPipelineDocument(document)} size="sm" variant="secondary" />
        ),
      },
      {
        key: 'status', header: t('kbd.status'), width: proportional(1), filter: 'status',
        renderCell: (document) => <StatusLabel label={statusLabel(document.status)} variant={statusVariant(document.status)} />,
      },
      {
        // Two controls plus cell padding need more than the 120px floor a bare
        // proportional column gets; without the room they overflow the column
        // and sit right of the header they are aligned to.
        key: 'open', header: t('kbd.source'), align: 'end', width: proportional(1, {minWidth: 160}),
        renderCell: (document) => (
          <HStack gap={1} hAlign="end">
            <Button
              icon={<ExternalLink size={14} />}
              label={t('kbd.open')}
              onClick={() => window.open(api.documentOriginalURL(kbID, document.id), '_blank', 'noopener,noreferrer')}
              size="sm"
              variant="secondary"
            />
            {canEdit ? (
              <IconButton
                icon={<Trash2 size={14} />}
                label={t('kb.docDelete')}
                onClick={() => setDeleting(document)}
                size="sm"
                variant="ghost"
              />
            ) : null}
          </HStack>
        ),
      },
    ],
    [canEdit, kbID, openDocument, statusLabel, t],
  );
  const filterPlugin = useTableFiltering<DocumentRow>({
    filters: tableFilters,
    onFilterChange,
    searchConfig,
    variant: 'popover',
  });
  const filteredDocuments = applyFilters(
    [...searchFilters, ...toSearchFilters(tableFilters, columns, searchConfig)],
    rows,
  );

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

  async function upload(files: File[]) {
    if (files.length === 0) return;
    setUploading(true);
    setError('');
    const failures: string[] = [];
    try {
      // Each request enters the backend queue immediately; submitting them in
      // order avoids a large multi-file selection exhausting browser memory
      // while all documents still process concurrently in the background.
      for (const file of files) {
        try {
          const result = await api.uploadKnowledgeDocument(kbID, file);
          setDocuments((current) => [result.document, ...current]);
          void openDocument(result.document);
        } catch (caught) {
          failures.push(`${file.name}: ${caught instanceof Error ? caught.message : t('kb.uploadFailed')}`);
        }
      }
      if (failures.length > 0) setError(failures.join('\n'));
    } finally {
      setUploading(false);
    }
  }

  // Publishing does not change what chat retrieves — that always reads the
  // latest documents. It is the owner saying the base is ready, which is what
  // lets installers see a new version and decide to take it.
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

  return (
    <>
      <Layout
        end={selectedDocument ? (
          <LayoutPanel hasDivider label={t('kbd.detailPanel')} padding={4} role="complementary" width={420}>
            <DocumentDetailPanel
              detail={detail}
              isLoading={detailLoading}
              kbID={kbID}
              onClose={() => { setSelectedDocument(null); setDetail(null); }}
              onSettled={handleSettled}
              selectedDocument={selectedDocument}
            />
          </LayoutPanel>
        ) : undefined}
        height="fill"
        header={
          <LayoutHeader hasDivider>
            <Toolbar
              endContent={
                canEdit ? (
                  <HStack gap={2} vAlign="center">
                    <StatusLabel
                      label={base && base.version > 0 ? t('kb.published', {version: base.version}) : t('kb.draft')}
                      variant={base && base.version > 0 ? 'neutral' : 'warning'}
                    />
                    <IconButton
                      icon={<SlidersHorizontal size={14} />}
                      label={t('kb.layoutMode')}
                      onClick={() => setSettingsOpen(true)}
                      size="sm"
                      variant="ghost"
                    />
                    <Button
                      icon={<Upload size={14} />}
										isDisabled={uploading || !base?.embedding_model}
                      isLoading={uploading}
                      label={t('kb.uploadMany')}
                      onClick={() => fileRef.current?.click()}
                      size="sm"
                      variant="secondary"
                    />
                    <Button
                      isDisabled={publishing || !documents.some((item) => item.status === 'ready')}
                      isLoading={publishing}
                      label={base && base.version > 0 ? t('kb.republish') : t('kb.publish')}
                      onClick={() => void publish()}
                      size="sm"
                      variant="primary"
                    />
                  </HStack>
                ) : undefined
              }
              label={base?.name ?? ''}
              startContent={<Text type="label">{base?.name ?? ''}</Text>}
            />
          </LayoutHeader>
        }
        content={
          <LayoutContent padding={6}>
            <VStack gap={4}>
              {error && <Banner isDismissable onDismiss={() => setError('')} status="error" title={error} />}
						{base && !base.embedding_model ? (
							<Banner status="warning" title={t('kbd.needEmbedding')} />
						) : null}

						{baseLoading ? (
							<Grid columns={{minWidth: 180, max: 3}} gap={3} width="100%">
								{[0, 1, 2].map((index) => <Skeleton height={88} index={index} key={index} width="100%" />)}
							</Grid>
						) : (
							<VStack gap={4} width="100%">
								{/* The reference opens with what the base is before what is in
								    it: icon, name, description, then the counts in words. */}
								<HStack gap={3} vAlign="center">
									<Text type="display-3">{base?.icon || '📚'}</Text>
									<VStack gap={0}>
										<Text type="large">{base?.name}</Text>
										<Text color="secondary" type="supporting">
											{base?.description || t('kb.noDescription')}
										</Text>
									</VStack>
								</HStack>
								<Grid columns={{minWidth: 180, max: 3}} gap={3} width="100%">
									<MetricCard label={t('kbd.totalDocuments')} value={documents.length} />
									<MetricCard isActive={processingCount > 0} label={t('kbd.processingCount')} value={processingCount} />
									<MetricCard isError={failedCount > 0} label={t('kbd.failedCount')} value={failedCount} />
								</Grid>
								{/* Recall test is the reference's third action. Cosmo measures
								    retrieval from a script, not from here, so the button shows
								    the shape and stays disabled. Tracked in docs/ui_backlog.md. */}
								<HStack gap={2} vAlign="center">
									<Button
										icon={<FlaskConical size={14} />}
										isDisabled
										label={t('kb.recallTest')}
										size="sm"
										variant="secondary"
									/>
								</HStack>
							</VStack>
						)}

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

              {documents.length === 0 ? (
                <EmptyState description={t('kb.noDocuments')} icon={<FileText size={64} strokeWidth={1} />} title={t('kb.documents')} />
              ) : (
                <VStack gap={4}>
                  <PowerSearch
                    config={searchConfig}
                    filters={searchFilters}
                    label={t('kbd.searchDocuments')}
                    onChange={(nextFilters) => setSearchFilters(nextFilters)}
                    placeholder={t('kbd.searchHint')}
                    resultCount={filteredDocuments.length}
                    size="sm"
                  />
                  <Table
                    columns={columns}
                    data={filteredDocuments}
                    density="compact"
                    dividers="rows"
                    hasHover
                    plugins={{filter: filterPlugin}}
                    textOverflow="truncate"
                  />
                </VStack>
              )}
            </VStack>
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
        <PipelineDialog
          document={pipelineDocument}
          kbID={kbID}
          onClose={() => setPipelineDocument(null)}
          onSettled={handleSettled}
        />
      ) : null}

      {settingsOpen && base ? (
        <LayoutDialog
          base={base}
          onClose={() => setSettingsOpen(false)}
          onError={setError}
          onSaved={(next) => { setBase(next); setSettingsOpen(false); }}
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

  async function save() {
    setBusy(true);
    try {
			const result = await api.updateKnowledgeBase(base.id, {
				layout_mode: layoutMode,
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
      onError(caught instanceof Error ? caught.message : t('kb.saveFailed'));
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
							<VStack gap={3}>
								<VStack gap={1}>
									<Text type="label">Tìm nội dung</Text>
									<Text color="secondary" type="supporting">Cách Cosmo tìm đoạn văn phù hợp để trả lời câu hỏi.</Text>
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

								<Selector
									isDisabled={modelsLoading || embeddingOptions.length === 0}
									label="Embedding model"
									onChange={setEmbeddingModel}
									options={embeddingOptions}
									placeholder={modelsLoading ? t('kbd.loadingModels') : t('kbd.pickEmbedding')}
									value={embeddingModel}
									width="100%"
								/>
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
										<Text type="label">Tách tài liệu mới</Text>
										<Text color="secondary" type="supporting">Chỉ áp dụng cho tài liệu import sau khi lưu.</Text>
									</VStack>
									<Grid columns={{minWidth: 220, max: 2}} gap={3} width="100%">
										<NumberInput isIntegerOnly label="Chunk size" max={4096} min={256} onChange={setChunkSize} units="tokens" value={chunkSize} width="100%" />
										<NumberInput isIntegerOnly label="Chunk overlap" max={Math.max(0, chunkSize - 1)} min={0} onChange={setChunkOverlap} units="tokens" value={chunkOverlap} width="100%" />
									</Grid>
									<Selector
										label={t('kb.layoutMode')}
										onChange={(value) => setLayoutMode(value as KnowledgeBase['layout_mode'])}
										options={[
											{value: 'auto', label: t('kb.layoutAuto')},
											{value: 'always', label: t('kb.layoutAlways')},
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
						<Button isDisabled={busy || !gatewayConfigured || !embeddingModel || (rerankEnabled && !rerankerModel)} isLoading={busy} label={t('common.save')} onClick={() => void save()} variant="primary" />
            </HStack>
          </LayoutFooter>
        }
			header={<DialogHeader onOpenChange={onClose} subtitle={base.name} title="Knowledge settings" />}
      />
    </Dialog>
  );
}

function DocumentDetailPanel({
  detail,
  isLoading,
  kbID,
  onClose,
  onSettled,
  selectedDocument,
}: {
  detail: KnowledgeDocumentDetail | null;
  isLoading: boolean;
  kbID: string;
  onClose: () => void;
  onSettled: () => void;
  selectedDocument: KnowledgeDocument;
}) {
  const t = useTranslation();
  const document = detail?.document ?? selectedDocument;
  const inspection = detail?.inspection;
  return (
    <VStack gap={4}>
      <HStack hAlign="between" vAlign="center">
        <Text type="large">{document.title || document.filename}</Text>
        <IconButton icon={<X size={16} />} label={t('kbd.closeDetail')} onClick={onClose} size="sm" variant="ghost" />
      </HStack>
      <Button
        icon={<ExternalLink size={14} />}
        label={t('kbd.openOriginal')}
        onClick={() => window.open(api.documentOriginalURL(kbID, document.id), '_blank', 'noopener,noreferrer')}
        variant="secondary"
      />
      {isLoading ? <Text color="secondary">{t('kbd.loadingDetail')}</Text> : null}
      <Section dividers={['top', 'bottom']} padding={3}>
        <VStack gap={2}>
          <Text type="label">Metadata</Text>
          <List>
            <Item label={t('kbd.file')} description={document.filename} />
            <Item label={t('kbd.kind')} description={document.content_type || t('kbd.unknownKind')} />
            <Item label={t('kbd.size')} description={formatSize(document.size_bytes)} />
            <Item label={t('kbd.version')} description={`v${document.version}`} />
          </List>
        </VStack>
      </Section>
      <Section dividers={['bottom']} padding={3}>
        <VStack gap={2}>
          <Text type="label">Qdrant</Text>
          <List>
            <Item label={t('kbd.status')} description={inspection?.indexed ? t('kbd.indexed') : t('kbd.notIndexed')} />
            <Item label={t('kbd.chunksRead')} description={String(inspection?.total ?? 0)} />
          </List>
          {detail?.index_error ? <Banner status="error" title={detail.index_error} /> : null}
        </VStack>
      </Section>
      <Section dividers={['bottom']} padding={3}>
        <VStack gap={2}>
          <Text type="label">{t('kbd.processed')}</Text>
          {inspection?.chunks.length ? (
            <List>
              {inspection.chunks.map((chunk) => (
                <Item
                  description={chunk.text}
                  key={chunk.chunk_index}
                  label={`Chunk ${chunk.chunk_index + 1}${chunk.section ? ` · ${chunk.section}` : ''}${chunk.page ? ` · ${t('kbd.page')} ${chunk.page}` : ''}`}
                />
              ))}
            </List>
          ) : <Text color="secondary" type="supporting">{t('kbd.noProcessed')}</Text>}
          {inspection?.truncated ? <Text color="secondary" type="supporting">{t('kbd.truncated')}</Text> : null}
        </VStack>
      </Section>
      <IngestionLog document={document} kbID={kbID} onSettled={onSettled} />
    </VStack>
  );
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function statusVariant(status: KnowledgeDocument['status']): 'success' | 'error' | 'neutral' {
  if (status === 'ready') return 'success';
  if (status === 'failed') return 'error';
  return 'neutral';
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
          events.map((event) => (
            <HStack gap={3} key={event.id} vAlign="start">
              <Text color="secondary" type="code">{formatTime(event.created_at)}</Text>
              <Text type="code" weight="medium">{stageLabel(event.stage, t)}</Text>
              <Text color="secondary" type="code">{event.message}</Text>
            </HStack>
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
