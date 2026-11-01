#!/usr/bin/env node
// OIDC login: discovery + authorization code flow with PKCE S256 (PKCE
// convention; the password grant is not used) as a public client, callback on
// http://127.0.0.1:<port>/callback. Works with any standard provider; the
// sandbox uses the local DEX container. Settings come from flags, env, or the
// oidc block of .aic/identity.json (see lib/oidc.mjs). Writes the selected token
// (access token by default) to the token_file of the checkout's .aic/identity.json
// and the refresh token, when the provider issues one, to <token_file>.refresh.
// The channel server and the hooks refresh from that file (lib/refresh.mjs).
import { createServer } from 'node:http';
import { spawn } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { join } from 'node:path';
import { parseArgs } from 'node:util';
import { findCheckout, loadIdentity } from './lib/identity.mjs';
import { authorizationUrl, challengeS256, createVerifier, b64url } from './lib/pkce.mjs';
import { fetchDiscovery, identityFromClaims, resolveOidcConfig, selectToken } from './lib/oidc.mjs';
import { refreshFile, writeAtomic } from './lib/refresh.mjs';

const USAGE = `usage: node plugin/login.mjs [--checkout <dir>] [--no-open]
         [--issuer <url>] [--client-id <id>] [--scopes "<s1 s2>"] [--audience <aud>]
         [--port <n>] [--token id|access] [--identity-claim <claim>]

Precedence: flag > env (AIC_OIDC_ISSUER, AIC_OIDC_CLIENT_ID, AIC_OIDC_SCOPES,
AIC_OIDC_AUDIENCE, AIC_OIDC_REDIRECT_PORT, AIC_OIDC_TOKEN, AIC_IDENTITY_CLAIM)
> the oidc block of <checkout>/.aic/identity.json > defaults
(scopes "openid email profile", port 5557, token access, identity claim email).
issuer and client id have no default.
`;

const HOST = '127.0.0.1';
const TIMEOUT_MS = 5 * 60_000;

let opt;
try {
  ({ values: opt } = parseArgs({ options: {
    checkout: { type: 'string' },
    issuer: { type: 'string' },
    'client-id': { type: 'string' },
    scopes: { type: 'string' },
    audience: { type: 'string' },
    port: { type: 'string' },
    token: { type: 'string' },
    'identity-claim': { type: 'string' },
    'no-open': { type: 'boolean', default: false },
    help: { type: 'boolean', short: 'h', default: false },
  } }));
} catch (e) {
  process.stderr.write(`aic-login: ${e.message}\n${USAGE}`);
  process.exit(2);
}

const say = (s) => process.stderr.write(`aic-login: ${s}\n`);

async function main() {
  if (opt.help) { process.stdout.write(USAGE); return; }
  const checkout = opt.checkout ?? findCheckout();
  if (!checkout) throw new Error('no .aic/identity.json found; pass --checkout');
  const identity = loadIdentity(checkout);
  const cfg = resolveOidcConfig({
    flags: opt, oidc: identity.oidc, env: process.env, where: join(checkout, '.aic', 'identity.json'),
  });
  const redirectUri = `http://${HOST}:${cfg.redirectPort}/callback`;

  const disco = await fetchDiscovery(cfg.issuer);
  const verifier = createVerifier();
  const state = b64url(randomBytes(16));
  const nonce = b64url(randomBytes(16));
  const url = authorizationUrl(disco.authorizationEndpoint, {
    clientId: cfg.clientId, redirectUri, challenge: challengeS256(verifier), state, nonce, scope: cfg.scopes,
    extra: { audience: cfg.audience },
  });

  const code = await waitForCode(url, state, cfg.redirectPort, redirectUri);
  const res = await fetch(disco.tokenEndpoint, {
    method: 'POST',
    headers: { 'content-type': 'application/x-www-form-urlencoded', accept: 'application/json' },
    body: new URLSearchParams({
      grant_type: 'authorization_code', code, redirect_uri: redirectUri, client_id: cfg.clientId, code_verifier: verifier,
    }),
  });
  const tok = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(`token endpoint: ${tok.error ?? res.status} ${tok.error_description ?? ''}`);

  const { token, field, claims } = selectToken(tok, cfg.token, nonce);
  const who = identityFromClaims(claims, cfg.identityClaim);
  say(`identity claim "${cfg.identityClaim}" = ${who ?? '(missing)'}`);
  if (who !== identity.user.toLowerCase()) say(`warning: token ${cfg.identityClaim} ${who} differs from identity user ${identity.user}`);

  writeAtomic(identity.tokenFile, token + '\n');
  const exp = Number.isFinite(claims.exp) ? new Date(claims.exp * 1000).toISOString() : 'unknown';
  say(`${field} for ${who} written to ${identity.tokenFile} (expires ${exp})`);
  if (typeof tok.refresh_token === 'string' && tok.refresh_token) {
    writeAtomic(refreshFile(identity.tokenFile), tok.refresh_token + '\n');
    say(`refresh token written to ${refreshFile(identity.tokenFile)}; the session refreshes the token before it expires`);
  } else {
    say('no refresh token issued; log in again when the token expires');
  }
}

/** Serves the loopback callback once, opens the browser, resolves the code. */
function waitForCode(url, state, port, redirectUri) {
  return new Promise((resolveP, reject) => {
    const server = createServer((req, res) => {
      const u = new URL(req.url, redirectUri);
      if (u.pathname !== '/callback') { res.writeHead(404).end(); return; }
      const finish = (status, text, err, code) => {
        res.writeHead(status, { 'content-type': 'text/plain; charset=utf-8' }).end(text);
        server.close();
        clearTimeout(timer);
        err ? reject(err) : resolveP(code);
      };
      if (u.searchParams.get('state') !== state) return finish(400, 'State mismatch.', new Error('state mismatch'));
      if (u.searchParams.get('error')) return finish(400, 'Login failed.', new Error(u.searchParams.get('error')));
      const code = u.searchParams.get('code');
      if (!code) return finish(400, 'No code.', new Error('callback without code'));
      finish(200, 'aic login done. You can close this tab.', null, code);
    });
    const timer = setTimeout(() => { server.close(); reject(new Error('login timed out')); }, TIMEOUT_MS);
    server.on('error', reject);
    server.listen(port, HOST, () => {
      say(`open ${url}`);
      if (!opt['no-open']) openBrowser(url);
    });
  });
}

function openBrowser(url) {
  const cmd = process.platform === 'darwin' ? 'open' : process.platform === 'win32' ? 'explorer' : 'xdg-open';
  spawn(cmd, [url], { stdio: 'ignore', detached: true }).on('error', () => say('could not open a browser; open the URL above')).unref();
}

main().catch((e) => {
  say(`failed: ${e.message}`);
  process.exitCode = 1;
});
