#!/usr/bin/env bash
# Hardens GitLab after the first boot. Run on the host from deploy/cloud once
# GitLab is healthy. Idempotent.
#   - web sign-in with a local password is disabled: everyone, including root,
#     signs in through Keycloak or not at all through the browser;
#   - the root account keeps API access for seed scripts (phase 3) through a
#     personal access token created on the host when needed.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
compose() { docker compose -f "$HERE/../compose.yaml" "$@"; }
echo "applying GitLab application settings (gitlab-rails takes about a minute)"
compose exec -T gitlab gitlab-rails runner '
  s = ApplicationSetting.current
  s.update!(password_authentication_enabled_for_web: false)
  puts "password_authentication_enabled_for_web=#{s.password_authentication_enabled_for_web}"
' </dev/null
