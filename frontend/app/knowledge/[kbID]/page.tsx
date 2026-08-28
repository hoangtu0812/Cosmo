'use client';

import {useCallback, useEffect, useRef, useState} from 'react';
import {useParams, useRouter} from 'next/navigation';
import {ArrowLeft, ChevronDown, ChevronRight, FileText, Library, Trash2, Upload} from 'lucide-react';
import {AlertDialog} from '@astryxdesign/core/AlertDialog';
import {AppShell} from '@astryxdesign/core/AppShell';
import {Badge} from '@astryxdesign/core/Badge';
import {Banner} from '@astryxdesign/core/Banner';
import {Button} from '@astryxdesign/core/Button';
import {Card} from '@astryxdesign/core/Card';
import {EmptyState} from '@astryxdesign/core/EmptyState';
import {Icon} from '@astryxdesign/core/Icon';
import {IconButton} from '@astryxdesign/core/IconButton';
import {Item} from '@astryxdesign/core/Item';
import {HStack, Layout, LayoutContent, LayoutHeader, VStack} from '@astryxdesign/core/Layout';
import {List} from '@astryxdesign/core/List';
import {Section} from '@astryxdesign/core/Section';
import {SideNav, SideNavHeading, SideNavItem, SideNavSection} from '@astryxdesign/core/SideNav';
import {Text} from '@astryxdesign/core/Text';
import {Toolbar} from '@astryxdesign/core/Toolbar';
import {ProgressBar} from '@astryxdesign/core/ProgressBar';
import {api, APIError, DocumentEvent, KnowledgeBase, KnowledgeDocument, User} from '../../lib/api';
import {useTranslation} from '../../lib/i18n';
import {UserProfileCard} from '../../components/UserProfileCard';

// Ingestion is asynchronous, so a document that is still being parsed is
// re-checked until it settles. The poll stops as soon as nothing is in flight,
// rather than running for as long as the page is open.
const POLL_INTERVAL = 4000;

export default function KnowledgeDetailPage() {
  const t = useTranslation();
  const router = useRouter();
  const params = useParams<{kbID: string}>();
  const kbID = params.kbID;

  const [base, setBase] = useState<KnowledgeBase | null>(null);
  const [user, setUser] = useState<User | null>(null);
  const [documents, setDocuments] = useState<KnowledgeDocument[]>([]);
  const [error, setError] = useState('');
  const [uploading, setUploading] = useState(false);
  const [deleting, setDeleting] = useState<KnowledgeDocument | null>(null);
  const [openLog, setOpenLog] = useState('');
  const [publishing, setPublishing] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);

  const canEdit = base?.access === 'owner';

  // Both read the translator, so they live here rather than as free functions
  // that would have to restate its key type.
  const describe = (document: KnowledgeDocument) => {
    const parts = [document.filename, formatSize(document.size_bytes)];
    if (document.status === 'ready') parts.push(t('kb.chunks', {count: document.chunk_count}));
    if (document.status === 'failed' && document.error) parts.push(document.error);
    return parts.join(' · ');
  };

  const statusLabel = (status: KnowledgeDocument['status']) => {
    if (status === 'ready') return t('kb.statusReady');
    if (status === 'failed') return t('kb.statusFailed');
    return t('kb.statusProcessing');
  };
  const isSettling = documents.some((item) => item.status === 'processing' || item.status === 'pending');

  const loadDocuments = useCallback(
    () => api.knowledgeDocuments(kbID).then((result) => setDocuments(result.documents)),
    [kbID],
  );

  // Stable across renders: the log subscribes on this callback, and a fresh
  // function each render would tear the stream down and reopen it every time.
  const handleSettled = useCallback(() => { void loadDocuments(); }, [loadDocuments]);

  useEffect(() => {
    Promise.all([api.me(), api.knowledgeBases()])
      .then(([me, result]) => {
        setUser(me.user);
        const found = result.knowledge_bases.find((item) => item.id === kbID);
        if (!found) router.replace('/knowledge');
        else setBase(found);
      })
      .catch((caught) => {
        if (caught instanceof APIError && caught.status === 401) router.replace('/');
      });
  }, [kbID, router]);

  useEffect(() => {
    loadDocuments().catch(() => setError(t('kb.docsFailed')));
  }, [loadDocuments, t]);

  useEffect(() => {
    if (!isSettling) return undefined;
    const timer = setInterval(() => { void loadDocuments().catch(() => undefined); }, POLL_INTERVAL);
    return () => clearInterval(timer);
  }, [isSettling, loadDocuments]);

  async function upload(file: File) {
    setUploading(true);
    setError('');
    try {
      const result = await api.uploadKnowledgeDocument(kbID, file);
      setDocuments((current) => [result.document, ...current]);
      // Open the log straight away: the upload is the moment the person most
      // wants to see that something is happening.
      setOpenLog(result.document.id);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : t('kb.uploadFailed'));
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
    <AppShell
      contentPadding={0}
      sideNav={
        <SideNav
          footer={user ? <UserProfileCard user={user} /> : undefined}
          header={<SideNavHeading heading={base?.name ?? ''} icon={<Icon icon={Library} size="sm" />} subheading={base?.description} />}
        >
          <SideNavSection isHeaderHidden title={t('kb.title')}>
            <SideNavItem
              icon={<Icon icon={ArrowLeft} size="sm" />}
              label={t('kb.title')}
              onClick={() => router.push('/knowledge')}
            />
          </SideNavSection>
        </SideNav>
      }
    >
      <Layout
        contentWidth={880}
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
                    <Button
                      icon={<Upload size={14} />}
                      isDisabled={uploading}
                      isLoading={uploading}
                      label={t('kb.upload')}
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
                onChange={(event) => {
                  const file = event.target.files?.[0];
                  if (file) void upload(file);
                  event.target.value = '';
                }}
                ref={fileRef}
                type="file"
              />

              {documents.length === 0 ? (
                <EmptyState description={t('kb.noDocuments')} icon={<FileText size={64} strokeWidth={1} />} title={t('kb.documents')} />
              ) : (
                <Card padding={0} width="100%">
                  <List>
                    {documents.map((document) => (
                      <VStack as="li" gap={0} key={document.id}>
                        <Item
                          description={describe(document)}
                          endContent={
                            <HStack gap={2} vAlign="center">
                              <Badge label={statusLabel(document.status)} variant={statusVariant(document.status)} />
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
                          }
                          label={document.title}
                          onClick={() => setOpenLog((current) => current === document.id ? '' : document.id)}
                          startContent={<Icon icon={openLog === document.id ? ChevronDown : ChevronRight} size="sm" />}
                        />
                        {openLog === document.id ? (
                          <IngestionLog document={document} kbID={kbID} onSettled={handleSettled} />
                        ) : null}
                      </VStack>
                    ))}
                  </List>
                </Card>
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
    </AppShell>
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
  'queued', 'received', 'stored', 'parsing', 'chunked', 'embedding', 'indexing', 'done', 'error',
]);

function stageLabel(stage: string, t: ReturnType<typeof useTranslation>): string {
  return STAGE_KEYS.has(stage) ? t(`kb.stage.${stage}` as Parameters<typeof t>[0]) : stage;
}
