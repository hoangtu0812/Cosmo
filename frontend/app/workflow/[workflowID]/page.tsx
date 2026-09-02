'use client';

import '@xyflow/react/dist/style.css';

import {Suspense, useCallback, useEffect, useMemo, useRef, useState} from 'react';
import {useParams, useRouter, useSearchParams} from 'next/navigation';
import {
  Background, BackgroundVariant, ReactFlow, ReactFlowProvider,
  addEdge, useEdgesState, useNodesState, useReactFlow,
  type Connection, type Edge as FlowEdge, type Node as FlowNodeType,
} from '@xyflow/react';
import {
  ArrowLeft, Braces, LayoutGrid, Maximize2, PanelLeft, Play, Save, Search, Trash2, ZoomIn, ZoomOut,
} from 'lucide-react';
import {Banner} from '@astryxdesign/core/Banner';
import {Button} from '@astryxdesign/core/Button';
import {Card} from '@astryxdesign/core/Card';
import {Divider} from '@astryxdesign/core/Divider';
import {HStack, VStack} from '@astryxdesign/core/Layout';
import {Icon, IconType} from '@astryxdesign/core/Icon';
import {IconButton} from '@astryxdesign/core/IconButton';
import {SideNavItem, SideNavSection} from '@astryxdesign/core/SideNav';
import {Text} from '@astryxdesign/core/Text';
import {TextArea} from '@astryxdesign/core/TextArea';
import {TextInput} from '@astryxdesign/core/TextInput';
import {StatusLabel} from '../../components/StatusLabel';
import {CopyButton} from '../../components/CopyButton';
import {
  APIError, Workflow, WorkflowGraph, WorkflowNodeKind, WorkflowStep,
  api, streamWorkflowRun,
} from '../../lib/api';
import {useTranslation} from '../../lib/i18n';
import {CanvasToolbar} from '../CanvasToolbar';
import {
  FlowNode, FlowNodeData, GROUP_LABELS, NODE_KINDS, NODE_LABELS, STATUS_LABELS,
  iconFor, isRunnable,
} from '../nodes';

// React Flow needs a stable map, or every render remounts every node and the
// canvas loses its selection mid-drag.
const nodeTypes = {step: FlowNode};

// Measured from the reference: the library is 340px including its tab rail.
const PANEL_WIDTH = 340;
const RAIL_WIDTH = 66;

// Where a node dropped from the library lands. Staggered so several in a row
// do not stack into one another.
const DROP_ORIGIN = {x: 320, y: 120};
const DROP_STEP = 40;

const GROUPS = ['flow', 'ai', 'logic', 'data', 'network', 'application', 'tool'] as const;

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
  const flow = useReactFlow();

  const [workflow, setWorkflow] = useState<Workflow | null>(null);
  const [nodes, setNodes, onNodesChange] = useNodesState<FlowNodeType>([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState<FlowEdge>([]);
  const [error, setError] = useState('');
  const [isSaving, setIsSaving] = useState(false);
  const [isRunning, setIsRunning] = useState(false);
  const [input, setInput] = useState('');
  const [steps, setSteps] = useState<WorkflowStep[]>([]);
  const [selectedID, setSelectedID] = useState('');
  const [isPanelOpen, setIsPanelOpen] = useState(true);
  const [tab, setTab] = useState<'basic' | 'variables'>('basic');
  const [query, setQuery] = useState('');
  const [zoom, setZoom] = useState(1);
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
      const data = node.data as FlowNodeData & {config?: Record<string, unknown>};
      return {
        id: node.id,
        kind: data.kind,
        name: data.label,
        x: node.position.x,
        y: node.position.y,
        config: data.config ?? {},
      };
    }),
    edges: edges.map((edge) => ({id: edge.id, source: edge.source, target: edge.target})),
  }), [nodes, edges]);

  const selected = nodes.find((node) => node.id === selectedID);
  const needle = query.trim().toLowerCase();

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

  function clearRun() {
    setSteps([]);
    setNodes((current) => current.map((node) => ({
      ...node, data: {...(node.data as FlowNodeData), status: undefined},
    })));
    setEdges((current) => current.map((edge) => ({...edge, animated: false})));
  }

  function rescale(next: number) {
    const clamped = Math.min(2, Math.max(0.25, next));
    setZoom(clamped);
    flow.zoomTo(clamped, {duration: 200});
  }

  if (!workflow) {
    return (
      <VStack gap={0} height="100vh" padding={6} width="100%">
        {error ? <Banner status="error" title={error} /> : null}
      </VStack>
    );
  }

  return (
    // The window. A canvas is the whole job while it is open, so the app rail
    // and its second column stand aside - a third of the room to draw in,
    // spent on navigation nobody uses mid-edit.
    <HStack gap={0} height="100vh" width="100%">
      {isPanelOpen ? (
        <HStack className="border-r border-[var(--color-border)]" gap={0} height="100%" width={PANEL_WIDTH}>
          {/* The tab rail: what you are adding, or what you can refer to. */}
          <VStack
            className="border-r border-[var(--color-border)]"
            gap={2}
            hAlign="center"
            height="100%"
            padding={2}
            width={RAIL_WIDTH}
          >
            <RailTab icon={LayoutGrid} isSelected={tab === 'basic'} label={t('workflow.tabBasic')} onClick={() => setTab('basic')} />
            <RailTab icon={Braces} isSelected={tab === 'variables'} label={t('workflow.tabVariables')} onClick={() => setTab('variables')} />
          </VStack>

          <VStack gap={0} height="100%" width="100%">
            <HStack gap={2} padding={3} vAlign="center" width="100%">
              <IconButton
                icon={<ArrowLeft size={16} />}
                label={t('nav.workflow')}
                onClick={() => router.push(`/workflow${workspaceID ? `?workspace=${encodeURIComponent(workspaceID)}` : ''}`)}
                size="sm"
                variant="ghost"
              />
              <Text maxLines={1} type="label">{workflow.name}</Text>
            </HStack>

            {tab === 'basic' ? (
              <VStack gap={0} height="100%" isScrollable width="100%">
                <VStack gap={0} padding={3} width="100%">
                  <TextInput
                    isLabelHidden
                    label={t('workflow.searchNodes')}
                    onChange={setQuery}
                    placeholder={t('workflow.searchNodes')}
                    startIcon={<Icon icon={Search} size="sm" />}
                    value={query}
                    width="100%"
                  />
                </VStack>
                {GROUPS.map((group) => {
                  const inGroup = NODE_KINDS.filter((item) => item.group === group
                    && (!needle || t(NODE_LABELS[item.kind]).toLowerCase().includes(needle)));
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
            ) : (
              <VariablesPanel nodes={nodes} t={t} />
            )}
          </VStack>
        </HStack>
      ) : null}

      <VStack className="relative" gap={0} height="100%" width="100%">
        <ReactFlow
          edges={edges}
          fitView
          nodeTypes={nodeTypes}
          nodes={nodes}
          onConnect={(connection: Connection) => setEdges((current) => addEdge({...connection, animated: false}, current))}
          onEdgesChange={onEdgesChange}
          onMove={(_event, viewport) => setZoom(viewport.zoom)}
          onNodeClick={(_event, node) => setSelectedID(node.id)}
          onNodesChange={onNodesChange}
          onPaneClick={() => setSelectedID('')}
        >
          {/* The dotted ground, in the theme's own border colour so the canvas
              follows light and dark with everything else. */}
          <Background color="var(--color-border)" gap={16} variant={BackgroundVariant.Dots} />
        </ReactFlow>

        {/* The bars float over the canvas rather than sitting in a header,
            which would take a strip of drawing room the whole time. Pointer
            events are off on the strip and back on for the bars, so the canvas
            under the gaps is still draggable. */}
        <HStack className="pointer-events-none absolute left-4 right-4 top-4 z-10" gap={3} hAlign="center" vAlign="start">
          <HStack className="pointer-events-auto" gap={3} vAlign="center">
            <CanvasToolbar>
              <IconButton icon={<ZoomOut size={15} />} label={t('workflow.zoomOut')} onClick={() => rescale(zoom - 0.1)} size="sm" variant="ghost" />
              <Text type="supporting">{Math.round(zoom * 100)}%</Text>
              <IconButton icon={<ZoomIn size={15} />} label={t('workflow.zoomIn')} onClick={() => rescale(zoom + 0.1)} size="sm" variant="ghost" />
              <Divider orientation="vertical" />
              <IconButton icon={<Maximize2 size={15} />} label={t('workflow.fitView')} onClick={() => flow.fitView({duration: 200})} size="sm" variant="ghost" />
              <IconButton icon={<PanelLeft size={15} />} label={t('workflow.togglePanel')} onClick={() => setIsPanelOpen((open) => !open)} size="sm" variant="ghost" />
            </CanvasToolbar>

            <CanvasToolbar>
              <Button
                icon={<Play size={14} />}
                isDisabled={isRunning}
                isLoading={isRunning}
                label={t('workflow.run')}
                onClick={() => void run()}
                size="sm"
                variant="ghost"
              />
              {steps.length > 0 ? (
                <Button label={t('workflow.clearRun')} onClick={clearRun} size="sm" variant="ghost" />
              ) : null}
              <Divider orientation="vertical" />
              <Button
                icon={<Save size={14} />}
                isDisabled={isSaving || !workflow.is_editable}
                isLoading={isSaving}
                label={t('workflow.save')}
                onClick={() => void save()}
                size="sm"
                variant="primary"
              />
            </CanvasToolbar>
          </HStack>
        </HStack>

        {error ? (
          <VStack className="absolute bottom-4 left-4 right-4 z-10" gap={0}>
            <Banner isDismissable onDismiss={() => setError('')} status="error" title={error} />
          </VStack>
        ) : null}
      </VStack>

      <VStack className="border-l border-[var(--color-border)]" gap={4} height="100%" isScrollable padding={4} width={320}>
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
                  {step.output ? (
                    <VStack gap={1} width="100%">
                      <Text color="secondary" maxLines={4} type="supporting">{step.output}</Text>
                      <HStack hAlign="end" width="100%"><CopyButton text={step.output} /></HStack>
                    </VStack>
                  ) : null}
                </VStack>
              </Card>
            ))}
          </VStack>
        )}
      </VStack>
    </HStack>
  );
}

function RailTab({icon, label, isSelected, onClick}: {
  icon: IconType;
  label: string;
  isSelected: boolean;
  onClick: () => void;
}) {
  return (
    <VStack gap={1} hAlign="center" width="100%">
      <IconButton
        icon={<Icon icon={icon} size="sm" />}
        label={label}
        onClick={onClick}
        size="sm"
        variant={isSelected ? 'primary' : 'ghost'}
      />
      <Text color={isSelected ? 'primary' : 'secondary'} type="supporting">{label}</Text>
    </VStack>
  );
}

/**
 * What a node can refer to. Every node's output is addressable by its id, and
 * the Start node's value by {{input}}, so this is a reading of the canvas
 * rather than a second list to keep in step with it.
 */
function VariablesPanel({nodes, t}: {nodes: FlowNodeType[]; t: ReturnType<typeof useTranslation>}) {
  return (
    <VStack gap={3} height="100%" isScrollable padding={3} width="100%">
      <Text color="secondary" type="supporting">{t('workflow.variablesHint')}</Text>
      <VariableRow label={t('workflow.node.start')} token="{{input}}" />
      {nodes
        .filter((node) => (node.data as FlowNodeData).kind !== 'start')
        .map((node) => (
          <VariableRow key={node.id} label={(node.data as FlowNodeData).label} token={`{{${node.id}}}`} />
        ))}
    </VStack>
  );
}

function VariableRow({label, token}: {label: string; token: string}) {
  return (
    <HStack gap={2} hAlign="between" vAlign="center" width="100%">
      <VStack gap={0}>
        <Text maxLines={1} type="supporting">{label}</Text>
        <Text color="secondary" type="code">{token}</Text>
      </VStack>
      <CopyButton text={token} />
    </HStack>
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
