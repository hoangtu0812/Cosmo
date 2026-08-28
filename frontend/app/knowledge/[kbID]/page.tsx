'use client';

import {useCallback, useEffect, useRef, useState} from 'react';
import {useParams, useRouter} from 'next/navigation';
import {ArrowLeft, FileText, Library, Trash2, Upload} from 'lucide-react';
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
import {SideNav, SideNavHeading, SideNavItem, SideNavSection} from '@astryxdesign/core/SideNav';
import {Text} from '@astryxdesign/core/Text';
import {Toolbar} from '@astryxdesign/core/Toolbar';
import {api, APIError, KnowledgeBase, KnowledgeDocument} from '../../lib/api';
import {useTranslation} from '../../lib/i18n';

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
  const [documents, setDocuments] = useState<KnowledgeDocument[]>([]);
  const [error, setError] = useState('');
  const [uploading, setUploading] = useState(false);
  const [deleting, setDeleting] = useState<KnowledgeDocument | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);

  const canEdit = base?.access === 'owner' || base?.access === 'editor';

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

  useEffect(() => {
    api.knowledgeBases()
      .then((result) => {
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
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : t('kb.uploadFailed'));
    } finally {
      setUploading(false);
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
                  <Button
                    icon={<Upload size={14} />}
                    isDisabled={uploading}
                    isLoading={uploading}
                    label={t('kb.upload')}
                    onClick={() => fileRef.current?.click()}
                    size="sm"
                    variant="primary"
                  />
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
                      <Item
                        as="li"
                        description={describe(document)}
                        endContent={
                          <HStack gap={2} vAlign="center">
                            <Badge label={statusLabel(document.status)} variant={statusVariant(document.status)} />
                            {canEdit && (
                              <IconButton
                                icon={<Trash2 size={14} />}
                                label={t('kb.docDelete')}
                                onClick={() => setDeleting(document)}
                                size="sm"
                                variant="ghost"
                              />
                            )}
                          </HStack>
                        }
                        key={document.id}
                        label={document.title}
                        startContent={<Icon icon={FileText} size="sm" />}
                      />
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
