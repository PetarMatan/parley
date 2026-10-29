import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, readdirSync, statSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { findClaudePid, writeBridge, readBridge, removeBridge, waitForBridge, bridgePath, psInfo } from '../lib/bridge.mjs';

const fakePs = (table) => (pid) => table[pid] ?? null;

test('findClaudePid walks parents to the first claude', () => {
  const ps = fakePs({
    100: { ppid: 90, comm: 'node' },
    90: { ppid: 80, comm: '/bin/zsh' },
    80: { ppid: 70, comm: '/Users/x/.local/bin/claude' },
    70: { ppid: 1, comm: 'claude' },
  });
  assert.equal(findClaudePid(100, { ps }), 80);
});

test('findClaudePid returns null without a claude ancestor or on a vanished process', () => {
  assert.equal(findClaudePid(100, { ps: fakePs({ 100: { ppid: 90, comm: 'node' }, 90: { ppid: 1, comm: 'zsh' } }) }), null);
  assert.equal(findClaudePid(100, { ps: fakePs({ 100: { ppid: 90, comm: 'node' } }) }), null);
  assert.equal(findClaudePid(100, { ps: fakePs({ 100: { ppid: 100, comm: 'claudette' } }) , maxDepth: 3 }), null);
});

test('psInfo reads this process', () => {
  const me = psInfo(process.pid);
  assert.equal(me.ppid, process.ppid);
  assert.match(me.comm, /node/);
});

test('bridge file round trip', () => {
  const dir = mkdtempSync(join(tmpdir(), 'aic-bridge-'));
  assert.equal(readBridge(dir, 4242), null);
  const w = writeBridge(dir, 4242, { sessionId: 'sess-1', cwd: dir });
  assert.deepEqual(Object.keys(w), ['session_id', 'cwd', 'claude_pid', 'ts']);
  assert.match(w.ts, /^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z$/);
  assert.deepEqual(readBridge(dir, 4242), w);
  assert.equal(statSync(bridgePath(dir, 4242)).mode & 0o777, 0o600);
  assert.deepEqual(readdirSync(join(dir, '.aic', 'sessions')), ['4242.json']); // no temp file left
  removeBridge(dir, 4242);
  removeBridge(dir, 4242); // idempotent
  assert.equal(readBridge(dir, 4242), null);
});

test('waitForBridge polls until the file appears, or times out', async () => {
  const dir = mkdtempSync(join(tmpdir(), 'aic-bridge-'));
  assert.equal(await waitForBridge(dir, 1, { timeoutMs: 60, intervalMs: 10 }), null);
  setTimeout(() => writeBridge(dir, 2, { sessionId: 'late', cwd: dir }), 30);
  const b = await waitForBridge(dir, 2, { timeoutMs: 2000, intervalMs: 10 });
  assert.equal(b.session_id, 'late');
});
