'use client';

import {useRef, useState} from 'react';
import {ImageUp, Trash2} from 'lucide-react';
import {Avatar} from '@astryxdesign/core/Avatar';
import {Banner} from '@astryxdesign/core/Banner';
import {Button} from '@astryxdesign/core/Button';
import {Grid} from '@astryxdesign/core/Grid';
import {IconButton} from '@astryxdesign/core/IconButton';
import {VStack} from '@astryxdesign/core/Layout';
import {Popover, PopoverTriggerRenderProps} from '@astryxdesign/core/Popover';
import {SegmentedControl, SegmentedControlItem} from '@astryxdesign/core/SegmentedControl';
import {Text} from '@astryxdesign/core/Text';
import {TextInput} from '@astryxdesign/core/TextInput';
import {useTranslation} from '../lib/i18n';
import {EMOJI_GROUPS, searchEmoji} from './avatars';
import {CHARACTER_GROUPS, CharacterGroup, characterDataURI, characterFile} from './characterAvatars';

// Group headings are looked up through a fixed map rather than a built key, so
// a group added without a translation fails to compile instead of rendering
// its own id to the reader.
const GROUP_LABELS = {
  work: 'agent.avatarGroupWork',
  knowledge: 'agent.avatarGroupKnowledge',
  industry: 'agent.avatarGroupIndustry',
  people: 'agent.avatarGroupPeople',
} as const;

const CHARACTER_LABELS = {
  people: 'agent.avatarGroupCharacters',
  robots: 'agent.avatarGroupRobots',
} as const;

// The largest upload the server keeps, checked here too so an oversized file
// is refused while it is still on the reader's machine rather than after a
// round trip that was always going to fail.
const MAX_AVATAR_BYTES = 256 * 1024;
const ACCEPTED = 'image/png,image/jpeg,image/webp,image/gif';

type Props = {
  value: string;
  file: File | null;
  onChangeEmoji: (emoji: string) => void;
  onChangeFile: (file: File | null) => void;
  imageURL?: string;
  t: ReturnType<typeof useTranslation>;
};

export function AgentAvatarPicker({value, file, onChangeEmoji, onChangeFile, imageURL, t}: Props) {
  const [isOpen, setIsOpen] = useState(false);
  const [tab, setTab] = useState('character');
  const [query, setQuery] = useState('');
  const [error, setError] = useState('');
  const inputRef = useRef<HTMLInputElement>(null);

  // A file chosen but not yet uploaded still has to be visible, so it is shown
  // from a local object URL until the agent exists to upload it to.
  const preview = file ? URL.createObjectURL(file) : imageURL;
  const matches = searchEmoji(query);

  function accept(candidate: File | undefined) {
    if (!candidate) return;
    if (!ACCEPTED.split(',').includes(candidate.type)) {
      setError(t('agent.avatarType'));
      return;
    }
    if (candidate.size > MAX_AVATAR_BYTES) {
      setError(t('agent.avatarTooBig'));
      return;
    }
    setError('');
    onChangeFile(candidate);
    setIsOpen(false);
  }

  function pickEmoji(emoji: string) {
    onChangeEmoji(emoji);
    setIsOpen(false);
  }

  // A generated character goes through the same upload path as a picture from
  // disk, so it becomes an ordinary agent image with nothing new to store.
  async function pickCharacter(group: CharacterGroup['id'], seed: string) {
    try {
      onChangeFile(await characterFile(group, seed));
      setError('');
      setIsOpen(false);
    } catch {
      setError(t('agent.avatarRenderFailed'));
    }
  }

  return (
    <Popover
      alignment="start"
      content={
        <VStack gap={3} padding={3} width={320}>
          <SegmentedControl label={t('agent.avatar')} onChange={setTab} size="sm" value={tab}>
            <SegmentedControlItem label={t('agent.avatarCharacter')} value="character" />
            <SegmentedControlItem label={t('agent.avatarEmoji')} value="emoji" />
            <SegmentedControlItem label={t('agent.avatarUpload')} value="upload" />
          </SegmentedControl>

          {tab === 'character' ? (
            <VStack gap={3} height={280} isScrollable width="100%">
              {CHARACTER_GROUPS.map((group) => (
                <VStack gap={1} key={group.id} width="100%">
                  <Text color="secondary" type="supporting">{t(CHARACTER_LABELS[group.id])}</Text>
                  <Grid columns={{minWidth: 40, max: 6}} gap={1} width="100%">
                    {group.seeds.map((seed) => (
                      <IconButton
                        icon={<Avatar name={seed} size="sm" src={characterDataURI(group.id, seed)} tooltip={false} />}
                        key={seed}
                        label={seed}
                        onClick={() => void pickCharacter(group.id, seed)}
                        variant="ghost"
                      />
                    ))}
                  </Grid>
                </VStack>
              ))}
              {error ? <Banner status="error" title={error} /> : null}
            </VStack>
          ) : tab === 'emoji' ? (
            <VStack gap={3} isScrollable height={280} width="100%">
              <TextInput
                isLabelHidden
                label={t('agent.avatarSearch')}
                onChange={setQuery}
                placeholder={t('agent.avatarSearch')}
                value={query}
                width="100%"
              />
              {query.trim() ? (
                matches.length === 0 ? (
                  <Text color="secondary" type="supporting">{t('agent.avatarNoMatch')}</Text>
                ) : (
                  <Grid columns={{minWidth: 40, max: 6}} gap={1} width="100%">
                    {matches.map((item) => (
                      <Button
                        icon={<Text type="body">{item.char}</Text>}
                        isIconOnly
                        key={item.char}
                        label={item.keywords}
                        onClick={() => pickEmoji(item.char)}
                        variant={item.char === value ? 'secondary' : 'ghost'}
                      />
                    ))}
                  </Grid>
                )
              ) : (
                EMOJI_GROUPS.map((group) => (
                  <VStack gap={1} key={group.id} width="100%">
                    <Text color="secondary" type="supporting">{t(GROUP_LABELS[group.id as keyof typeof GROUP_LABELS])}</Text>
                    <Grid columns={{minWidth: 40, max: 6}} gap={1} width="100%">
                      {group.emoji.map((item) => (
                        <Button
                          icon={<Text type="body">{item.char}</Text>}
                          isIconOnly
                          key={item.char}
                          label={item.keywords}
                          onClick={() => pickEmoji(item.char)}
                          variant={item.char === value ? 'secondary' : 'ghost'}
                        />
                      ))}
                    </Grid>
                  </VStack>
                ))
              )}
            </VStack>
          ) : (
            <VStack gap={3} hAlign="center" padding={4} width="100%">
              <ImageUp size={28} strokeWidth={1.5} />
              <Text color="secondary" type="supporting">{t('agent.avatarUploadHint')}</Text>
              <Button label={t('agent.avatarChoose')} onClick={() => inputRef.current?.click()} size="sm" variant="secondary" />
              {file ? (
                <Button
                  icon={<Trash2 size={14} />}
                  label={t('agent.avatarRemove')}
                  onClick={() => { onChangeFile(null); setIsOpen(false); }}
                  size="sm"
                  variant="ghost"
                />
              ) : null}
              {error ? <Banner status="error" title={error} /> : null}
              <input
                accept={ACCEPTED}
                hidden
                onChange={(event) => accept(event.target.files?.[0])}
                ref={inputRef}
                type="file"
              />
            </VStack>
          )}
        </VStack>
      }
      isOpen={isOpen}
      label={t('agent.avatar')}
      onOpenChange={setIsOpen}
      placement="below"
    >
      {/* The render-prop form is what wires the trigger: Popover needs the
          ref to anchor against and supplies its own toggle and ARIA state. */}
      {({ref, onClick, ...aria}: PopoverTriggerRenderProps) => (
        <IconButton
          {...aria}
          icon={preview ? <Avatar name={value} size="sm" src={preview} /> : <Text type="body">{value || '🤖'}</Text>}
          label={t('agent.avatar')}
          onClick={onClick}
          ref={ref}
          variant="secondary"
        />
      )}
    </Popover>
  );
}
