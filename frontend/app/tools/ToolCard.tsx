'use client';

import {Card} from '@astryxdesign/core/Card';
import {Button} from '@astryxdesign/core/Button';
import {HStack, VStack} from '@astryxdesign/core/Layout';
import {Section} from '@astryxdesign/core/Section';
import {Switch} from '@astryxdesign/core/Switch';
import {Text} from '@astryxdesign/core/Text';
import {Token} from '@astryxdesign/core/Token';
import {StatusLabel} from '../components/StatusLabel';
import {CardContextMenu, CardMenuButton, CardMenuItems} from '../components/CardMenu';
import {cardHover} from '../components/motion';
import {Tool} from '../lib/api';
import {useTranslation} from '../lib/i18n';

/**
 * One tool as the workspace sees it.
 *
 * A card, not a section of the list screen, because it holds three separate
 * decisions - open it, install it, let it answer questions - and reading them
 * together is easier than reading them threaded through the screen around it.
 */
export function ToolCard({tool, actions, canInstall, isBusy, origin, onOpen, onInstall, onAutoCall}: {
  tool: Tool;
  actions: CardMenuItems;
  /** Whether this reader may install into the workspace at all. The server
      allows only its owners and admins, so for everyone else the card states
      the tool's condition and offers nothing to change it. */
  canInstall: boolean;
  isBusy: boolean;
  /** The owning workspace's name, when it is not the one being read. */
  origin: string;
  onOpen: () => void;
  onInstall: () => void;
  onAutoCall: (autoCall: boolean) => void;
}) {
  const t = useTranslation();

  return (
    <CardContextMenu items={actions} label={t('kb.manage')}>
      <Card className={cardHover} height="100%" onClick={onOpen} padding={0} width="100%">
        <VStack gap={0} height="100%">
          <Section padding={5} variant="muted">
            <HStack hAlign="center" width="100%">
              <Card padding={3}>
                <Text type="display-3">{tool.icon || '🔌'}</Text>
              </Card>
            </HStack>
          </Section>
          {/* Takes what the band above leaves, so the control at its foot can
              sit on the bottom edge and line up with its neighbours. */}
          <Section className="grow" padding={4}>
            <VStack className="h-full" gap={2}>
              <HStack gap={2} hAlign="between" vAlign="center" width="100%">
                <Text maxLines={1} type="label">{tool.name}</Text>
                {actions.length > 0 ? <CardMenuButton items={actions} label={t('kb.manage')} /> : null}
              </HStack>
              <Text color="secondary" maxLines={2} type="supporting">
                {tool.description || tool.base_url}
              </Text>
              <HStack gap={2} vAlign="center" wrap="wrap">
                <Text color="secondary" type="supporting">
                  {tool.action_count === 1 ? t('tool.actionCountOne') : t('tool.actionCount', {count: tool.action_count})}
                </Text>
                {/* How many agents depend on this, which is what a reader
                    wants before changing it. */}
                <Text color="secondary" type="supporting">
                  {tool.reference_count === 1 ? t('capability.referencesOne') : t('capability.references', {count: tool.reference_count})}
                </Text>
                {tool.has_secret ? <Token label={t('tool.keySet', {hint: tool.auth_hint})} size="sm" /> : null}
                {/* Whose tool this is, said only where it is not obvious: in
                    the workspace that owns it, every card would say the same
                    thing. */}
                {origin ? <Token label={t('tool.from', {workspace: origin})} size="sm" /> : null}
                <StatusLabel label={t(visibilityKey(tool))} variant="neutral" />
              </HStack>

              {/* Installing puts the tool in the workspace; the switch decides
                  whether a plain question may reach it. Two acts, so two
                  controls - and the switch is dead while the tool holds a key,
                  which the server refuses anyway. Letting go of it is in the
                  menu, with the other things done once. */}
              <HStack className="mt-auto" gap={2} onClick={(event) => event.stopPropagation()} vAlign="center" width="100%">
                {tool.is_installed ? (
                  <Switch
                    isDisabled={isBusy || (tool.has_secret && tool.auth_type !== 'oauth2_user') || !canInstall}
                    label={t('tool.autoCall')}
                    onChange={(checked: boolean) => onAutoCall(checked)}
                    size="sm"
                    value={tool.auto_call}
                  />
                ) : canInstall ? (
                  <Button isLoading={isBusy} label={t('tool.install')} onClick={onInstall} size="sm" variant="secondary" />
                ) : null}
              </HStack>
            </VStack>
          </Section>
        </VStack>
      </Card>
    </CardContextMenu>
  );
}

// Four rungs, said plainly: kept to its author, open to the owning workspace,
// offered to named workspaces, offered to all of them.
function visibilityKey(tool: Tool) {
  if (tool.visibility === 'everyone') return 'tool.visEveryone' as const;
  if (tool.visibility === 'selected') return 'tool.visSelected' as const;
  if (tool.visibility === 'workspace') return 'agent.visibilityWorkspace' as const;
  return 'agent.visibilityPrivate' as const;
}
