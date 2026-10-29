#!/usr/bin/env node
// SessionStart (contract section 9): register the session, pull what waited
// while it was closed, ack it, and inject it as framed untrusted data. Outside
// a quarantine, messages for the MR author are shown read-only and not acked.
import { runHook } from '../lib/hook.mjs';
import { writeBridge } from '../lib/bridge.mjs';
import { sessionInfo } from '../lib/repo.mjs';
import { checkEnvelope, renderSessionContext } from '../lib/render.mjs';
import { authorMessageIds, quarantineNotice } from '../lib/hold.mjs';

await runHook('session-start', async ({ input, checkout, identity, client, log, claudePid }) => {
  const sessionId = input.session_id;
  if (!sessionId) throw new Error('hook input has no session_id');
  const cwd = input.cwd || checkout;
  try {
    const info = sessionInfo(checkout, cwd);
    await client.registerSession(sessionId, info);
    log.info('session_registered', { session_id: sessionId, repo: info.repo, branch: info.branch, source: input.source, outcome: 'ok' });

    const { messages = [] } = (await client.listInbox(info.repo, info.branch)) ?? {};
    const shown = [];
    const states = new Map();
    let dropped = 0;
    for (const env of messages) {
      const c = checkEnvelope(env);
      if (!c.ok) {
        dropped++;
        log.warn('message_dropped', { session_id: sessionId, error_code: c.code, error: c.message, outcome: 'dropped' });
        continue;
      }
      shown.push(c.msg);
      states.set(c.msg.message_id, env.state);
    }
    // Messages for the MR author wait for the quarantine. A normal session
    // shows them read-only; the inbox would refuse its ack anyway.
    const held = info.quarantine ? new Set() : await authorMessageIds(client, identity.user, shown);
    for (const msg of shown) {
      if (held.has(msg.message_id)) {
        log.info('message_held', { session_id: sessionId, message_id: msg.message_id, task_id: msg.task_id, sender: msg.from, outcome: 'held_for_quarantine' });
        continue;
      }
      // A pushed message is in flight to another live session; acking it
      // here would take the task over.
      if (states.get(msg.message_id) !== 'queued') continue;
      try {
        await client.ack(msg.message_id, sessionId);
        log.info('message_acked', { session_id: sessionId, message_id: msg.message_id, task_id: msg.task_id, sender: msg.from, outcome: 'acked' });
      } catch (e) {
        // Still shown: a duplicate on the live stream is better than a lost finding.
        log.warn('message_ack_failed', { session_id: sessionId, message_id: msg.message_id, error_code: e.code, outcome: 'error' });
      }
    }
    if (!shown.length && !dropped) return null;
    const heldMsgs = shown.filter((m) => held.has(m.message_id));
    const notice = heldMsgs.length ? [quarantineNotice(heldMsgs)] : [];
    return {
      hookSpecificOutput: { hookEventName: 'SessionStart', additionalContext: renderSessionContext(shown, dropped, notice) },
    };
  } finally {
    // Written last so the channel server opens its stream only after the
    // pulled messages are acked; otherwise the stream would push them again.
    writeBridge(checkout, claudePid, { sessionId, cwd });
  }
}, {
  onError: (message) => ({ systemMessage: `aic-inbox: inbox not reached at session start (${message}). Messages may be waiting.` }),
});
