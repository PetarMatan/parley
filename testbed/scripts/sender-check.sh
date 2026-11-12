#!/usr/bin/env bash
# Scenarios S4 to S6 of parley-testbed.md: a person who is not a reviewer or
# assignee of the merge request sends a finding through the real gateway. The
# gateway accepts any valid realm token (group policy T7 is off), the inbox
# must refuse with 403 sender_not_allowed and log a sender_refused security
# event. Prints one line per sender: HTTP status, error code, and the task id
# to look for in the inbox log.
#   sender-check.sh <iid> <diff-file> <user>...
#   e.g. sender-check.sh 2 internal/order/store.go dave erin mallory
# Env: DOMAIN (parleylab.dev), PROJECT (parley-lab/orders-service), AIC_TOKEN_DIR (~/.parley/tokens)
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
[ $# -ge 3 ] || die "usage: $0 <iid> <diff-file> <user>..."
IID="$1"; DIFF_FILE="$2"; shift 2
DOMAIN="${DOMAIN:-parleylab.dev}"
PROJECT="${PROJECT:-parley-lab/orders-service}"
TOKEN_DIR="${AIC_TOKEN_DIR:-$HOME/.parley/tokens}"
command -v jq >/dev/null || die "jq is required"

node "$TESTBED_DIR/scripts/refresh-tokens.mjs" "$@" >&2
fail=0
for user in "$@"; do
  task="f-$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n')"
  body="$(jq -nc --arg ctx "$PROJECT!$IID" --arg task "$task" --arg file "$DIFF_FILE" --arg user "$user" \
    '{schema:"finding.v1", context_id:$ctx, task_id:$task, file:$file, line:1, category:"bug", severity:"major", class:"fact",
      title:("Sender check from " + $user), note:"Testbed sender check (S4 to S6). The inbox must refuse this message."}')"
  out="$(curl -sS --max-time 20 -o /tmp/sender-check.$$ -w '%{http_code}' -X POST "https://channel.$DOMAIN/v1/messages" \
    -H "Authorization: Bearer $(tr -d '\n' < "$TOKEN_DIR/$user.jwt")" -H 'Content-Type: application/json' --data "$body")"
  code="$(jq -r '.error.code // "-"' /tmp/sender-check.$$ 2>/dev/null || echo '-')"
  msg="$(jq -r '.error.message // .message_id // "-"' /tmp/sender-check.$$ 2>/dev/null || echo '-')"
  printf '%-8s HTTP %s %-20s task %s  %s\n' "$user" "$out" "$code" "$task" "$msg"
  [ "$out" = 403 ] && [ "$code" = sender_not_allowed ] || fail=1
done
rm -f /tmp/sender-check.$$
exit "$fail"
