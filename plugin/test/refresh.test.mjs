// refreshIfNeeded against a fake provider: due or not, rotation, refusal, token kind.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { refreshIfNeeded, refreshFile, tokenExp, needsLogin } from '../lib/refresh.mjs';
import { refreshRequest, selectToken } from '../lib/oidc.mjs';

const jwt = (claims) => ['{"alg":"RS256"}', JSON.stringify(claims), 'sig']
  .map((s, i) => (i < 2 ? Buffer.from(s).toString('base64url') : s)).join('.');
const NOW = 1_800_000_000;

/** A provider that serves discovery and a token endpoint; answer() decides the response. */
async function provider(answer) {
  const calls = [];
  const srv = createServer((req, res) => {
    let body = '';
    req.on('data', (d) => { body += d; });
    req.on('end', () => {
      calls.push({ url: req.url, body: Object.fromEntries(new URLSearchParams(body)) });
      const iss = `http://127.0.0.1:${srv.address().port}/realm`;
      if (req.url === '/realm/.well-known/openid-configuration') {
        res.writeHead(200, { 'content-type': 'application/json' })
          .end(JSON.stringify({ issuer: iss, authorization_endpoint: `${iss}/auth`, token_endpoint: `${iss}/token`, code_challenge_methods_supported: ['S256'] }));
        return;
      }
      if (req.url === '/realm/token') {
        const { status, json } = answer(Object.fromEntries(new URLSearchParams(body)));
        res.writeHead(status, { 'content-type': 'application/json' }).end(JSON.stringify(json));
        return;
      }
      res.writeHead(404).end();
    });
  });
  await new Promise((r) => srv.listen(0, '127.0.0.1', r));
  return { srv, issuer: `http://127.0.0.1:${srv.address().port}/realm`, calls, close: () => { srv.closeAllConnections(); return new Promise((r) => srv.close(r)); } };
}

function identityIn(issuer, { exp, refresh = 'r-old', token = 'access' } = {}) {
  const dir = mkdtempSync(join(tmpdir(), 'aic-refresh-'));
  const tokenFile = join(dir, '.aic', 'token.jwt');
  const idn = { user: 'b@example.com', tokenFile, checkout: dir, oidc: issuer ? { issuer, client_id: 'aic-cli', token } : null, identityClaim: 'email' };
  mkdirSync(join(dir, '.aic'), { recursive: true });
  writeFileSync(tokenFile, jwt({ email: 'b@example.com', exp }) + '\n');
  if (refresh) writeFileSync(refreshFile(tokenFile), refresh + '\n');
  return idn;
}

test('refreshRequest builds the refresh grant for a public client', () => {
  const r = refreshRequest({ tokenEndpoint: 'https://p/token', clientId: 'c', refreshToken: 'r' });
  assert.equal(r.url, 'https://p/token');
  assert.equal(r.init.method, 'POST');
  assert.deepEqual(Object.fromEntries(new URLSearchParams(r.init.body)), { grant_type: 'refresh_token', refresh_token: 'r', client_id: 'c' });
  assert.throws(() => refreshRequest({ tokenEndpoint: 'x', clientId: 'c' }), /required/);
});

test('selectToken skips the nonce check only when asked', () => {
  const id = jwt({ nonce: 'n1', email: 'b@example.com' });
  assert.throws(() => selectToken({ id_token: id, access_token: jwt({ exp: 1 }) }, 'access', 'other'), /nonce/);
  assert.ok(selectToken({ id_token: id, access_token: jwt({ exp: 1 }) }, 'access', undefined, { checkNonce: false }));
});

test('no oidc block or no refresh file: nothing happens and nothing is fetched', async () => {
  let fetched = 0;
  const fetch = async () => { fetched++; throw new Error('must not be called'); };
  const noOidc = identityIn(null, { exp: NOW - 10 });
  assert.deepEqual(await refreshIfNeeded(noOidc, null, { now: NOW, fetch }), { refreshed: false, reason: 'no_oidc', exp: NOW - 10 });
  const noRefresh = identityIn('https://p/realm', { exp: NOW - 10, refresh: null });
  assert.deepEqual(await refreshIfNeeded(noRefresh, null, { now: NOW, fetch }), { refreshed: false, reason: 'no_refresh_token', exp: NOW - 10 });
  assert.equal(fetched, 0);
});

test('not due: a token with more than the margin left is kept', async () => {
  let fetched = 0;
  const fetch = async () => { fetched++; throw new Error('must not be called'); };
  const idn = identityIn('https://p/realm', { exp: NOW + 600 });
  const r = await refreshIfNeeded(idn, null, { now: NOW, fetch, margin: 120 });
  assert.deepEqual(r, { refreshed: false, reason: 'not_due', exp: NOW + 600 });
  assert.equal(fetched, 0);
  assert.equal(needsLogin(r), false);
});

test('due: the access token is refreshed and the refresh token rotated', async (t) => {
  const newAccess = jwt({ email: 'b@example.com', exp: NOW + 300, aud: 'parley' });
  const p = await provider((form) => {
    assert.equal(form.grant_type, 'refresh_token');
    assert.equal(form.client_id, 'aic-cli');
    return { status: 200, json: { access_token: newAccess, id_token: jwt({ nonce: 'none', exp: NOW + 300 }), refresh_token: 'r-new', token_type: 'Bearer' } };
  });
  t.after(p.close);
  const idn = identityIn(p.issuer, { exp: NOW + 60 });
  const r = await refreshIfNeeded(idn, null, { now: NOW, margin: 120 });
  assert.deepEqual(r, { refreshed: true, exp: NOW + 300 });
  assert.equal(readFileSync(idn.tokenFile, 'utf8'), newAccess + '\n');
  assert.equal(readFileSync(refreshFile(idn.tokenFile), 'utf8'), 'r-new\n');
  assert.equal(tokenExp(idn.tokenFile), NOW + 300);
  assert.deepEqual(p.calls.map((c) => c.url), ['/realm/.well-known/openid-configuration', '/realm/token']);
  assert.equal(p.calls[1].body.refresh_token, 'r-old');
  // Discovery is cached per process: a second refresh posts straight to the token endpoint.
  writeFileSync(idn.tokenFile, jwt({ exp: NOW + 10 }) + '\n');
  await refreshIfNeeded(idn, null, { now: NOW, margin: 120 });
  assert.deepEqual(p.calls.map((c) => c.url).slice(2), ['/realm/token']);
  assert.equal(p.calls[2].body.refresh_token, 'r-new');
});

test('token kind id selects the ID token on refresh', async (t) => {
  const newId = jwt({ email: 'b@example.com', exp: NOW + 240 });
  const p = await provider(() => ({ status: 200, json: { access_token: jwt({ exp: NOW + 240 }), id_token: newId } }));
  t.after(p.close);
  const idn = identityIn(p.issuer, { exp: NOW - 1, token: 'id' });
  const r = await refreshIfNeeded(idn, null, { now: NOW });
  assert.equal(r.refreshed, true);
  assert.equal(readFileSync(idn.tokenFile, 'utf8'), newId + '\n');
  // No new refresh token in the response: the old one stays.
  assert.equal(readFileSync(refreshFile(idn.tokenFile), 'utf8'), 'r-old\n');
});

test('a refused refresh leaves both files untouched and asks for a login', async (t) => {
  const p = await provider(() => ({ status: 400, json: { error: 'invalid_grant', error_description: 'Session not active' } }));
  t.after(p.close);
  const idn = identityIn(p.issuer, { exp: NOW - 100 });
  const before = [readFileSync(idn.tokenFile, 'utf8'), readFileSync(refreshFile(idn.tokenFile), 'utf8')];
  const r = await refreshIfNeeded(idn, null, { now: NOW });
  assert.deepEqual(r, { refreshed: false, reason: 'provider_400', error: 'invalid_grant', exp: NOW - 100 });
  assert.equal(needsLogin(r), true);
  assert.deepEqual([readFileSync(idn.tokenFile, 'utf8'), readFileSync(refreshFile(idn.tokenFile), 'utf8')], before);
  assert.ok(existsSync(refreshFile(idn.tokenFile)));
});

test('an unreachable provider is an error, not a crash', async () => {
  const idn = identityIn('http://127.0.0.1:1/realm', { exp: NOW - 100 });
  const r = await refreshIfNeeded(idn, null, { now: NOW, timeoutMs: 500 });
  assert.equal(r.refreshed, false);
  assert.equal(r.reason, 'error');
  assert.equal(needsLogin(r), true);
});

test('two concurrent refreshes: one network call, the other sees the new token', async (t) => {
  const { mkdtempSync, writeFileSync, readFileSync } = await import('node:fs');
  const { tmpdir } = await import('node:os');
  const { join } = await import('node:path');
  const { refreshIfNeeded, refreshFile } = await import('../lib/refresh.mjs');
  const dir = mkdtempSync(join(tmpdir(), 'aic-refresh-'));
  const tokenFile = join(dir, 'u.jwt');
  const jwt = (exp) => 'h.' + Buffer.from(JSON.stringify({ exp, preferred_username: 'u' })).toString('base64url') + '.s';
  const now = 1_800_000_000;
  writeFileSync(tokenFile, jwt(now + 10) + '\n');
  writeFileSync(refreshFile(tokenFile), 'r1\n');
  const seen = [];
  const fetch = async (url, init) => {
    if (String(url).endsWith('/.well-known/openid-configuration')) {
      return { ok: true, status: 200, json: async () => ({ issuer: 'https://issuer.test', authorization_endpoint: 'https://issuer.test/a', token_endpoint: 'https://issuer.test/t', jwks_uri: 'https://issuer.test/k', code_challenge_methods_supported: ['S256'] }) };
    }
    const rt = new URLSearchParams(init.body).get('refresh_token');
    seen.push(rt);
    await new Promise((r) => setTimeout(r, 50));
    if (rt !== 'r1') return { ok: false, status: 400, json: async () => ({ error: 'invalid_grant' }) };
    return { ok: true, status: 200, json: async () => ({ access_token: jwt(now + 300), refresh_token: 'r2' }) };
  };
  const identity = { tokenFile, oidc: { issuer: 'https://issuer.test', client_id: 'c', token: 'access' } };
  const cfg = { issuer: 'https://issuer.test', clientId: 'c', token: 'access', scopes: 'openid', identityClaim: 'preferred_username' };
  const [a, b] = await Promise.all([
    refreshIfNeeded(identity, cfg, { now, fetch }),
    refreshIfNeeded(identity, cfg, { now, fetch }),
  ]);
  assert.equal(seen.length, 1, 'exactly one refresh request');
  assert.deepEqual([a.refreshed, b.refreshed].sort(), [false, true]);
  const other = a.refreshed ? b : a;
  assert.equal(other.reason, 'refreshed_elsewhere');
  assert.equal(readFileSync(refreshFile(tokenFile), 'utf8').trim(), 'r2');
});
