#!/usr/bin/env bash
# Syncs the repository working tree to the host over SSH, copies the untracked
# secret files, and starts the cloud bundle. The host needs no git credentials,
# and the repository may be private. Safe to run again: rsync sends only what
# changed and Compose restarts only what changed.
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

REF="$(cd "$REPO_DIR" && git rev-parse --short HEAD 2>/dev/null || echo unknown)"
ENV_FILE="$REPO_DIR/deploy/cloud/.env"
USERS_FILE="$REPO_DIR/deploy/cloud/seed/users.env"

[ -f "$ENV_FILE" ] || die "$ENV_FILE missing; copy deploy/cloud/.env.example and fill it in"
[ -f "$USERS_FILE" ] || die "$USERS_FILE missing; copy deploy/cloud/seed/users.env.example and fill it in"

IP="$(host_ip)"
say "host $IP, local tree at commit $REF"

say "waiting for first boot to finish"
for _ in $(seq 1 60); do
  if ssh "${SSH_OPTS[@]}" "$SSH_USER@$IP" test -f /var/lib/parley/cloud-init-done 2>/dev/null; then break; fi
  sleep 10
done
remote test -f /var/lib/parley/cloud-init-done || die "cloud-init marker not found after 10 minutes"

say "syncing the repository to $REMOTE_REPO"
remote "sudo mkdir -p $REMOTE_REPO && sudo chown $SSH_USER:$SSH_USER $REMOTE_REPO"
# Excluded paths are also protected from --delete, so the secret files placed
# below survive the next sync.
# --inplace keeps the inode of every file: Caddy, GitLab and the gateway bind
# mount single files, and a renamed replacement would never reach them.
rsync -az --inplace --delete -e "ssh ${SSH_OPTS[*]}" \
  --exclude .git --exclude node_modules --exclude work --exclude .aic \
  --exclude 'testbed/terraform/.terraform' --exclude '*.tfstate*' --exclude 'terraform.tfvars' \
  --exclude '.env' --exclude 'users.env' \
  "$REPO_DIR/" "$SSH_USER@$IP:$REMOTE_REPO/"

say "copying secrets"
scp "${SSH_OPTS[@]}" "$ENV_FILE" "$SSH_USER@$IP:$REMOTE_REPO/deploy/cloud/.env"
scp "${SSH_OPTS[@]}" "$USERS_FILE" "$SSH_USER@$IP:$REMOTE_REPO/deploy/cloud/seed/users.env"
remote chmod 600 "$REMOTE_REPO/deploy/cloud/.env" "$REMOTE_REPO/deploy/cloud/seed/users.env"

say "starting the bundle (GitLab needs about five minutes after this returns)"
remote "cd $REMOTE_REPO/deploy/cloud && docker compose up -d --build --remove-orphans"
# Compose does not restart a service whose bind-mounted file changed.
remote "cd $REMOTE_REPO/deploy/cloud && docker compose exec -T caddy caddy reload --config /etc/caddy/Caddyfile >/dev/null 2>&1 || true"
remote "cd $REMOTE_REPO/deploy/cloud && docker compose ps"
say "done; run scripts/status.sh to check the services"
