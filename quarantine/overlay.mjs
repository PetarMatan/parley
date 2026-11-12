#!/usr/bin/env node
// Writes the quarantine's generated files. Called by make-quarantine.sh after
// the base-branch files are restored:
//   <state>/aic-quarantine.json         guard config
//   <state>/aic-session-settings.json   passed with --settings
// <state> is $AIC_WORK/quarantine/<iid>: outside the worktree and outside
// clone-b/.git, both of which the session's sandboxed Bash may write.
//   <worktree>/.claude/settings.json    base-branch settings + guard overlay
//   <worktree>/start-session.sh
// The channel server, the hooks and the skills come from the installed Parley
// plugin (D49), so no server is added to the worktree's .mcp.json.
//
// node overlay.mjs --root R --worktree Q --state S --work W --mr <mr.json>
//                  --source-sha S --target-sha T [--extra-guard <command>]

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const HERE = path.dirname(fileURLToPath(import.meta.url));
const TEMPLATES = path.join(HERE, 'templates');

const readJSON = (p, fallback) => {
  try { return JSON.parse(fs.readFileSync(p, 'utf8')); } catch (e) {
    if (e.code === 'ENOENT') return fallback;
    throw new Error(`${p}: ${e.message}`);
  }
};
const q = (s) => {
  if (/["\\$`]/.test(s)) throw new Error(`path must not contain quotes, backslashes, $ or backticks: ${s}`);
  return `"${s}"`;
};

// mergeSettings adds the guard overlay to the base-branch settings. Base hooks
// stay (SessionStart registration lives there); a base "disableAllHooks"
// would switch the guard off, so it is removed.
export function mergeSettings(base, overlay) {
  const out = structuredClone(base ?? {});
  delete out.disableAllHooks;
  out.hooks ??= {};
  out.hooks.PreToolUse = [...(out.hooks.PreToolUse ?? []), ...overlay.hooks.PreToolUse];
  out.permissions ??= {};
  out.permissions.deny = [...new Set([...(out.permissions.deny ?? []), ...overlay.permissions.deny])];
  out.sandbox = { ...(out.sandbox ?? {}), ...overlay.sandbox, network: { ...(out.sandbox?.network ?? {}), ...overlay.sandbox.network } };
  return out;
}

export function overlayFor({ root, configPath, extraGuard }) {
  const o = JSON.parse(fs.readFileSync(path.join(TEMPLATES, 'settings.quarantine.json'), 'utf8'));
  const guardCmd = `node ${q(path.join(root, 'quarantine', 'guard.mjs'))} --config ${q(configPath)}`;
  for (const m of o.hooks.PreToolUse) {
    m.hooks = m.hooks
      .filter((h) => h.command !== '__EXTRA_GUARD__' || extraGuard)
      .map((h) => ({ ...h, command: h.command === '__GUARD__' ? guardCmd : extraGuard }));
  }
  return o;
}

// Worktree paths sandboxed Bash must not write, whatever command it uses; the
// guard only parses commands, so interpreter writes need this floor. Claude
// Code already protects .claude/settings*.json and .mcp.json.
export const DENY_WRITE_IN_WORKTREE = [
  '.aic', '.claude', '.mcp.json', 'CLAUDE.md', '.husky', '.github', '.gitlab-ci.yml', '.npmrc',
  'package.json', 'package-lock.json', 'npm-shrinkwrap.json', 'pnpm-lock.yaml', 'yarn.lock',
];
// Secrets Bash must not read: the mock signing key and every user's token
// (the plugin and hooks read the token outside the sandbox), and the usual
// credential stores in $HOME.
export const DENY_READ_HOME = ['~/.ssh', '~/.aws', '~/.gnupg', '~/.config/gh', '~/.config/glab-cli', '~/.docker', '~/.kube', '~/.netrc', '~/.npmrc'];

export function sessionSettingsFor(base, { worktree, work }) {
  const out = structuredClone(base);
  out.sandbox.filesystem = {
    ...(out.sandbox.filesystem ?? {}),
    denyRead: [path.join(work, 'keys'), path.join(work, 'tokens'), path.join(work, 'quarantine'), ...DENY_READ_HOME],
    denyWrite: DENY_WRITE_IN_WORKTREE.map((p) => path.join(worktree, p)),
  };
  return out;
}

function args(argv) {
  const a = {};
  for (let i = 2; i < argv.length; i += 2) a[argv[i].replace(/^--/, '')] = argv[i + 1];
  for (const k of ['root', 'worktree', 'state', 'work', 'mr', 'source-sha', 'target-sha']) {
    if (!a[k]) throw new Error(`missing --${k}`);
  }
  return a;
}

function main() {
  const a = args(process.argv);
  const mr = readJSON(a.mr);
  const configPath = path.join(a.state, 'aic-quarantine.json');
  const sessionSettings = path.join(a.state, 'aic-session-settings.json');
  const write = (p, v) => { fs.mkdirSync(path.dirname(p), { recursive: true }); fs.writeFileSync(p, JSON.stringify(v, null, 2) + '\n'); };

  write(configPath, {
    worktree: a.worktree, work: a.work, mr_iid: mr.iid, repo: mr.repo,
    source_branch: mr.source_branch, target_branch: mr.target_branch,
    source_sha: a['source-sha'], target_sha: a['target-sha'],
    created_at: new Date().toISOString(),
  });
  write(sessionSettings, sessionSettingsFor(readJSON(path.join(TEMPLATES, 'session-settings.json')), { worktree: a.worktree, work: a.work }));

  const settingsPath = path.join(a.worktree, '.claude', 'settings.json');
  write(settingsPath, mergeSettings(readJSON(settingsPath, {}), overlayFor({ root: a.root, configPath, extraGuard: a['extra-guard'] })));
  const tmpl = fs.readFileSync(path.join(TEMPLATES, 'start-session.sh.tmpl'), 'utf8');
  for (const p of [a.worktree, a.work, sessionSettings]) q(p);
  const script = tmpl.replaceAll('__QDIR__', a.worktree).replaceAll('__WORK__', a.work).replaceAll('__SESSION_SETTINGS__', sessionSettings);
  fs.writeFileSync(path.join(a.worktree, 'start-session.sh'), script, { mode: 0o755 });
}

if (process.argv[1] && fs.realpathSync(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try { main(); } catch (e) { process.stderr.write(`overlay: ${e.message}\n`); process.exit(1); }
}
