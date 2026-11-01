// Token refresh for the channel server and the hooks. The token file holds
// the bearer token the inbox sees; <token_file>.refresh holds the provider's
// refresh token. Both are rewritten atomically. Nothing here deletes a file:
// a failed refresh leaves the files as they were, and a login replaces them.
import { closeSync, existsSync, mkdirSync, openSync, readFileSync, renameSync, statSync, unlinkSync, writeFileSync } from 'node:fs';
import { dirname } from 'node:path';
import { decodeJwtPayload } from './pkce.mjs';
import { fetchDiscovery, refreshRequest, resolveOidcConfig, selectToken } from './oidc.mjs';

export const DEFAULT_MARGIN_S = 120;

export function refreshFile(tokenFile) {
  return `${tokenFile}.refresh`;
}

/** Writes content to path through a temp file and a rename, mode 0600. */
export function writeAtomic(path, content) {
  mkdirSync(dirname(path), { recursive: true });
  const tmp = `${path}.${process.pid}.tmp`;
  writeFileSync(tmp, content, { mode: 0o600 });
  renameSync(tmp, path);
}

/** exp (seconds) of the JWT in tokenFile; 0 when the file is missing or unreadable. */
export function tokenExp(tokenFile) {
  try {
    const exp = decodeJwtPayload(readFileSync(tokenFile, 'utf8').trim()).exp;
    return Number.isFinite(exp) ? exp : 0;
  } catch {
    return 0;
  }
}

const discoveryCache = new Map(); // issuer -> {authorizationEndpoint, tokenEndpoint}

/**
 * Refreshes the token when it expires within margin seconds and a refresh
 * token exists. cfg is the resolved OIDC config, or null to resolve it from
 * identity.oidc and env. Returns {refreshed, exp, reason?}. Reasons that are
 * not failures: no_oidc, no_refresh_token, not_due. Failures: provider_<status>
 * (the provider refused; files untouched) and error (network or response shape).
 */
export async function refreshIfNeeded(identity, cfg = null, {
  now = Date.now() / 1000, fetch = globalThis.fetch, margin = DEFAULT_MARGIN_S, env = process.env, timeoutMs = 0, lockWaitMs = 5000,
} = {}) {
  const rf = refreshFile(identity.tokenFile);
  const exp = tokenExp(identity.tokenFile);
  if (!identity.oidc && !cfg) return { refreshed: false, reason: 'no_oidc', exp };
  if (!existsSync(rf)) return { refreshed: false, reason: 'no_refresh_token', exp };
  if (exp - now >= margin) return { refreshed: false, reason: 'not_due', exp };

  // One refresh at a time per token file. A hook and the channel server can
  // both find the token due within the same second; with refresh token
  // rotation the second use of the same refresh token revokes the session.
  const lock = await acquireLock(`${identity.tokenFile}.lock`, lockWaitMs);
  if (!lock) return { refreshed: false, reason: 'locked', exp };
  try {
    // Re-read under the lock: another process may have refreshed meanwhile.
    const exp2 = tokenExp(identity.tokenFile);
    if (exp2 - now >= margin) return { refreshed: false, reason: 'refreshed_elsewhere', exp: exp2 };
    return await doRefresh(identity, cfg, rf, exp2, { fetch, env, timeoutMs });
  } finally {
    lock.release();
  }
}

export const LOCK_STALE_MS = 60_000;

/** Creates <lock> exclusively; waits up to waitMs for a holder; removes a stale lock. */
async function acquireLock(path, waitMs) {
  const deadline = Date.now() + waitMs;
  for (;;) {
    try {
      const fd = openSync(path, 'wx');
      closeSync(fd);
      return { release: () => { try { unlinkSync(path); } catch { /* already gone */ } } };
    } catch (e) {
      if (e?.code !== 'EEXIST') throw e;
      try {
        if (Date.now() - statSync(path).mtimeMs > LOCK_STALE_MS) { unlinkSync(path); continue; }
      } catch { continue; }
      if (Date.now() >= deadline) return null;
      await new Promise((r) => setTimeout(r, 100));
    }
  }
}

async function doRefresh(identity, cfg, rf, exp, { fetch, env, timeoutMs }) {
  const f = timeoutMs > 0 ? (url, init = {}) => fetch(url, { ...init, signal: AbortSignal.timeout(timeoutMs) }) : fetch;
  try {
    const c = cfg ?? resolveOidcConfig({ oidc: identity.oidc, env });
    let disco = discoveryCache.get(c.issuer);
    if (!disco) {
      disco = await fetchDiscovery(c.issuer, f);
      discoveryCache.set(c.issuer, disco);
    }
    const refreshToken = readFileSync(rf, 'utf8').trim();
    const { url, init } = refreshRequest({ tokenEndpoint: disco.tokenEndpoint, clientId: c.clientId, refreshToken });
    const res = await f(url, init);
    const body = await res.json().catch(() => ({}));
    if (!res.ok) {
      return { refreshed: false, reason: `provider_${res.status}`, error: String(body.error ?? ''), exp };
    }
    const { token, claims } = selectToken(body, c.token, undefined, { checkNonce: false });
    writeAtomic(identity.tokenFile, token + '\n');
    // Rotation: the provider may hand out a new refresh token; the old one is then spent.
    if (typeof body.refresh_token === 'string' && body.refresh_token) writeAtomic(rf, body.refresh_token + '\n');
    return { refreshed: true, exp: Number.isFinite(claims.exp) ? claims.exp : 0 };
  } catch (e) {
    return { refreshed: false, reason: 'error', error: String(e?.message ?? e).split('\n')[0].slice(0, 300), exp };
  }
}

/** True when a refresh result means the person has to log in again. */
export function needsLogin(result) {
  return !result.refreshed && (result.reason === 'error' || String(result.reason).startsWith('provider_'));
}
