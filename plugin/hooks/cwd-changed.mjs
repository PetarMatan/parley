#!/usr/bin/env node
// CwdChanged (contract section 9): re-register so the inbox sees the current
// cwd and branch, and keep the bridge file's cwd current.
import { runHook } from '../lib/hook.mjs';
import { writeBridge } from '../lib/bridge.mjs';
import { sessionInfo } from '../lib/repo.mjs';

await runHook('cwd-changed', async ({ input, checkout, client, log, claudePid }) => {
  if (!input.session_id) throw new Error('hook input has no session_id');
  const cwd = input.cwd || checkout;
  const info = sessionInfo(checkout, cwd);
  await client.registerSession(input.session_id, info);
  writeBridge(checkout, claudePid, { sessionId: input.session_id, cwd });
  log.info('session_registered', { session_id: input.session_id, repo: info.repo, branch: info.branch, cwd, outcome: 'ok' });
  return null;
});
