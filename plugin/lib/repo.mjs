// Repository id and branch for a checkout (contract section 4).
import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { hostname } from 'node:os';
import { basename, join } from 'node:path';

const strip = (s) => s.replace(/^\/+/, '').replace(/\/+$/, '').replace(/\.git$/, '');

/**
 * Normalizes a git remote to a repo id, the foundation rule: strip scheme,
 * user and host; scp form takes the part after ':'; strip a leading '/' and a
 * trailing '.git'; a local path or file:// URL becomes local/<basename>.
 * Symlinks are not resolved.
 */
export function normalizeRepo(remote) {
  const r = remote.trim();
  if (r.startsWith('file://')) return `local/${strip(basename(r.slice(7).replace(/\/+$/, '')))}`;
  const url = r.match(/^[a-z][a-z0-9+.-]*:\/\/(?:[^@/]*@)?[^/]*(\/.*)?$/i);
  if (url) return strip(url[1] ?? '');
  const scp = r.match(/^(?:[^@/]+@)?[^:/]+:(.+)$/);
  if (scp) return strip(scp[1]);
  return `local/${strip(basename(r.replace(/\/+$/, '')))}`;
}

function git(cwd, ...args) {
  return execFileSync('git', ['-C', cwd, ...args], { encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'] }).trim();
}

/**
 * Reads the marker make-quarantine.sh writes to <checkout>/.aic/quarantine,
 * or returns null. The worktree sits on the local branch aic/q-<iid>, so the
 * marker carries the MR source branch for routing (D15).
 */
export function quarantineMarker(checkout) {
  try {
    const m = JSON.parse(readFileSync(join(checkout, '.aic', 'quarantine'), 'utf8'));
    return m && typeof m === 'object' ? m : {};
  } catch (e) {
    return e.code === 'ENOENT' ? null : {};
  }
}

/** Returns {host, repo, branch, cwd, quarantine} as PUT /v1/sessions/{id} expects. */
export function sessionInfo(checkout, cwd = checkout) {
  const marker = quarantineMarker(checkout);
  const head = git(checkout, 'rev-parse', '--abbrev-ref', 'HEAD');
  return {
    host: hostname(),
    repo: normalizeRepo(git(checkout, 'remote', 'get-url', 'origin')),
    branch: marker && typeof marker.source_branch === 'string' && marker.source_branch ? marker.source_branch : head,
    cwd,
    quarantine: marker !== null,
  };
}
