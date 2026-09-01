'use client';

import {useState} from 'react';
import {Button} from '@astryxdesign/core/Button';
import {Grid} from '@astryxdesign/core/Grid';
import {Popover, PopoverTriggerRenderProps} from '@astryxdesign/core/Popover';
import {Text} from '@astryxdesign/core/Text';
import {VStack} from '@astryxdesign/core/Layout';

const KNOWLEDGE_ICONS = [
  {value: '📚', label: 'Thư viện'},
  {value: '📖', label: 'Sổ tay'},
  {value: '📄', label: 'Tài liệu'},
  {value: '🔍', label: 'Tra cứu'},
  {value: '🧠', label: 'Tri thức'},
  {value: '🗂️', label: 'Hồ sơ'},
  {value: '🏭', label: 'Nhà máy'},
  {value: '🧰', label: 'Vận hành'},
  {value: '🛡️', label: 'An toàn'},
  {value: '⚙️', label: 'Kỹ thuật'},
  {value: '📊', label: 'Báo cáo'},
  {value: '✨', label: 'AI'},
] as const;

export function KnowledgeIconPicker({value, onChange}: {value: string; onChange: (value: string) => void}) {
  const [isOpen, setIsOpen] = useState(false);

  return (
    <Popover
      alignment="start"
      content={
        <VStack gap={2} padding={3} width={260}>
          <Text type="label">Biểu tượng</Text>
          <Grid columns={{minWidth: 44, max: 4}} gap={1} width="100%">
            {KNOWLEDGE_ICONS.map((item) => (
              <Button
                icon={<Text type="body">{item.value}</Text>}
                isIconOnly
                key={item.value}
                label={item.label}
                onClick={() => { onChange(item.value); setIsOpen(false); }}
                variant={item.value === value ? 'secondary' : 'ghost'}
              />
            ))}
          </Grid>
        </VStack>
      }
      isOpen={isOpen}
      label="Chọn biểu tượng"
      onOpenChange={setIsOpen}
      placement="below"
    >
      {({ref, onClick, ...aria}: PopoverTriggerRenderProps) => (
        <Button
          {...aria}
          icon={<Text type="body">{value || '📚'}</Text>}
          isIconOnly
          label="Chọn biểu tượng"
          onClick={onClick}
          ref={ref}
          variant="secondary"
        />
      )}
    </Popover>
  );
}
