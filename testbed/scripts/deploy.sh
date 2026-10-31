#!/usr/bin/env bash
# Copies the untracked secret files to the host, updates the checkout there, and
# starts the cloud bundle. Safe to run again: Compose only restarts what changed.
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

REF="${1:-$(cd "$REPO_DIR" && git rev-parse --abbrev-ref HEAD)}"
ENV_FILE="$REPO_DIR/deploy/cloud/.env"
USERS_FILE="$REPO_DIR/deploy/cloud/seed/users.env"

[ -f "$ENV_FILE" ] || die "$ENV_FILE missing; copy deploy/cloud/.env.example and fill it in"
[ -f "$USERS_FILE" ] || die "$USERS_FILE missing; copy deploy/cloud/seed/users.env.example and fill it in"

IP="$(host_ip)"
say "host $IP, ref $REF"

say "waiting for first boot to finish"
for _ in $(seq 1 60); do
  if ssh "${SSH_OPTS[@]}" "$SSH_USER@$IP" test -f /var/lib/parley/cloud-init-done 2>/dev/null; then break; fi
  sleep 10
done
remote test -f /var/lib/parley/cloud-init-done || die "cloud-init marker not found after 10 minutes"

say "copying secrets"
scp "${SSH_OPTS[@]}" "$ENV_FILE" "$SSH_USER@$IP:$REMOTE_REPO/deploy/cloud/.env"
scp "${SSH_OPTS[@]}" "$USERS_FILE" "$SSH_USER@$IP:$REMOTE_REPO/deploy/cloud/seed/users.env"
remote chmod 600 "$REMOTE_REPO/deploy/cloud/.env" "$REMOTE_REPO/deploy/cloud/seed/users.env"

say "updating checkout to $REF"
remote "git -C $REMOTE_REPO fetch --quiet origin && git -C $REMOTE_REPO checkout --quiet $REF && (git -C $REMOTE_REPO pull --quiet --ff-only origin $REF || true)"

say "starting the bundle (GitLab needs about five minutes after this returns)"
remote "cd $REMOTE_REPO/deploy/cloud && docker compose up -d --build --remove-orphans"
remote "cd $REMOTE_REPO/deploy/cloud && docker compose ps"
say "done; run scripts/status.sh to check the services"
