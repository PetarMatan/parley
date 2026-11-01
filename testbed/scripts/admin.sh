#!/usr/bin/env bash
# Opens the Keycloak admin console through the host. Caddy answers /admin only
# to private addresses, so the browser is sent through a SOCKS tunnel on the
# host: a separate Chrome profile with that proxy, nothing else changes.
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
PORT="${SOCKS_PORT:-1080}"
DOMAIN="${DOMAIN:-parleylab.dev}"
IP="$(host_ip)"
say "SOCKS tunnel on 127.0.0.1:$PORT through $IP; close this window to end it"
ssh "${SSH_OPTS[@]}" -N -D "$PORT" "$SSH_USER@$IP" &
TUNNEL=$!
trap 'kill $TUNNEL 2>/dev/null || true' EXIT
sleep 2
URL="https://auth.$DOMAIN/admin/"
case "$(uname -s)" in
  Darwin) open -na "Google Chrome" --args --proxy-server="socks5://127.0.0.1:$PORT" --user-data-dir="$HOME/.parley-admin-chrome" "$URL" ;;
  *) say "open a browser with proxy socks5://127.0.0.1:$PORT at $URL" ;;
esac
say "admin user and password: KC_BOOTSTRAP_ADMIN_* in deploy/cloud/.env"
wait $TUNNEL
