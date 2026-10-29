import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SSEParser, backoffMs, keepStream, InboxClient } from '../lib/client.mjs';
import { startStub, TOKEN, envelope } from './stub-inbox.mjs';
import { mkdtempSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

function collect(chunks) {
  const out = [];
  const p = new SSEParser((ev) => out.push(ev));
  for (const c of chunks) p.push(c);
  return out;
}

test('parses events split across chunks and line endings', () => {
  const evs = collect(['event: mess', 'age\r', '\ndata: {"a"', ':1}\r\n\r', '\n: ping\n\ndata: x\rdata: y\r\r', '\n']);
  assert.deepEqual(evs, [
    { event: 'message', data: '{"a":1}', id: undefined },
    { comment: 'ping' },
    { event: 'message', data: 'x\ny', id: undefined },
  ]);
});

test('handles field without colon, id, single leading space only, and no-data blocks', () => {
  const evs = collect(['id: 7\nevent: message\ndata:  two spaces\n\nevent: empty\n\ndata\n\n']);
  assert.deepEqual(evs, [
    { event: 'message', data: ' two spaces', id: '7' },
    { event: 'message', data: '', id: undefined },
  ]);
});

test('an incomplete event is not dispatched', () => {
  assert.deepEqual(collect(['data: half']), []);
});

test('backoff doubles from 1 s and caps at 30 s', () => {
  assert.deepEqual([0, 1, 2, 3, 4, 5, 6, 40].map((n) => backoffMs(n)), [1000, 2000, 4000, 8000, 16000, 30000, 30000, 30000]);
});

test('keepStream reconnects after the stream closes and resets the backoff', async () => {
  const env = envelope();
  const stub = await startStub({
    onStream: (res, n) => {
      if (n === 1) return res.end(); // first stream drops at once
      res.write(`event: message\ndata: ${JSON.stringify(env)}\n\n`);
    },
  });
  const dir = mkdtempSync(join(tmpdir(), 'aic-sse-'));
  writeFileSync(join(dir, 'tok'), TOKEN);
  const client = new InboxClient({ baseUrl: stub.baseUrl, tokenFile: join(dir, 'tok') });
  const waits = [];
  const got = await new Promise((resolve) => {
    const s = keepStream(client, 'sess-1', {
      backoff: (n) => { waits.push(n); return 10; },
      onEvent: (ev) => { s.stop(); resolve(JSON.parse(ev.data)); },
    });
  });
  assert.equal(got.message_id, env.message_id);
  assert.deepEqual(waits, [0]);
  const streamCalls = stub.calls.filter((c) => c.url === '/v1/sessions/sess-1/events');
  assert.equal(streamCalls.length, 2);
  assert.equal(streamCalls[0].auth, `Bearer ${TOKEN}`);
  await stub.close();
});

test('keepStream treats a silent stream as dead', async () => {
  const stub = await startStub({ onStream: () => {} }); // only the initial ping, then silence
  const dir = mkdtempSync(join(tmpdir(), 'aic-sse-'));
  writeFileSync(join(dir, 'tok'), TOKEN);
  const client = new InboxClient({ baseUrl: stub.baseUrl, tokenFile: join(dir, 'tok') });
  await new Promise((resolve) => {
    let n = 0;
    const s = keepStream(client, 's', {
      idleMs: 50,
      backoff: () => { if (++n === 2) { s.stop(); resolve(); } return 5; },
      onEvent: () => {},
    });
  });
  assert.ok(stub.calls.filter((c) => c.url.endsWith('/events')).length >= 2);
  await stub.close();
});
