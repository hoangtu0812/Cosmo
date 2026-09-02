'use client';

import {useEffect, useMemo, useState} from 'react';
import {Check, Download, Search} from 'lucide-react';
import {Banner} from '@astryxdesign/core/Banner';
import {Button} from '@astryxdesign/core/Button';
import {Card} from '@astryxdesign/core/Card';
import {Dialog, DialogHeader} from '@astryxdesign/core/Dialog';
import {EmptyState} from '@astryxdesign/core/EmptyState';
import {Grid} from '@astryxdesign/core/Grid';
import {Icon} from '@astryxdesign/core/Icon';
import {HStack, Layout, LayoutContent, VStack} from '@astryxdesign/core/Layout';
import {SelectableCard} from '@astryxdesign/core/SelectableCard';
import {Text} from '@astryxdesign/core/Text';
import {TextInput} from '@astryxdesign/core/TextInput';
import {Token} from '@astryxdesign/core/Token';
import {api, ToolCatalogEntry} from '../lib/api';
import {cardHover} from '../components/motion';
import {useTranslation} from '../lib/i18n';

// The market is a place rather than a prompt: the reference gives it the whole
// window, a column of categories down one side and the toolkits down the
// other. A short list in a small dialog reads as an afterthought, which is the
// wrong thing to say about the fastest way to get a working tool.
//
// The one thing not carried over is install counts. The reference can show
// them because it counts across many deployments; here they would be invented.
export function ToolMarket({isOpen, onOpen, onOpenChange, workspaceID}: {
  isOpen: boolean;
  onOpenChange: (open: boolean) => void;
  workspaceID: string;
  onOpen: (toolID: string) => void;
}) {
  const t = useTranslation();
  const [entries, setEntries] = useState<ToolCatalogEntry[]>([]);
  const [categories, setCategories] = useState<string[]>([]);
  const [category, setCategory] = useState('');
  const [query, setQuery] = useState('');
  const [installing, setInstalling] = useState('');
  // Entry id -> the tool it produced, so an entry already in the workspace
  // offers the way to it rather than a second copy of it.
  const [installed, setInstalled] = useState<Record<string, string>>({});
  const [error, setError] = useState('');

  useEffect(() => {
    if (!isOpen) return;
    let cancelled = false;
    api.toolCatalog(workspaceID)
      .then((result) => {
        if (cancelled) return;
        setEntries(result.entries);
        setCategories(result.categories);
        setInstalled(result.installed ?? {});
      })
      .catch(() => undefined);
    return () => { cancelled = true; };
  }, [isOpen, workspaceID]);

  const needle = query.trim().toLowerCase();
  const matching = useMemo(
    () => entries.filter((entry) => !needle
      || entry.name.toLowerCase().includes(needle)
      || entry.description.toLowerCase().includes(needle)
      || entry.actions.some((action) => action.name.toLowerCase().includes(needle))),
    [entries, needle],
  );

  // Searching looks across everything: a category chosen earlier should not
  // quietly hide the thing being searched for.
  const shownCategories = needle
    ? categories.filter((name) => matching.some((entry) => entry.category === name))
    : category
      ? [category]
      : categories;

  async function install(entry: ToolCatalogEntry) {
    setInstalling(entry.id);
    setError('');
    try {
      const result = await api.installCatalogTool(entry.id, workspaceID);
      setInstalled((current) => ({...current, [entry.id]: result.tool.id}));
      onOpen(result.tool.id);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : t('tool.createFailed'));
    } finally {
      setInstalling('');
    }
  }

  return (
    <Dialog isOpen={isOpen} onOpenChange={onOpenChange} purpose="info" width={1180}>
      <Layout
        content={
          <LayoutContent padding={0}>
            <HStack gap={0} height={620} width="100%">
              {/* The categories are navigation, so they get a column rather
                  than a row of chips: there are more of them than fit. */}
              <VStack gap={1} isScrollable padding={3} width={240}>
                <SelectableCard
                  isSelected={category === ''}
                  label={t('tool.allCategories')}
                  onChange={() => setCategory('')}
                  padding={2}
                  width="100%"
                >
                  <HStack gap={2} hAlign="between" vAlign="center" width="100%">
                    <Text type="label">{t('tool.allCategories')}</Text>
                    <Text color="secondary" type="supporting">{entries.length}</Text>
                  </HStack>
                </SelectableCard>

                {categories.map((name) => {
                  const count = entries.filter((entry) => entry.category === name).length;
                  if (count === 0) return null;
                  return (
                    <SelectableCard
                      isSelected={category === name}
                      key={name}
                      label={name}
                      onChange={() => setCategory(name)}
                      padding={2}
                      width="100%"
                    >
                      <HStack gap={2} hAlign="between" vAlign="center" width="100%">
                        <Text maxLines={1}>{name}</Text>
                        <Text color="secondary" type="supporting">{count}</Text>
                      </HStack>
                    </SelectableCard>
                  );
                })}
              </VStack>

              <VStack gap={4} isScrollable height="100%" padding={4} width="100%">
                <TextInput
                  isLabelHidden
                  label={t('tool.searchCatalog')}
                  onChange={setQuery}
                  placeholder={t('tool.searchCatalog')}
                  size="lg"
                  startIcon={<Icon icon={Search} size="sm" />}
                  value={query}
                  width="100%"
                />

                {error ? <Banner isDismissable onDismiss={() => setError('')} status="error" title={error} /> : null}

                {shownCategories.length === 0 ? (
                  <EmptyState description={t('tool.catalogNoMatch')} isCompact title="—" />
                ) : shownCategories.map((name) => {
                  const inCategory = matching.filter((entry) => entry.category === name);
                  if (inCategory.length === 0) return null;
                  return (
                    <VStack gap={3} key={name} width="100%">
                      <HStack gap={2} vAlign="center">
                        <Text type="label">{name}</Text>
                        <Text color="secondary" type="supporting">{inCategory.length}</Text>
                      </HStack>
                      <Grid columns={{minWidth: 240, max: 3}} gap={3} width="100%">
                        {inCategory.map((entry) => (
                          <Card className={cardHover} key={entry.id} padding={4} width="100%">
                            <VStack gap={3} height="100%" width="100%">
                              <HStack gap={3} vAlign="start">
                                <Card padding={2}>
                                  <Text type="large">{entry.icon}</Text>
                                </Card>
                                <VStack gap={0}>
                                  <Text maxLines={1} type="label">{entry.name}</Text>
                                  {/* A built-in needs no network, which is the
                                      most useful thing to know about it. */}
                                  {entry.kind === 'builtin' ? (
                                    <Token label={t('tool.noNetwork')} size="sm" />
                                  ) : null}
                                </VStack>
                              </HStack>
                              <Text color="secondary" maxLines={3} type="supporting">
                                {entry.description}
                              </Text>
                              <HStack gap={2} hAlign="between" vAlign="center" width="100%">
                                <Text color="secondary" type="supporting">
                                  {t('tool.actionCount', {count: entry.actions.length})}
                                </Text>
                                {installed[entry.id] ? (
                                  <Button
                                    icon={<Check size={14} />}
                                    label={t('tool.installed')}
                                    onClick={() => onOpen(installed[entry.id])}
                                    size="sm"
                                    variant="ghost"
                                  />
                                ) : (
                                  <Button
                                    icon={<Download size={14} />}
                                    isDisabled={installing !== ''}
                                    isLoading={installing === entry.id}
                                    label={t('tool.install')}
                                    onClick={() => void install(entry)}
                                    size="sm"
                                    variant="secondary"
                                  />
                                )}
                              </HStack>
                            </VStack>
                          </Card>
                        ))}
                      </Grid>
                    </VStack>
                  );
                })}
              </VStack>
            </HStack>
          </LayoutContent>
        }
        header={
          <DialogHeader
            onOpenChange={onOpenChange}
            title={`${t('tool.marketplace')} · ${entries.length}`}
          />
        }
      />
    </Dialog>
  );
}
