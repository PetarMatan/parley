// Session bridge (DECISIONS D9): the SessionStart hook knows session_id, the
// channel server does not. The hook writes <checkout>/.aic/sessions/<claude_pid>.json,
// the server reads it by the same Claude process id.
import { execFileSync } from 'node:child_process';
import { mkdirSync, readFileSync, renameSync, rmSync, writeFileSync } from 'node:fs';
import { basename, join } from 'node:path';

/** Returns {ppid, comm} for a pid via ps, or null when the process is gone. */
export function psInfo(pid) {
  try {
    const out = execFileSync('ps', ['-o', 'ppid=,comm=', '-p', String(pid)], { encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'] }).trim();
    const m = out.match(/^(\d+)\s+(.*)$/);
    return m ? { ppid: Number(m[1]), comm: m[2] } : null;
  } catch {
    return null;
  }
}

/**
 * Walks up from startPid's parent to the first process whose command is
 * `claude` (comm may be a full path). Returns its pid or null. ps is
 * injectable for tests.
 */
export function findClaudePid(startPid = process.pid, { ps = psInfo, maxDepth = 32 } = {}) {
  let pid = ps(startPid)?.ppid;
  for (let i = 0; pid && pid > 1 && i < maxDepth; i++) {
    const info = ps(pid);
    if (!info) return null;
    if (basename(info.comm) === 'claude') return pid;
    pid = info.ppid;
  }
  return null;
}

export function bridgePath(checkout, claudePid) {
  return join(checkout, '.aic', 'sessions', `${claudePid}.json`);
}

/** Writes the bridge file atomically (temp file and rename). */
export function writeBridge(checkout, claudePid, { sessionId, cwd }) {
  const file = bridgePath(checkout, claudePid);
  mkdirSync(join(checkout, '.aic', 'sessions'), { recursive: true });
  const tmp = `${file}.${process.pid}.tmp`;
  const body = { session_id: sessionId, cwd, claude_pid: claudePid, ts: new Date().toISOString() };
  writeFileSync(tmp, JSON.stringify(body) + '\n', { mode: 0o600 });
  renameSync(tmp, file);
  return body;
}

/** Returns the bridge record or null when missing or unreadable. */
export function readBridge(checkout, claudePid) {
  try {
    const b = JSON.parse(readFileSync(bridgePath(checkout, claudePid), 'utf8'));
    return typeof b.session_id === 'string' && b.session_id ? b : null;
  } catch {
    return null;
  }
}

export function removeBridge(checkout, claudePid) {
  rmSync(bridgePath(checkout, claudePid), { force: true });
}

/** Polls for the bridge file every intervalMs, up to timeoutMs. Resolves the record or null. */
export async function waitForBridge(checkout, claudePid, { timeoutMs = 30_000, intervalMs = 500 } = {}) {
  const end = Date.now() + timeoutMs;
  for (;;) {
    const b = readBridge(checkout, claudePid);
    if (b) return b;
    if (Date.now() >= end) return null;
    await new Promise((r) => setTimeout(r, intervalMs));
  }
}
