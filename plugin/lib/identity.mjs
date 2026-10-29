// Per-checkout identity: <checkout>/.aic/identity.json
// = {"user","token_file","base_url","oidc"} (contract section 2). The optional
// oidc block holds the login settings (see lib/oidc.mjs).
import { existsSync, readFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { OIDC_DEFAULTS, parseOidcBlock } from './oidc.mjs';

export const DEFAULT_BASE_URL = 'http://localhost:3000';

/** The checkout is AIC_CHECKOUT, else the nearest ancestor of start holding .aic/identity.json. */
export function findCheckout(start = process.cwd(), env = process.env) {
  if (env.AIC_CHECKOUT) return resolve(env.AIC_CHECKOUT);
  let dir = resolve(start);
  for (;;) {
    if (existsSync(join(dir, '.aic', 'identity.json'))) return dir;
    const up = dirname(dir);
    if (up === dir) return null;
    dir = up;
  }
}

/**
 * Reads the identity. base_url precedence: env AIC_BASE_URL, then
 * identity.json, then the gateway default. identityClaim precedence: env
 * AIC_IDENTITY_CLAIM, then oidc.identity_claim, then "email". oidc is the
 * type-checked oidc block, or null.
 */
export function loadIdentity(checkout, env = process.env) {
  const file = join(checkout, '.aic', 'identity.json');
  const id = JSON.parse(readFileSync(file, 'utf8'));
  if (typeof id.user !== 'string' || typeof id.token_file !== 'string') {
    throw new Error(`${file}: user and token_file are required`);
  }
  const oidc = parseOidcBlock(id.oidc, file);
  return {
    user: id.user,
    tokenFile: resolve(checkout, id.token_file),
    baseUrl: (env.AIC_BASE_URL || id.base_url || DEFAULT_BASE_URL).replace(/\/+$/, ''),
    checkout,
    oidc,
    identityClaim: env.AIC_IDENTITY_CLAIM || oidc?.identity_claim || OIDC_DEFAULTS.identityClaim,
  };
}

/** Reads the bearer token on each call, so a fresh login takes effect at once. */
export function readToken(identity) {
  return readFileSync(identity.tokenFile, 'utf8').trim();
}
