#!/usr/bin/env node
// PreToolUse hook for the quarantine session (design 8.1, layer 3).
// Reads the hook event JSON on stdin. Blocks with exit 2 and the reason on
// stderr; otherwise exits 0 with no output, so the normal permission flow
// still applies (the guard never auto-allows). Any internal error blocks.
//
//   node guard.mjs --config $AIC_WORK/quarantine/<iid>/aic-quarantine.json
//
// The config lives outside the worktree and outside clone-b/.git (which the
// Claude Code sandbox lets a linked worktree write), so the session cannot
// rewrite it.

import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const HERE = path.dirname(fileURLToPath(import.meta.url));

// ---------- blocked files (mirror of inbox/internal/rules BlockedFiles) ----------

export const BLOCKED_GLOBS = JSON.parse(fs.readFileSync(path.join(HERE, 'blocked.json'), 'utf8')).globs;

// Same translation as rules.globToRegexp: a glob without "/" matches the base
// name at any depth, "**/" spans zero or more directories, case-insensitive.
export function globToRegExp(g) {
  let re = '^';
  if (!g.includes('/')) re += '(?:.*/)?';
  for (let i = 0; i < g.length; i++) {
    if (g.startsWith('**/', i)) { re += '(?:.*/)?'; i += 2; }
    else if (g.startsWith('**', i)) { re += '.*'; i += 1; }
    else if (g[i] === '*') re += '[^/]*';
    else if (g[i] === '?') re += '[^/]';
    else re += g[i].replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  }
  return new RegExp(re + '$', 'i');
}
const BLOCKED_RES = BLOCKED_GLOBS.map(globToRegExp);

export function isBlockedFile(rel) {
  const p = path.posix.normalize(rel.replaceAll('\\', '/')).replace(/^\.\//, '').replace(/^\/+/, '');
  return BLOCKED_RES.some((re) => re.test(p));
}

// ---------- secret files ----------

const SECRET_BASENAME = [
  /^\.env(\..*)?$/i, /^\.envrc$/i, /\.(pem|key|p12|pfx|jks|keystore|jwt)$/i,
  /^id_(rsa|dsa|ecdsa|ed25519)(\.pub)?$/i, /^\.(netrc|npmrc|pypirc|pgpass)$/i,
  /^credentials(\.json)?$/i, /^secrets?(\..*)?$/i, /^identity\.json$/i,
];
const SECRET_SEGMENT = /(^|\/)(\.ssh|\.aws|\.gnupg|\.docker|\.kube|tokens|keys)(\/|$)/i;

export function isSecretPath(p) {
  const s = String(p).replaceAll('\\', '/');
  return SECRET_BASENAME.some((re) => re.test(path.posix.basename(s))) || SECRET_SEGMENT.test(s);
}

const GIT_INTERNALS = /(^|\/)\.git(\/|$)/;

// ---------- paths ----------

// Resolves symlinks on the longest existing prefix, so a link inside the
// worktree that points outside it is caught.
export function realResolve(p, cwd) {
  let abs = path.resolve(cwd || '/', p);
  const rest = [];
  for (;;) {
    try {
      return path.join(fs.realpathSync(abs), ...rest.reverse());
    } catch {
      const parent = path.dirname(abs);
      if (parent === abs) return path.resolve(cwd || '/', p);
      rest.push(path.basename(abs));
      abs = parent;
    }
  }
}

function insideDir(abs, dir) {
  const rel = path.relative(dir, abs);
  return rel === '' || (!rel.startsWith('..') && !path.isAbsolute(rel));
}

const SCRATCH_DIRS = ['/dev/null', '/dev/stdout', '/dev/stderr', '/tmp', '/private/tmp', '/private/var/folders', '/var/folders'];

// ---------- shell tokenizer ----------

// Splits a command line into simple commands. Quotes are honored, $(...) and
// backtick bodies are returned in `subs` for a recursive check, and the word
// after a redirection operator is collected in `redirects`. It is deliberately
// simple: anything it misreads tends to produce more words, not fewer.
export function tokenize(src) {
  const commands = [];
  const redirects = [];
  const subs = [];
  let words = [];
  let cur = null;
  let redirectNext = false;

  const pushWord = () => {
    if (cur === null) return;
    if (redirectNext) { redirects.push(cur); redirectNext = false; } else words.push(cur);
    cur = null;
  };
  const endCommand = () => { pushWord(); if (words.length) commands.push(words); words = []; };
  const add = (s) => { cur = (cur ?? '') + s; };
  const readSub = (i, open, close) => { // returns [body, indexAfter]
    let depth = 1; let j = i;
    while (j < src.length && depth > 0) {
      if (src[j] === '\\') { j += 2; continue; }
      if (open && src[j] === open) depth++;
      else if (src[j] === close) depth--;
      j++;
    }
    return [src.slice(i, depth === 0 ? j - 1 : j), j];
  };

  for (let i = 0; i < src.length; i++) {
    const c = src[i];
    if (c === '\\') { add(src[i + 1] ?? ''); i++; continue; }
    if (c === "'") {
      const end = src.indexOf("'", i + 1);
      const stop = end === -1 ? src.length : end;
      add(src.slice(i + 1, stop)); i = stop; continue;
    }
    if (c === '"') {
      let j = i + 1; let s = '';
      while (j < src.length && src[j] !== '"') {
        if (src[j] === '\\') { s += src[j + 1] ?? ''; j += 2; continue; }
        if (src.startsWith('$(', j)) { const [b, k] = readSub(j + 2, '(', ')'); subs.push(b); s += `$(${b})`; j = k; continue; }
        if (src[j] === '`') { const [b, k] = readSub(j + 1, null, '`'); subs.push(b); s += '`' + b + '`'; j = k; continue; }
        s += src[j++];
      }
      add(s); i = j; continue;
    }
    if (src.startsWith('$(', i)) { const [b, k] = readSub(i + 2, '(', ')'); subs.push(b); add(`$(${b})`); i = k - 1; continue; }
    if (c === '`') { const [b, k] = readSub(i + 1, null, '`'); subs.push(b); add('`' + b + '`'); i = k - 1; continue; }
    if (c === '#' && cur === null) { while (i < src.length && src[i] !== '\n') i++; endCommand(); continue; }
    if (c === '<') { pushWord(); while (src[i + 1] === '<') i++; continue; } // input: next word is checked as a plain word
    if (c === '>' || (c === '&' && src[i + 1] === '>')) {
      if (cur !== null && /^\d+$/.test(cur)) cur = null; // "2>" names a fd, not a word
      pushWord();
      if (c === '&') i++;
      while (src[i + 1] === '>' || src[i + 1] === '|') i++;
      if (src[i + 1] === '&') { i++; while (/[\d-]/.test(src[i + 1] ?? '')) i++; continue; } // ">&2" duplicates a fd
      redirectNext = true;
      continue;
    }
    if (';&|\n()'.includes(c)) { endCommand(); continue; }
    if (c === ' ' || c === '\t' || c === '\r') { pushWord(); continue; }
    add(c);
  }
  endCommand();
  return { commands, redirects, subs };
}

// ---------- bash rules ----------

const NET_TOOLS = new Set([
  'curl', 'wget', 'nc', 'ncat', 'netcat', 'telnet', 'ssh', 'scp', 'sftp', 'rsync', 'ftp',
  'socat', 'docker', 'podman', 'kubectl', 'open', 'osascript', 'npx', 'pnpx', 'bunx',
  'brew', 'pipx', 'aws', 'gcloud', 'gh', 'glab', 'nslookup', 'dig', 'host', 'ping',
]);
const SHELLS = new Set(['sh', 'bash', 'zsh', 'dash', 'ksh', 'fish']);
const WRAPPERS = new Set(['sudo', 'command', 'builtin', 'exec', 'nohup', 'time', 'nice', 'stdbuf', 'caffeinate']);
const PKG_NET = {
  npm: ['install', 'i', 'in', 'ins', 'inst', 'insta', 'instal', 'isnt', 'isnta', 'isntal', 'add', 'ci', 'clean-install', 'install-test', 'it', 'publish', 'update', 'up', 'upgrade', 'exec', 'x', 'login', 'adduser', 'link', 'ln', 'unpublish', 'deprecate', 'dist-tag', 'owner', 'access', 'token', 'audit', 'outdated', 'view', 'info', 'show', 'pack', 'create', 'init'],
  yarn: ['add', 'install', 'dlx', 'publish', 'upgrade', 'up', 'npm', 'create', 'init'],
  pnpm: ['add', 'install', 'i', 'dlx', 'publish', 'update', 'up', 'exec', 'x', 'create', 'init'],
  bun: ['add', 'install', 'i', 'x', 'publish', 'update', 'create', 'init', 'pm'],
  pip: ['install', 'download', 'wheel', 'index'],
  pip3: ['install', 'download', 'wheel', 'index'],
  go: ['get', 'install', 'mod'],
  cargo: ['install', 'add', 'fetch', 'publish', 'update'],
  gem: ['install', 'push', 'fetch', 'update'],
};
const GIT_NET = new Set(['push', 'fetch', 'pull', 'remote', 'clone', 'ls-remote', 'submodule', 'daemon', 'send-email', 'request-pull', 'credential', 'http-fetch', 'http-push', 'upload-pack', 'receive-pack', 'archive', 'bundle', 'lfs', 'filter-branch', 'update-ref', 'symbolic-ref', 'replace', 'hook']);
const GIT_CONFIG_READ = new Set(['--get', '--get-all', '--get-regexp', '--list', '-l', '--show-origin', '--show-scope']);
const INLINE_CODE_FLAGS = { node: ['-e', '--eval', '-p', '--print'], python: ['-c'], perl: ['-e', '-E'], ruby: ['-e'], deno: ['eval'], php: ['-r'] };
const NET_IN_CODE = /\b(fetch|https?|net|dgram|dns|tls|child_process|spawn|exec|socket|urllib|requests|XMLHttpRequest|WebSocket|curl|wget)\b/i;
const MUTATORS = new Set(['tee', 'cp', 'mv', 'rm', 'touch', 'chmod', 'chown', 'ln', 'truncate', 'install', 'dd', 'patch', 'unlink', 'rmdir', 'mkdir']);

const deny = (rule, target, reason) => ({ decision: 'deny', rule, target, reason });
const ALLOW = { decision: 'allow', rule: 'none', target: '' };

function stripPrefix(words) {
  let w = words.slice();
  for (;;) {
    while (w.length && /^[A-Za-z_][A-Za-z0-9_]*=/.test(w[0])) w.shift();
    if (!w.length) return w;
    const name = path.basename(w[0]);
    if (WRAPPERS.has(name)) { w.shift(); while (w.length && w[0].startsWith('-')) w.shift(); continue; }
    if (name === 'env') { w.shift(); while (w.length && (w[0].startsWith('-') || w[0].includes('='))) w.shift(); continue; }
    if (name === 'timeout') { w.shift(); while (w.length && w[0].startsWith('-')) w.shift(); w.shift(); continue; }
    if (name === 'xargs') { w.shift(); while (w.length && w[0].startsWith('-')) w.shift(); continue; }
    return w;
  }
}

// git subcommands that write the paths they name. checkout and restore name
// paths after "--", or after the first operand (the source ref).
const GIT_PATH_WRITERS = new Set(['rm', 'mv', 'checkout', 'restore']);
const GIT_VALUE_FLAGS = new Set(['-s', '--source', '-m', '--message', '-b', '-B', '--orphan']);

function gitPathTargets(sub, rest) {
  const dd = rest.indexOf('--');
  if (dd !== -1) return rest.slice(dd + 1);
  const ops = [];
  for (let k = 0; k < rest.length; k++) {
    if (GIT_VALUE_FLAGS.has(rest[k])) { k++; continue; }
    if (!rest[k].startsWith('-')) ops.push(rest[k]);
  }
  return sub === 'checkout' ? ops.slice(1) : ops;
}

function checkGit(args, ctx) {
  let i = 0;
  while (i < args.length && args[i].startsWith('-')) {
    const a = args[i];
    if (a.startsWith('-c') || a.startsWith('--config-env')) return deny('git-config-override', a, 'git -c overrides are not allowed in the quarantine');
    if (a.startsWith('--git-dir') || a.startsWith('--work-tree') || a.startsWith('--exec-path')) return deny('git-internals', a, 'git must run on this worktree only');
    i += a === '-C' ? 2 : 1;
  }
  const sub = args[i];
  if (!sub) return null;
  if (GIT_NET.has(sub)) return deny('git-network', `git ${sub}`, `git ${sub} is not allowed in the quarantine; the promoter pushes`);
  if (sub === 'apply' || sub === 'am') return deny('patch-apply', `git ${sub}`, 'applying patch files is not allowed; edit the files in the diff directly');
  if (GIT_PATH_WRITERS.has(sub)) {
    for (const t of gitPathTargets(sub, args.slice(i + 1))) {
      const r = checkWriteTarget(t, ctx);
      if (r) return r;
    }
  }
  if (sub === 'config') {
    const rest = args.slice(i + 1);
    const read = rest.some((a) => GIT_CONFIG_READ.has(a)) || rest.filter((a) => !a.startsWith('-')).length <= 1;
    if (!read) return deny('git-config-write', `git config ${rest.join(' ')}`, 'git config writes are not allowed in the quarantine');
  }
  return null;
}

function checkCommand(words, ctx, depth) {
  const w = stripPrefix(words);
  if (!w.length) return null;
  const name = path.basename(w[0]);
  const args = w.slice(1);

  if (NET_TOOLS.has(name)) return deny('network-command', name, `${name} is a network or exfiltration tool`);
  if (name === 'git') { const r = checkGit(args, ctx); if (r) return r; }
  if (name === 'patch') return deny('patch-apply', name, 'applying patch files is not allowed; edit the files in the diff directly');
  const pkg = PKG_NET[name];
  if (pkg) {
    const sub = args.find((a) => !a.startsWith('-'));
    if (sub && pkg.includes(sub)) return deny('package-network', `${name} ${sub}`, `${name} ${sub} reaches the network or changes dependencies`);
  }
  if (/^python(\d(\.\d+)?)?$/.test(name)) {
    const m = args.indexOf('-m');
    if (m !== -1 && /^(pip|http\.server|ensurepip|venv|SimpleHTTPServer|smtpd|ftplib|webbrowser)$/.test(args[m + 1] ?? '')) {
      return deny('package-network', `${name} -m ${args[m + 1]}`, `python -m ${args[m + 1]} is not allowed in the quarantine`);
    }
  }
  const flags = INLINE_CODE_FLAGS[name.replace(/[\d.]+$/, '')];
  if (flags) {
    const k = args.findIndex((a) => flags.includes(a));
    if (k !== -1 && NET_IN_CODE.test(args.slice(k + 1).join(' '))) {
      return deny('network-inline-code', name, `inline ${name} code that uses network or process APIs is not allowed`);
    }
  }
  if (SHELLS.has(name) || name === 'eval' || name === 'source' || name === '.') {
    const ci = args.indexOf('-c');
    const body = name === 'eval' ? args.join(' ') : ci !== -1 ? args[ci + 1] : null;
    if (body != null) {
      if (depth > 4) return deny('too-deep', name, 'nested shell commands are too deep to check');
      const r = checkBash(body, ctx, depth + 1);
      if (r) return r;
    }
  }
  if (MUTATORS.has(name) || (name === 'sed' && args.some((a) => /^-[a-zA-Z]*i/.test(a) || a.startsWith('--in-place'))) || (name === 'perl' && args.some((a) => /^-[a-zA-Z]*i/.test(a)))) {
    // sed and perl take the script as their first operand unless -e/-f gives it.
    let skipScript = (name === 'sed' || name === 'perl') && !args.some((a) => /^-[a-zA-Z]*[ef]$/.test(a) || a.startsWith('--expression'));
    for (let k = 0; k < args.length; k++) {
      const a = args[k];
      if ((name === 'sed' || name === 'perl') && /^-[a-zA-Z]*[ef]$/.test(a)) { k++; continue; }
      if (a.startsWith('-')) continue;
      if (skipScript && a !== '') { skipScript = false; continue; }
      const target = a.startsWith('of=') ? a.slice(3) : a;
      const r = checkWriteTarget(target, ctx);
      if (r) return r;
    }
  }
  return null;
}

function checkWriteTarget(p, ctx) {
  if (!p) return null;
  // The guard cannot expand variables, globs or "~"; such a target is refused.
  if (/[$`~*?]/.test(p)) return deny('unresolved-target', p, `cannot check the write target ${p}; use a literal path`);
  const abs = realResolve(p, ctx.cwd);
  // The worktree itself lives under $TMPDIR, so check it before the scratch dirs.
  if (!insideDir(abs, ctx.worktree)) {
    if (SCRATCH_DIRS.some((d) => insideDir(abs, d))) return null;
    return deny('outside-worktree', abs, 'writes outside the quarantine worktree are not allowed');
  }
  const rel = path.relative(ctx.worktree, abs);
  if (GIT_INTERNALS.test(rel)) return deny('git-internals', rel, 'writes to .git are not allowed');
  if (isBlockedFile(rel)) return deny('blocked-file', rel, `${rel} is a blocked file class (CI, hooks, Claude config, dependencies)`);
  // Same scope as the Edit tools: inside the worktree only files in the MR
  // diff may change (this also stops deleting a whole test folder).
  if (!ctx.diffFiles().has(rel.split(path.sep).join('/'))) return deny('not-in-diff', rel, `${rel} is not part of the merge request diff; write scratch files under /tmp`);
  return null;
}

export function checkBash(command, ctx, depth = 0) {
  const { commands, redirects, subs } = tokenize(String(command));
  for (const words of commands) {
    for (const word of words) {
      if (isSecretPath(word)) return deny('secret-read', word, 'secret and credential files are not readable in the quarantine');
      if (GIT_INTERNALS.test(word) || /core\.hookspath/i.test(word)) return deny('git-internals', word, '.git internals and hooks are off limits');
    }
    const r = checkCommand(words, ctx, depth);
    if (r) return r;
  }
  for (const t of redirects) {
    if (isSecretPath(t)) return deny('secret-read', t, 'secret and credential files are off limits');
    const r = checkWriteTarget(t, ctx);
    if (r) return r;
  }
  for (const s of subs) {
    if (depth > 4) return deny('too-deep', s, 'nested command substitution is too deep to check');
    const r = checkBash(s, ctx, depth + 1);
    if (r) return r;
  }
  return null;
}

// ---------- tool dispatch ----------

const EDIT_TOOLS = new Set(['Edit', 'Write', 'MultiEdit', 'NotebookEdit']);

// decide returns {decision, rule, target, reason?}. ctx = {worktree, cwd, diffFiles(): Set<string>}.
export function decide(input, ctx) {
  const tool = input?.tool_name;
  const ti = input?.tool_input ?? {};
  if (tool === 'WebFetch' || tool === 'WebSearch') {
    return deny('network-tool', ti.url ?? ti.query ?? '', `${tool} is disabled in the quarantine`);
  }
  if (EDIT_TOOLS.has(tool)) {
    const p = ti.file_path ?? ti.notebook_path;
    if (typeof p !== 'string' || p === '') return deny('bad-input', '', `${tool} without a file path`);
    const abs = realResolve(p, ctx.cwd);
    if (!insideDir(abs, ctx.worktree)) return deny('outside-worktree', abs, 'edits outside the quarantine worktree are not allowed');
    const rel = path.relative(ctx.worktree, abs).split(path.sep).join('/');
    if (GIT_INTERNALS.test(rel)) return deny('git-internals', rel, 'edits to .git are not allowed');
    if (isBlockedFile(rel)) return deny('blocked-file', rel, `${rel} is a blocked file class (CI, hooks, Claude config, dependencies)`);
    if (isSecretPath(rel)) return deny('secret-read', rel, 'secret files are off limits');
    if (!ctx.diffFiles().has(rel)) return deny('not-in-diff', rel, `${rel} is not part of the merge request diff`);
    return { ...ALLOW, target: rel };
  }
  if (tool === 'Read' || tool === 'Grep' || tool === 'Glob') {
    const p = ti.file_path ?? ti.path ?? '';
    const pat = ti.pattern && tool === 'Glob' ? ti.pattern : '';
    if ((p && isSecretPath(p)) || (pat && isSecretPath(pat))) return deny('secret-read', p || pat, 'secret and credential files are not readable in the quarantine');
    if (tool === 'Grep' && ti.glob && isSecretPath(ti.glob)) return deny('secret-read', ti.glob, 'secret and credential files are not readable in the quarantine');
    return { ...ALLOW, target: p };
  }
  if (tool === 'Bash') {
    if (typeof ti.command !== 'string') return deny('bad-input', '', 'Bash without a command');
    return checkBash(ti.command, ctx) ?? { ...ALLOW, target: ti.command.slice(0, 200) };
  }
  return { ...ALLOW, target: '' };
}

// ---------- diff files, cached per session ----------

export function diffFilesLoader(cfg, sessionId, gitDir) {
  let memo = null;
  return () => {
    if (memo) return memo;
    const cache = path.join(gitDir, `aic-guard-diff-${String(sessionId).replace(/[^A-Za-z0-9_-]/g, '_')}.json`);
    try {
      const c = JSON.parse(fs.readFileSync(cache, 'utf8'));
      if (c.source_sha === cfg.source_sha && c.target_sha === cfg.target_sha) return (memo = new Set(c.files));
    } catch { /* no cache yet */ }
    // SHAs recorded at quarantine creation, not refs: the session cannot move them.
    const out = execFileSync('git', ['-C', cfg.worktree, '-c', 'core.hooksPath=/dev/null', 'diff', '--name-only', '--no-renames', `${cfg.target_sha}...${cfg.source_sha}`], { encoding: 'utf8' });
    const files = out.split('\n').filter(Boolean);
    try { fs.writeFileSync(cache, JSON.stringify({ source_sha: cfg.source_sha, target_sha: cfg.target_sha, files })); } catch { /* cache is optional */ }
    return (memo = new Set(files));
  };
}

// ---------- logging ----------

export function logLine(work, entry, now = new Date()) {
  const level = entry.decision === 'deny' ? 'WARN' : entry.decision === 'error' ? 'ERROR' : 'INFO';
  const intLevel = { ERROR: 5, WARN: 4, INFO: 3 }[level];
  const line = JSON.stringify({ ts: now.toISOString(), level, intLevel, event: 'guard_decision', ...entry }) + '\n';
  try {
    fs.mkdirSync(path.join(work, 'logs'), { recursive: true });
    fs.appendFileSync(path.join(work, 'logs', 'guard.jsonl'), line);
  } catch { /* logging must not change the decision */ }
}

// ---------- main ----------

async function main() {
  let cfg; let input = {}; let work = process.env.AIC_WORK || '';
  const block = (rule, reason, target = '') => {
    if (work) logLine(work, { session_id: input.session_id ?? '', tool: input.tool_name ?? '', decision: 'deny', rule, target });
    process.stderr.write(`aic quarantine guard: ${reason}\n`);
    process.exit(2);
  };
  try {
    const i = process.argv.indexOf('--config');
    if (i === -1) return block('bad-config', 'guard started without --config');
    const cfgPath = process.argv[i + 1];
    cfg = JSON.parse(fs.readFileSync(cfgPath, 'utf8'));
    work = cfg.work || work;
    let raw = '';
    for await (const chunk of process.stdin) raw += chunk;
    input = JSON.parse(raw);
    const worktree = fs.realpathSync(cfg.worktree);
    const ctx = {
      worktree,
      cwd: input.cwd || worktree,
      diffFiles: diffFilesLoader({ ...cfg, worktree }, input.session_id ?? 'none', path.dirname(cfgPath)),
    };
    const d = decide(input, ctx);
    logLine(work, { session_id: input.session_id ?? '', tool: input.tool_name ?? '', decision: d.decision, rule: d.rule, target: d.target });
    if (d.decision === 'deny') {
      process.stderr.write(`aic quarantine guard: ${d.reason}\n`);
      process.exit(2);
    }
    process.exit(0);
  } catch (err) {
    block('guard-error', `blocked because the guard failed: ${err.message}`);
  }
}

if (process.argv[1] && fs.realpathSync(process.argv[1]) === fileURLToPath(import.meta.url)) main();
