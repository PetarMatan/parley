// Shared runner for the hook scripts. A hook never fails the session: every
// error ends in exit code 0 with a one-line notice.
import { join } from 'node:path';
import { createLogger } from './log.mjs';
import { findCheckout, loadIdentity } from './identity.mjs';
import { InboxClient } from './client.mjs';
import { findClaudePid } from './bridge.mjs';
import { needsLogin, refreshIfNeeded } from './refresh.mjs';

export async function readStdin() {
  let text = '';
  for await (const chunk of process.stdin) text += chunk;
  return text.trim() ? JSON.parse(text) : {};
}

/**
 * Runs fn({input, checkout, identity, client, log, claudePid}). fn returns the
 * hook's stdout object or null. Outside an aic checkout the hook does nothing.
 * onError(message) returns the stdout object for a failure, or null.
 */
export async function runHook(name, fn, { onError = () => null } = {}) {
  const log = createLogger({ component: `aic-hook-${name}` });
  let out = null;
  try {
    const input = await readStdin();
    const checkout = findCheckout(input.cwd || process.cwd());
    if (!checkout) return;
    log.setFile(join(checkout, '.aic', 'plugin.jsonl'));
    const identity = loadIdentity(checkout);
    const timeoutMs = Number(process.env.AIC_HOOK_TIMEOUT_MS) || 5000;
    // A token that expired while no session was open is refreshed before the
    // first request. One round trip when due; nothing when not.
    const r = await refreshIfNeeded(identity, null, { timeoutMs });
    if (r.refreshed) log.info('token_refreshed', { exp: r.exp, outcome: 'ok' });
    else if (needsLogin(r)) log.warn('token_refresh_failed', { reason: r.reason, error: r.error, outcome: 'login_required' });
    const client = new InboxClient(identity, { timeoutMs });
    const claudePid = Number(process.env.AIC_CLAUDE_PID) || findClaudePid() || process.ppid;
    out = await fn({ input, checkout, identity, client, log, claudePid });
  } catch (e) {
    const message = String(e?.message ?? e).split('\n')[0].slice(0, 300);
    log.warn('hook_failed', { hook: name, outcome: 'error', error_code: e?.code, error: message });
    out = onError(message);
    if (!out) process.stderr.write(`aic-inbox ${name}: ${message}\n`);
  }
  if (out) process.stdout.write(JSON.stringify(out) + '\n');
  process.exitCode = 0;
}
