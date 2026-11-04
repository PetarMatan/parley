import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, readdirSync, statSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { findClaudePid, writeBridge, readBridge, removeBridge, waitForBridge, bridgePath, psInfo, psArgs, channelMode, detectChannelMode } from '../lib/bridge.mjs';

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
  assert.deepEqual(Object.keys(w), ['session_id', 'cwd', 'claude_pid', 'channels', 'transcript_path', 'ts']);
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

test('channelMode reads the channel flags of a claude command line', () => {
  const m = (line) => channelMode(line.split(' '));
  assert.deepEqual(m('claude'), { channels: false, print: false });
  assert.deepEqual(m('claude --dangerously-load-development-channels server:aic-inbox'), { channels: true, print: false });
  assert.deepEqual(m('claude --channels plugin:aic-inbox@acme --model opus'), { channels: true, print: false });
  assert.deepEqual(m('claude --channels plugin:telegram@claude-plugins-official plugin:aic-inbox@acme'), { channels: true, print: false });
  assert.deepEqual(m('claude --channels=server:aic-inbox'), { channels: true, print: false });
  // -p ignores the development flag, not --channels.
  assert.deepEqual(m('claude -p hi --dangerously-load-development-channels server:aic-inbox'), { channels: false, print: true });
  assert.deepEqual(m('claude --print hi --channels plugin:aic-inbox@acme'), { channels: true, print: true });
  // Another server, or our name as a prompt word, is not our channel.
  assert.deepEqual(m('claude --channels server:webhook'), { channels: false, print: false });
  assert.deepEqual(m('claude --channels -c server:aic-inbox'), { channels: false, print: false });
  assert.deepEqual(m('claude server:aic-inbox'), { channels: false, print: false });
  assert.deepEqual(channelMode(null), { channels: false, print: false });
});

test('detectChannelMode honours AIC_CHANNELS and treats an unreadable process as no channel', () => {
  const args = () => ['claude', '--channels', 'server:aic-inbox'];
  assert.equal(detectChannelMode(1, { AIC_CHANNELS: '0' }, { args }).channels, false);
  assert.equal(detectChannelMode(1, { AIC_CHANNELS: '1' }, { args: () => null }).channels, true);
  assert.equal(detectChannelMode(1, {}, { args }).channels, true);
  assert.equal(detectChannelMode(1, {}, { args: () => null }).channels, false);
});

test('psArgs reads this process', () => {
  const args = psArgs(process.pid);
  assert.ok(args.some((a) => a.endsWith('node') || a.includes('node')));
});
