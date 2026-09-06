import assert from 'node:assert/strict';
import test from 'node:test';
import {approvalsForCall} from '../app/lib/tool-approval-anchor.ts';
const items = [
  {id:'approval-old', message_id:'answer-old', call_id:'reused-call', status:'uncertain'},
  {id:'approval-new', message_id:'answer-new', call_id:'reused-call', status:'pending'},
];
test('legacy approval stays on its original message when a call ID repeats', () => {
  assert.deepEqual(approvalsForCall(items,{id:'reused-call'},'answer-old'),[items[0]]);
  assert.deepEqual(approvalsForCall(items,{id:'reused-call'},'answer-new'),[items[1]]);
  assert.deepEqual(approvalsForCall(items,{id:'clock-call'},'answer-clock'),[]);
  assert.deepEqual(approvalsForCall(items,{id:'reused-call'}),[]);
});
test('live approval uses its durable ID before the stream message gets its final ID', () => {
  assert.deepEqual(approvalsForCall(items,{id:'reused-call',approval_id:'approval-new'},'stream-temp'),[items[1]]);
  assert.deepEqual(approvalsForCall(items,{id:'reused-call',approval_id:'missing'},'answer-old'),[]);
});
