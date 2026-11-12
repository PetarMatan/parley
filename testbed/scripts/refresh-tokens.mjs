#!/usr/bin/env node
// Refreshes the Keycloak access token of each named testbed person from the
// refresh token next to it (plugin/lib/refresh.mjs, the same code the channel
// server runs). Prints one line per person: refreshed | not_due | <reason>.
// Exit 1 when any token is unusable, so a scripted run stops before sending.
//   node testbed/scripts/refresh-tokens.mjs alice bob dave
// Env: DOMAIN (parleylab.dev), AIC_TOKEN_DIR (~/.parley/tokens)
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { pathToFileURL } from 'node:url';

const HERE = path.dirname(fileURLToPath(import.meta.url));
const { refreshIfNeeded, tokenExp } = await import(pathToFileURL(path.join(HERE, '..', '..', 'plugin', 'lib', 'refresh.mjs')).href);
const DOMAIN = process.env.DOMAIN ?? 'parleylab.dev';
const TOKEN_DIR = process.env.AIC_TOKEN_DIR ?? path.join(os.homedir(), '.parley', 'tokens');
const users = process.argv.slice(2);
if (users.length === 0) { process.stderr.write('usage: refresh-tokens.mjs <user>...\n'); process.exit(2); }

let bad = 0;
for (const user of users) {
  const identity = {
    user, tokenFile: path.join(TOKEN_DIR, `${user}.jwt`),
    oidc: { issuer: `https://auth.${DOMAIN}/realms/parley`, client_id: 'parley-cli', identity_claim: 'preferred_username' },
  };
  const r = await refreshIfNeeded(identity, null, { margin: 240, timeoutMs: 15000 });
  const left = Math.round(tokenExp(identity.tokenFile) - Date.now() / 1000);
  const ok = r.refreshed || r.reason === 'not_due' || r.reason === 'refreshed_elsewhere';
  if (!ok) bad++;
  process.stdout.write(`${user}: ${r.refreshed ? 'refreshed' : r.reason}, ${left}s left\n`);
}
process.exit(bad ? 1 : 0);
