#!/usr/bin/env bash
# Prepares one clone of a testbed project for a Parley session:
#   .aic/identity.json  who this checkout acts as, where the token lives,
#                       the channel URL and the OIDC login settings
#   .aic/policy.json    authority ceiling and limits (same values as the sandbox)
# then runs the OIDC login for that person in the browser.
# Usage: checkout-setup.sh <clone-dir> <username>     e.g. ... ~/work/orders-alice alice
# Env: DOMAIN (parleylab.dev), AIC_TOKEN_DIR (~/.parley/tokens), SKIP_LOGIN=1
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

[ $# -eq 2 ] || die "usage: $0 <clone-dir> <username>"
DIR="$(cd "$1" && pwd)"
USER_NAME="$2"
DOMAIN="${DOMAIN:-parleylab.dev}"
TOKEN_DIR="${AIC_TOKEN_DIR:-$HOME/.parley/tokens}"
[ -d "$DIR/.git" ] || die "$DIR is not a git checkout"

mkdir -p "$DIR/.aic" "$TOKEN_DIR"
chmod 700 "$TOKEN_DIR"
cat > "$DIR/.aic/identity.json" <<JSON
{
  "user": "$USER_NAME",
  "token_file": "$TOKEN_DIR/$USER_NAME.jwt",
  "base_url": "https://channel.$DOMAIN",
  "oidc": {
    "issuer": "https://auth.$DOMAIN/realms/parley",
    "client_id": "parley-cli",
    "identity_claim": "preferred_username"
  }
}
JSON
cat > "$DIR/.aic/policy.json" <<'JSON'
{"ceiling":{"fact":"L2","taste":"L0","product":"L0"},"max_rounds":5,"max_patch_lines":400}
JSON
# The project's committed .mcp.json and hooks reference ${PARLEY_ROOT}. Local
# settings set it for sessions started in this checkout; the export covers
# tools that read .mcp.json before settings apply.
mkdir -p "$DIR/.claude"
cat > "$DIR/.claude/settings.local.json" <<JSON
{"env":{"PARLEY_ROOT":"$REPO_DIR"}}
JSON
say "PARLEY_ROOT written to .claude/settings.local.json for hooks; the MCP entry needs it exported in the shell that starts Claude Code:"
say "    export PARLEY_ROOT=$REPO_DIR"
git -C "$DIR" config user.name "$USER_NAME"
git -C "$DIR" config user.email "$USER_NAME@$DOMAIN"
say "wrote $DIR/.aic/identity.json and policy.json for $USER_NAME"

if [ "${SKIP_LOGIN:-0}" != 1 ]; then
  say "logging in as $USER_NAME (browser opens; use the Keycloak password from deploy/cloud/seed/users.env)"
  node "$REPO_DIR/plugin/login.mjs" --checkout "$DIR"
fi
