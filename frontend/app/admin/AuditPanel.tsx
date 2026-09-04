'use client';

import {useCallback, useEffect, useState} from 'react';
import {Download, RotateCcw} from 'lucide-react';
import {Button} from '@astryxdesign/core/Button';
import {Card} from '@astryxdesign/core/Card';
import {CodeBlock} from '@astryxdesign/core/CodeBlock';
import {Collapsible} from '@astryxdesign/core/Collapsible';
import {EmptyState} from '@astryxdesign/core/EmptyState';
import {Grid} from '@astryxdesign/core/Grid';
import {Heading} from '@astryxdesign/core/Heading';
import {HStack, VStack} from '@astryxdesign/core/Layout';
import {MetadataList} from '@astryxdesign/core/MetadataList';
import {Selector} from '@astryxdesign/core/Selector';
import {Text} from '@astryxdesign/core/Text';
import {TextInput} from '@astryxdesign/core/TextInput';
import {Timestamp} from '@astryxdesign/core/Timestamp';
import {Token} from '@astryxdesign/core/Token';
import {StatusLabel} from '../components/StatusLabel';
import {AuditEvent, AuditFilterSet, AuditQuery, api} from '../lib/api';
import {useTranslation} from '../lib/i18n';

/**
 * The record of who changed what.
 *
 * Three things make it usable rather than merely present: it filters, it pages
 * by cursor rather than by offset - so a page stays still while new events
 * arrive above it - and it exports, because a compliance review is read in a
 * spreadsheet and not in a browser.
 *
 * The filter options come from the log itself, so an action added in the API
 * appears in the picker the first time it happens and nothing here has to be
 * kept in step with the server by hand.
 */

const outcomeVariant = {success: 'success', failure: 'error', denied: 'warning'} as const;

export function AuditPanel({onError}: {onError: (message: string) => void}) {
  const t = useTranslation();
  const [events, setEvents] = useState<AuditEvent[]>([]);
  const [filters, setFilters] = useState<AuditFilterSet | null>(null);
  const [domain, setDomain] = useState('');
  const [action, setAction] = useState('');
  const [workspace, setWorkspace] = useState('');
  const [actor, setActor] = useState('');
  const [outcome, setOutcome] = useState('');
  const [search, setSearch] = useState('');
  // What the list was actually loaded with. Typing in the search box should not
  // fire a request per keystroke, so the query is committed on submit.
  const [committed, setCommitted] = useState<AuditQuery>({});
  const [cursor, setCursor] = useState(0);
  const [hasMore, setHasMore] = useState(false);
  const [isLoading, setIsLoading] = useState(true);

  useEffect(() => {
    api.auditFilters().then(setFilters).catch(() => setFilters(null));
  }, []);

  // Loading is switched on where the query is committed rather than here: the
  // list only reloads because somebody pressed Apply or Reset.
  useEffect(() => {
    let isCurrent = true;
    api.auditEvents(committed)
      .then((result) => {
        if (!isCurrent) return;
        setEvents(result.events);
        setCursor(result.next_cursor);
        setHasMore(result.has_more);
      })
      .catch((caught) => onError(caught instanceof Error ? caught.message : t('admin.loadFailed')))
      .finally(() => { if (isCurrent) setIsLoading(false); });
    return () => { isCurrent = false; };
  }, [committed, onError, t]);

  const apply = useCallback(() => {
    setIsLoading(true);
    setCommitted({
      ...(domain ? {domain} : {}),
      ...(action ? {action} : {}),
      ...(workspace ? {workspace} : {}),
      ...(actor ? {actor} : {}),
      ...(outcome ? {outcome} : {}),
      ...(search.trim() ? {q: search.trim()} : {}),
    });
  }, [domain, action, workspace, actor, outcome, search]);

  function reset() {
    setDomain('');
    setAction('');
    setWorkspace('');
    setActor('');
    setOutcome('');
    setSearch('');
    setIsLoading(true);
    setCommitted({});
  }

  async function loadMore() {
    if (!hasMore || cursor === 0) return;
    setIsLoading(true);
    try {
      const result = await api.auditEvents({...committed, before: cursor});
      setEvents((current) => [...current, ...result.events]);
      setCursor(result.next_cursor);
      setHasMore(result.has_more);
    } catch (caught) {
      onError(caught instanceof Error ? caught.message : t('admin.loadFailed'));
    } finally {
      setIsLoading(false);
    }
  }

  // Selecting an area narrows the actions on offer, so the two pickers cannot
  // be set to a combination that matches nothing.
  const actionOptions = (filters?.actions ?? [])
    .filter((item) => !domain || item.value.startsWith(`${domain}.`))
    .map((item) => ({label: `${item.value} (${item.count})`, value: item.value}));

  const anyOption = {label: t('admin.audit.all'), value: ''};

  return (
    <VStack gap={4}>
      <HStack hAlign="between" vAlign="center" wrap="wrap">
        <Heading level={1} type="display-3">{t('admin.audit')}</Heading>
        <HStack gap={2}>
          <Button
            icon={<RotateCcw size={15} />}
            label={t('admin.audit.reset')}
            onClick={reset}
            size="sm"
            variant="ghost"
          />
          <Button
            icon={<Download size={15} />}
            label={t('admin.audit.export')}
            onClick={() => window.open(api.auditExportURL(committed), '_blank', 'noopener')}
            size="sm"
            variant="secondary"
          />
        </HStack>
      </HStack>

      <Card width="100%">
        <VStack gap={3}>
          <Grid columns={{minWidth: 200}} gap={3}>
            <Selector
              label={t('admin.audit.domain')}
              onChange={(value) => { setDomain(value); setAction(''); }}
              options={[anyOption, ...(filters?.domains ?? []).map((item) => ({label: `${item.label} (${item.count})`, value: item.value}))]}
              value={domain}
              width="100%"
            />
            <Selector
              label={t('admin.audit.action')}
              onChange={setAction}
              options={[anyOption, ...actionOptions]}
              value={action}
              width="100%"
            />
            <Selector
              label={t('admin.audit.workspace')}
              onChange={setWorkspace}
              options={[anyOption, ...(filters?.workspaces ?? []).map((item) => ({label: item.label || item.value, value: item.value}))]}
              value={workspace}
              width="100%"
            />
            <Selector
              label={t('admin.audit.actor')}
              onChange={setActor}
              options={[anyOption, ...(filters?.actors ?? []).map((item) => ({label: item.label || item.value, value: item.value}))]}
              value={actor}
              width="100%"
            />
            <Selector
              label={t('admin.audit.outcome')}
              onChange={setOutcome}
              options={[
                anyOption,
                {label: t('admin.audit.success'), value: 'success'},
                {label: t('admin.audit.failure'), value: 'failure'},
                {label: t('admin.audit.denied'), value: 'denied'},
              ]}
              value={outcome}
              width="100%"
            />
            <TextInput label={t('admin.audit.search')} onChange={setSearch} value={search} />
          </Grid>
          <HStack hAlign="end">
            <Button isLoading={isLoading} label={t('admin.audit.apply')} onClick={apply} size="sm" variant="primary" />
          </HStack>
        </VStack>
      </Card>

      {events.length === 0 && !isLoading
        ? <EmptyState description={t('admin.auditEmpty')} title="—" />
        : (
          <VStack gap={2}>
            <Text color="secondary" type="supporting">{t('admin.audit.count', {count: events.length})}</Text>
            {events.map((event) => <AuditRow event={event} key={event.id} />)}
            {hasMore ? (
              <HStack hAlign="center">
                <Button isLoading={isLoading} label={t('admin.audit.loadMore')} onClick={loadMore} size="sm" variant="secondary" />
              </HStack>
            ) : null}
          </VStack>
        )}
    </VStack>
  );
}

/**
 * One event, closed to a line and open to everything that was recorded.
 *
 * The line answers who, what and when. The detail answers from where and to
 * what - the address, the request id, and the metadata the handler chose to
 * keep - which is what a reader needs only once they have found the row.
 */
function AuditRow({event}: {event: AuditEvent}) {
  const t = useTranslation();
  const actor = event.actor_name || event.actor_email || t('admin.audit.system');
  const target = event.target_label || event.target_id;
  return (
    <Card padding={3} width="100%">
      <Collapsible
        defaultIsOpen={false}
        trigger={
          <VStack gap={1} width="100%">
            <HStack gap={2} vAlign="center" wrap="wrap">
              <Text type="label">{event.action}</Text>
              <StatusLabel label={t(`admin.audit.${event.outcome}`)} variant={outcomeVariant[event.outcome] ?? 'neutral'} />
              {event.workspace_name ? <Token label={event.workspace_name} size="sm" /> : null}
            </HStack>
            <HStack gap={2} vAlign="center" wrap="wrap">
              <Text color="secondary" type="supporting">{actor}</Text>
              {target ? <Text color="secondary" type="supporting">{`· ${event.target_type || '—'} · ${target}`}</Text> : null}
              <Timestamp format="date_time" value={event.created_at} />
            </HStack>
          </VStack>
        }
      >
        <VStack gap={3} padding={2}>
          <MetadataList columns={2}>
            <Text type="supporting">{`${t('admin.audit.targetId')}: ${event.target_id || '—'}`}</Text>
            <Text type="supporting">{`${t('admin.audit.actorEmail')}: ${event.actor_email || '—'}`}</Text>
            <Text type="supporting">{`${t('admin.audit.ip')}: ${event.ip_address || '—'}`}</Text>
            <Text type="supporting">{`${t('admin.audit.requestId')}: ${event.request_id || '—'}`}</Text>
          </MetadataList>
          {event.user_agent ? <Text color="secondary" type="supporting">{event.user_agent}</Text> : null}
          {Object.keys(event.metadata).length > 0
            ? <CodeBlock code={JSON.stringify(event.metadata, null, 2)} hasLanguageLabel={false} isWrapped language="json" maxHeight={240} size="sm" />
            : null}
        </VStack>
      </Collapsible>
    </Card>
  );
}
