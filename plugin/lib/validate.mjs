// Node mirror of inbox/internal/rules/validate.go. Go is canonical.
// Each validator takes the raw body (string or Buffer) and returns
// { ok: true, value, tags } or { ok: false, error: { code, message } }.
import { clean, mergeStripped, matchSecret, injectionScore, hiddenClass } from './sanitize.mjs';

export const CATEGORIES = ['bug', 'security', 'performance', 'maintainability', 'test', 'docs', 'style'];
export const SEVERITIES = ['blocker', 'major', 'minor', 'nit'];
export const CLASSES = ['fact', 'taste', 'product'];
export const REPLY_KINDS = ['fixed', 'disputed', 'answer', 'accepted', 'withdrawn', 'escalated', 'acknowledged'];
export const REPLY_KIND_STATE = {
  fixed: 'completed', accepted: 'completed', withdrawn: 'canceled',
  escalated: 'input-required', disputed: 'working', answer: 'working', acknowledged: 'working',
};

export const MAX = { title: 120, note: 2000, patch: 4000, body: 8000, file: 512, contextId: 256 };

export const CONTEXT_ID_RE = /^[A-Za-z0-9._-]+(?:\/[A-Za-z0-9._-]+)*![1-9][0-9]{0,9}$/;
export const TASK_ID_RE = /^f-[0-9a-f]{8}$/;
export const COMMIT_RE = /^[0-9a-f]{40}$/;

class RuleError extends Error {
  constructor(code, message) { super(message); this.code = code; }
}
const fail = (code, message) => new RuleError(code, message);

// Field specs in Go struct order (that is also the JSON output order).
const FINDING = {
  schema: 's', context_id: 's', task_id: 's', file: 's', line: 'i', category: 's',
  severity: 's', class: 's', title: 's', note: 's', suggested_patch: 's',
};
const REPLY = { schema: 's', context_id: 's', task_id: 's', kind: 's', note: 's', commit: 's' };
const NOTE = { schema: 's', context_id: 's', body: 's' };

export function validateFinding(raw) {
  return run(() => {
    const f = parse(raw, FINDING, ['schema', 'context_id', 'task_id', 'file', 'line', 'category', 'severity', 'class', 'title', 'note']);
    firstErr(
      checkSchema(f.schema, 'finding.v1'),
      checkContextID(f.context_id),
      checkTaskID(f.task_id),
      checkFile(f.file),
      checkLine(f.line),
      checkEnum('category', f.category, CATEGORIES),
      checkEnum('severity', f.severity, SEVERITIES),
      checkEnum('class', f.class, CLASSES),
      checkLen('title', f.title, MAX.title, true),
      checkLen('note', f.note, MAX.note, true),
      checkLen('suggested_patch', f.suggested_patch, MAX.patch, false),
    );
    const rawText = [f.title, f.note, f.suggested_patch];
    const t = clean(f.title, false);
    const n = clean(f.note, false);
    const p = clean(f.suggested_patch, true);
    f.title = t.text; f.note = n.text; f.suggested_patch = p.text;
    if (isBlank(f.title)) throw fail('empty_field', 'title is empty after cleaning');
    const cleaned = [f.title, f.note, f.suggested_patch];
    scan(rawText, cleaned);
    if (f.suggested_patch === '') delete f.suggested_patch;
    return { value: f, tags: { injection_score: injectionScore(cleaned.join('\n')), stripped: mergeStripped(t.stripped, n.stripped, p.stripped) } };
  });
}

export function validateReply(raw) {
  return run(() => {
    const r = parse(raw, REPLY, ['schema', 'context_id', 'task_id', 'kind', 'note']);
    firstErr(
      checkSchema(r.schema, 'reply.v1'),
      checkContextID(r.context_id),
      checkTaskID(r.task_id),
      checkEnum('kind', r.kind, REPLY_KINDS),
      checkLen('note', r.note, MAX.note, true),
      checkCommit(r.commit),
    );
    const rawNote = r.note;
    const c = clean(r.note, false);
    r.note = c.text;
    scan([rawNote], [r.note]);
    if (r.commit === '') delete r.commit;
    return { value: r, tags: { injection_score: injectionScore(r.note), stripped: c.stripped } };
  });
}

export function validateNote(raw) {
  return run(() => {
    const n = parse(raw, NOTE, ['schema', 'context_id', 'body']);
    firstErr(
      checkSchema(n.schema, 'note.v1'),
      checkContextID(n.context_id),
      checkLen('body', n.body, MAX.body, true),
    );
    const rawBody = n.body;
    const c = clean(n.body, false);
    n.body = c.text;
    scan([rawBody], [n.body]);
    return { value: n, tags: { injection_score: injectionScore(n.body), stripped: c.stripped } };
  });
}

/** Picks the validator by the payload's schema field. */
export function validatePayload(payload) {
  const raw = typeof payload === 'string' ? payload : JSON.stringify(payload);
  const schema = payload && typeof payload === 'object' ? payload.schema : undefined;
  if (schema === 'finding.v1') return validateFinding(raw);
  if (schema === 'reply.v1') return validateReply(raw);
  return { ok: false, error: { code: 'invalid_field', message: 'payload schema must be finding.v1 or reply.v1' } };
}

function run(fn) {
  try {
    const { value, tags } = fn();
    return { ok: true, value, tags };
  } catch (e) {
    if (e instanceof RuleError) return { ok: false, error: { code: e.code, message: e.message } };
    throw e;
  }
}

/**
 * Mirrors Go: json.Unmarshal into a map (syntax, non-object), then a strict
 * struct decode. Go matches keys exactly, else case-insensitively, walks keys
 * in source order, lets the last duplicate win, and reports the first type or
 * unknown-field error. JSON.parse reorders integer-like keys and merges
 * duplicates, so the top-level keys are scanned from the text instead.
 */
function parse(raw, spec, required) {
  const text = typeof raw === 'string' ? raw : new TextDecoder().decode(raw);
  let top;
  try {
    top = JSON.parse(text);
  } catch (e) {
    throw fail('bad_json', e.message);
  }
  const out = Object.fromEntries(Object.keys(spec).map((k) => [k, spec[k] === 'i' ? 0 : '']));
  // Go: null into a map is a no-op, so a null body ends in missing_field.
  if (top === null) return requireKeys(out, new Map(), required);
  if (typeof top !== 'object' || Array.isArray(top)) throw fail('bad_json', 'body must be a JSON object');

  const entries = scanTopLevel(text);
  const last = new Map();
  let saved = null;
  for (const { key, src } of entries) {
    last.set(key, src);
    const field = matchField(key, spec);
    if (!field) {
      saved ??= fail('unknown_field', `unknown field "${key}"`);
      continue;
    }
    if (src === 'null') continue;
    const v = decodeValue(src, spec[field]);
    if (v === TYPE_ERROR) {
      saved ??= fail('invalid_type', `field ${field} has the wrong type`);
      continue;
    }
    out[field] = v;
  }
  if (saved) throw saved;
  return requireKeys(out, last, required);
}

function requireKeys(out, last, required) {
  for (const k of required) {
    if (!last.has(k) || last.get(k) === 'null') throw fail('missing_field', `field ${k} is required`);
  }
  return out;
}

function matchField(key, spec) {
  if (Object.hasOwn(spec, key)) return key;
  // Go falls back to Unicode simple case folding; the iu flags do the same.
  return Object.keys(spec).find((f) => new RegExp(`^${f}$`, 'iu').test(key)) ?? null;
}

const TYPE_ERROR = Symbol('type error');
const INT_RE = /^-?(?:0|[1-9][0-9]*)$/;

function decodeValue(src, type) {
  if (type === 's') {
    if (src[0] !== '"') return TYPE_ERROR;
    // Go replaces lone surrogates with U+FFFD; JSON.parse keeps them.
    return JSON.parse(src).toWellFormed();
  }
  // Go int: an integer literal within int64; 12.0 and 1e2 are type errors.
  if (!INT_RE.test(src)) return TYPE_ERROR;
  const big = BigInt(src);
  if (big > 9223372036854775807n || big < -9223372036854775808n) return TYPE_ERROR;
  return Number(big);
}

/** Returns the top-level keys of an already valid JSON object with raw value text, in order. */
export function scanTopLevel(text) {
  const out = [];
  let i = skipWs(text, 0) + 1; // past '{'
  for (;;) {
    i = skipWs(text, i);
    if (text[i] === '}') return out;
    if (text[i] === ',') i = skipWs(text, i + 1);
    const keyEnd = stringEnd(text, i);
    const key = JSON.parse(text.slice(i, keyEnd));
    i = skipWs(text, keyEnd) + 1; // past ':'
    i = skipWs(text, i);
    const valEnd = valueEnd(text, i);
    out.push({ key, src: text.slice(i, valEnd) });
    i = valEnd;
  }
}

function skipWs(t, i) {
  while (t[i] === ' ' || t[i] === '\t' || t[i] === '\n' || t[i] === '\r') i++;
  return i;
}

function stringEnd(t, i) {
  for (let j = i + 1; ; j++) {
    if (t[j] === '\\') j++;
    else if (t[j] === '"') return j + 1;
  }
}

function valueEnd(t, i) {
  if (t[i] === '"') return stringEnd(t, i);
  if (t[i] === '{' || t[i] === '[') {
    let depth = 0;
    for (let j = i; ; j++) {
      const c = t[j];
      if (c === '"') j = stringEnd(t, j) - 1;
      else if (c === '{' || c === '[') depth++;
      else if (c === '}' || c === ']') { if (--depth === 0) return j + 1; }
    }
  }
  let j = i;
  while (j < t.length && !',}] \t\n\r'.includes(t[j])) j++;
  return j;
}

function scan(raw, cleaned) {
  for (const t of [...raw, ...cleaned]) {
    const name = matchSecret(t);
    if (name) throw fail('secret_detected', `text matches secret pattern ${name}`);
  }
}

function firstErr(...errs) {
  const e = errs.find(Boolean);
  if (e) throw e;
}

const cpLen = (s) => [...s].length;

// Go strings.TrimSpace uses unicode.IsSpace; String.prototype.trim differs
// (it trims U+FEFF, keeps U+0085).
const GO_BLANK_RE = /^[\t\n\v\f\r \u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]*$/;
const isBlank = (s) => GO_BLANK_RE.test(s);

function checkSchema(got, want) {
  return got === want ? null : fail('invalid_field', `schema must be "${want}"`);
}

function checkContextID(v) {
  return cpLen(v) <= MAX.contextId && CONTEXT_ID_RE.test(v) ? null : fail('invalid_field', 'context_id must be <repo>!<iid>');
}

function checkTaskID(v) {
  return TASK_ID_RE.test(v) ? null : fail('invalid_field', 'task_id must be f-<8 hex>');
}

function checkCommit(v) {
  return v === '' || COMMIT_RE.test(v) ? null : fail('invalid_field', 'commit must be a 40-hex sha');
}

function checkLine(v) {
  return v >= 1 ? null : fail('invalid_field', 'line must be >= 1');
}

function checkFile(v) {
  const bad = fail('invalid_field', 'file must be a repository-relative path');
  if (v === '' || cpLen(v) > MAX.file || v.startsWith('/') || v.includes('\\')) return bad;
  if (v.split('/').some((seg) => seg === '' || seg === '.' || seg === '..')) return bad;
  for (const ch of v) {
    const cp = ch.codePointAt(0);
    if (cp < 0x20 || cp === 0x7f || hiddenClass(cp)) return fail('invalid_field', 'file contains a control or hidden character');
  }
  return null;
}

function checkEnum(field, v, allowed) {
  return allowed.includes(v) ? null : fail('bad_enum', `${field} must be one of ${allowed.join(', ')}`);
}

function checkLen(field, v, max, required) {
  if (required && isBlank(v)) return fail('empty_field', `${field} must not be empty`);
  if (cpLen(v) > max) return fail('too_long', `${field} exceeds ${max} characters`);
  return null;
}
