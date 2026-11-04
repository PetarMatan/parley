// Delivery receipts from the Claude Code session transcript (DECISIONS D42).
// Claude Code gives a channel server no receipt and drops an event for a
// session without a registered channel without an error. It does write every
// event it accepts to the transcript:
// - `queue-operation` `enqueue` when the event arrives (the channel is alive),
// - then, when the event enters the conversation, either a `user` record with
//   origin {kind: "channel"} (idle session) or an `attachment` of type
//   `queued_command` with that origin (busy session).
// The format is internal to Claude Code. Anything this module cannot read
// counts as no receipt, so a message is never acked without one.
import { closeSync, openSync, readSync, statSync } from 'node:fs';
import { StringDecoder } from 'node:string_decoder';

// The tag Claude Code writes at the start of the event text. Anchored, so a
// message_id written inside the framed data cannot count.
const TAG_ID = /^<channel source="([^"]*)" message_id="(m-[0-9A-Za-z_-]{1,64})"/;

function channelId(text, server) {
  const m = typeof text === 'string' ? TAG_ID.exec(text) : null;
  return m && sourceMatches(m[1], server) ? m[2] : null;
}

// source is "aic-inbox" for a server: entry and plugin:<name>:<server> for a plugin.
function sourceMatches(source, server) {
  return source === server || source.endsWith(`:${server}`);
}

function fromChannel(origin, server) {
  return origin?.kind === 'channel' && typeof origin.server === 'string' && sourceMatches(origin.server, server);
}

/**
 * Classifies one transcript record. Returns {kind: 'enqueued' | 'delivered',
 * id} for a record about one of our channel events, else null.
 */
export function classifyRecord(rec, server = 'aic-inbox') {
  if (!rec || typeof rec !== 'object') return null;
  if (rec.type === 'queue-operation' && rec.operation === 'enqueue') {
    const id = channelId(rec.content, server);
    return id ? { kind: 'enqueued', id } : null;
  }
  if (rec.type === 'user' && fromChannel(rec.origin, server)) {
    const content = rec.message?.content;
    const id = channelId(typeof content === 'string' ? content : null, server);
    return id ? { kind: 'delivered', id } : null;
  }
  const a = rec.attachment;
  if (rec.type === 'attachment' && a?.type === 'queued_command' && fromChannel(a.origin, server)) {
    const id = channelId(a.prompt, server);
    return id ? { kind: 'delivered', id } : null;
  }
  return null;
}

/**
 * Reads a transcript from a byte offset on each scan() and returns the
 * receipts found in complete new lines. start: 'end' skips what the file holds
 * now (history of a resumed session), 'start' reads it all. A missing file is
 * not an error: Claude Code creates it with its first record.
 */
export class TranscriptWatcher {
  constructor(path, { server = 'aic-inbox', start = 'end', fs = { openSync, readSync, closeSync, statSync } } = {}) {
    this.path = path;
    this.server = server;
    this.fs = fs;
    this.offset = 0;
    this.rest = '';
    this.decoder = new StringDecoder('utf8'); // keeps a split multi-byte character for the next read
    if (start === 'end') {
      try { this.offset = fs.statSync(path).size; } catch { this.offset = 0; }
    }
  }

  /** Returns [{kind, id}] in file order. */
  scan() {
    let size;
    try { size = this.fs.statSync(this.path).size; } catch { return []; }
    // A shorter file was replaced or truncated: start over from its beginning.
    if (size < this.offset) { this.offset = 0; this.rest = ''; this.decoder = new StringDecoder('utf8'); }
    if (size === this.offset) return [];
    const buf = Buffer.alloc(size - this.offset);
    let fd;
    try {
      fd = this.fs.openSync(this.path, 'r');
      const n = this.fs.readSync(fd, buf, 0, buf.length, this.offset);
      this.offset += n;
      this.rest += this.decoder.write(buf.subarray(0, n));
    } catch {
      return [];
    } finally {
      if (fd !== undefined) this.fs.closeSync(fd);
    }
    const lines = this.rest.split('\n');
    this.rest = lines.pop(); // an incomplete last line waits for the next scan
    const out = [];
    for (const line of lines) {
      if (!line.includes('message_id=')) continue;
      let rec;
      try { rec = JSON.parse(line); } catch { continue; }
      const r = classifyRecord(rec, this.server);
      if (r) out.push(r);
    }
    return out;
  }
}
