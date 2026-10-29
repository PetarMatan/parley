// Messages for the merge request author belong to the author's quarantine
// session (contract section 6). The inbox pushes them only there and refuses
// an ack from any other session. A normal checkout may show them read-only
// (an L0 brief) and never acks them.
import { quarantineMarker } from './repo.mjs';

/** True when checkout is a quarantine worktree (make-quarantine.sh wrote the marker). */
export function isQuarantine(checkout) {
  return Boolean(checkout) && quarantineMarker(checkout) !== null;
}

/**
 * Returns the set of message ids in msgs that are addressed to user as the MR
 * author: the task's addressee is user. msgs are checked messages addressed to
 * user. One GET /v1/contexts call per context. When that call fails, every
 * message of the context counts as the author's, so nothing is acked by mistake.
 */
export async function authorMessageIds(client, user, msgs) {
  const held = new Set();
  const byContext = new Map();
  for (const m of msgs) {
    if (!byContext.has(m.context_id)) byContext.set(m.context_id, []);
    byContext.get(m.context_id).push(m);
  }
  for (const [contextId, list] of byContext) {
    let authorTasks = null;
    try {
      const conv = await client.context(contextId);
      authorTasks = new Set((conv?.tasks ?? []).filter((t) => t?.addressee === user).map((t) => t.task_id));
    } catch {
      authorTasks = null;
    }
    for (const m of list) if (!authorTasks || authorTasks.has(m.task_id)) held.add(m.message_id);
  }
  return held;
}

/** The MR iid of a context id <repo>!<iid>, or null. */
export function iidOf(contextId) {
  return /!([1-9][0-9]{0,9})$/.exec(contextId ?? '')?.[1] ?? null;
}

/** The notice a normal checkout shows for messages that wait for the quarantine. */
export function quarantineNotice(heldMsgs) {
  const iids = [...new Set(heldMsgs.map((m) => iidOf(m.context_id)).filter(Boolean))];
  const cmds = iids.length ? iids.map((i) => `quarantine/make-quarantine.sh ${i}`).join(', ') : 'quarantine/make-quarantine.sh <iid>';
  return `aic-inbox: ${heldMsgs.length} finding message(s) for the merge request author wait for the quarantine session. ` +
    'This session is not the quarantine: it shows them read-only and does not ack them, and they stay queued. ' +
    `Brief the person only (L0). To handle them, open the quarantine with ${cmds}, then run the start-session.sh it prints.`;
}
