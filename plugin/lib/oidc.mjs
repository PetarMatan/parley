// Generic OIDC helpers for login.mjs: config resolution, discovery document
// validation, token selection and claim lookup. Pure functions, except
// fetchDiscovery, which does one GET.
import { decodeJwtPayload } from './pkce.mjs';

export const OIDC_DEFAULTS = Object.freeze({
  scopes: 'openid email profile',
  redirectPort: 5557,
  token: 'id',
  identityClaim: 'email',
  audience: undefined,
});

/** identity.json oidc key -> resolved name, env var, CLI flag. */
const SETTINGS = [
  { key: 'issuer', name: 'issuer', env: 'AIC_OIDC_ISSUER', flag: 'issuer' },
  { key: 'client_id', name: 'clientId', env: 'AIC_OIDC_CLIENT_ID', flag: 'client-id' },
  { key: 'scopes', name: 'scopes', env: 'AIC_OIDC_SCOPES', flag: 'scopes' },
  { key: 'audience', name: 'audience', env: 'AIC_OIDC_AUDIENCE', flag: 'audience' },
  { key: 'redirect_port', name: 'redirectPort', env: 'AIC_OIDC_REDIRECT_PORT', flag: 'port' },
  { key: 'token', name: 'token', env: 'AIC_OIDC_TOKEN', flag: 'token' },
  { key: 'identity_claim', name: 'identityClaim', env: 'AIC_IDENTITY_CLAIM', flag: 'identity-claim' },
];

/**
 * Checks the types of the optional oidc block of identity.json. Returns the
 * block (or null when absent). redirect_port is an integer; the rest are strings.
 */
export function parseOidcBlock(oidc, where = 'identity.json') {
  if (oidc === undefined || oidc === null) return null;
  if (typeof oidc !== 'object' || Array.isArray(oidc)) throw new Error(`${where}: oidc must be an object`);
  for (const { key } of SETTINGS) {
    const v = oidc[key];
    if (v === undefined) continue;
    if (key === 'redirect_port') {
      if (!Number.isInteger(v)) throw new Error(`${where}: oidc.redirect_port must be an integer`);
    } else if (typeof v !== 'string') {
      throw new Error(`${where}: oidc.${key} must be a string`);
    }
  }
  return { ...oidc };
}

const blank = (v) => v === undefined || v === null || v === '';

/**
 * Resolves the login settings. Precedence: CLI flag > env > identity.json oidc
 * block > default. issuer and client_id have no default.
 */
export function resolveOidcConfig({ flags = {}, oidc = null, env = {}, where = 'identity.json' } = {}) {
  const out = {};
  for (const s of SETTINGS) {
    const candidates = [flags[s.flag], env[s.env], oidc?.[s.key], OIDC_DEFAULTS[s.name]];
    out[s.name] = candidates.find((v) => !blank(v));
  }
  for (const s of SETTINGS.slice(0, 2)) {
    if (blank(out[s.name])) {
      throw new Error(`OIDC ${s.key} is not set: pass --${s.flag}, set ${s.env}, or set oidc.${s.key} in ${where}`);
    }
  }
  const port = Number(out.redirectPort);
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    throw new Error(`OIDC redirect_port must be an integer 1..65535, got ${JSON.stringify(out.redirectPort)}`);
  }
  out.redirectPort = port;
  if (out.token !== 'id' && out.token !== 'access') {
    throw new Error(`OIDC token must be "id" or "access", got ${JSON.stringify(out.token)}`);
  }
  out.issuer = String(out.issuer);
  out.identityClaim = String(out.identityClaim);
  return out;
}

export const trimSlash = (s) => String(s).replace(/\/+$/, '');

export function discoveryUrl(issuer) {
  return `${trimSlash(issuer)}/.well-known/openid-configuration`;
}

/**
 * Validates a discovery document against the configured issuer (RFC 8414
 * section 3.3: the issuer must match). Returns the two endpoints.
 */
export function validateDiscovery(doc, issuer) {
  if (!doc || typeof doc !== 'object') throw new Error('discovery: document is not a JSON object');
  if (typeof doc.issuer !== 'string' || trimSlash(doc.issuer) !== trimSlash(issuer)) {
    throw new Error(`discovery: issuer ${JSON.stringify(doc.issuer)} does not match the configured issuer ${JSON.stringify(issuer)}`);
  }
  for (const k of ['authorization_endpoint', 'token_endpoint']) {
    if (typeof doc[k] !== 'string' || !doc[k]) throw new Error(`discovery: ${k} is missing`);
  }
  const methods = doc.code_challenge_methods_supported;
  if (methods !== undefined && !(Array.isArray(methods) && methods.includes('S256'))) {
    throw new Error(`discovery: provider does not support PKCE S256 (code_challenge_methods_supported: ${JSON.stringify(methods)})`);
  }
  return { authorizationEndpoint: doc.authorization_endpoint, tokenEndpoint: doc.token_endpoint };
}

/** GETs and validates the discovery document. */
export async function fetchDiscovery(issuer, fetchImpl = fetch) {
  const url = discoveryUrl(issuer);
  const res = await fetchImpl(url, { headers: { accept: 'application/json' } });
  if (!res.ok) throw new Error(`discovery: GET ${url} returned ${res.status}`);
  let doc;
  try { doc = await res.json(); } catch { throw new Error(`discovery: ${url} did not return JSON`); }
  return validateDiscovery(doc, issuer);
}

/** True for a compact JWS: three non-empty base64url segments. */
export function isJwt(s) {
  return typeof s === 'string' && /^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$/.test(s);
}

/**
 * Picks the token to save from a token endpoint response. Checks the ID token
 * nonce whenever an ID token is present. Returns the token and its claims.
 */
export function selectToken(resp, which, nonce) {
  if (!resp || typeof resp !== 'object') throw new Error('token endpoint: response is not a JSON object');
  if (resp.id_token !== undefined) {
    if (!isJwt(resp.id_token)) throw new Error('token endpoint: id_token is not a JWT');
    if (decodeJwtPayload(resp.id_token).nonce !== nonce) throw new Error('ID token nonce does not match');
  }
  const field = which === 'access' ? 'access_token' : 'id_token';
  const token = resp[field];
  if (typeof token !== 'string' || !token) throw new Error(`token endpoint: response has no ${field}`);
  if (!isJwt(token)) {
    const hint = which === 'access' ? ' (opaque access token; some providers, such as Auth0, need audience set to issue a JWT)' : '';
    throw new Error(`token endpoint: ${field} is not a JWT${hint}`);
  }
  return { token, field, claims: decodeJwtPayload(token) };
}

/** The identity claim, lower-cased, or null when absent or not a non-empty string. */
export function identityFromClaims(claims, claim) {
  const v = claims?.[claim];
  if (typeof v !== 'string' || !v.trim()) return null;
  return v.toLowerCase(); // same rule as the inbox: lower-cased, not trimmed
}
