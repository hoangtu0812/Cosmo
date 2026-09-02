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
export function ToolCard({tool, actions, isBusy, onOpen, onInstall, onAutoCall}: {
  tool: Tool;
  actions: CardMenuItems;
  isBusy: boolean;
  onOpen: () => void;
  onInstall: () => void;
  onAutoCall: (autoCall: boolean) => void;
}) {
  const t = useTranslation();

  return (
    <CardContextMenu items={actions} label={t('kb.manage')}>
      <Card className={cardHover} onClick={onOpen} padding={0} width="100%">
        <VStack gap={0} height="100%">
          <Section padding={5} variant="muted">
            <HStack hAlign="center" width="100%">
              <Card padding={3}>
                <Text type="display-3">{tool.icon || '🔌'}</Text>
              </Card>
            </HStack>
          </Section>
          <Section padding={4}>
            <VStack gap={2}>
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
                <StatusLabel label={t(visibilityKey(tool))} variant="neutral" />
              </HStack>

              {/* Installing puts the tool in the workspace; the switch decides
                  whether a plain question may reach it. Two acts, so two
                  controls - and the switch is dead while the tool holds a key,
                  which the server refuses anyway. Letting go of it is in the
                  menu, with the other things done once. */}
              <HStack gap={2} onClick={(event) => event.stopPropagation()} vAlign="center" width="100%">
                {tool.is_installed ? (
                  <Switch
                    isDisabled={isBusy || tool.has_secret}
                    label={t('tool.autoCall')}
                    onChange={(checked: boolean) => onAutoCall(checked)}
                    size="sm"
                    value={tool.auto_call}
                  />
                ) : (
                  <Button isLoading={isBusy} label={t('tool.install')} onClick={onInstall} size="sm" variant="secondary" />
                )}
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
