#!/usr/bin/env node
// SessionStart (contract section 9): register the session and hand over what
// waited while it was closed. Two paths (D41):
// - Channel session: ack nothing and only note what waits. The channel
//   server's stream receives the messages and emits them as channel events,
//   which start a turn; hook context reaches the model only with the person's
//   first prompt.
// - No channel (no flag, or -p with the development flag): pull, ack, and
//   inject the messages as framed untrusted data. Channel events would be
//   dropped, and the channel server opens no stream.
// Outside a quarantine, messages for the MR author are never pushed here: both
// paths show them read-only and never ack them.
import { runHook } from '../lib/hook.mjs';
import { detectChannelMode, writeBridge } from '../lib/bridge.mjs';
import { sessionInfo } from '../lib/repo.mjs';
import { checkEnvelope, renderChannelContext, renderSessionContext, waitingNotice, NO_CHANNEL_NOTICE } from '../lib/render.mjs';
import { authorMessageIds, quarantineNotice } from '../lib/hold.mjs';

await runHook('session-start', async ({ input, checkout, identity, client, log, claudePid }) => {
  const sessionId = input.session_id;
  if (!sessionId) throw new Error('hook input has no session_id');
  const cwd = input.cwd || checkout;
  const mode = detectChannelMode(claudePid);
  try {
    const info = sessionInfo(checkout, cwd);
    await client.registerSession(sessionId, info);
    log.info('session_registered', { session_id: sessionId, repo: info.repo, branch: info.branch, source: input.source, channels: mode.channels, outcome: 'ok' });

    const { messages = [] } = (await client.listInbox(info.repo, info.branch)) ?? {};
    const out = mode.channels
      ? await channelSession(messages, { sessionId, info, identity, client, log })
      : await pullSession(messages, { sessionId, info, identity, client, log });
    // A person who forgot the flag is told once per launch; a -p run
    // (canaries) is not, and neither is /clear or a compaction.
    const launch = !input.source || input.source === 'startup' || input.source === 'resume';
    if (!mode.channels && !mode.print && launch) return { ...out, systemMessage: NO_CHANNEL_NOTICE };
    return out;
  } finally {
    // Written last: the channel server waits for this file before it opens
    // its stream, so in the pull path the stream never sees what was acked.
    writeBridge(checkout, claudePid, { sessionId, cwd, channels: mode.channels });
  }
}, {
  onError: (message) => ({ systemMessage: `aic-inbox: inbox not reached at session start (${message}). Messages may be waiting.` }),
});

function contextOutput(additionalContext) {
  return { hookSpecificOutput: { hookEventName: 'SessionStart', additionalContext } };
}

async function channelSession(messages, { sessionId, info, identity, client, log }) {
  // A pushed message is in flight to another live session. An invalid one is
  // dropped and logged by the channel server.
  const waiting = messages.filter((env) => env.state === 'queued').map(checkEnvelope).filter((c) => c.ok).map((c) => c.msg);
  const held = info.quarantine ? new Set() : await authorMessageIds(client, identity.user, waiting);
  const heldMsgs = waiting.filter((m) => held.has(m.message_id));
  const toStream = waiting.length - heldMsgs.length;
  log.info('inbox_waiting', { session_id: sessionId, queued: toStream, held: heldMsgs.length, outcome: 'ok' });
  if (!waiting.length) return null;
  const notices = toStream ? [waitingNotice(toStream)] : [];
  if (heldMsgs.length) notices.push(quarantineNotice(heldMsgs));
  return contextOutput(renderChannelContext(heldMsgs, notices));
}

async function pullSession(messages, { sessionId, info, identity, client, log }) {
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
      // Still shown: a duplicate later is better than a lost finding.
      log.warn('message_ack_failed', { session_id: sessionId, message_id: msg.message_id, error_code: e.code, outcome: 'error' });
    }
  }
  if (!shown.length && !dropped) return null;
  const heldMsgs = shown.filter((m) => held.has(m.message_id));
  const notices = heldMsgs.length ? [quarantineNotice(heldMsgs)] : [];
  return contextOutput(renderSessionContext(shown, dropped, notices));
}
