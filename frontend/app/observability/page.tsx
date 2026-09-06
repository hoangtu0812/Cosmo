"use client";
import {useEffect,useState} from 'react';
import {useSearchParams} from 'next/navigation';
import {Button} from '@astryxdesign/core/Button';
import {HStack,Layout,LayoutContent,VStack} from '@astryxdesign/core/Layout';
import {Selector} from '@astryxdesign/core/Selector';
import {Table} from '@astryxdesign/core/Table';
import {Text} from '@astryxdesign/core/Text';
import {PageHeader} from '../components/PageHeader';
import {api,UsageSummary,Workspace} from '../lib/api';

const number=(value:number|null|undefined)=>value==null?'—':value.toLocaleString('vi-VN',{maximumFractionDigits:1});
export default function ObservabilityPage(){
 const search=useSearchParams();const requested=search.get('workspace')??'';
 const [workspaces,setWorkspaces]=useState<Workspace[]>([]);const [workspace,setWorkspace]=useState('');
 const [days,setDays]=useState('7');const [audience,setAudience]=useState('me');const [refresh,setRefresh]=useState(0);
 const [data,setData]=useState<UsageSummary|null>(null);const [error,setError]=useState('');
 useEffect(()=>{let active=true;Promise.all([api.me(),api.workspaces()]).then(([me,mine])=>{if(active){setWorkspaces(mine.workspaces);setWorkspace(requested||me.user.last_workspace_id||mine.workspaces[0]?.id||'');}}).catch((e:Error)=>{if(active)setError(e.message);});return()=>{active=false;};},[requested]);
 useEffect(()=>{if(!workspace)return;let active=true;setData(null);setError('');api.usage(workspace,Number(days),audience).then((value)=>{if(active)setData(value);}).catch((e:Error)=>{if(active)setError(e.message);});return()=>{active=false;};},[workspace,days,audience,refresh]);
 const admin=['owner','admin'].includes(workspaces.find(w=>w.id===workspace)?.role??'');
 return <Layout header={<PageHeader title="Số liệu sử dụng" />} content={<LayoutContent padding={6}><VStack gap={5} width="100%">
  <HStack gap={3} wrap="wrap">
   <Selector label="Workspace" value={workspace} options={workspaces.map(w=>({value:w.id,label:w.name}))} onChange={(id)=>{setAudience('me');setWorkspace(id);}} />
   <Selector label="Khoảng thời gian" value={days} onChange={setDays} options={[{value:'7',label:'7 ngày'},{value:'30',label:'30 ngày'},{value:'90',label:'90 ngày'},{value:'365',label:'365 ngày'}]} />
   <Selector label="Phạm vi" value={audience} onChange={setAudience} options={[{value:'me',label:'Của tôi'},...(admin?[{value:'workspace',label:'Toàn workspace'}]:[])]} />
   <Button label="Làm mới" variant="secondary" onClick={()=>setRefresh(value=>value+1)} />
  </HStack>
  {error?<Text type="supporting">{error}</Text>:!data?<Text type="supporting">Đang tải…</Text>:<>
   <HStack gap={6} wrap="wrap">
    {[['Phiên chạy',number(data.executions)],['Lần gọi model',number(data.model_calls)],['Token đã ghi nhận',number(data.known_total_tokens)],['Lần gọi thiếu usage',number(data.unknown_usage_calls)],['Thời gian phiên trung bình (ms)',number(data.avg_elapsed_ms)],['Chi phí ước tính (USD)',data.cost==null?'Chưa đủ usage / đơn giá':data.cost.toLocaleString('vi-VN',{maximumFractionDigits:6})]].map(([label,value])=><VStack key={label} gap={1}><Text type="supporting">{label}</Text><Text type="label">{value}</Text></VStack>)}
   </HStack>
   <Text type="label">Model và giai đoạn</Text>
   <Table data={data.groups} idKey={row=>`${row.model}/${row.phase}`} density="compact" columns={[
    {key:'model',header:'Model'},{key:'phase',header:'Giai đoạn'},{key:'calls',header:'Lần gọi'},
    {key:'total_tokens',header:'Token đã ghi nhận',renderCell:row=>number(row.total_tokens)},
    {key:'unknown_usage_calls',header:'Thiếu usage'},{key:'failed_calls',header:'Lỗi'},
    {key:'call_duration_ms',header:'Tổng thời gian gọi (ms)',renderCell:row=>number(row.call_duration_ms)},
   ]} />
   <Text type="label">Theo ngày (UTC)</Text>
   <Table data={data.daily} idKey="day" density="compact" columns={[{key:'day',header:'Ngày'},{key:'executions',header:'Phiên chạy'},{key:'failed_or_stopped',header:'Lỗi / đã dừng'},{key:'avg_elapsed_ms',header:'Thời gian trung bình (ms)',renderCell:row=>number(row.avg_elapsed_ms)}]} />
  </>}
 </VStack></LayoutContent>} />;
}
