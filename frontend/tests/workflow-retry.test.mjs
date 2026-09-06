import assert from 'node:assert/strict';
import test from 'node:test';
import {streamWorkflowRun} from '../app/lib/api.ts';

test('lost admission retries the same request, then reconnects by execution and cursor', async (t) => {
  const requests = [];
  const steps = [];
  t.mock.method(globalThis, 'fetch', async (url, init) => {
    requests.push({url, ...init});
    if (requests.length === 1) throw new TypeError('network lost after admission');
    return new Response(requests.length === 2
      ? 'event: execution\ndata: {"id":"saved-run"}\n\nid: 10\nevent: step\ndata: {"node_id":"first","status":"completed"}\n\n'
      : 'id: 11\nevent: step\ndata: {"node_id":"last","status":"completed"}\n\nid: 12\nevent: done\ndata: {}\n\n');
  });
  let completed = 0;
  await streamWorkflowRun('retry-workflow', 'input', 'workspace', {onStep: (step) => steps.push(step.node_id), onDone: () => completed++});
  assert.equal(requests.length, 3);
  assert.equal(JSON.parse(requests[0].body).request_id, JSON.parse(requests[1].body).request_id);
  assert.equal(requests[2].method, 'GET');
  assert.match(requests[2].url, /executions\/saved-run\/events/);
  assert.equal(requests[2].headers['Last-Event-ID'], '10');
  assert.deepEqual(steps, ['first', 'last']);
  assert.equal(completed, 1);
});

test('following after reload never submits work and server errors stop retry', async (t) => {
  let calls = 0;
  t.mock.method(globalThis, 'fetch', async (url, init) => {
    calls++;
    assert.equal(init.method, 'GET');
    assert.equal(init.body, undefined);
    assert.match(url, /executions\/existing\/events/);
    return new Response('event: error\ndata: {"message":"Stopped"}\n\n');
  });
  await assert.rejects(streamWorkflowRun('follow-workflow', '', 'workspace', {onStep() {}}, 'existing', true), /Stopped/);
  assert.equal(calls, 1);
});
