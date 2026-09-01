'use client';

import {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import {useParams, useRouter, useSearchParams} from 'next/navigation';
import {ExternalLink, FileText, SlidersHorizontal, Trash2, Upload, X} from 'lucide-react';
import {AlertDialog} from '@astryxdesign/core/AlertDialog';
import {Badge} from '@astryxdesign/core/Badge';
import {Banner} from '@astryxdesign/core/Banner';
import {Button} from '@astryxdesign/core/Button';
import {Dialog, DialogHeader} from '@astryxdesign/core/Dialog';
import {EmptyState} from '@astryxdesign/core/EmptyState';
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
import {api, APIError, DocumentEvent, KnowledgeBase, KnowledgeDocument, KnowledgeDocumentDetail} from '../../lib/api';
import {useTranslation} from '../../lib/i18n';

// Ingestion is asynchronous, so a document that is still being parsed is
// re-checked until it settles. The poll stops as soon as nothing is in flight,
// rather than running for as long as the page is open.
const POLL_INTERVAL = 4000;

const documentSearchFields = [
  {key: 'name', type: 'string', label: 'Tên tài liệu'},
  {
    key: 'status', type: 'enum', label: 'Trạng thái', enumValues: [
      {value: 'pending', label: 'Đang chờ'},
      {value: 'processing', label: 'Đang xử lý'},
      {value: 'ready', label: 'Sẵn sàng'},
      {value: 'failed', label: 'Lỗi'},
    ],
  },
] as const;

type DocumentRow = KnowledgeDocument & Record<string, unknown> & {name: string};

export default function KnowledgeDetailPage() {
  const t = useTranslation();
  const router = useRouter();
  const search = useSearchParams();
  const params = useParams<{kbID: string}>();
  const kbID = params.kbID;
  const workspaceID = search.get('workspace') ?? '';

  const [base, setBase] = useState<KnowledgeBase | null>(null);
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
  const {config: searchConfig, applyFilters} = usePowerSearchConfig(documentSearchFields, 'KnowledgeDocuments');
  const {filters: tableFilters, onFilterChange} = useTableFilterState();

  const canEdit = base?.access === 'owner';

  const statusLabel = (status: KnowledgeDocument['status']) => {
    if (status === 'ready') return t('kb.statusReady');
    if (status === 'failed') return t('kb.statusFailed');
    return t('kb.statusProcessing');
  };
  const isSettling = documents.some((item) => item.status === 'processing' || item.status === 'pending');

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
        key: 'name', header: 'Tên tài liệu', width: proportional(3), filter: 'name',
        renderCell: (document) => (
          <Button label={document.name} onClick={() => void openDocument(document)} size="sm" variant="ghost" />
        ),
      },
      {key: 'size_bytes', header: 'Dung lượng', width: proportional(1), renderCell: (document) => <Text>{formatSize(document.size_bytes)}</Text>},
      {key: 'chunk_count', header: 'Chunks', align: 'end', width: proportional(1), renderCell: (document) => <Text>{document.chunk_count}</Text>},
      {
        key: 'pipeline', header: 'Tiến trình', width: proportional(1),
        renderCell: (document) => (
          <Button label={t('kb.pipelineView')} onClick={() => setPipelineDocument(document)} size="sm" variant="secondary" />
        ),
      },
      {
        key: 'status', header: 'Trạng thái', width: proportional(1), filter: 'status',
        renderCell: (document) => <Badge label={statusLabel(document.status)} variant={statusVariant(document.status)} />,
      },
      {
        // Two controls plus cell padding need more than the 120px floor a bare
        // proportional column gets; without the room they overflow the column
        // and sit right of the header they are aligned to.
        key: 'open', header: 'Bản gốc', align: 'end', width: proportional(1, {minWidth: 160}),
        renderCell: (document) => (
          <HStack gap={1} hAlign="end">
            <Button
              icon={<ExternalLink size={14} />}
              label="Mở"
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
    [canEdit, kbID, openDocument, t],
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
      });
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
          <LayoutPanel hasDivider label="Chi tiết tài liệu" padding={4} role="complementary" width={420}>
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
                    <Badge
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
                      isDisabled={uploading}
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
              startContent={<Text type="label" weight="semibold">{base?.name ?? ''}</Text>}
            />
          </LayoutHeader>
        }
        content={
          <LayoutContent padding={6}>
            <VStack gap={4}>
              {error && <Banner isDismissable onDismiss={() => setError('')} status="error" title={error} />}

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
                    label="Tìm và lọc tài liệu"
                    onChange={(nextFilters) => setSearchFilters(nextFilters)}
                    placeholder="Tìm tài liệu hoặc lọc trạng thái"
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
        />
      ) : null}
    </>
  );
}

// Layout analysis is billed per page, so which documents are worth it belongs
// to the owner of the corpus rather than to the deployment. It applies to
// documents ingested from here on; what is already indexed is only re-read by
// a re-index.
function LayoutDialog({base, onClose, onError, onSaved}: {
  base: KnowledgeBase;
  onClose: () => void;
  onError: (value: string) => void;
  onSaved: (base: KnowledgeBase) => void;
}) {
  const t = useTranslation();
  const [mode, setMode] = useState(base.layout_mode);
  const [busy, setBusy] = useState(false);

  async function save() {
    setBusy(true);
    try {
      const result = await api.updateKnowledgeBase(base.id, {layout_mode: mode});
      onSaved(result.knowledge_base);
    } catch (caught) {
      onError(caught instanceof Error ? caught.message : t('kb.saveFailed'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog isOpen onOpenChange={onClose} purpose="form" width={480}>
      <Layout
        content={
          <LayoutContent>
            <Selector
              label={t('kb.layoutMode')}
              onChange={(value) => setMode(value as KnowledgeBase['layout_mode'])}
              options={[
                {value: 'auto', label: t('kb.layoutAuto')},
                {value: 'always', label: t('kb.layoutAlways')},
                {value: 'off', label: t('kb.layoutOff')},
              ]}
              value={mode}
              width="100%"
            />
          </LayoutContent>
        }
        footer={
          <LayoutFooter>
            <HStack gap={2} hAlign="end">
              <Button label={t('common.cancel')} onClick={onClose} variant="secondary" />
              <Button isDisabled={busy} isLoading={busy} label={t('common.save')} onClick={() => void save()} variant="primary" />
            </HStack>
          </LayoutFooter>
        }
        header={<DialogHeader onOpenChange={onClose} subtitle={base.name} title={t('kb.layoutMode')} />}
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
  const document = detail?.document ?? selectedDocument;
  const inspection = detail?.inspection;
  return (
    <VStack gap={4}>
      <HStack hAlign="between" vAlign="center">
        <Text type="large" weight="semibold">{document.title || document.filename}</Text>
        <IconButton icon={<X size={16} />} label="Đóng chi tiết" onClick={onClose} size="sm" variant="ghost" />
      </HStack>
      <Button
        icon={<ExternalLink size={14} />}
        label="Mở tài liệu gốc"
        onClick={() => window.open(api.documentOriginalURL(kbID, document.id), '_blank', 'noopener,noreferrer')}
        variant="secondary"
      />
      {isLoading ? <Text color="secondary">Đang tải chi tiết…</Text> : null}
      <Section dividers={['top', 'bottom']} padding={3}>
        <VStack gap={2}>
          <Text type="label" weight="semibold">Metadata</Text>
          <List>
            <Item label="Tệp" description={document.filename} />
            <Item label="Loại" description={document.content_type || 'Không xác định'} />
            <Item label="Dung lượng" description={formatSize(document.size_bytes)} />
            <Item label="Phiên bản" description={`v${document.version}`} />
          </List>
        </VStack>
      </Section>
      <Section dividers={['bottom']} padding={3}>
        <VStack gap={2}>
          <Text type="label" weight="semibold">Qdrant</Text>
          <List>
            <Item label="Trạng thái" description={inspection?.indexed ? 'Đã lập chỉ mục' : 'Chưa có dữ liệu chỉ mục'} />
            <Item label="Chunks đã đọc" description={String(inspection?.total ?? 0)} />
          </List>
          {detail?.index_error ? <Banner status="error" title={detail.index_error} /> : null}
        </VStack>
      </Section>
      <Section dividers={['bottom']} padding={3}>
        <VStack gap={2}>
          <Text type="label" weight="semibold">Dữ liệu đã xử lý</Text>
          {inspection?.chunks.length ? (
            <List>
              {inspection.chunks.map((chunk) => (
                <Item
                  description={chunk.text}
                  key={chunk.chunk_index}
                  label={`Chunk ${chunk.chunk_index + 1}${chunk.section ? ` · ${chunk.section}` : ''}${chunk.page ? ` · Trang ${chunk.page}` : ''}`}
                />
              ))}
            </List>
          ) : <Text color="secondary" type="supporting">Chưa có dữ liệu xử lý.</Text>}
          {inspection?.truncated ? <Text color="secondary" type="supporting">Chỉ hiển thị 25 chunks đầu tiên.</Text> : null}
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
