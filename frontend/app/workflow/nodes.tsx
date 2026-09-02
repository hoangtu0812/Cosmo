'use client';

import {Handle, NodeProps, Position} from '@xyflow/react';
import {
  Bot, Braces, Code2, GitBranch, Globe, LogIn, LogOut, RefreshCw, Sparkles, Wrench,
} from 'lucide-react';
import {Card} from '@astryxdesign/core/Card';
import {HStack, VStack} from '@astryxdesign/core/Layout';
import {Icon, IconType} from '@astryxdesign/core/Icon';
import {Spinner} from '@astryxdesign/core/Spinner';
import {Text} from '@astryxdesign/core/Text';
import {RUNNABLE_NODE_KINDS, WorkflowNodeKind} from '../lib/api';

/**
 * What each kind of node looks like, and which of them do anything yet.
 *
 * The list of runnable kinds comes from the API module, which mirrors the
 * server's own list, so a node the editor offers as working and the server
 * refuses cannot exist.
 */
export const NODE_KINDS: {kind: WorkflowNodeKind; icon: IconType; group: string}[] = [
  {kind: 'start', icon: LogIn, group: 'flow'},
  {kind: 'llm', icon: Sparkles, group: 'ai'},
  {kind: 'knowledge', icon: Braces, group: 'ai'},
  {kind: 'condition', icon: GitBranch, group: 'logic'},
  {kind: 'loop', icon: RefreshCw, group: 'logic'},
  {kind: 'code', icon: Code2, group: 'data'},
  {kind: 'http', icon: Globe, group: 'network'},
  {kind: 'agent', icon: Bot, group: 'application'},
  {kind: 'tool', icon: Wrench, group: 'tool'},
  {kind: 'end', icon: LogOut, group: 'flow'},
];

export function iconFor(kind: string): IconType {
  return NODE_KINDS.find((item) => item.kind === kind)?.icon ?? Sparkles;
}

export function isRunnable(kind: string): boolean {
  return RUNNABLE_NODE_KINDS.includes(kind as WorkflowNodeKind);
}

export type StepStatus = 'running' | 'complete' | 'error' | 'skipped';

export type FlowNodeData = {
  kind: WorkflowNodeKind;
  label: string;
  detail: string;
  status?: StepStatus;
};

// The ring a node wears while a run passes through it. Colour is a token so it
// follows the theme; the pulse is the design system's own motion duration.
const RING: Record<StepStatus, string> = {
  running: 'var(--color-icon-blue)',
  complete: 'var(--color-icon-green)',
  error: 'var(--color-icon-red)',
  skipped: 'var(--color-border)',
};

/**
 * One node on the canvas.
 *
 * A node that cannot run yet is drawn faded, so the canvas says which parts of
 * the shape are real without a legend. While a run passes through, the node
 * takes a coloured ring and the running one keeps a spinner - which is the
 * whole point of streaming the steps rather than returning them at the end.
 */
export function FlowNode({data, selected}: NodeProps & {data: FlowNodeData}) {
  const status = data.status;
  const ring = status ? RING[status] : undefined;
  const faded = !isRunnable(data.kind) && !status;

  return (
    <VStack
      className={[
        'transition-all duration-200',
        status === 'running' ? 'animate-pulse' : '',
        faded ? 'opacity-60' : '',
      ].join(' ')}
      style={ring ? {outline: `2px solid ${ring}`, outlineOffset: 2, borderRadius: 12} : undefined}
      width={200}
    >
      {/* Start has nothing before it and End nothing after, so neither grows a
          handle it could never use. */}
      {data.kind !== 'start' ? <Handle position={Position.Left} type="target" /> : null}
      <Card padding={3} width="100%" xstyle={selected ? undefined : undefined}>
        <VStack gap={1} width="100%">
          <HStack gap={2} vAlign="center">
            {status === 'running' ? <Spinner size="sm" /> : <Icon icon={iconFor(data.kind)} size="sm" />}
            <Text maxLines={1} type="label">{data.label}</Text>
          </HStack>
          {data.detail ? (
            <Text color="secondary" maxLines={2} type="supporting">{data.detail}</Text>
          ) : null}
        </VStack>
      </Card>
      {data.kind === 'condition' ? (
        <>
          {/* Two ways out, placed apart and labelled, because an unlabelled
              pair is a coin toss for whoever draws the next edge. */}
          <Handle id="true" position={Position.Right} style={{top: '35%'}} type="source" />
          <Handle id="false" position={Position.Right} style={{top: '70%'}} type="source" />
        </>
      ) : data.kind !== 'end' ? (
        <Handle position={Position.Right} type="source" />
      ) : null}
    </VStack>
  );
}

// Labels are looked up through fixed maps rather than a built key, so a kind
// or a group added without a translation fails to compile instead of printing
// its own id to the reader.
export const NODE_LABELS = {
  start: 'workflow.node.start',
  llm: 'workflow.node.llm',
  knowledge: 'workflow.node.knowledge',
  condition: 'workflow.node.condition',
  loop: 'workflow.node.loop',
  code: 'workflow.node.code',
  http: 'workflow.node.http',
  agent: 'workflow.node.agent',
  tool: 'workflow.node.tool',
  end: 'workflow.node.end',
} as const;

export const GROUP_LABELS = {
  flow: 'workflow.group.flow',
  ai: 'workflow.group.ai',
  logic: 'workflow.group.logic',
  data: 'workflow.group.data',
  network: 'workflow.group.network',
  application: 'workflow.group.application',
  tool: 'workflow.group.tool',
} as const;

export const STATUS_LABELS = {
  running: 'workflow.status.running',
  complete: 'workflow.status.complete',
  error: 'workflow.status.error',
  skipped: 'workflow.status.skipped',
} as const;
