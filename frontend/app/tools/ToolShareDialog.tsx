'use client';

import {useEffect, useState} from 'react';
import {Banner} from '@astryxdesign/core/Banner';
import {Button} from '@astryxdesign/core/Button';
import {Card} from '@astryxdesign/core/Card';
import {CheckboxList, CheckboxListItem} from '@astryxdesign/core/CheckboxList';
import {Dialog, DialogHeader} from '@astryxdesign/core/Dialog';
import {HStack, Layout, LayoutContent, LayoutFooter, VStack} from '@astryxdesign/core/Layout';
import {Section} from '@astryxdesign/core/Section';
import {Selector} from '@astryxdesign/core/Selector';
import {Text} from '@astryxdesign/core/Text';
import {Tool, WorkspaceRef, api} from '../lib/api';
import {useTranslation} from '../lib/i18n';

/**
 * Who a tool is offered to.
 *
 * Deliberately the same dialog a knowledge base gets, because it answers the
 * same question about a different thing - and two dialogs that ask the same
 * question differently make a reader learn it twice.
 *
 * An offer is not an installation. Another workspace still has to install it,
 * and installing still does not let a model call it on its own; that is a
 * third decision, made where the tool is installed.
 */
export function ToolShareDialog({tool, directory, onClose, onError, onSaved}: {
  tool: Tool;
  directory: WorkspaceRef[];
  onClose: () => void;
  onError: (value: string) => void;
  onSaved: (visibility: Tool['visibility']) => void;
}) {
  const t = useTranslation();
  const [visibility, setVisibility] = useState(tool.visibility);
  const [selected, setSelected] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api.toolShares(tool.id, tool.workspace_id)
      .then((result) => setSelected(result.workspaces))
      .catch(() => setSelected([]));
  }, [tool.id, tool.workspace_id]);

  async function save() {
    setBusy(true);
    try {
      // The list is saved whatever the visibility, so switching to everyone
      // and back does not lose it.
      await api.setToolShares(tool.id, selected, tool.workspace_id);
      await api.updateTool(tool.id, {visibility}, tool.workspace_id);
      onSaved(visibility);
    } catch (caught) {
      onError(caught instanceof Error ? caught.message : t('tool.saveFailed'));
    } finally {
      setBusy(false);
    }
  }

  // The owning workspace always has the tool; offering it there would suggest
  // the offer could be withdrawn from its owner.
  const others = directory.filter((item) => item.id !== tool.workspace_id);

  return (
    <Dialog isOpen onOpenChange={onClose} purpose="form" width={520}>
      <Layout
        content={
          <LayoutContent>
            <VStack gap={4}>
              <Selector
                label={t('tool.visibility')}
                onChange={(value) => setVisibility(value as Tool['visibility'])}
                options={[
                  {value: 'private', label: t('tool.visPrivate')},
                  {value: 'workspace', label: t('tool.visWorkspace')},
                  {value: 'selected', label: t('tool.visSelected')},
                  {value: 'everyone', label: t('tool.visEveryone')},
                ]}
                value={visibility}
                width="100%"
              />

              {visibility === 'selected' ? (
                <Card padding={0} width="100%">
                  <Section dividers={['bottom']} padding={3}>
                    <Text type="label">{t('tool.sharedWorkspaces')}</Text>
                  </Section>
                  <Section padding={3}>
                    {others.length === 0 ? (
                      <Text color="secondary" type="supporting">{t('tool.noOtherWorkspaces')}</Text>
                    ) : (
                      <CheckboxList
                        isLabelHidden
                        label={t('tool.sharedWorkspaces')}
                        onChange={setSelected}
                        value={selected}
                      >
                        {others.map((item) => (
                          <CheckboxListItem key={item.id} label={item.name} value={item.id} />
                        ))}
                      </CheckboxList>
                    )}
                  </Section>
                </Card>
              ) : null}

              {tool.has_secret && (visibility === 'selected' || visibility === 'everyone') ? (
                // Worth saying here rather than at the switch: the credential
                // travels with the tool, and a workspace that installs it will
                // be calling the owner's API with the owner's key.
                <Banner status="warning" title={t('tool.shareKeyWarning')} />
              ) : null}

              <Text color="secondary" type="supporting">{t('tool.shareHint')}</Text>
            </VStack>
          </LayoutContent>
        }
        footer={
          <LayoutFooter>
            <HStack gap={2} hAlign="end" width="100%">
              <Button label={t('common.cancel')} onClick={onClose} variant="secondary" />
              <Button isLoading={busy} label={t('tool.save')} onClick={() => void save()} variant="primary" />
            </HStack>
          </LayoutFooter>
        }
        header={<DialogHeader onOpenChange={onClose} title={t('tool.shareTitle', {name: tool.name})} />}
      />
    </Dialog>
  );
}
