'use client';

import {useEffect, useState} from 'react';
import {useRouter} from 'next/navigation';
import {Button} from '@astryxdesign/core/Button';
import {HStack, VStack} from '@astryxdesign/core/Layout';
import {Text} from '@astryxdesign/core/Text';
import {TextArea} from '@astryxdesign/core/TextArea';
import {api, ToolApproval} from '../lib/api';

export function InlineToolApprovals({workspaceID, kind, sourceID}: {workspaceID: string; kind: 'conversation' | 'workflow'; sourceID: string}) {
  const router = useRouter();
  const [items, setItems] = useState<ToolApproval[]>([]);
  const [busy, setBusy] = useState('');
  const [error, setError] = useState('');
  useEffect(() => {
    let active = true;
    let timer: ReturnType<typeof setTimeout>;
    async function poll() {
      try {
        const response = await api.toolApprovals(workspaceID, kind, sourceID);
        if (active) setItems(response.approvals);
      } catch (caught) {
        if (active) setError(caught instanceof Error ? caught.message : 'Không tải được yêu cầu xác nhận.');
      } finally {if (active) timer = setTimeout(() => void poll(), 2000);}
    }
    if (workspaceID && sourceID) void poll();
    return () => {active = false; clearTimeout(timer);};
  }, [workspaceID, kind, sourceID]);

  async function decide(item: ToolApproval, decision: string) {
    setBusy(item.id); setError('');
    try {
      await api.decideToolApproval(item.id, workspaceID, decision, item.request.definition);
      setItems((current) => current.map((value) => value.id === item.id ? {...value, status:decision} : value));
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : 'Chưa xác định trạng thái xác nhận.');
    } finally {setBusy('');}
  }
  const visible = items.filter((item) => ['pending','approved','uncertain'].includes(item.status));
  if (!visible.length && !error) return null;
  return <VStack gap={3} width="100%">
    {error ? <Text type="supporting">{error}</Text> : null}
    {visible.map((item) => <VStack key={item.id} gap={2} padding={3} width="100%">
      <Text type="label">{item.status === 'pending' ? 'Xác nhận thao tác' : item.status === 'approved' ? 'Đã xác nhận · đang thực hiện' : 'Chưa xác định kết quả · cần đối soát'}</Text>
      <Text type="label">{item.request.action}</Text>
      <Text type="code">{item.request.destination}</Text>
      <Text type="code">{`${item.request.method} ${item.request.path}`}</Text>
      <TextArea label="Tham số sẽ gửi" isReadOnly rows={5} width="100%" value={JSON.stringify(item.request.arguments,null,2)} />
      {item.status === 'pending' ? <>
        <Text type="supporting">{`Có thể thay đổi dữ liệu. Hết hạn lúc ${new Date(item.expires_at).toLocaleTimeString()}.`}</Text>
        <HStack gap={2} wrap="wrap">
          <Button label="Từ chối" variant="secondary" isDisabled={!!busy} onClick={() => void decide(item,'rejected')} />
          <Button label="Xác nhận và tiếp tục" variant="primary" isDisabled={!!busy || Date.now() >= Date.parse(item.expires_at)} onClick={() => void decide(item,'approved')} />
        </HStack>
      </> : null}
      {item.status === 'uncertain' ? <Button label="Mở tool để đối soát" variant="secondary" onClick={() => router.push(`/tools/${encodeURIComponent(item.tool_id)}?workspace=${encodeURIComponent(workspaceID)}`)} /> : null}
    </VStack>)}
  </VStack>;
}
