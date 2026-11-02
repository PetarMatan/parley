// Session bridge (DECISIONS D9): the SessionStart hook knows session_id, the
// channel server does not. The hook writes <checkout>/.aic/sessions/<claude_pid>.json,
// the server reads it by the same Claude process id.
import { execFileSync } from 'node:child_process';
import { mkdirSync, readFileSync, renameSync, rmSync, writeFileSync } from 'node:fs';
import { basename, join } from 'node:path';

// One ps call for pid; returns the trimmed output, or null when the process is gone.
function ps(pid, args) {
  try {
    return execFileSync('ps', [...args, '-p', String(pid)], { encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'] }).trim() || null;
  } catch {
    return null;
  }
}

/** Returns {ppid, comm} for a pid via ps, or null when the process is gone. */
export function psInfo(pid) {
  const m = ps(pid, ['-o', 'ppid=,comm='])?.match(/^(\d+)\s+(.*)$/);
  return m ? { ppid: Number(m[1]), comm: m[2] } : null;
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

/**
 * Returns the command line of pid split on whitespace, or null when the
 * process is gone. -ww stops ps from cutting a long line. Quoting is lost: a
 * prompt argument that contains a flag reads as that flag (D41).
 */
export function psArgs(pid) {
  return ps(pid, ['-ww', '-o', 'args='])?.split(/\s+/) ?? null;
}

const CHANNEL_ENTRY = /^(server:aic-inbox|plugin:aic-inbox@\S+)$/;

/**
 * Reads a claude command line (D41). channels: the session was started with
 * our server or plugin named in --channels, or in
 * --dangerously-load-development-channels outside print mode (-p ignores the
 * development flag). print: -p or --print. Claude Code gives no other signal:
 * an event for a session without a channel is dropped without an error.
 */
export function channelMode(argv) {
  const args = argv ?? [];
  const print = args.includes('-p') || args.includes('--print');
  let channels = false;
  for (let i = 0; i < args.length; i++) {
    const [flag, inline] = args[i].split(/=(.*)/s);
    const dev = flag === '--dangerously-load-development-channels';
    if (flag !== '--channels' && !dev) continue;
    // The flag takes space-separated entries up to the next option.
    const entries = inline !== undefined ? [inline] : [];
    for (let j = i + 1; inline === undefined && j < args.length && !args[j].startsWith('-'); j++) entries.push(args[j]);
    if (entries.some((e) => CHANNEL_ENTRY.test(e)) && !(dev && print)) channels = true;
  }
  return { channels, print };
}

/**
 * channelMode of the claude process. AIC_CHANNELS=1 or 0 overrides it (tests,
 * or a launcher that knows better). A command line that cannot be read counts
 * as no channel, so the hook pulls and nothing is acked into a dropped event.
 */
export function detectChannelMode(claudePid, env = process.env, { args = psArgs } = {}) {
  if (env.AIC_CHANNELS === '1' || env.AIC_CHANNELS === '0') return { channels: env.AIC_CHANNELS === '1', print: false };
  return channelMode(args(claudePid));
}

export function bridgePath(checkout, claudePid) {
  return join(checkout, '.aic', 'sessions', `${claudePid}.json`);
}

/** Writes the bridge file atomically (temp file and rename). */
export function writeBridge(checkout, claudePid, { sessionId, cwd, channels = true }) {
  const file = bridgePath(checkout, claudePid);
  mkdirSync(join(checkout, '.aic', 'sessions'), { recursive: true });
  const tmp = `${file}.${process.pid}.tmp`;
  const body = { session_id: sessionId, cwd, claude_pid: claudePid, channels, ts: new Date().toISOString() };
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
