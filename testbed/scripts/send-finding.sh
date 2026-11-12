#!/usr/bin/env bash
# Sends one finding.v1 as a testbed person through the real gateway, for
# scenarios whose reviewer side is fixed text and needs no agent (S10, S11).
# Prints the inbox response (message id, round, delivery state).
#   send-finding.sh <iid> <user> <file> <line> <class> <category> <severity> <title> <note>
# Env: DOMAIN (parleylab.dev), PROJECT (parley-lab/orders-service), AIC_TOKEN_DIR (~/.parley/tokens)
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
[ $# -eq 9 ] || die "usage: $0 <iid> <user> <file> <line> <class> <category> <severity> <title> <note>"
IID="$1"; USER_NAME="$2"; FILE="$3"; LINE="$4"; CLASS="$5"; CATEGORY="$6"; SEVERITY="$7"; TITLE="$8"; NOTE="$9"
DOMAIN="${DOMAIN:-parleylab.dev}"
PROJECT="${PROJECT:-parley-lab/orders-service}"
TOKEN_DIR="${AIC_TOKEN_DIR:-$HOME/.parley/tokens}"
node "$TESTBED_DIR/scripts/refresh-tokens.mjs" "$USER_NAME" >&2
task="f-$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n')"
body="$(jq -nc --arg ctx "$PROJECT!$IID" --arg task "$task" --arg file "$FILE" --argjson line "$LINE" --arg class "$CLASS" \
  --arg category "$CATEGORY" --arg severity "$SEVERITY" --arg title "$TITLE" --arg note "$NOTE" \
  '{schema:"finding.v1", context_id:$ctx, task_id:$task, file:$file, line:$line, category:$category, severity:$severity, class:$class, title:$title, note:$note}')"
say "sending task $task as $USER_NAME to $PROJECT!$IID"
curl -sS --max-time 20 -w '\nHTTP %{http_code}\n' -X POST "https://channel.$DOMAIN/v1/messages" \
  -H "Authorization: Bearer $(tr -d '\n' < "$TOKEN_DIR/$USER_NAME.jwt")" -H 'Content-Type: application/json' --data "$body"
