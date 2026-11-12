#!/usr/bin/env bash
# Issues a GitLab personal access token for one person with the admin API, so
# that person can clone and push over HTTPS without opening GitLab's settings.
# Git on the command line does not use the Keycloak login: GitLab accepts only
# a token it issued (or an SSH key) as the password. Printed once; hand it
# over with the person's Keycloak password over a private channel.
#   gitlab-user-token.sh <username> [days]        default 90 days
# Env: DOMAIN (parleylab.dev), GITLAB_ROOT_TOKEN_FILE (testbed/.gitlab-root-token)
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

[ $# -ge 1 ] || die "usage: $0 <username> [days]"
USER_NAME="$1"
DAYS="${2:-90}"
DOMAIN="${DOMAIN:-parleylab.dev}"
API="https://gitlab.$DOMAIN/api/v4"
TOKEN_FILE="${GITLAB_ROOT_TOKEN_FILE:-$TESTBED_DIR/.gitlab-root-token}"
[ -f "$TOKEN_FILE" ] || die "$TOKEN_FILE missing; run make testbed-root-token"
TOKEN="$(tr -d '[:space:]' < "$TOKEN_FILE")"
command -v jq >/dev/null || die "jq is required"

gl() { local method="$1" path="$2"; shift 2; curl -fsS -X "$method" -H "PRIVATE-TOKEN: $TOKEN" "$API$path" "$@"; }
ID="$(gl GET "/users?username=$(jq -rn --arg v "$USER_NAME" '$v|@uri')" | jq -r '.[0].id // empty')"
[ -n "$ID" ] || die "GitLab has no user $USER_NAME (run make testbed-seed-gitlab first)"
if date -v +1d >/dev/null 2>&1; then EXP="$(date -v "+${DAYS}d" +%F)"; else EXP="$(date -d "+${DAYS} days" +%F)"; fi
NAME="parley-git-$(date +%Y%m%d)"
OUT="$(gl POST "/users/$ID/personal_access_tokens" --data-urlencode "name=$NAME" \
  --data "scopes[]=read_repository" --data "scopes[]=write_repository" --data "expires_at=$EXP")"
say "token $NAME for $USER_NAME (user id $ID) expires $EXP; shown once:"
printf 'GITLAB_USER=%s\nGITLAB_TOKEN=%s\n' "$USER_NAME" "$(jq -r .token <<<"$OUT")"
say "clone with: git clone https://gitlab.$DOMAIN/parley-lab/orders-service.git  (user $USER_NAME, password = the token)"
