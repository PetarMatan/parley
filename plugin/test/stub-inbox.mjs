// Test helper (not a test): a stub of the inbox HTTP API and a temp checkout.
// node --test also loads this file; it defines no tests.
import { createServer } from 'node:http';
import { execFileSync } from 'node:child_process';
import { mkdtempSync, mkdirSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

export const TOKEN = 'test-token';

export function envelope(overrides = {}, payload = {}) {
  return {
    message_id: 'm-01J0000000000000000000000A', context_id: 'local/shop!1', task_id: 'f-1a2b3c4d', round: 1,
    from: 'a@example.com', to: 'b@example.com', sent_at: '2026-10-28T14:30:00Z',
    payload: {
      schema: 'finding.v1', context_id: 'local/shop!1', task_id: 'f-1a2b3c4d', file: 'src/discount.js', line: 12,
      category: 'bug', severity: 'major', class: 'fact', title: 'Discount above 100% is not clamped',
      note: 'applyPercentDiscount(100, 150) returns -50.', ...payload,
    },
    tags: { injection_score: 0, stripped: [] },
    state: 'queued',
    ...overrides,
  };
}

/**
 * Starts the stub. inbox: envelopes for GET /v1/inbox. contexts: context_id ->
 * conversation for GET /v1/contexts/{id} (404 otherwise). onStream(res, n)
 * runs for the n-th SSE connection. Every request is recorded in calls.
 */
export async function startStub({ inbox = [], contexts = {}, onStream } = {}) {
  const calls = [];
  let streams = 0;
  const server = createServer(async (req, res) => {
    let body = '';
    for await (const c of req) body += c;
    const call = { method: req.method, url: req.url, auth: req.headers.authorization, body: body ? JSON.parse(body) : null };
    calls.push(call);
    if (req.headers.authorization !== `Bearer ${TOKEN}`) return json(res, 401, { error: { code: 'unauthorized', message: 'no' } });
    const u = new URL(req.url, 'http://x');
    if (req.method === 'PUT' && u.pathname.startsWith('/v1/sessions/')) return json(res, 200, { ok: true });
    if (req.method === 'DELETE' && u.pathname.startsWith('/v1/sessions/')) { res.writeHead(204).end(); return; }
    if (req.method === 'GET' && u.pathname === '/v1/inbox') return json(res, 200, { messages: inbox });
    if (req.method === 'GET' && u.pathname.startsWith('/v1/contexts/')) {
      const conv = contexts[decodeURIComponent(u.pathname.slice('/v1/contexts/'.length))];
      return conv ? json(res, 200, conv) : json(res, 404, { error: { code: 'not_found', message: u.pathname } });
    }
    if (req.method === 'POST' && /^\/v1\/messages\/[^/]+\/ack$/.test(u.pathname)) { res.writeHead(204).end(); return; }
    if (req.method === 'POST' && u.pathname === '/v1/messages') return json(res, 201, { message_id: 'm-NEW', round: 1, delivered: 'queued' });
    if (req.method === 'GET' && u.pathname.endsWith('/events')) {
      res.writeHead(200, { 'content-type': 'text/event-stream', 'cache-control': 'no-cache' });
      res.write(': ping\n\n');
      return onStream ? onStream(res, ++streams) : undefined;
    }
    json(res, 404, { error: { code: 'not_found', message: u.pathname } });
  });
  await new Promise((r) => server.listen(0, '127.0.0.1', r));
  const baseUrl = `http://127.0.0.1:${server.address().port}`;
  return {
    baseUrl, calls,
    close: () => { server.closeAllConnections(); return new Promise((r) => server.close(r)); },
  };
}

function json(res, status, obj) {
  res.writeHead(status, { 'content-type': 'application/json' }).end(JSON.stringify(obj));
}

/** The quarantine marker make-quarantine.sh writes. */
export function markQuarantine(dir, mr = 1, sourceBranch = 'feature/discount') {
  mkdirSync(join(dir, '.aic'), { recursive: true });
  writeFileSync(join(dir, '.aic', 'quarantine'), JSON.stringify({ mr, source_branch: sourceBranch }));
}

/** A conversation where b@ is the MR author (addressee) of each task id given. */
export function authorContext(taskIds, extra = []) {
  return {
    context_id: 'local/shop!1', participants: ['a@example.com', 'b@example.com'],
    tasks: [...taskIds.map((task_id) => ({ task_id, sender: 'a@example.com', addressee: 'b@example.com' })), ...extra],
    messages: [],
  };
}

/** A git checkout with an origin that normalizes to local/shop, plus .aic/identity.json. */
export function makeCheckout(baseUrl, { branch = 'feature/discount' } = {}) {
  const dir = mkdtempSync(join(tmpdir(), 'aic-plugin-test-'));
  const g = (...a) => execFileSync('git', [
    '-c', 'init.defaultBranch=main', '-c', 'core.hooksPath=/dev/null', '-c', 'commit.gpgsign=false',
    '-c', 'user.name=t', '-c', 'user.email=t@example.invalid', '-C', dir, ...a,
  ], { stdio: 'ignore' });
  g('init', '-q');
  g('commit', '-q', '--allow-empty', '-m', 'init');
  g('remote', 'add', 'origin', '/nowhere/work/shop.git');
  g('checkout', '-q', '-b', branch);
  mkdirSync(join(dir, '.aic'), { recursive: true });
  writeFileSync(join(dir, '.aic', 'token.jwt'), TOKEN + '\n');
  writeFileSync(join(dir, '.aic', 'identity.json'), JSON.stringify({ user: 'b@example.com', token_file: join(dir, '.aic', 'token.jwt'), base_url: baseUrl }));
  return dir;
}
