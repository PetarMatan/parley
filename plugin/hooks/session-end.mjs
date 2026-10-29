#!/usr/bin/env node
// SessionEnd (contract section 9): delete the registration and the bridge file.
import { runHook } from '../lib/hook.mjs';
import { removeBridge } from '../lib/bridge.mjs';

await runHook('session-end', async ({ input, checkout, client, log, claudePid }) => {
  try {
    if (input.session_id) {
      await client.deleteSession(input.session_id);
      log.info('session_deleted', { session_id: input.session_id, reason: input.reason, outcome: 'ok' });
    }
  } finally {
    removeBridge(checkout, claudePid);
  }
  return null;
});
