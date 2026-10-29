#!/usr/bin/env bash
# Creates the quarantine for one fake merge request (design 8.1, layer 3):
# a git worktree of $AIC_WORK/clone-b at the head of origin/<source_branch>,
# in $TMPDIR/aic-quarantine-<iid>, on the local branch aic/q-<iid>.
#
#   quarantine/make-quarantine.sh <mr-iid> [--work DIR]
#
# Repository instructions come from the target branch (D16): .claude/,
# .mcp.json and every CLAUDE.md are replaced by their target-branch version,
# then the guard settings are laid over them. It does not start a session;
# run <quarantine>/start-session.sh for that. Re-running recreates it.
#
# Logs go to stderr. The last stdout line is machine-readable:
#   {"worktree":"<dir>","state":"$AIC_WORK/quarantine/<iid>"}
# The state dir holds the guard config, the --settings file and the policy
# the promoter reads. It is outside the worktree and outside clone-b/.git,
# which the Claude Code sandbox lets a linked worktree write.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
usage() { echo "usage: $0 <mr-iid> [--work DIR]" >&2; exit 64; }
log() { printf '[quarantine] %s\n' "$*" >&2; }
die() { printf '[quarantine] error: %s\n' "$*" >&2; exit 1; }

[[ $# -ge 1 ]] || usage
IID="$1"; shift
WORK="${AIC_WORK:-$ROOT/work}"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --work) [[ $# -ge 2 ]] || usage; WORK="$2"; shift 2 ;;
    *) usage ;;
  esac
done
[[ "$IID" =~ ^[1-9][0-9]{0,9}$ ]] || die "mr iid must be a positive integer"
[[ -d "$WORK" ]] || die "work dir $WORK does not exist (run make setup)"
WORK="$(cd "$WORK" && pwd -P)"

MR="$WORK/mr/$IID.json"
CLONE="$WORK/clone-b"
[[ -f "$MR" ]] || die "$MR not found"
[[ -d "$CLONE/.git" ]] || die "$CLONE is not a git clone"

# git without the user's hooks or signing; the checkout must not run repo hooks.
g() { git -c core.hooksPath=/dev/null -c commit.gpgsign=false -c advice.detachedHead=false "$@"; }

SRC="$(node -e 'const m=JSON.parse(require("fs").readFileSync(process.argv[1],"utf8"));process.stdout.write(String(m.source_branch??""))' "$MR")"
TGT="$(node -e 'const m=JSON.parse(require("fs").readFileSync(process.argv[1],"utf8"));process.stdout.write(String(m.target_branch??""))' "$MR")"
g check-ref-format --branch "$SRC" >/dev/null || die "bad source_branch '$SRC'"
g check-ref-format --branch "$TGT" >/dev/null || die "bad target_branch '$TGT'"

log "fetching origin in $CLONE"
g -C "$CLONE" fetch -q --no-tags origin
SRC_SHA="$(g -C "$CLONE" rev-parse --verify "refs/remotes/origin/$SRC^{commit}")"
TGT_SHA="$(g -C "$CLONE" rev-parse --verify "refs/remotes/origin/$TGT^{commit}")"

TMP="${TMPDIR:-/tmp}"; TMP="${TMP%/}"
QDIR="$TMP/aic-quarantine-$IID"
if [[ -e "$QDIR" ]]; then
  log "removing the old quarantine at $QDIR"
  g -C "$CLONE" worktree remove --force "$QDIR" 2>/dev/null || rm -rf "$QDIR"
fi
g -C "$CLONE" worktree prune

log "worktree $QDIR at origin/$SRC ($SRC_SHA)"
g -C "$CLONE" worktree add -q --no-track -B "aic/q-$IID" "$QDIR" "$SRC_SHA"
QDIR="$(cd "$QDIR" && pwd -P)"
GITDIR="$(g -C "$QDIR" rev-parse --absolute-git-dir)"
case "$ROOT$WORK$QDIR$GITDIR" in *'"'* | *'\'* | *'$'* | *'`'*) die "paths must not contain quotes, backslashes, \$ or backticks" ;; esac

# Base-branch rule (D16). Paths known to either branch are replaced by the
# target-branch version or removed when the target has none.
BASE_RE='(^|/)\.claude/|^\.mcp\.json$|(^|/)claude(\.local)?\.md$'
SKIP=()     # tracked on the MR branch: hidden from git with skip-worktree
EXCLUDE=()  # not tracked on the MR branch: hidden through the worktree's excludes
while IFS= read -r p; do
  [[ -n "$p" ]] || continue
  rm -f "$QDIR/$p"
  if g -C "$QDIR" cat-file -e "$TGT_SHA:$p" 2>/dev/null; then
    mkdir -p "$(dirname "$QDIR/$p")"
    g -C "$QDIR" show "$TGT_SHA:$p" > "$QDIR/$p"
  fi
done < <({ g -C "$QDIR" ls-tree -r --name-only "$SRC_SHA"; g -C "$QDIR" ls-tree -r --name-only "$TGT_SHA"; } | grep -iE "$BASE_RE" | sort -u)

STATE="$WORK/quarantine/$IID"
mkdir -p "$STATE"
case "$STATE" in *'"'* | *'\'* | *'$'* | *'`'*) die "paths must not contain quotes, backslashes, \$ or backticks" ;; esac

log "laying the guard settings over the base-branch settings"
EXTRA_GUARD="${AIC_EXTRA_GUARD:-}"
[[ -n "$EXTRA_GUARD" ]] || log "note: AIC_EXTRA_GUARD unset, no extra guard hook is added"
node "$ROOT/quarantine/overlay.mjs" --root "$ROOT" --worktree "$QDIR" --state "$STATE" --work "$WORK" \
  --mr "$MR" --source-sha "$SRC_SHA" --target-sha "$TGT_SHA" ${EXTRA_GUARD:+--extra-guard "$EXTRA_GUARD"}

while IFS= read -r p; do
  [[ -n "$p" ]] || continue
  if g -C "$QDIR" ls-files --error-unmatch -- "$p" >/dev/null 2>&1; then SKIP+=("$p"); else EXCLUDE+=("/$p"); fi
done < <({ g -C "$QDIR" ls-tree -r --name-only "$SRC_SHA"; g -C "$QDIR" ls-tree -r --name-only "$TGT_SHA"; printf '%s\n' .claude/settings.json .mcp.json; } | grep -iE "$BASE_RE" | sort -u)

# A removed tracked file shows as deleted unless it is skip-worktree too.
if [[ ${#SKIP[@]} -gt 0 ]]; then g -C "$QDIR" update-index --skip-worktree -- "${SKIP[@]}"; fi

# Per-worktree excludes, so clone-b's own status is not affected.
g -C "$CLONE" config extensions.worktreeConfig true
printf '%s\n' "/.aic/" "/start-session.sh" ${EXCLUDE[@]+"${EXCLUDE[@]}"} > "$GITDIR/aic-exclude"
g -C "$QDIR" config --worktree core.excludesFile "$GITDIR/aic-exclude"

mkdir -p "$QDIR/.aic"
for f in identity.json policy.json; do
  [[ -f "$CLONE/.aic/$f" ]] || die "$CLONE/.aic/$f missing (run make setup)"
  cp "$CLONE/.aic/$f" "$QDIR/.aic/$f"
done
# The promoter reads this copy, never the worktree's (the session can write there).
cp "$CLONE/.aic/policy.json" "$STATE/policy.json"
# Marker for review-handle and the plugin: the plugin registers the session
# as the quarantine on the MR source branch (D15).
node -e 'process.stdout.write(JSON.stringify({mr:Number(process.argv[1]),source_branch:process.argv[2],created_at:new Date().toISOString().replace(/\.\d+Z$/,"Z")})+"\n")' "$IID" "$SRC" > "$QDIR/.aic/quarantine"

log "skip-worktree: ${SKIP[*]:-none}"
log "ready: $QDIR"
log "start the session with: $QDIR/start-session.sh"
printf '{"worktree":"%s","state":"%s"}\n' "$QDIR" "$STATE"
