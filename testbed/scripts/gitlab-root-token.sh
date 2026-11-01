#!/usr/bin/env bash
# Creates (or rotates) a root personal access token for the seed scripts and
# prints only the token. Web password sign-in is off on the testbed GitLab, so
# root's API access comes from this token alone. Store it in the untracked
# testbed/.gitlab-root-token with mode 0600:
#   bash testbed/scripts/gitlab-root-token.sh > testbed/.gitlab-root-token
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
NAME="${TOKEN_NAME:-parley-seed}"
DAYS="${TOKEN_DAYS:-30}"
say "creating root token '$NAME' on the host (gitlab-rails takes about a minute)"
remote "cd $REMOTE_REPO/deploy/cloud && docker compose exec -T gitlab gitlab-rails runner '
  u = User.find_by_username(\"root\") or abort(\"no root user\")
  u.personal_access_tokens.where(name: \"$NAME\").each(&:revoke!)
  t = u.personal_access_tokens.create!(name: \"$NAME\", scopes: [:api, :admin_mode, :sudo], expires_at: $DAYS.days.from_now)
  puts t.token
' </dev/null" | tail -n 1
