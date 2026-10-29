// Node mirror of inbox/internal/rules (clean.go, scan.go). Go is canonical;
// both pass every case in schema/vectors.json. Regexes avoid \s and \S because
// Go RE2 means ASCII [\t\n\f\r ] while JavaScript means Unicode whitespace.
import { posix } from 'node:path';

export const STRIP_ORDER = [
  'zero-width', 'bidi', 'unicode-tag', 'bom',
  'html-comment', 'image', 'link', 'tag', 'blank-lines',
];

export const FRAME_NAMES = ['channel', 'system', 'system-reminder', 'user', 'assistant', 'human', 'instructions'];

/** Returns the stripped name for a code point that must be removed, or ''. */
export function hiddenClass(cp) {
  if ((cp >= 0x200b && cp <= 0x200d) || (cp >= 0x2060 && cp <= 0x2064) || cp === 0xad || cp === 0x180e || (cp >= 0xfff9 && cp <= 0xfffb)) return 'zero-width';
  if (cp === 0x061c || cp === 0x200e || cp === 0x200f || (cp >= 0x202a && cp <= 0x202e) || (cp >= 0x2066 && cp <= 0x2069)) return 'bidi';
  if (cp >= 0xe0000 && cp <= 0xe007f) return 'unicode-tag';
  if (cp === 0xfeff) return 'bom';
  return '';
}

const HTML_COMMENT_RE = /<!--[\s\S]*?(?:-->|$)/gu;
const IMAGE_RE = /!\[[^\]\n]*\]\([^)\n]*\)/gu;
const LINK_RE = /\[([^\]\n]*)\]\([^)\n]*\)/gu;
const BLANK_LINES_RE = /\n(?:[ \t]*\n){3,}/gu;

/**
 * Cleans one text field. Returns { text, stripped } with stripped in STRIP_ORDER.
 * A patch skips the markdown and blank-line rules and keeps a tag-shaped match
 * right after an identifier character (Array<string>), unless it is a closing
 * tag, its name has a hyphen, or its name is a frame name.
 */
export function clean(text, isPatch = false) {
  const fired = new Set();
  let s = '';
  for (const ch of text) {
    const cp = ch.codePointAt(0);
    const c = hiddenClass(cp);
    if (c) fired.add(c);
    else s += String.fromCodePoint(foldCodePoint(cp));
  }
  // Repeat until stable: removing one tag can join the halves of another.
  for (let i = 0; i < 8; i++) {
    const prev = s;
    s = replaceFired(HTML_COMMENT_RE, s, '', 'html-comment', fired);
    if (!isPatch) {
      s = replaceFired(IMAGE_RE, s, '[image removed]', 'image', fired);
      s = replaceFired(LINK_RE, s, '$1', 'link', fired);
    }
    s = stripTags(s, isPatch, fired);
    if (s === prev) break;
  }
  if (!isPatch) s = replaceFired(BLANK_LINES_RE, s, '\n\n\n', 'blank-lines', fired);
  return { text: s, stripped: orderStripped(fired) };
}

function replaceFired(re, s, repl, name, fired) {
  if (s.search(re) === -1) return s;
  fired.add(name);
  return s.replace(re, repl);
}

/**
 * Mirrors Go foldRune: the stdlib-only stand-in for NFKC. Look-alike angle
 * brackets become ASCII < and >, fullwidth ASCII (U+FF01–U+FF5E) becomes ASCII,
 * every Unicode space separator (Zs) becomes ASCII space. Deliberately not
 * String.prototype.normalize('NFKC'): Go has no NFKC without x/text, and both
 * implementations must give the same text.
 */
export function foldCodePoint(cp) {
  switch (cp) {
    case 0xff1c: case 0xfe64: case 0x2039: case 0x3008: case 0x27e8: case 0x2329: return 0x3c;
    case 0xff1e: case 0xfe65: case 0x203a: case 0x3009: case 0x27e9: case 0x232a: return 0x3e;
    case 0x00a0: case 0x1680: case 0x202f: case 0x205f: case 0x3000: return 0x20;
    default:
  }
  if (cp >= 0x2000 && cp <= 0x200a) return 0x20;
  if (cp >= 0xff01 && cp <= 0xff5e) return cp - 0xfee0;
  return cp;
}

// Mirrors Go isTagSpace: ASCII whitespace incl. newline, NEL, U+2028, U+2029.
const isTagSpace = (c) => c === '\t' || c === '\n' || c === '\v' || c === '\f' || c === '\r' || c === ' ' ||
  c === '\u0085' || c === ' ' || c === ' ';
const isNameStart = (c) => c !== undefined && /^[A-Za-z]$/.test(c);
const isNameChar = (c) => c !== undefined && /^[A-Za-z0-9_:.-]$/.test(c);

/**
 * Mirrors Go tagAt on an array of code points. Strict <name…>, </name>;
 * tolerant < name>, </ name>; unterminated <name … or </name … (no space
 * after < or /) removed to end of line.
 * Returns null or {end, name, closing, loose}.
 */
function tagAt(rs, i) {
  const n = rs.length;
  let j = i + 1;
  let loose = false;
  let closing = false;
  while (j < n && isTagSpace(rs[j])) { j++; loose = true; }
  if (j < n && rs[j] === '/') {
    closing = true;
    j++;
    while (j < n && isTagSpace(rs[j])) { j++; loose = true; }
  }
  if (j >= n || !isNameStart(rs[j])) return null;
  let k = j;
  while (k < n && isNameChar(rs[k])) k++;
  const name = rs.slice(j, k).join('').toLowerCase();
  if (k < n && rs[k] === '>') return { end: k + 1, name, closing, loose };
  if (k + 1 < n && rs[k] === '/' && rs[k + 1] === '>') return { end: k + 2, name, closing, loose };
  if (k < n && !isTagSpace(rs[k])) return null; // Array<string, f<T>(x), <https://…>
  let q = k;
  while (q < n && rs[q] !== '<' && rs[q] !== '>') q++;
  if (q < n && rs[q] === '>') return { end: q + 1, name, closing, loose };
  if (loose) return null; // spaced and unterminated: a comparison (x < limit)
  let end = k;
  while (end < n && rs[end] !== '\n') end++;
  return { end, name, closing, loose: true };
}

function stripTags(s, isPatch, fired) {
  if (!s.includes('<')) return s;
  const rs = Array.from(s);
  const out = [];
  for (let i = 0; i < rs.length;) {
    if (rs[i] !== '<') { out.push(rs[i++]); continue; }
    const m = tagAt(rs, i);
    if (!m) { out.push(rs[i++]); continue; }
    const afterIdent = i > 0 && rs[i - 1].length === 1 && isIdentChar(rs[i - 1].charCodeAt(0));
    if (isPatch && !m.closing && (afterIdent || m.loose) && !m.name.includes('-') && !FRAME_NAMES.includes(m.name)) {
      out.push(rs[i++]);
      continue;
    }
    fired.add('tag');
    i = m.end;
  }
  return out.join('');
}

// Mirrors Go isIdentByte: a non-ASCII predecessor is never an identifier byte.
function isIdentChar(c) {
  return c === 0x5f || c === 0x24 || c === 0x29 || c === 0x5d ||
    (c >= 0x61 && c <= 0x7a) || (c >= 0x41 && c <= 0x5a) || (c >= 0x30 && c <= 0x39);
}

export function orderStripped(fired) {
  return STRIP_ORDER.filter((n) => fired.has(n));
}

export function mergeStripped(...lists) {
  return orderStripped(new Set(lists.flat()));
}

// Secret patterns, contract section 5. Only "assignment" is case-insensitive;
// the u flag gives it Go's simple case folding and code-point counting.
export const SECRET_PATTERNS = [
  { name: 'private-key', re: /-----BEGIN [A-Z ]*PRIVATE KEY-----/ },
  { name: 'aws-access-key', re: /AKIA[0-9A-Z]{16}/ },
  { name: 'gitlab-pat', re: /glpat-[0-9A-Za-z_-]{20,}/ },
  { name: 'github-pat', re: /ghp_[0-9A-Za-z]{36}/ },
  { name: 'anthropic-key', re: /sk-ant-[0-9A-Za-z_-]{20,}/ },
  { name: 'jwt', re: /eyJ[A-Za-z0-9_-]+\.eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+/ },
  { name: 'assignment', re: /(password|secret|api[_-]?key|token)[A-Za-z0-9_]*["']?[\t\n\f\r ]*[:=][\t\n\f\r ]*[^\t\n\f\r ]{8,}/iu },
  { name: 'url-credential', re: /:\/\/[^/\t\n\f\r :@]+:[^@\t\n\f\r /]{6,}@/u },
];

export const CODE_SECRET_DETECTED = 'secret_detected';

/** Returns the name of the first matching secret pattern, or ''. */
export function matchSecret(text) {
  for (const p of SECRET_PATTERNS) if (p.re.test(text)) return p.name;
  return '';
}

export function scanSecrets(text) {
  return matchSecret(text) ? CODE_SECRET_DETECTED : '';
}

export const INJECTION_KEYWORDS = [
  'ignore previous', 'system prompt', 'delete', 'push', 'force', 'curl',
  'wget', '.env', 'credentials', 'rm -rf', 'base64',
];

// Go strings.ToLower maps per rune; JS toLowerCase on a whole string applies
// full mappings (U+0130 becomes two code points) and final-sigma context.
function goLower(text) {
  let out = '';
  for (const ch of text) out += ch === '\u0130' ? 'i' : ch.toLowerCase();
  return out;
}

/** 0.25 per distinct keyword, case-insensitive substring match, capped at 1. */
export function injectionScore(text) {
  const lower = goLower(text);
  const hits = INJECTION_KEYWORDS.filter((k) => lower.includes(k)).length;
  return Math.min(1, hits * 0.25);
}

export const BLOCKED_FILES = [
  '.gitlab-ci.yml', '.github/**', '.claude/**', '.mcp.json', 'CLAUDE.md',
  '.husky/**', '**/package.json', '**/package-lock.json', '**/go.mod',
  '**/go.sum', '**/*.lock', '**/requirements*.txt', '**/pyproject.toml',
  'Dockerfile*', '.aic/**',
  '**/pnpm-lock.yaml', '**/npm-shrinkwrap.json', '**/.npmrc', '**/.claude/**',
];

function globToRegExp(g) {
  // Go's . excludes only \n; JavaScript's also excludes \r, U+2028, U+2029.
  let re = '^';
  if (!g.includes('/')) re += '(?:[^\\n]*/)?';
  for (let i = 0; i < g.length; i++) {
    const c = g[i];
    if (g.startsWith('**/', i)) { re += '(?:[^\\n]*/)?'; i += 2; }
    else if (g.startsWith('**', i)) { re += '[^\\n]*'; i += 1; }
    else if (c === '*') re += '[^/]*';
    else if (c === '?') re += '[^/]';
    else re += c.replace(/[.*+?^${}()|[\]\\/]/g, '\\$&');
  }
  return new RegExp(re + '$', 'iu');
}

const BLOCKED_RES = BLOCKED_FILES.map(globToRegExp);

// Go path.Clean: like posix.normalize, but without a trailing slash.
function goCleanPath(p) {
  let c = posix.normalize(p);
  if (c.length > 1 && c.endsWith('/')) c = c.slice(0, -1);
  return c;
}

/** Reports whether a repository-relative path is a blocked file class. */
export function isBlockedFile(p) {
  let c = goCleanPath(p.replaceAll('\\', '/'));
  if (c.startsWith('./')) c = c.slice(2);
  if (c.startsWith('/')) c = c.slice(1);
  return BLOCKED_RES.some((re) => re.test(c));
}
