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
import { existsSync, mkdirSync } from 'node:fs';
import { homedir } from 'node:os';
import { join, resolve } from 'node:path';
import { parseArgs } from 'node:util';
import { findCheckout, loadIdentity, writeIdentity } from './lib/identity.mjs';
import { authorizationUrl, challengeS256, createVerifier, b64url } from './lib/pkce.mjs';
import { OIDC_DEFAULTS, fetchDiscovery, identityFromClaims, resolveOidcConfig, selectToken } from './lib/oidc.mjs';
import { refreshFile, writeAtomic } from './lib/refresh.mjs';

const USAGE = `usage: node plugin/login.mjs [--checkout <dir>] [--no-open]
         [--issuer <url>] [--client-id <id>] [--scopes "<s1 s2>"] [--audience <aud>]
         [--port <n>] [--token id|access] [--identity-claim <claim>]
         [--base-url <channel url>] [--token-dir <dir>]

Precedence: flag > env (AIC_OIDC_ISSUER, AIC_OIDC_CLIENT_ID, AIC_OIDC_SCOPES,
AIC_OIDC_AUDIENCE, AIC_OIDC_REDIRECT_PORT, AIC_OIDC_TOKEN, AIC_IDENTITY_CLAIM)
> the oidc block of <checkout>/.aic/identity.json > defaults
(scopes "openid email profile", port 5557, token access, identity claim email).
issuer and client id have no default.

A checkout without .aic/identity.json is initialised when --base-url, --issuer
and --client-id are given: the person signs in, and the file is written for
the user the token names, with the token in <token-dir>/<user>.jwt
(token dir: --token-dir, env AIC_TOKEN_DIR, default ~/.parley/tokens).
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
    'base-url': { type: 'string' },
    'token-dir': { type: 'string' },
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
  const init = initRequest(opt, process.env);
  const checkout = init ? resolve(opt.checkout ?? process.cwd()) : (opt.checkout ?? findCheckout());
  if (!checkout) throw new Error('no .aic/identity.json found; pass --checkout, or --base-url, --issuer and --client-id to create one');
  const identityFile = join(checkout, '.aic', 'identity.json');
  // A checkout without an identity is created from the flags (D50). The user
  // name is not asked: it comes from the token after the sign-in.
  const identity = existsSync(identityFile) ? loadIdentity(checkout) : null;
  if (!identity && !init) throw new Error(`${identityFile} missing; pass --base-url, --issuer and --client-id to create it`);
  const cfg = resolveOidcConfig({ flags: opt, oidc: identity?.oidc ?? null, env: process.env, where: identityFile });
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
  // A token for another person would make every session in this checkout act
  // as that person. A browser that is still signed in as someone else causes it.
  if (identity && who !== identity.user.toLowerCase()) {
    throw new Error(`signed in as ${who ?? `(no ${cfg.identityClaim} claim)`}, but this checkout belongs to ${identity.user}. ` +
      `Nothing was written. Sign in as ${identity.user} (a private window avoids a provider session of another user) and run the login again.`);
  }
  if (!who) throw new Error(`the token has no ${cfg.identityClaim} claim; nothing was written`);
  const target = identity ?? initIdentity(checkout, who, cfg, opt, init);
  if (!identity) say(`${identityFile} written for ${who}`);

  writeAtomic(target.tokenFile, token + '\n');
  const exp = Number.isFinite(claims.exp) ? new Date(claims.exp * 1000).toISOString() : 'unknown';
  say(`${field} for ${who} written to ${target.tokenFile} (expires ${exp})`);
  if (typeof tok.refresh_token === 'string' && tok.refresh_token) {
    writeAtomic(refreshFile(target.tokenFile), tok.refresh_token + '\n');
    say(`refresh token written to ${refreshFile(target.tokenFile)}; the session refreshes the token before it expires`);
  } else {
    say('no refresh token issued; log in again when the token expires');
  }
}

/** The three values that create an identity, or null when any is missing. */
function initRequest(o, env) {
  const baseUrl = o['base-url'];
  const issuer = o.issuer ?? env.AIC_OIDC_ISSUER;
  const clientId = o['client-id'] ?? env.AIC_OIDC_CLIENT_ID;
  if (!baseUrl || !issuer || !clientId) return null;
  return { baseUrl, tokenDir: resolve(o['token-dir'] ?? env.AIC_TOKEN_DIR ?? join(homedir(), '.parley', 'tokens')) };
}

/**
 * Writes <checkout>/.aic/identity.json for the person the token named. The
 * oidc block keeps the login settings that were given, so the hooks and the
 * server refresh the token without flags; defaults are left out.
 */
function initIdentity(checkout, user, cfg, o, { baseUrl, tokenDir }) {
  const oidc = { issuer: cfg.issuer, client_id: cfg.clientId };
  if (cfg.identityClaim !== OIDC_DEFAULTS.identityClaim) oidc.identity_claim = cfg.identityClaim;
  if (cfg.scopes !== OIDC_DEFAULTS.scopes) oidc.scopes = cfg.scopes;
  if (cfg.audience) oidc.audience = cfg.audience;
  if (cfg.redirectPort !== OIDC_DEFAULTS.redirectPort) oidc.redirect_port = cfg.redirectPort;
  if (cfg.token !== OIDC_DEFAULTS.token) oidc.token = cfg.token;
  mkdirSync(tokenDir, { recursive: true, mode: 0o700 });
  writeIdentity(checkout, { user, token_file: join(tokenDir, `${user}.jwt`), base_url: baseUrl, oidc });
  return loadIdentity(checkout);
}

/** Serves the loopback callback until one carries this state, opens the browser, resolves the code. */
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
      // A callback for another login attempt, such as a restored old tab, is
      // refused but does not end this one.
      if (u.searchParams.get('state') !== state) {
        res.writeHead(400, { 'content-type': 'text/plain; charset=utf-8' }).end('This login link is old. Use the newest link the login printed.');
        say('ignored a callback from an older login attempt; still waiting');
        return;
      }
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
