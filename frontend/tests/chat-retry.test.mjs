import assert from 'node:assert/strict';
import test from 'node:test';
import {streamChat} from '../app/lib/api.ts';

test('an interrupted stream keeps its request identity until a saved answer arrives', async (t) => {
  const bodies = [];
  const storage = new Map();
  Object.defineProperty(globalThis, 'sessionStorage', {configurable: true, value: {getItem: (key) => storage.get(key) ?? null, setItem: (key, value) => storage.set(key, value), removeItem: (key) => storage.delete(key)}});
  t.after(() => {delete globalThis.sessionStorage;});
  t.mock.method(globalThis, 'fetch', async (_url, init) => {
    bodies.push(JSON.parse(init.body));
    const frame = bodies.length === 1
      ? 'event: delta\ndata: {"content":"partial"}\n\n'
      : 'event: done\ndata: {"message":{"id":"answer","content":"saved"}}\n\n';
    return new Response(frame, {headers: {'Content-Type': 'text/event-stream'}});
  });
  await assert.rejects(streamChat('retry-conversation', 'Question', {}, {onDelta() {}}), /gián đoạn/);
  assert.equal(storage.size, 1);
  assert.ok([...storage.keys()].every((key) => !key.includes('Question')));
  let answer;
  await streamChat('retry-conversation', 'Question', {}, {onDelta() {}, onDone: (data) => {answer = data.message;}});
  assert.equal(answer.content, 'saved');
  assert.equal(bodies[0].client_message_id, bodies[1].client_message_id);
  assert.equal(storage.size, 0);
  await streamChat('retry-conversation', 'Question', {}, {onDelta() {}});
  assert.notEqual(bodies[2].client_message_id, bodies[1].client_message_id);
});

test('an explicit client identity is sent unchanged and HTTP failure is surfaced', async (t) => {
  t.mock.method(globalThis, 'fetch', async (_url, init) => {
    assert.equal(JSON.parse(init.body).client_message_id, 'explicit-retry-id');
    return new Response('{"error":{"message":"Câu hỏi đang được xử lý."}}', {status: 409});
  });
  await assert.rejects(streamChat('another-conversation', 'Question', {clientMessageID: 'explicit-retry-id'}, {onDelta() {}}), /đang được xử lý/);
});
