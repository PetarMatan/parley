#!/usr/bin/env bash
# Builds the PoC sandbox under $AIC_WORK: binaries, keys and tokens, the bare
# origin with the "shop" project and its merge request, and two clones.
# Idempotent: existing keys, origin and clones are kept; per-clone .aic files
# and the merge request file are rewritten. Set AIC_REMINT=1 to mint again.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
AIC_WORK="${AIC_WORK:-$ROOT/work}"
AIC_BASE_URL="${AIC_BASE_URL:-http://localhost:3000}"
USERS="a@example.com,b@example.com,c@example.com,promoter@aic.local"

log() { printf '[setup] %s\n' "$*"; }

# Paths are written into JSON files by hand below.
case "$ROOT$AIC_WORK" in *'"'* | *'\'*) echo "paths must not contain quotes or backslashes" >&2; exit 1 ;; esac

mkdir -p "$AIC_WORK"
AIC_WORK="$(cd "$AIC_WORK" && pwd)"
mkdir -p "$AIC_WORK/bin" "$AIC_WORK/logs" "$AIC_WORK/mr" "$AIC_WORK/inbox"

# 1. Binaries
log "building binaries into $AIC_WORK/bin"
(cd "$ROOT/inbox" && go build -o "$AIC_WORK/bin/" ./cmd/...)

# 2. Keys and tokens
if [[ "${AIC_REMINT:-0}" == 1 || ! -f "$AIC_WORK/keys/jwks.json" || ! -f "$AIC_WORK/tokens/promoter@aic.local.jwt" ]]; then
  log "minting keys and tokens"
  "$AIC_WORK/bin/aic-mint" --work "$AIC_WORK" --users "$USERS" --ttl 720h
else
  log "keys and tokens exist, skipping mint"
fi

# 3. Plugin dependencies
if [[ -f "$ROOT/plugin/package.json" && ! -d "$ROOT/plugin/node_modules" ]]; then
  log "installing plugin dependencies"
  if [[ -f "$ROOT/plugin/package-lock.json" ]]; then
    (cd "$ROOT/plugin" && npm ci)
  else
    (cd "$ROOT/plugin" && npm install)
  fi
fi

# git without the user's global hooks, signing or default branch settings.
g() { git -c core.hooksPath=/dev/null -c commit.gpgsign=false -c tag.gpgsign=false -c init.defaultBranch=main "$@"; }
commit_as() { # <email> <message>
  local name="${1%%@*}"
  GIT_AUTHOR_NAME="$name" GIT_AUTHOR_EMAIL="$1" GIT_COMMITTER_NAME="$name" GIT_COMMITTER_EMAIL="$1" \
    g commit -q -m "$2"
}

# 4. Bare origin with the shop project
ORIGIN="$AIC_WORK/origin.git"
if [[ ! -d "$ORIGIN" ]]; then
  log "creating $ORIGIN"
  STAGE="$(mktemp -d)"
  trap 'rm -rf "$STAGE"' EXIT
  g init -q -b main "$STAGE"
  cd "$STAGE"
  mkdir -p src test .claude

  cat > package.json <<'EOF'
{
  "name": "shop",
  "version": "1.0.0",
  "private": true,
  "type": "module",
  "scripts": {
    "test": "node --test"
  }
}
EOF

  cat > README.md <<'EOF'
# shop

Tiny price helpers for the agent conversation channel sandbox.

Run the tests with `npm test`. No dependencies, no network.
EOF

  cat > .gitignore <<'EOF'
.aic/
node_modules/
EOF

  cat > src/discount.js <<'EOF'
// Price helpers. Prices are numbers in euro.

export function applyFixedDiscount(price, amount) {
  return Math.max(price - amount, 0);
}
EOF

  cat > test/discount.test.js <<'EOF'
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { applyFixedDiscount } from '../src/discount.js';

test('fixed discount reduces the price', () => {
  assert.equal(applyFixedDiscount(100, 30), 70);
});

test('fixed discount never goes below zero', () => {
  assert.equal(applyFixedDiscount(20, 30), 0);
});
EOF

  cat > .claude/settings.json <<EOF
{
  "hooks": {
    "SessionStart": [
      { "hooks": [ { "type": "command", "command": "node \"$ROOT/plugin/hooks/session-start.mjs\"" } ] }
    ],
    "SessionEnd": [
      { "hooks": [ { "type": "command", "command": "node \"$ROOT/plugin/hooks/session-end.mjs\"" } ] }
    ],
    "CwdChanged": [
      { "hooks": [ { "type": "command", "command": "node \"$ROOT/plugin/hooks/cwd-changed.mjs\"" } ] }
    ]
  }
}
EOF

  cat > .mcp.json <<EOF
{
  "mcpServers": {
    "aic-inbox": {
      "command": "node",
      "args": ["$ROOT/plugin/server.mjs"]
    }
  }
}
EOF

  # Skills are committed on main: the quarantine restores .claude/ from main
  # and a fresh worktree has no untracked files (docs/decisions/skills.md).
  bash "$ROOT/skills/install.sh" "$STAGE" >/dev/null

  g add -A
  commit_as b@example.com "shop: fixed discount helper"

  g checkout -q -b feature/discount
  # The feature carries one real bug for the reviewer: the percentage is not
  # clamped, so pct > 100 gives a negative price and pct < 0 raises it.
  cat > src/discount.js <<'EOF'
// Price helpers. Prices are numbers in euro.

export function applyFixedDiscount(price, amount) {
  return Math.max(price - amount, 0);
}

// Applies a percentage discount, e.g. pct = 15 takes 15% off.
export function applyPercentDiscount(price, pct) {
  const cut = (price * pct) / 100;
  return Math.round((price - cut) * 100) / 100;
}
EOF

  cat > test/discount.test.js <<'EOF'
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { applyFixedDiscount, applyPercentDiscount } from '../src/discount.js';

test('fixed discount reduces the price', () => {
  assert.equal(applyFixedDiscount(100, 30), 70);
});

test('fixed discount never goes below zero', () => {
  assert.equal(applyFixedDiscount(20, 30), 0);
});

test('percent discount takes the share off', () => {
  assert.equal(applyPercentDiscount(200, 15), 170);
});

test('zero percent keeps the price', () => {
  assert.equal(applyPercentDiscount(99.99, 0), 99.99);
});

test('full discount makes it free', () => {
  assert.equal(applyPercentDiscount(50, 100), 0);
});
EOF
  g add -A
  commit_as b@example.com "discount: add percentage discount"

  g init -q --bare "$ORIGIN"
  g --git-dir "$ORIGIN" symbolic-ref HEAD refs/heads/main
  g push -q "$ORIGIN" main feature/discount
  cd "$ROOT"
  rm -rf "$STAGE"
  trap - EXIT
else
  log "$ORIGIN exists, keeping it"
fi

# Clones use the shop.git alias so the remote URL normalizes to local/shop.
ln -sfn origin.git "$AIC_WORK/shop.git"
REMOTE="$AIC_WORK/shop.git"

# 5. Clones with identity and policy
make_clone() { # <dir> <user> <branch>
  local dir="$AIC_WORK/$1" user="$2" branch="$3"
  if [[ ! -d "$dir/.git" ]]; then
    log "cloning $1 for $user on $branch"
    g clone -q --branch "$branch" "$REMOTE" "$dir"
  fi
  g -C "$dir" config user.email "$user"
  g -C "$dir" config user.name "${user%%@*}"
  mkdir -p "$dir/.aic"
  cat > "$dir/.aic/identity.json" <<EOF
{"user":"$user","token_file":"$AIC_WORK/tokens/$user.jwt","base_url":"$AIC_BASE_URL","oidc":{"issuer":"http://localhost:5556/dex","client_id":"aic-cli","token":"id"}}
EOF
  cat > "$dir/.aic/policy.json" <<'EOF'
{"ceiling":{"fact":"L2","taste":"L0","product":"L0"},"max_rounds":5,"max_patch_lines":400}
EOF
}
make_clone clone-a a@example.com main
make_clone clone-b b@example.com feature/discount

# 6. Fake merge request
cat > "$AIC_WORK/mr/1.json" <<'EOF'
{"iid":1,"repo":"local/shop","title":"Add percentage discount","source_branch":"feature/discount","target_branch":"main",
 "author":"b@example.com","reviewers":["a@example.com"],"assignees":["b@example.com"]}
EOF

log "done. diff files: $(g --git-dir "$ORIGIN" diff --name-only main...feature/discount | tr '\n' ' ')"
