'use client';

import {useEffect, useMemo, useState} from 'react';
import {Bot, Cpu, Search} from 'lucide-react';
import {Avatar} from '@astryxdesign/core/Avatar';
import {Icon} from '@astryxdesign/core/Icon';
import {VStack} from '@astryxdesign/core/Layout';
import {SegmentedControl, SegmentedControlItem} from '@astryxdesign/core/SegmentedControl';
import {SideNavItem, SideNavSection} from '@astryxdesign/core/SideNav';
import {Skeleton} from '@astryxdesign/core/Skeleton';
import {Text} from '@astryxdesign/core/Text';
import {TextInput} from '@astryxdesign/core/TextInput';
import {Agent, api, Workspace} from '../lib/api';
import {useTranslation} from '../lib/i18n';

// On the chat route the second column stops being navigation and becomes the
// list of things you can talk to, the way the reference has it. Choosing one
// is a route change - ?target=agent:… or model:… - so the chat page can read
// its own target from the URL and nothing has to be lifted into shared state.
//
// The search and the filter sit in the SideNav's sticky area and the list in
// its scrolling one, so the two are separate exports over one piece of state.
export function useChatTargets(workspace: Workspace | null) {
  const [agents, setAgents] = useState<Agent[]>([]);
  const [models, setModels] = useState<string[]>([]);
  const [query, setQuery] = useState('');
  const [kind, setKind] = useState('all');
  // "Nothing to talk to yet" is a claim about the workspace. Until both reads
  // land the list does not know enough to make it, and on a cold container it
  // was making it for several seconds.
  const [isLoading, setIsLoading] = useState(true);

  useEffect(() => {
    if (!workspace) return;
    Promise.allSettled([
      api.agents(workspace.id).then((result) => setAgents(result.agents)).catch(() => setAgents([])),
      api.workspaceModels(workspace.id)
        .then((result) => setModels(result.models.map((item) => typeof item === 'string' ? item : item.id)))
        .catch(() => setModels([])),
    ]).finally(() => setIsLoading(false));
  }, [workspace]);

  const needle = query.trim().toLowerCase();
  const shownAgents = useMemo(
    () => (kind === 'models' ? [] : agents.filter((item) => !needle || item.name.toLowerCase().includes(needle))),
    [agents, kind, needle],
  );
  const shownModels = useMemo(
    () => (kind === 'agents' ? [] : models.filter((item) => !needle || item.toLowerCase().includes(needle))),
    [models, kind, needle],
  );

  return {query, setQuery, kind, setKind, shownAgents, shownModels, isLoading};
}

export type ChatTargets = ReturnType<typeof useChatTargets>;

export function ChatTargetFilters({targets, t}: {targets: ChatTargets; t: ReturnType<typeof useTranslation>}) {
  return (
    <VStack gap={2} padding={2} width="100%">
      <TextInput
        isLabelHidden
        label={t('chat.searchTargets')}
        onChange={targets.setQuery}
        placeholder={t('chat.searchTargets')}
        size="lg"
        startIcon={<Icon icon={Search} size="sm" />}
        value={targets.query}
        width="100%"
      />
      <SegmentedControl label={t('chat.targetKind')} onChange={targets.setKind} size="md" value={targets.kind}>
        <SegmentedControlItem label={t('agent.scopeAll')} value="all" />
        <SegmentedControlItem label={t('agent.title')} value="agents" />
        <SegmentedControlItem label={t('agent.model')} value="models" />
      </SegmentedControl>
    </VStack>
  );
}

export function ChatTargetList({targets, workspace, activeTarget, onPick, t}: {
  targets: ChatTargets;
  workspace: Workspace | null;
  activeTarget: string;
  onPick: (target: string) => void;
  t: ReturnType<typeof useTranslation>;
}) {
  const {shownAgents, shownModels, isLoading} = targets;
  if (isLoading) {
    return (
      <VStack gap={2} padding={2} width="100%">
        {[0, 1, 2, 3].map((index) => <Skeleton height={32} index={index} key={index} width="100%" />)}
      </VStack>
    );
  }
  return (
    <>
      {shownAgents.length > 0 ? (
        <SideNavSection title={t('agent.title')}>
          {shownAgents.map((agent) => (
            <SideNavItem
              icon={<Avatar
                name={agent.avatar || agent.name}
                size="xsm"
                src={agent.has_avatar_image && workspace ? api.agentAvatarURL(agent.id, workspace.id) : undefined}
                tooltip={false}
              />}
              isSelected={activeTarget === `agent:${agent.id}`}
              key={agent.id}
              label={agent.name}
              onClick={() => onPick(`agent:${agent.id}`)}
              size="lg"
            />
          ))}
        </SideNavSection>
      ) : null}

      {shownModels.length > 0 ? (
        <SideNavSection title={t('agent.model')}>
          {shownModels.map((model) => (
            <SideNavItem
              icon={<Icon icon={Cpu} size="sm" />}
              isSelected={activeTarget === `model:${model}`}
              key={model}
              label={model}
              onClick={() => onPick(`model:${model}`)}
              size="lg"
            />
          ))}
        </SideNavSection>
      ) : null}

      {shownAgents.length === 0 && shownModels.length === 0 ? (
        <VStack gap={2} hAlign="center" padding={4}>
          <Icon icon={Bot} size="md" />
          <Text color="secondary" type="supporting">{t('chat.noTargets')}</Text>
        </VStack>
      ) : null}
    </>
  );
}
