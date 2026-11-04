import { test } from 'node:test';
import assert from 'node:assert/strict';
import { appendFileSync, mkdtempSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { classifyRecord, TranscriptWatcher } from '../lib/receipt.mjs';

const ID = 'm-01M6KXGZZ8QYVQC1X4WKQ7MFM3';
const tag = (id = ID, source = 'aic-inbox') => `<channel source="${source}" message_id="${id}" context_id="local/shop!1" task_id="f-1a2b3c4d">\n[aic-inbox] To act…`;
const CH = { kind: 'channel', server: 'aic-inbox' };

// The three record shapes seen in Claude Code 2.1.303 transcripts (D42 tests).
const enqueue = (id) => ({ type: 'queue-operation', operation: 'enqueue', content: tag(id), timestamp: 't' });
const idle = (id) => ({ type: 'user', isMeta: true, origin: CH, message: { role: 'user', content: tag(id) } });
const busy = (id) => ({ type: 'attachment', attachment: { type: 'queued_command', origin: CH, prompt: tag(id), commandMode: 'prompt' } });
const line = (r) => JSON.stringify(r) + '\n';

test('classifyRecord reads enqueue and both delivery shapes', () => {
  assert.deepEqual(classifyRecord(enqueue(ID)), { kind: 'enqueued', id: ID });
  assert.deepEqual(classifyRecord(idle(ID)), { kind: 'delivered', id: ID });
  assert.deepEqual(classifyRecord(busy(ID)), { kind: 'delivered', id: ID });
  // A plugin channel's source and origin carry a prefix.
  const plugin = { type: 'user', origin: { kind: 'channel', server: 'plugin:aic-inbox:aic-inbox' }, message: { content: tag(ID, 'plugin:aic-inbox:aic-inbox') } };
  assert.deepEqual(classifyRecord(plugin), { kind: 'delivered', id: ID });
});

test('classifyRecord ignores other sources, human prompts and ids inside the data', () => {
  assert.equal(classifyRecord({ ...idle(ID), origin: { kind: 'human' } }), null);
  assert.equal(classifyRecord({ ...idle(ID), origin: { kind: 'channel', server: 'webhook' } }), null);
  assert.equal(classifyRecord({ type: 'user', origin: CH, message: { content: tag(ID, 'webhook') } }), null);
  // A human prompt that quotes a channel tag is not a receipt.
  assert.equal(classifyRecord({ type: 'user', origin: { kind: 'human' }, message: { content: tag(ID) } }), null);
  // An id that is not at the start of the tag does not count.
  assert.equal(classifyRecord({ type: 'user', origin: CH, message: { content: `note: message_id="${ID}"` } }), null);
  assert.equal(classifyRecord({ type: 'queue-operation', operation: 'remove', content: tag(ID) }), null);
  assert.equal(classifyRecord(null), null);
  assert.equal(classifyRecord({ type: 'user', origin: CH, message: { content: [{ type: 'text', text: tag(ID) }] } }), null);
});

test('TranscriptWatcher reads only new complete lines, from the end by default', () => {
  const dir = mkdtempSync(join(tmpdir(), 'aic-receipt-'));
  const file = join(dir, 't.jsonl');
  writeFileSync(file, line(idle('m-OLD')));
  const w = new TranscriptWatcher(file);
  assert.deepEqual(w.scan(), [], 'history of a resumed session is skipped');
  const full = line(enqueue(ID));
  appendFileSync(file, full.slice(0, 40));
  assert.deepEqual(w.scan(), [], 'an incomplete line waits');
  appendFileSync(file, full.slice(40) + line({ type: 'mode', mode: 'normal' }) + 'not json with message_id=\n' + line(busy(ID)));
  assert.deepEqual(w.scan(), [{ kind: 'enqueued', id: ID }, { kind: 'delivered', id: ID }]);
  assert.deepEqual(w.scan(), []);
  assert.deepEqual(new TranscriptWatcher(file, { start: 'start' }).scan().map((r) => r.id), ['m-OLD', ID, ID]);
});

test('TranscriptWatcher waits for a missing file and starts over on a truncated one', () => {
  const dir = mkdtempSync(join(tmpdir(), 'aic-receipt-'));
  const file = join(dir, 't.jsonl');
  const w = new TranscriptWatcher(file);
  assert.deepEqual(w.scan(), []);
  writeFileSync(file, line(idle(ID)));
  assert.deepEqual(w.scan(), [{ kind: 'delivered', id: ID }], 'a file created later is read from its start');
  writeFileSync(file, line(enqueue('m-NEW')));
  assert.deepEqual(w.scan(), [{ kind: 'enqueued', id: 'm-NEW' }]);
});

test('TranscriptWatcher keeps a multi-byte character split across reads', () => {
  const dir = mkdtempSync(join(tmpdir(), 'aic-receipt-'));
  const file = join(dir, 't.jsonl');
  const w = new TranscriptWatcher(file);
  const rec = idle(ID);
  rec.message.content += ' café ✓';
  const bytes = Buffer.from(line(rec));
  const cut = bytes.indexOf(Buffer.from('✓')) + 1; // inside the 3-byte character
  writeFileSync(file, bytes.subarray(0, cut));
  assert.deepEqual(w.scan(), []);
  appendFileSync(file, bytes.subarray(cut));
  assert.deepEqual(w.scan(), [{ kind: 'delivered', id: ID }]);
});
