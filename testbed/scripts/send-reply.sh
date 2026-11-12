#!/usr/bin/env bash
# Sends one reply.v1 as a testbed person through the real gateway, for
# scenarios whose exchange is scripted (S9). Prints the inbox response.
#   send-reply.sh <iid> <user> <task-id> <kind> <note>
# Env: DOMAIN (parleylab.dev), PROJECT (parley-lab/orders-service), AIC_TOKEN_DIR (~/.parley/tokens)
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
[ $# -eq 5 ] || die "usage: $0 <iid> <user> <task-id> <kind> <note>"
IID="$1"; USER_NAME="$2"; TASK="$3"; KIND="$4"; NOTE="$5"
DOMAIN="${DOMAIN:-parleylab.dev}"
PROJECT="${PROJECT:-parley-lab/orders-service}"
TOKEN_DIR="${AIC_TOKEN_DIR:-$HOME/.parley/tokens}"
node "$TESTBED_DIR/scripts/refresh-tokens.mjs" "$USER_NAME" >&2
body="$(jq -nc --arg ctx "$PROJECT!$IID" --arg task "$TASK" --arg kind "$KIND" --arg note "$NOTE" \
  '{schema:"reply.v1", context_id:$ctx, task_id:$task, kind:$kind, note:$note}')"
curl -sS --max-time 20 -w '\nHTTP %{http_code}\n' -X POST "https://channel.$DOMAIN/v1/messages" \
  -H "Authorization: Bearer $(tr -d '\n' < "$TOKEN_DIR/$USER_NAME.jwt")" -H 'Content-Type: application/json' --data "$body"
