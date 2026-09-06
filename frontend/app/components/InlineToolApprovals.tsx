'use client';

import {createContext, useContext, useEffect, useState} from 'react';
import {useRouter} from 'next/navigation';
import {Collapsible} from '@astryxdesign/core/Collapsible';
import {Button} from '@astryxdesign/core/Button';
import {HStack, VStack} from '@astryxdesign/core/Layout';
import {Text} from '@astryxdesign/core/Text';
import {TextArea} from '@astryxdesign/core/TextArea';
import {approvalsForCall} from '../lib/tool-approval-anchor';
import {api, MessageToolCall, ToolApproval} from '../lib/api';

type ApprovalScope = {workspaceID: string; kind: 'conversation' | 'workflow'; sourceID: string};
function useApprovals({workspaceID, kind, sourceID}: ApprovalScope) {
  const [items, setItems] = useState<ToolApproval[]>([]);
  const [busy, setBusy] = useState('');
  const [error, setError] = useState('');
  useEffect(() => {
    let active = true;
    let timer: ReturnType<typeof setTimeout>;
    async function poll() {
      try {
        const response = await api.toolApprovals(workspaceID, kind, sourceID);
        if (active) {setItems(response.approvals);setError('');}
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
  return {items, busy, error, decide, workspaceID};
}

type ApprovalState = ReturnType<typeof useApprovals>;
const ApprovalContext = createContext<ApprovalState | null>(null);
export function ToolApprovalProvider({children, ...scope}: ApprovalScope & {children: React.ReactNode}) {
  const state = useApprovals(scope);
  return <ApprovalContext.Provider value={state}>{children}</ApprovalContext.Provider>;
}
export function ToolCallApproval({call, messageID}: {call: MessageToolCall; messageID?: string}) {
  const state = useContext(ApprovalContext);
  if (!state) return null;
  const items = approvalsForCall(state.items, call, messageID);
  if (!items.length) return call.approval_id && state.error ? <Text type="supporting">{state.error}</Text> : null;
  return <ApprovalCards {...state} items={items} />;
}
export function InlineToolApprovals(scope: ApprovalScope) {
  return <ApprovalCards {...useApprovals(scope)} />;
}
function ApprovalCards({items, busy, error, decide, workspaceID}: ApprovalState) {
  const router = useRouter();
  const visible = items.filter((item) => ['pending','approved','uncertain'].includes(item.status));
  if (!visible.length && !error) return null;
  return <VStack gap={3} width="100%">
    {error ? <Text type="supporting">{error}</Text> : null}
    {visible.map((item) => <VStack key={item.id} gap={2} padding={3} width="100%">
      <Text type="label">{item.status === 'pending' ? 'Xác nhận thao tác' : item.status === 'approved' ? 'Đã xác nhận · đang thực hiện' : 'Chưa xác định kết quả · cần đối soát'}</Text>
      <Collapsible key={item.status === 'pending' ? 'review' : 'receipt'} defaultIsOpen={item.status === 'pending'} trigger={<Text type="supporting">{item.request.action}</Text>}>
        <VStack gap={2} width="100%">
          <Text type="code">{item.request.destination}</Text>
          <Text type="code">{`${item.request.method} ${item.request.path}`}</Text>
          <TextArea label={item.status === 'pending' ? 'Tham số sẽ gửi' : 'Tham số đã duyệt'} isReadOnly rows={5} width="100%" value={JSON.stringify(item.request.arguments,null,2)} />
        </VStack>
      </Collapsible>
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
