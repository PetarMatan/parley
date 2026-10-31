#!/usr/bin/env bash
# Shared helpers for the testbed scripts. Sourced, not run.
# Every AWS call uses the dedicated profile; never a work profile.

TESTBED_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO_DIR="$(cd "$TESTBED_DIR/.." && pwd)"
TF_DIR="$TESTBED_DIR/terraform"
AWS_PROFILE="${AWS_PROFILE:-parley}"
export AWS_PROFILE
SSH_USER="${SSH_USER:-ubuntu}"
SSH_OPTS=(-o StrictHostKeyChecking=accept-new -o ConnectTimeout=10 -o ServerAliveInterval=30)
REMOTE_REPO=/opt/parley

say() { printf 'testbed: %s\n' "$*" >&2; }
die() { say "$*"; exit 1; }

tf_output() {
  terraform -chdir="$TF_DIR" output -raw "$1" 2>/dev/null || die "terraform output $1 missing; run make testbed-apply first"
}

host_ip() { tf_output public_ip; }
instance_id() { tf_output instance_id; }
aws_region() { tf_output region; }

remote() { ssh "${SSH_OPTS[@]}" "$SSH_USER@$(host_ip)" "$@"; }
