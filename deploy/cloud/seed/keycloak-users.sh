#!/usr/bin/env bash
# Sets the passwords of the realm users from deploy/cloud/seed/users.env.
# Run on the host from deploy/cloud after `docker compose up -d` and once
# Keycloak answers. Idempotent: every run resets the listed passwords.
# The realm users themselves come from keycloak/realm-parley.json.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
USERS="${1:-$HERE/users.env}"
REALM=parley
[ -f "$USERS" ] || { echo "missing $USERS (copy users.env.example)" >&2; exit 1; }
[ -f "$HERE/../.env" ] || { echo "missing deploy/cloud/.env" >&2; exit 1; }
# shellcheck disable=SC1091
set -a; . "$HERE/../.env"; set +a

kc() { docker compose -f "$HERE/../compose.yaml" exec -T keycloak /opt/keycloak/bin/kcadm.sh "$@"; }

kc config credentials --server http://localhost:8080 --realm master \
  --user "$KC_BOOTSTRAP_ADMIN_USERNAME" --password "$KC_BOOTSTRAP_ADMIN_PASSWORD" >/dev/null

while IFS='=' read -r user pass; do
  case "$user" in ''|\#*) continue ;; esac
  [ -n "$pass" ] || { echo "empty password for $user" >&2; exit 1; }
  kc set-password -r "$REALM" --username "$user" --new-password "$pass" >/dev/null
  echo "password set: $user"
done < "$USERS"
