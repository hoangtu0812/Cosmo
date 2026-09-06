'use client';

import {useEffect, useState} from 'react';
import {Button} from '@astryxdesign/core/Button';
import {VStack} from '@astryxdesign/core/Layout';
import {Text} from '@astryxdesign/core/Text';
import {api, WorkflowExecution} from '../lib/api';

const labels: Record<string,string> = {running:'Đang chạy',succeeded:'Hoàn tất',interrupted:'Bị gián đoạn',failed:'Thất bại'};
export function WorkflowExecutionHistory({workflowID,workspaceID,isRunning,onResume}: {workflowID:string;workspaceID:string;isRunning:boolean;onResume:(id:string)=>void}) {
  const [items,setItems] = useState<WorkflowExecution[]>([]);
  const [error,setError] = useState('');
  useEffect(() => {
    let active = true;
    let timer: ReturnType<typeof setTimeout>;
    async function poll() {
      try {const response=await api.workflowExecutions(workflowID,workspaceID);if(active){setItems(response.executions);setError('');}}
      catch(caught){if(active)setError(caught instanceof Error ? caught.message : 'Không tải được tiến độ.');}
      finally{if(active)timer=setTimeout(() => void poll(),3000);}
    }
    void poll();
    return () => {active=false;clearTimeout(timer);};
  },[workflowID,workspaceID]);
  if(!items.length && !error)return null;
  return <VStack gap={2} width="100%">
    <Text type="label">Phiên chạy đã lưu</Text>
    {error ? <Text type="supporting">{error}</Text> : null}
    {items.slice(0,5).map((item) => <VStack key={item.id} gap={1} width="100%">
      <Text type="supporting">{`${new Date(item.created_at).toLocaleString()} · ${labels[item.status] ?? item.status} · ${Object.keys(item.completed).length} bước đã lưu`}</Text>
      {item.status==='interrupted' ? <Button label="Tiếp tục phiên đã lưu" variant="secondary" isDisabled={isRunning || items.some((run) => run.status==='running')} onClick={() => onResume(item.id)} /> : null}
    </VStack>)}
  </VStack>;
}
