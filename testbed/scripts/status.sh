#!/usr/bin/env bash
# Checks each public service from here and lists the containers on the host.
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

DOMAIN="${DOMAIN:-parleylab.dev}"
fail=0

check() { # name url expected-status
  local code
  code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 15 "$2" || true)"
  if [ "$code" = "$3" ]; then
    printf '  ok   %-8s %s -> %s\n' "$1" "$2" "$code"
  else
    printf '  FAIL %-8s %s -> %s (expected %s)\n' "$1" "$2" "$code" "$3"
    fail=1
  fi
}

say "public checks"
check keycloak "https://auth.$DOMAIN/realms/parley/.well-known/openid-configuration" 200
check gitlab "https://gitlab.$DOMAIN/-/readiness" 200
check gateway "https://channel.$DOMAIN/" 401

say "containers on $(host_ip)"
remote "cd $REMOTE_REPO/deploy/cloud && docker compose ps" || fail=1

exit "$fail"
