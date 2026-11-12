#!/usr/bin/env bash
# Prepares one clone of a testbed project for a Parley session on this laptop:
# runs the plugin's login for that person, which writes .aic/identity.json
# (who this checkout acts as, the token path, the channel URL and the OIDC
# settings) and stores the token. The project itself commits .aic/policy.json;
# the channel server, hooks and skills come from the installed plugin (D49).
# Usage: checkout-setup.sh <clone-dir> <username>     e.g. ... ~/work/orders-alice alice
# Env: DOMAIN (parleylab.dev), AIC_TOKEN_DIR (~/.parley/tokens),
#      SKIP_LOGIN=1 writes the identity for <username> without a login (scripts).
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

[ $# -eq 2 ] || die "usage: $0 <clone-dir> <username>"
DIR="$(cd "$1" && pwd)"
USER_NAME="$2"
DOMAIN="${DOMAIN:-parleylab.dev}"
TOKEN_DIR="${AIC_TOKEN_DIR:-$HOME/.parley/tokens}"
CHANNEL="https://channel.$DOMAIN"
ISSUER="https://auth.$DOMAIN/realms/parley"
CLIENT_ID="parley-cli"
CLAIM="preferred_username"
[ -d "$DIR/.git" ] || die "$DIR is not a git checkout"
[ ! -f "$DIR/.aic/policy.json" ] && say "note: $DIR has no .aic/policy.json; the project's main should commit it"

git -C "$DIR" config user.name "$USER_NAME"
git -C "$DIR" config user.email "$USER_NAME@$DOMAIN"

if [ "${SKIP_LOGIN:-0}" = 1 ]; then
  mkdir -p "$DIR/.aic" "$TOKEN_DIR"
  chmod 700 "$TOKEN_DIR"
  cat > "$DIR/.aic/identity.json" <<JSON
{
  "user": "$USER_NAME",
  "token_file": "$TOKEN_DIR/$USER_NAME.jwt",
  "base_url": "$CHANNEL",
  "oidc": {
    "issuer": "$ISSUER",
    "client_id": "$CLIENT_ID",
    "identity_claim": "$CLAIM"
  }
}
JSON
  say "wrote $DIR/.aic/identity.json for $USER_NAME (no login)"
  exit 0
fi

say "logging in as $USER_NAME (browser opens; use the Keycloak password from deploy/cloud/seed/users.env)"
if [ -f "$DIR/.aic/identity.json" ]; then
  node "$REPO_DIR/plugin/login.mjs" --checkout "$DIR"
else
  node "$REPO_DIR/plugin/login.mjs" --checkout "$DIR" --base-url "$CHANNEL" --issuer "$ISSUER" \
    --client-id "$CLIENT_ID" --identity-claim "$CLAIM" --token-dir "$TOKEN_DIR"
fi
got="$(node -e 'process.stdout.write(String(JSON.parse(require("fs").readFileSync(process.argv[1],"utf8")).user))' "$DIR/.aic/identity.json")"
[ "$got" = "$USER_NAME" ] || die "$DIR is now logged in as $got, not $USER_NAME; remove $DIR/.aic/identity.json and log in again as $USER_NAME"
say "$DIR acts as $USER_NAME; start sessions there with: claude --dangerously-load-development-channels plugin:parley@parley"
