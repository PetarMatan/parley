import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { spawn } from 'node:child_process';
import { mkdtempSync, mkdirSync, writeFileSync, existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  OIDC_DEFAULTS, discoveryUrl, fetchDiscovery, identityFromClaims, isJwt, parseOidcBlock,
  resolveOidcConfig, selectToken, validateDiscovery,
} from '../lib/oidc.mjs';
import { authorizationUrl } from '../lib/pkce.mjs';
import { loadIdentity } from '../lib/identity.mjs';

const LOGIN = fileURLToPath(new URL('../login.mjs', import.meta.url));
const ISS = 'https://login.example.com/realms/dev';
const jwt = (claims) => ['{"alg":"RS256"}', JSON.stringify(claims), 'sig']
  .map((s, i) => (i < 2 ? Buffer.from(s).toString('base64url') : s)).join('.');

test('config: defaults apply and issuer/client_id are required', () => {
  const c = resolveOidcConfig({ oidc: { issuer: ISS, client_id: 'aic-cli' } });
  assert.deepEqual(c, {
    issuer: ISS, clientId: 'aic-cli', scopes: 'openid email profile', audience: undefined,
    redirectPort: 5557, token: 'access', identityClaim: 'email',
  });
  assert.equal(OIDC_DEFAULTS.redirectPort, 5557);
  assert.throws(() => resolveOidcConfig({ oidc: { client_id: 'x' } }), /OIDC issuer is not set: pass --issuer, set AIC_OIDC_ISSUER, or set oidc\.issuer/);
  assert.throws(() => resolveOidcConfig({ env: { AIC_OIDC_ISSUER: ISS } }), /OIDC client_id is not set: pass --client-id, set AIC_OIDC_CLIENT_ID/);
});

test('config: precedence is flag > env > identity.json > default', () => {
  const oidc = { issuer: 'https://file', client_id: 'file', scopes: 'openid', redirect_port: 6000, token: 'access', identity_claim: 'upn', audience: 'file-aud' };
  const env = { AIC_OIDC_ISSUER: 'https://env', AIC_OIDC_CLIENT_ID: 'env', AIC_OIDC_REDIRECT_PORT: '6001', AIC_IDENTITY_CLAIM: 'preferred_username' };
  const flags = { issuer: 'https://flag', port: '6002' };
  const c = resolveOidcConfig({ flags, env, oidc });
  assert.equal(c.issuer, 'https://flag');
  assert.equal(c.clientId, 'env');
  assert.equal(c.redirectPort, 6002);
  assert.equal(c.identityClaim, 'preferred_username');
  assert.equal(c.scopes, 'openid');
  assert.equal(c.token, 'access');
  assert.equal(c.audience, 'file-aud');
  // Empty env values do not override the file.
  assert.equal(resolveOidcConfig({ env: { AIC_OIDC_ISSUER: '' }, oidc }).issuer, 'https://file');
});

test('config: bad port and token values are errors', () => {
  const base = { issuer: ISS, client_id: 'x' };
  assert.throws(() => resolveOidcConfig({ oidc: base, flags: { port: 'abc' } }), /redirect_port/);
  assert.throws(() => resolveOidcConfig({ oidc: base, flags: { port: '70000' } }), /redirect_port/);
  assert.throws(() => resolveOidcConfig({ oidc: { ...base, token: 'refresh' } }), /token must be "id" or "access"/);
});

test('identity.json oidc block is type-checked and exposed', () => {
  assert.equal(parseOidcBlock(undefined), null);
  assert.throws(() => parseOidcBlock('x'), /oidc must be an object/);
  assert.throws(() => parseOidcBlock({ redirect_port: '5557' }), /redirect_port must be an integer/);
  assert.throws(() => parseOidcBlock({ client_id: 5 }), /client_id must be a string/);
  const dir = mkdtempSync(join(tmpdir(), 'aic-oidc-'));
  mkdirSync(join(dir, '.aic'));
  writeFileSync(join(dir, '.aic', 'identity.json'), JSON.stringify({ user: 'b@example.com', token_file: 't.jwt', oidc: { issuer: ISS, client_id: 'c', identity_claim: 'upn' } }));
  const id = loadIdentity(dir, {});
  assert.equal(id.oidc.issuer, ISS);
  assert.equal(id.identityClaim, 'upn');
  assert.equal(loadIdentity(dir, { AIC_IDENTITY_CLAIM: 'sub' }).identityClaim, 'sub');
  writeFileSync(join(dir, '.aic', 'identity.json'), JSON.stringify({ user: 'b@example.com', token_file: 't.jwt' }));
  assert.equal(loadIdentity(dir, {}).oidc, null);
  assert.equal(loadIdentity(dir, {}).identityClaim, 'email');
});

test('discovery: URL, issuer match, endpoints and S256', () => {
  assert.equal(discoveryUrl(`${ISS}/`), `${ISS}/.well-known/openid-configuration`);
  const doc = { issuer: ISS, authorization_endpoint: `${ISS}/auth`, token_endpoint: `${ISS}/token` };
  assert.deepEqual(validateDiscovery(doc, `${ISS}/`), { authorizationEndpoint: `${ISS}/auth`, tokenEndpoint: `${ISS}/token` });
  assert.throws(() => validateDiscovery({ ...doc, issuer: 'https://evil' }, ISS), /does not match the configured issuer/);
  assert.throws(() => validateDiscovery({ ...doc, token_endpoint: undefined }, ISS), /token_endpoint is missing/);
  assert.throws(() => validateDiscovery({ ...doc, authorization_endpoint: '' }, ISS), /authorization_endpoint is missing/);
  assert.throws(() => validateDiscovery({ ...doc, code_challenge_methods_supported: ['plain'] }, ISS), /does not support PKCE S256/);
  assert.ok(validateDiscovery({ ...doc, code_challenge_methods_supported: ['plain', 'S256'] }, ISS));
});

test('authorization URL carries audience only when set', () => {
  const p = { clientId: 'c', redirectUri: 'http://127.0.0.1:5557/callback', challenge: 'ch', state: 's', nonce: 'n' };
  const without = new URL(authorizationUrl('https://idp/auth', { ...p, extra: { audience: undefined } }));
  assert.equal(without.searchParams.has('audience'), false);
  const withAud = new URL(authorizationUrl('https://idp/auth', { ...p, extra: { audience: 'https://api', client_id: 'evil' } }));
  assert.equal(withAud.searchParams.get('audience'), 'https://api');
  assert.equal(withAud.searchParams.get('client_id'), 'c');
  assert.equal(withAud.searchParams.get('code_challenge_method'), 'S256');
});

test('token selection: id or access, JWT only, nonce always checked', () => {
  const id = jwt({ nonce: 'n1', email: 'B@Example.com' });
  const at = jwt({ preferred_username: 'b' });
  assert.equal(isJwt(id), true);
  assert.equal(isJwt('opaque-token'), false);
  assert.equal(selectToken({ id_token: id }, 'id', 'n1').claims.email, 'B@Example.com');
  const a = selectToken({ id_token: id, access_token: at }, 'access', 'n1');
  assert.equal(a.token, at);
  assert.equal(a.field, 'access_token');
  assert.throws(() => selectToken({ id_token: id, access_token: at }, 'access', 'other'), /nonce does not match/);
  assert.throws(() => selectToken({ access_token: at }, 'id', 'n1'), /no id_token/);
  assert.throws(() => selectToken({ id_token: id, access_token: 'opaque' }, 'access', 'n1'), /access_token is not a JWT.*audience/);
  assert.ok(selectToken({ access_token: at }, 'access', 'n1'));
});

test('claim lookup lower-cases and rejects missing values', () => {
  assert.equal(identityFromClaims({ email: 'B@Example.com' }, 'email'), 'b@example.com');
  assert.equal(identityFromClaims({ upn: 'B@corp' }, 'upn'), 'b@corp');
  assert.equal(identityFromClaims({ email: '' }, 'email'), null);
  assert.equal(identityFromClaims({ email: 5 }, 'email'), null);
  assert.equal(identityFromClaims({}, 'email'), null);
});

/** A local provider that serves one discovery document. */
async function fakeProvider(docFor) {
  const srv = createServer((req, res) => {
    if (req.url !== '/realm/.well-known/openid-configuration') { res.writeHead(404).end(); return; }
    res.writeHead(200, { 'content-type': 'application/json' }).end(JSON.stringify(docFor(srv)));
  });
  await new Promise((r) => srv.listen(0, '127.0.0.1', r));
  const issuer = `http://127.0.0.1:${srv.address().port}/realm`;
  return { srv, issuer };
}

test('fetchDiscovery against a local server', async (t) => {
  const { srv, issuer } = await fakeProvider((s) => {
    const iss = `http://127.0.0.1:${s.address().port}/realm`;
    return { issuer: iss, authorization_endpoint: `${iss}/auth`, token_endpoint: `${iss}/token`, code_challenge_methods_supported: ['S256'] };
  });
  t.after(() => srv.close());
  assert.equal((await fetchDiscovery(`${issuer}/`)).tokenEndpoint, `${issuer}/token`);
  await assert.rejects(fetchDiscovery(`${issuer}/nope`), /returned 404/);
});

test('login.mjs: --help and a mismatched discovery issuer fail cleanly', async (t) => {
  const run = (args) => new Promise((resolve) => {
    const p = spawn(process.execPath, [LOGIN, ...args], { env: { ...process.env, AIC_OIDC_ISSUER: '', AIC_OIDC_CLIENT_ID: '' } });
    let out = '';
    p.stdout.on('data', (d) => { out += d; });
    p.stderr.on('data', (d) => { out += d; });
    p.on('close', (code) => resolve({ code, out }));
  });
  const help = await run(['--help']);
  assert.equal(help.code, 0);
  assert.match(help.out, /--client-id/);
  const bad = await run(['--bogus']);
  assert.equal(bad.code, 2);

  const { srv, issuer } = await fakeProvider(() => ({
    issuer: 'https://someone-else.example', authorization_endpoint: 'https://x/auth', token_endpoint: 'https://x/token',
  }));
  t.after(() => srv.close());
  const dir = mkdtempSync(join(tmpdir(), 'aic-login-'));
  mkdirSync(join(dir, '.aic'));
  writeFileSync(join(dir, '.aic', 'identity.json'), JSON.stringify({ user: 'b@example.com', token_file: join(dir, 'tok.jwt') }));

  const missing = await run(['--checkout', dir, '--no-open']);
  assert.equal(missing.code, 1);
  assert.match(missing.out, /OIDC issuer is not set/);

  const mismatch = await run(['--checkout', dir, '--no-open', '--issuer', issuer, '--client-id', 'aic-cli']);
  assert.equal(mismatch.code, 1);
  assert.match(mismatch.out, /discovery: issuer "https:\/\/someone-else\.example" does not match/);
  assert.equal(existsSync(join(dir, 'tok.jwt')), false);
});
