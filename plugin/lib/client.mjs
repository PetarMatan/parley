// fetch-based client for the inbox HTTP API (contract section 6), plus an SSE
// parser and a reconnecting event stream.
import { readToken } from './identity.mjs';

export class InboxError extends Error {
  constructor(status, code, message) {
    super(`${status} ${code}: ${message}`);
    this.status = status;
    this.code = code;
  }
}

export class InboxClient {
  /** identity: {baseUrl, tokenFile}. timeoutMs applies to plain requests, not the stream. */
  constructor(identity, { timeoutMs = 10_000, fetchImpl = globalThis.fetch } = {}) {
    this.identity = identity;
    this.timeoutMs = timeoutMs;
    this.fetch = fetchImpl;
  }

  url(path) {
    return this.identity.baseUrl + path;
  }

  headers(extra = {}) {
    return { authorization: `Bearer ${readToken(this.identity)}`, ...extra };
  }

  async request(method, path, body, extraHeaders = {}) {
    const res = await this.fetch(this.url(path), {
      method,
      headers: this.headers({ ...(body === undefined ? {} : { 'content-type': 'application/json' }), ...extraHeaders }),
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: AbortSignal.timeout(this.timeoutMs),
    });
    const text = await res.text();
    if (!res.ok) {
      let code = `http_${res.status}`;
      let message = text.slice(0, 500);
      try {
        const e = JSON.parse(text).error;
        if (e?.code) ({ code, message } = e);
      } catch { /* not the contract error body */ }
      throw new InboxError(res.status, code, message);
    }
    return text ? JSON.parse(text) : null;
  }

  registerSession(id, info) { return this.request('PUT', `/v1/sessions/${enc(id)}`, info); }
  deleteSession(id) { return this.request('DELETE', `/v1/sessions/${enc(id)}`); }
  listInbox(repo, branch) {
    const q = new URLSearchParams({ repo, branch });
    return this.request('GET', `/v1/inbox?${q}`);
  }
  /** sessionId, when known, lets the inbox route replies back to this session. */
  send(message, sessionId) {
    return this.request('POST', '/v1/messages', message, sessionId ? { 'aic-session-id': sessionId } : {});
  }
  ack(messageId, sessionId) { return this.request('POST', `/v1/messages/${enc(messageId)}/ack`, { session_id: sessionId }); }
  context(contextId) { return this.request('GET', `/v1/contexts/${enc(contextId)}`); }
  note(contextId, note) { return this.request('POST', `/v1/contexts/${enc(contextId)}/notes`, note); }

  /** Opens GET /v1/sessions/{id}/events once. Resolves when the stream ends. */
  async openStream(sessionId, { signal, onOpen, onEvent }) {
    const res = await this.fetch(this.url(`/v1/sessions/${enc(sessionId)}/events`), {
      headers: this.headers({ accept: 'text/event-stream' }),
      signal,
    });
    if (!res.ok) {
      const text = await res.text().catch(() => '');
      throw new InboxError(res.status, `http_${res.status}`, text.slice(0, 300));
    }
    onOpen?.();
    const parser = new SSEParser(onEvent);
    const decoder = new TextDecoder();
    for await (const chunk of res.body) parser.push(decoder.decode(chunk, { stream: true }));
    parser.push(decoder.decode());
  }
}

const enc = encodeURIComponent;

/**
 * Incremental parser for text/event-stream (WHATWG rules): lines end in
 * CRLF, LF or CR; ':' starts a comment; a blank line dispatches; multiple data
 * lines join with '\n'. onEvent gets {event, data, id} or {comment}.
 */
export class SSEParser {
  constructor(onEvent) {
    this.onEvent = onEvent;
    this.buf = '';
    this.reset();
  }

  reset() {
    this.event = '';
    this.data = [];
    this.id = undefined;
  }

  push(text) {
    this.buf += text;
    for (;;) {
      const m = /\r\n|\r|\n/.exec(this.buf);
      // A lone CR at the end may be the first half of CRLF; wait for more.
      if (!m || (m[0] === '\r' && m.index === this.buf.length - 1)) return;
      const line = this.buf.slice(0, m.index);
      this.buf = this.buf.slice(m.index + m[0].length);
      this.line(line);
    }
  }

  line(line) {
    if (line === '') {
      if (this.data.length) this.onEvent({ event: this.event || 'message', data: this.data.join('\n'), id: this.id });
      this.reset();
      return;
    }
    if (line.startsWith(':')) {
      this.onEvent({ comment: line.slice(1).trimStart() });
      return;
    }
    const i = line.indexOf(':');
    const field = i === -1 ? line : line.slice(0, i);
    let value = i === -1 ? '' : line.slice(i + 1);
    if (value.startsWith(' ')) value = value.slice(1);
    if (field === 'event') this.event = value;
    else if (field === 'data') this.data.push(value);
    else if (field === 'id') this.id = value;
  }
}

/** Backoff for attempt n (0-based): 1 s, 2 s, 4 s ... capped at 30 s. */
export function backoffMs(attempt, { min = 1000, max = 30_000 } = {}) {
  return Math.min(max, min * 2 ** Math.min(attempt, 30));
}

/**
 * Keeps one SSE stream open with reconnect. The backoff resets after a stream
 * opened successfully. A stream silent for idleMs (no event, no ping) is
 * treated as dead; the inbox pings every 15 s.
 * Returns {done, stop()}; stop() aborts the current stream and ends the loop.
 */
export function keepStream(client, sessionId, { onEvent, log, idleMs = 45_000, backoff = backoffMs } = {}) {
  let stopped = false;
  let current = null;
  let wake = () => {};
  const done = (async () => {
    let attempt = 0;
    while (!stopped) {
      current = new AbortController();
      let idle;
      const touch = () => {
        clearTimeout(idle);
        idle = setTimeout(() => current.abort(new Error('stream idle')), idleMs);
      };
      try {
        touch();
        await client.openStream(sessionId, {
          signal: current.signal,
          onOpen: () => { attempt = 0; log?.info('stream_open', { session_id: sessionId }); },
          onEvent: (ev) => { touch(); if (!ev.comment) onEvent(ev); },
        });
        log?.warn('stream_closed', { session_id: sessionId });
      } catch (e) {
        if (!stopped) log?.warn('stream_error', { session_id: sessionId, error: String(e?.message ?? e) });
      } finally {
        clearTimeout(idle);
      }
      if (stopped) break;
      const wait = backoff(attempt++);
      log?.info('stream_reconnect', { session_id: sessionId, wait_ms: wait });
      await new Promise((r) => {
        const t = setTimeout(r, wait);
        wake = () => { clearTimeout(t); r(); };
      });
    }
  })();
  return {
    done,
    stop() {
      stopped = true;
      current?.abort(new Error('stopped'));
      wake();
    },
  };
}
