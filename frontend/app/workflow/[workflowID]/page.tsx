'use client';

import '@xyflow/react/dist/style.css';

import {Suspense, useCallback, useEffect, useMemo, useRef, useState} from 'react';
import {useParams, useRouter, useSearchParams} from 'next/navigation';
import {
  Background, BackgroundVariant, Controls, MiniMap, ReactFlow, ReactFlowProvider,
  addEdge, useEdgesState, useNodesState,
  type Connection, type Edge as FlowEdge, type Node as FlowNodeType,
} from '@xyflow/react';
import {ArrowLeft, Play, Save, Trash2} from 'lucide-react';
import {Banner} from '@astryxdesign/core/Banner';
import {Button} from '@astryxdesign/core/Button';
import {Card} from '@astryxdesign/core/Card';
import {HStack, Layout, LayoutContent, LayoutHeader, VStack} from '@astryxdesign/core/Layout';
import {Icon} from '@astryxdesign/core/Icon';
import {IconButton} from '@astryxdesign/core/IconButton';
import {SideNavItem, SideNavSection} from '@astryxdesign/core/SideNav';
import {Text} from '@astryxdesign/core/Text';
import {TextArea} from '@astryxdesign/core/TextArea';
import {TextInput} from '@astryxdesign/core/TextInput';
import {Toolbar} from '@astryxdesign/core/Toolbar';
import {StatusLabel} from '../../components/StatusLabel';
import {
  APIError, Workflow, WorkflowGraph, WorkflowNodeKind, WorkflowStep,
  api, streamWorkflowRun,
} from '../../lib/api';
import {useTranslation} from '../../lib/i18n';
import {FlowNode, FlowNodeData, GROUP_LABELS, NODE_KINDS, NODE_LABELS, STATUS_LABELS, iconFor, isRunnable} from '../nodes';

// React Flow needs a stable map, or every render remounts every node and the
// canvas loses its selection mid-drag.
const nodeTypes = {step: FlowNode};

// Where a node dropped from the library lands. Staggered so several in a row
// do not stack into one another.
const DROP_ORIGIN = {x: 320, y: 120};
const DROP_STEP = 40;

export default function WorkflowEditorPage() {
  return (
    <Suspense fallback={null}>
      <ReactFlowProvider>
        <WorkflowEditor />
      </ReactFlowProvider>
    </Suspense>
  );
}

function WorkflowEditor() {
  const t = useTranslation();
  const router = useRouter();
  const params = useParams<{workflowID: string}>();
  const search = useSearchParams();
  const workspaceID = search.get('workspace') ?? '';
  const workflowID = params.workflowID;

  const [workflow, setWorkflow] = useState<Workflow | null>(null);
  const [nodes, setNodes, onNodesChange] = useNodesState<FlowNodeType>([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState<FlowEdge>([]);
  const [error, setError] = useState('');
  const [isSaving, setIsSaving] = useState(false);
  const [isRunning, setIsRunning] = useState(false);
  const [input, setInput] = useState('');
  const [steps, setSteps] = useState<WorkflowStep[]>([]);
  const [selectedID, setSelectedID] = useState('');
  const dropped = useRef(0);

  useEffect(() => {
    api.workflow(workflowID, workspaceID)
      .then(({workflow: loaded}) => {
        setWorkflow(loaded);
        setNodes(loaded.graph.nodes.map(toFlowNode));
        setEdges(loaded.graph.edges.map((edge) => ({
          id: edge.id, source: edge.source, target: edge.target, animated: false,
        })));
      })
      .catch((caught) => setError(caught instanceof APIError ? caught.message : ''));
  }, [workflowID, workspaceID, setNodes, setEdges]);

  const graph: WorkflowGraph = useMemo(() => ({
    nodes: nodes.map((node) => {
      const data = node.data as FlowNodeData;
      return {
        id: node.id,
        kind: data.kind,
        name: data.label,
        x: node.position.x,
        y: node.position.y,
        config: (node.data as {config?: Record<string, unknown>}).config ?? {},
      };
    }),
    edges: edges.map((edge) => ({id: edge.id, source: edge.source, target: edge.target})),
  }), [nodes, edges]);

  const selected = nodes.find((node) => node.id === selectedID);

  function addNode(kind: WorkflowNodeKind) {
    const index = dropped.current++;
    // Unique within this editing session, which is all an id has to be before
    // the graph is saved - and unlike a clock reading it is pure.
    const id = `${kind}_${index}_${nodes.length}`;
    setNodes((current) => [...current, toFlowNode({
      id,
      kind,
      name: t(NODE_LABELS[kind]),
      x: DROP_ORIGIN.x + index * DROP_STEP,
      y: DROP_ORIGIN.y + index * DROP_STEP,
      config: {},
    })]);
    setSelectedID(id);
  }

  function updateSelected(changes: {name?: string; config?: Record<string, unknown>}) {
    setNodes((current) => current.map((node) => node.id !== selectedID ? node : {
      ...node,
      data: {
        ...(node.data as FlowNodeData),
        ...(changes.name === undefined ? {} : {label: changes.name}),
        ...(changes.config === undefined ? {} : {config: changes.config}),
        detail: detailFor((node.data as FlowNodeData).kind,
          changes.config ?? (node.data as {config?: Record<string, unknown>}).config ?? {}),
      },
    }));
  }

  async function save() {
    setIsSaving(true);
    setError('');
    try {
      const {workflow: saved} = await api.saveWorkflowGraph(workflowID, graph, workspaceID);
      setWorkflow(saved);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : t('workflow.saveFailed'));
    } finally {
      setIsSaving(false);
    }
  }

  // A run has to see the graph on screen, so it is saved first. Running a
  // stale copy and animating the new one would light up the wrong nodes.
  async function run() {
    setSteps([]);
    setError('');
    setIsRunning(true);
    try {
      await api.saveWorkflowGraph(workflowID, graph, workspaceID);
      await streamWorkflowRun(workflowID, input, workspaceID, {
        onStep: (step) => {
          setSteps((current) => {
            const index = current.findIndex((item) => item.node_id === step.node_id);
            if (index < 0) return [...current, step];
            const next = [...current];
            next[index] = step;
            return next;
          });
          paint(step);
        },
      });
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : t('workflow.runFailed'));
    } finally {
      setIsRunning(false);
    }
  }

  // The animation: the node takes its status, and every edge feeding it is
  // drawn moving while it works. Both come from the step stream, so what moves
  // on screen is what is actually happening on the server.
  const paint = useCallback((step: WorkflowStep) => {
    setNodes((current) => current.map((node) => node.id !== step.node_id ? node : {
      ...node,
      data: {...(node.data as FlowNodeData), status: step.status},
    }));
    setEdges((current) => current.map((edge) => edge.target !== step.node_id ? edge : {
      ...edge,
      animated: step.status === 'running',
    }));
  }, [setNodes, setEdges]);

  function clearRun() {
    setSteps([]);
    setNodes((current) => current.map((node) => ({
      ...node, data: {...(node.data as FlowNodeData), status: undefined},
    })));
    setEdges((current) => current.map((edge) => ({...edge, animated: false})));
  }

  if (!workflow) {
    return (
      <Layout
        content={<LayoutContent padding={6}>{error ? <Banner status="error" title={error} /> : null}</LayoutContent>}
        height="fill"
      />
    );
  }

  return (
    <Layout
      content={
        <LayoutContent padding={0}>
          <HStack gap={0} height="100%" width="100%">
            {/* The library. Kinds that do nothing yet are disabled rather than
                hidden: the shape of the thing being built should be visible. */}
            <VStack gap={0} height="100%" isScrollable width={240}>
              {(['flow', 'ai', 'logic', 'data', 'network', 'application', 'tool'] as const).map((group) => {
                const inGroup = NODE_KINDS.filter((item) => item.group === group);
                if (inGroup.length === 0) return null;
                return (
                  <SideNavSection key={group} title={t(GROUP_LABELS[group])}>
                    {inGroup.map((item) => (
                      <SideNavItem
                        icon={<Icon icon={item.icon} size="sm" />}
                        isDisabled={!isRunnable(item.kind)}
                        key={item.kind}
                        label={t(NODE_LABELS[item.kind])}
                        onClick={() => addNode(item.kind)}
                      />
                    ))}
                  </SideNavSection>
                );
              })}
            </VStack>

            <VStack gap={0} height="100%" width="100%">
              {error ? <Banner isDismissable onDismiss={() => setError('')} status="error" title={error} /> : null}
              <ReactFlow
                edges={edges}
                fitView
                nodeTypes={nodeTypes}
                nodes={nodes}
                onConnect={(connection: Connection) => setEdges((current) => addEdge({...connection, animated: false}, current))}
                onEdgesChange={onEdgesChange}
                onNodeClick={(_event, node) => setSelectedID(node.id)}
                onNodesChange={onNodesChange}
                onPaneClick={() => setSelectedID('')}
                proOptions={{hideAttribution: false}}
              >
                {/* The dotted ground, in the theme's own border colour so the
                    canvas follows light and dark with everything else. */}
                <Background color="var(--color-border)" gap={16} variant={BackgroundVariant.Dots} />
                <Controls />
                <MiniMap pannable zoomable />
              </ReactFlow>
            </VStack>

            <VStack gap={4} height="100%" isScrollable padding={4} width={320}>
              {selected ? (
                <NodeSettings
                  node={selected.data as FlowNodeData & {config?: Record<string, unknown>}}
                  onChange={updateSelected}
                  onRemove={() => {
                    setNodes((current) => current.filter((node) => node.id !== selectedID));
                    setEdges((current) => current.filter((edge) => edge.source !== selectedID && edge.target !== selectedID));
                    setSelectedID('');
                  }}
                  t={t}
                />
              ) : (
                <VStack gap={3} width="100%">
                  <Text type="label">{t('workflow.testRun')}</Text>
                  <TextArea
                    label={t('workflow.input')}
                    onChange={setInput}
                    placeholder={t('workflow.inputHint')}
                    rows={3}
                    value={input}
                    width="100%"
                  />
                  <HStack gap={2}>
                    <Button
                      icon={<Play size={14} />}
                      isDisabled={isRunning}
                      isLoading={isRunning}
                      label={t('workflow.run')}
                      onClick={() => void run()}
                      size="sm"
                      variant="primary"
                    />
                    {steps.length > 0 ? (
                      <Button label={t('workflow.clearRun')} onClick={clearRun} size="sm" variant="ghost" />
                    ) : null}
                  </HStack>
                  {steps.map((step) => (
                    <Card key={step.node_id} padding={3} width="100%">
                      <VStack gap={1} width="100%">
                        <HStack gap={2} hAlign="between" vAlign="center" width="100%">
                          <HStack gap={2} vAlign="center">
                            <Icon icon={iconFor(step.kind)} size="sm" />
                            <Text type="label">{step.name}</Text>
                          </HStack>
                          <StatusLabel
                            label={t(STATUS_LABELS[step.status])}
                            variant={step.status === 'complete' ? 'success' : step.status === 'error' ? 'error' : 'neutral'}
                          />
                        </HStack>
                        {step.error ? <Text color="secondary" type="supporting">{step.error}</Text> : null}
                        {step.output ? <Text color="secondary" maxLines={4} type="supporting">{step.output}</Text> : null}
                      </VStack>
                    </Card>
                  ))}
                </VStack>
              )}
            </VStack>
          </HStack>
        </LayoutContent>
      }
      header={
        <LayoutHeader hasDivider>
          <Toolbar
            endContent={
              <HStack gap={2} vAlign="center">
                <Button
                  icon={<Save size={14} />}
                  isDisabled={isSaving || !workflow.is_editable}
                  isLoading={isSaving}
                  label={t('workflow.save')}
                  onClick={() => void save()}
                  size="sm"
                  variant="secondary"
                />
              </HStack>
            }
            label={workflow.name}
            startContent={
              <HStack gap={2} vAlign="center">
                <IconButton
                  icon={<ArrowLeft size={16} />}
                  label={t('nav.workflow')}
                  onClick={() => router.push(`/workflow${workspaceID ? `?workspace=${encodeURIComponent(workspaceID)}` : ''}`)}
                  size="sm"
                  variant="ghost"
                />
                <Text type="label">{workflow.name}</Text>
              </HStack>
            }
          />
        </LayoutHeader>
      }
      height="fill"
    />
  );
}

function toFlowNode(node: {id: string; kind: WorkflowNodeKind; name: string; x: number; y: number; config?: Record<string, unknown>}): FlowNodeType {
  return {
    id: node.id,
    type: 'step',
    position: {x: node.x, y: node.y},
    data: {
      kind: node.kind,
      label: node.name,
      detail: detailFor(node.kind, node.config ?? {}),
      config: node.config ?? {},
    },
  };
}

// The one line a node shows under its name: what it will actually do, so the
// canvas can be read without opening every node.
function detailFor(kind: WorkflowNodeKind, config: Record<string, unknown>): string {
  if (kind === 'llm') return String(config.prompt ?? '');
  if (kind === 'end') return String(config.template ?? '');
  if (kind === 'tool') return String(config.action_id ?? '');
  return '';
}

function NodeSettings({node, onChange, onRemove, t}: {
  node: FlowNodeData & {config?: Record<string, unknown>};
  onChange: (changes: {name?: string; config?: Record<string, unknown>}) => void;
  onRemove: () => void;
  t: ReturnType<typeof useTranslation>;
}) {
  const config = node.config ?? {};
  return (
    <VStack gap={3} width="100%">
      <HStack gap={2} hAlign="between" vAlign="center" width="100%">
        <HStack gap={2} vAlign="center">
          <Icon icon={iconFor(node.kind)} size="sm" />
          <Text type="label">{t(NODE_LABELS[node.kind])}</Text>
        </HStack>
        <IconButton icon={<Trash2 size={15} />} label={t('workflow.removeNode')} onClick={onRemove} size="sm" variant="ghost" />
      </HStack>

      <TextInput label={t('workflow.nodeName')} onChange={(value) => onChange({name: value})} value={node.label} width="100%" />

      {node.kind === 'llm' ? (
        <TextArea
          label={t('workflow.prompt')}
          onChange={(value) => onChange({config: {...config, prompt: value}})}
          placeholder={t('workflow.promptHint')}
          rows={6}
          value={String(config.prompt ?? '')}
          width="100%"
        />
      ) : null}

      {node.kind === 'end' ? (
        <TextArea
          label={t('workflow.template')}
          onChange={(value) => onChange({config: {...config, template: value}})}
          placeholder={t('workflow.templateHint')}
          rows={4}
          value={String(config.template ?? '')}
          width="100%"
        />
      ) : null}

      {node.kind === 'tool' ? (
        <VStack gap={2} width="100%">
          <TextInput
            label={t('workflow.toolID')}
            onChange={(value) => onChange({config: {...config, tool_id: value}})}
            value={String(config.tool_id ?? '')}
            width="100%"
          />
          <TextInput
            label={t('workflow.actionID')}
            onChange={(value) => onChange({config: {...config, action_id: value}})}
            value={String(config.action_id ?? '')}
            width="100%"
          />
        </VStack>
      ) : null}

      {!isRunnable(node.kind) ? (
        <Banner status="warning" title={t('workflow.nodeShell')} />
      ) : null}
    </VStack>
  );
}
