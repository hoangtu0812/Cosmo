'use client';

import {useEffect, useState} from 'react';
import {Button} from '@astryxdesign/core/Button';
import {Dialog, DialogHeader} from '@astryxdesign/core/Dialog';
import {HStack, Layout, LayoutContent, LayoutFooter, VStack} from '@astryxdesign/core/Layout';
import {Selector} from '@astryxdesign/core/Selector';
import {Text} from '@astryxdesign/core/Text';
import {TextArea} from '@astryxdesign/core/TextArea';
import {api, ToolActionPolicy, ToolCallResult, ToolWriteOperation} from '../lib/api';

const states: Record<string, string> = {executing:'Đang thực hiện', succeeded:'Đã nhận kết quả', uncertain:'Chưa xác định kết quả', reconciled_succeeded:'Đã đối soát: đã thực hiện', reconciled_no_effect:'Đã đối soát: chưa thực hiện'};

export function useToolWriteControl(toolID: string, actionID: string, workspaceID: string, isEditable: boolean, onResult: (result: ToolCallResult) => void, onFailure: (message: string) => void) {
  const [policy, setPolicy] = useState<ToolActionPolicy | null>(null);
  const [operation, setOperation] = useState<ToolWriteOperation | null>(null);
  const [pending, setPending] = useState<{args: Record<string, unknown>; key: string; policy: ToolActionPolicy} | null>(null);
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState('');
  const [outcome, setOutcome] = useState('');
  const [networkUnknown, setNetworkUnknown] = useState(false);
  const intentStorage = `cosmo.tool-write.${workspaceID}.${toolID}.${actionID}`;

  function recoverIntent(records: ToolWriteOperation[]) {
    const key = sessionStorage.getItem(intentStorage);
    if (key && records.some((item) => item.idempotency_key === key && ['succeeded', 'reconciled_succeeded', 'reconciled_no_effect'].includes(item.status))) {
      sessionStorage.removeItem(intentStorage);
    }
  }
  async function refresh() {
    const [review, records] = await Promise.all([api.toolActionPolicy(toolID, actionID, workspaceID), api.toolWriteOperations(toolID, workspaceID)]);
    setPolicy(review);
    recoverIntent(records.operations);
    const latest = records.operations.find((item) => item.action_id === actionID) ?? null;
    setOperation(latest);
    setNetworkUnknown(false);
    return {review, latest};
  }
  useEffect(() => {
    let active = true;
    Promise.all([api.toolActionPolicy(toolID, actionID, workspaceID), api.toolWriteOperations(toolID, workspaceID)])
      .then(([review, records]) => {if (active) {recoverIntent(records.operations);setPolicy(review);setOperation(records.operations.find((item) => item.action_id === actionID) ?? null);}})
      .catch((error: Error) => {if (active) onFailure(error.message);});
    return () => {active = false;};
    // Callback identities change with the editor; scope changes own this fetch.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [toolID, actionID, workspaceID, isEditable]);

  async function test(args: Record<string, unknown>) {
    const {review, latest} = await refresh();
    if (review.effect === 'blocked') throw new Error('Action đã bị chặn.');
    if (latest && ['executing', 'uncertain'].includes(latest.status)) throw new Error('Cần đối soát thao tác trước khi gửi lệnh mới.');
    if (['approval','approval_shared'].includes(review.effect)) {
      const effective = {...args};
      for (const parameter of review.parameters) {if (parameter.source === 'fixed') effective[parameter.name] = parameter.value;}
      const key = sessionStorage.getItem(intentStorage) ?? crypto.randomUUID();
      sessionStorage.setItem(intentStorage, key);
      setPending({args: structuredClone(effective), key, policy: review});
      return;
    }
    const response = await api.testToolAction(toolID, actionID, args, workspaceID);
    if (response.error) throw new Error(response.error.message);
    onResult(response.result);
  }
  async function confirm() {
    if (!pending || busy) return;
    setBusy(true);
    try {
      const response = await api.testToolAction(toolID, actionID, pending.args, workspaceID, {confirmed:true, idempotency_key:pending.key, definition:pending.policy.definition});
      setOperation(response.operation ?? null);
      onResult(response.result);
      if (response.error) onFailure(response.error.message);
      if (response.operation?.status === 'succeeded') sessionStorage.removeItem(intentStorage);
      setPending(null);
    } catch (error) {
      // Keep the session's exact key after a lost response. Check status before
      // presenting another intent; a transport error is not proof of no effect.
      setNetworkUnknown(true);
      setPending(null);
      onFailure(error instanceof Error ? error.message : 'Chưa xác định kết quả thao tác.');
    } finally {setBusy(false);}
  }
  async function changePolicy(effect: string) {
    if (!policy) return;
    setBusy(true);
    try {setPolicy(await api.setToolActionPolicy(toolID, actionID, workspaceID, effect, policy.definition));}
    catch (error) {onFailure(error instanceof Error ? error.message : 'Không thể lưu chính sách.');}
    finally {setBusy(false);}
  }
  async function reconcile() {
    if (!operation) return;
    setBusy(true);
    try {await api.reconcileToolWrite(toolID, workspaceID, operation.id, outcome, note);await refresh();sessionStorage.removeItem(intentStorage);setNote('');setOutcome('');}
    catch (error) {onFailure(error instanceof Error ? error.message : 'Không thể đối soát.');}
    finally {setBusy(false);}
  }
  const controls = <VStack gap={3} width="100%">
    {isEditable ? <Selector label="Chính sách action" value={policy?.effect} isDisabled={busy || !policy} onChange={(value) => void changePolicy(value)} options={[
      {value:'approval', label:'Chủ tool xác nhận'}, {value:'approval_shared',label:'Người sử dụng xác nhận'}, {value:'read', label:'Chỉ đọc — cho phép tự gọi'}, {value:'blocked', label:'Chặn action'},
    ]} /> : null}
    {operation ? <Text type="supporting">{`${states[operation.status] ?? operation.status} · ${operation.id}`}</Text> : null}
    {networkUnknown || operation?.status === 'executing' || operation?.status === 'uncertain' ?
      <Button label="Kiểm tra trạng thái" variant="secondary" isDisabled={busy} onClick={() => void refresh().catch((error: Error) => onFailure(error.message))} /> : null}
    {operation?.status === 'uncertain' ? <VStack gap={2} width="100%">
      <Text type="supporting">Kiểm tra hệ thống đích trước khi ghi nhận kết quả.</Text>
      <TextArea label="Nội dung đã duyệt" isReadOnly rows={6} value={JSON.stringify(operation.request, null, 2)} width="100%" />
      <Selector label="Kết quả đối soát" value={outcome} onChange={setOutcome} options={[
        {value:'reconciled_succeeded', label:'Đã thực hiện'}, {value:'reconciled_no_effect', label:'Chưa thực hiện'},
      ]} />
      <TextArea label="Bằng chứng / mã giao dịch" value={note} onChange={setNote} />
      <Button label="Ghi nhận đối soát" variant="secondary" isDisabled={busy || !outcome || note.trim().length < 5} onClick={() => void reconcile()} />
    </VStack> : null}
    <Dialog isOpen={pending !== null} onOpenChange={(open) => {if (!open && !busy) setPending(null);}} purpose="form">
      <Layout header={<DialogHeader title="Xác nhận thực hiện action" />} content={<LayoutContent>
        <VStack gap={3} width="100%">
          <Text type="body">Thao tác này có thể thay đổi dữ liệu trên hệ thống đích.</Text>
          <Text type="code">{pending?.policy.destination}</Text>
          <Text type="label">{pending?.policy.action}</Text>
          <Text type="code">{`${pending?.policy.method ?? ''} ${pending?.policy.path ?? ''}`}</Text>
          <TextArea isReadOnly label="Tham số sẽ gửi" rows={8} value={JSON.stringify(pending?.args ?? {}, null, 2)} width="100%" />
        </VStack>
      </LayoutContent>} footer={<LayoutFooter><HStack gap={2} hAlign="end">
        <Button label="Hủy" variant="secondary" isDisabled={busy} onClick={() => setPending(null)} />
        <Button label="Xác nhận và thực hiện" variant="primary" isLoading={busy} isDisabled={busy} onClick={() => void confirm()} />
      </HStack></LayoutFooter>} />
    </Dialog>
  </VStack>;
  return {test, controls, isBlocked:busy || networkUnknown || operation?.status === 'executing' || operation?.status === 'uncertain'};
}
