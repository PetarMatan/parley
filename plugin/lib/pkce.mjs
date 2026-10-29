// PKCE (RFC 7636, S256) and authorization URL helpers for login.mjs (PKCE convention).
import { createHash, randomBytes } from 'node:crypto';

export const b64url = (buf) => Buffer.from(buf).toString('base64url');

/** 32 random bytes give a 43-character verifier, within RFC 7636's 43..128. */
export function createVerifier() {
  return b64url(randomBytes(32));
}

export function challengeS256(verifier) {
  return b64url(createHash('sha256').update(verifier, 'ascii').digest());
}

/**
 * extra holds optional additional query parameters (e.g. audience); entries
 * with an undefined or empty value are skipped. They cannot replace the
 * standard parameters above.
 */
export function authorizationUrl(endpoint, { clientId, redirectUri, challenge, state, nonce, scope = 'openid email profile', extra = {} }) {
  const u = new URL(endpoint);
  const params = new URLSearchParams({
    response_type: 'code', client_id: clientId, redirect_uri: redirectUri, scope,
    code_challenge: challenge, code_challenge_method: 'S256', state, nonce,
  });
  for (const [k, v] of Object.entries(extra)) {
    if (v !== undefined && v !== null && v !== '' && !params.has(k)) params.set(k, String(v));
  }
  u.search = params.toString();
  return u.toString();
}

/** Decodes a JWT payload without verifying it; the inbox and gateway verify the signature. */
export function decodeJwtPayload(jwt) {
  const part = jwt.split('.')[1];
  if (!part) throw new Error('not a JWT');
  return JSON.parse(Buffer.from(part, 'base64url').toString('utf8'));
}
