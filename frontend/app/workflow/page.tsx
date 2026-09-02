'use client';

import {Suspense, useEffect, useState} from 'react';
import {useRouter, useSearchParams} from 'next/navigation';
import {GitBranch, Play, Workflow as WorkflowIcon} from 'lucide-react';
import {Button} from '@astryxdesign/core/Button';
import {Card} from '@astryxdesign/core/Card';
import {Dialog, DialogHeader} from '@astryxdesign/core/Dialog';
import {Grid} from '@astryxdesign/core/Grid';
import {HStack, Layout, LayoutContent, LayoutFooter, VStack} from '@astryxdesign/core/Layout';
import {Banner} from '@astryxdesign/core/Banner';
import {Skeleton} from '@astryxdesign/core/Skeleton';
import {Text} from '@astryxdesign/core/Text';
import {TextArea} from '@astryxdesign/core/TextArea';
import {TextInput} from '@astryxdesign/core/TextInput';
import {CapabilityHero} from '../components/CapabilityHero';
import {PageHeader} from '../components/PageHeader';
import {cardHover} from '../components/motion';
import {APIError, Workflow, api} from '../lib/api';
import {useTranslation} from '../lib/i18n';

export default function WorkflowPage() {
  return <Suspense fallback={null}><WorkflowList /></Suspense>;
}

function WorkflowList() {
  const t = useTranslation();
  const router = useRouter();
  const search = useSearchParams();
  const workspaceID = search.get('workspace') ?? '';

  const [workflows, setWorkflows] = useState<Workflow[]>([]);
  // "No workflows yet" is a claim about the workspace; until the read lands
  // the page does not know enough to make it.
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState('');
  const [isCreating, setIsCreating] = useState(false);
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [isSaving, setIsSaving] = useState(false);

  useEffect(() => {
    api.workflows(workspaceID)
      .then((result) => setWorkflows(result.workflows))
      .catch((caught) => setError(caught instanceof APIError ? caught.message : ''))
      .finally(() => setIsLoading(false));
  }, [workspaceID]);

  function open(id: string) {
    router.push(`/workflow/${id}${workspaceID ? `?workspace=${encodeURIComponent(workspaceID)}` : ''}`);
  }

  async function create() {
    setIsSaving(true);
    setError('');
    try {
      const {workflow} = await api.createWorkflow({name, description}, workspaceID);
      setIsCreating(false);
      setName('');
      setDescription('');
      open(workflow.id);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : t('workflow.createFailed'));
    } finally {
      setIsSaving(false);
    }
  }

  return (
    <>
      <Layout
        content={
          <LayoutContent padding={workflows.length === 0 && !isLoading ? 0 : 6}>
            {error ? <Banner isDismissable onDismiss={() => setError('')} status="error" title={error} /> : null}
            {isLoading ? (
              <Grid columns={{minWidth: 220, max: 4}} gap={4} width="100%">
                {[0, 1, 2].map((index) => <Skeleton height={150} index={index} key={index} width="100%" />)}
              </Grid>
            ) : workflows.length === 0 ? (
              <CapabilityHero
                action={<Button label={t('workflow.new')} onClick={() => setIsCreating(true)} variant="primary" />}
                description={t('workflow.heroBody')}
                flow={[
                  {icon: Play, label: t('workflow.flowStart')},
                  {icon: WorkflowIcon, label: t('nav.workflow')},
                  {icon: GitBranch, label: t('workflow.flowResult')},
                ]}
                points={[
                  {icon: WorkflowIcon, title: t('workflow.pointOrder'), description: t('workflow.pointOrderBody')},
                  {icon: Play, title: t('workflow.pointRun'), description: t('workflow.pointRunBody')},
                  {icon: GitBranch, title: t('workflow.pointReuse'), description: t('workflow.pointReuseBody')},
                ]}
                title={t('workflow.hero')}
              />
            ) : (
              <Grid columns={{minWidth: 220, max: 4}} gap={4} width="100%">
                {workflows.map((workflow) => (
                  <Card className={cardHover} key={workflow.id} onClick={() => open(workflow.id)} padding={4} width="100%">
                    <VStack gap={2} height="100%">
                      <Text maxLines={1} type="label">{workflow.name}</Text>
                      <Text color="secondary" maxLines={2} type="supporting">{workflow.description}</Text>
                      <Text color="secondary" type="supporting">
                        {t('workflow.nodeCount', {count: workflow.graph.nodes.length})}
                      </Text>
                    </VStack>
                  </Card>
                ))}
              </Grid>
            )}
          </LayoutContent>
        }
        header={
          <PageHeader
            actions={<Button label={t('workflow.new')} onClick={() => setIsCreating(true)} size="sm" variant="primary" />}
            count={workflows.length}
            description={t('workflow.subtitle')}
            title={t('nav.workflow')}
          />
        }
        height="fill"
      />

      <Dialog isOpen={isCreating} onOpenChange={setIsCreating} purpose="form">
        <Layout
          content={
            <LayoutContent>
              <VStack gap={4}>
                <TextInput label={t('workflow.name')} onChange={setName} value={name} width="100%" />
                <TextArea
                  label={t('workflow.description')}
                  maxLength={512}
                  onChange={setDescription}
                  placeholder={t('workflow.descriptionHint')}
                  rows={3}
                  value={description}
                  width="100%"
                />
              </VStack>
            </LayoutContent>
          }
          footer={
            <LayoutFooter>
              <HStack gap={2} hAlign="end" width="100%">
                <Button label={t('common.cancel')} onClick={() => setIsCreating(false)} variant="secondary" />
                <Button
                  isDisabled={!name.trim() || isSaving}
                  isLoading={isSaving}
                  label={t('workflow.create')}
                  onClick={() => void create()}
                  variant="primary"
                />
              </HStack>
            </LayoutFooter>
          }
          header={<DialogHeader onOpenChange={setIsCreating} title={t('workflow.new')} />}
        />
      </Dialog>
    </>
  );
}
