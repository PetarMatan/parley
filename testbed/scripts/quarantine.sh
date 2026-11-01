#!/usr/bin/env bash
# Builds the quarantine for one GitLab merge request on the author's laptop,
# using the unchanged quarantine/make-quarantine.sh. That script expects the
# sandbox layout: $AIC_WORK/clone-b (the author's clone) and
# $AIC_WORK/mr/<iid>.json (the merge request). This script gives the real
# stack the same shape: a symlink to the clone and a file written from the
# GitLab API. Nothing in the quarantine itself changes.
#
#   quarantine.sh <iid> [<author-clone>]       default clone: ~/work/orders-bob
# Env: PARLEY_TESTBED_WORK (default ~/.parley/work; AIC_WORK is NOT used because
# the root Makefile exports it as the local sandbox directory), GITLAB_TOKEN (default: the root
# token in testbed/.gitlab-root-token; a person uses their own token),
# DOMAIN (parleylab.dev), PROJECT (parley-lab/orders-service)
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
[ $# -ge 1 ] || die "usage: $0 <iid> [<author-clone>]"
IID="$1"
CLONE="$(cd "${2:-$HOME/work/orders-bob}" && pwd -P)"
DOMAIN="${DOMAIN:-parleylab.dev}"
PROJECT="${PROJECT:-parley-lab/orders-service}"
WORK="${PARLEY_TESTBED_WORK:-$HOME/.parley/work}"
TOKEN="${GITLAB_TOKEN:-$(tr -d '\n' < "$TESTBED_DIR/.gitlab-root-token" 2>/dev/null || true)}"
[ -n "$TOKEN" ] || die "no GitLab token: set GITLAB_TOKEN or run make testbed-root-token"
command -v jq >/dev/null || die "jq is required"

mkdir -p "$WORK/mr"
[ ! -e "$WORK/clone-b" ] || [ -L "$WORK/clone-b" ] || die "$WORK/clone-b exists and is not a symlink; choose another PARLEY_TESTBED_WORK"
ln -sfn "$CLONE" "$WORK/clone-b"
# The channel server runs $AIC_WORK/bin/aic-promote; share the built binaries.
[ -x "$REPO_DIR/work/bin/aic-promote" ] || die "run make build first ($REPO_DIR/work/bin/aic-promote missing)"
[ ! -e "$WORK/bin" ] || [ -L "$WORK/bin" ] || die "$WORK/bin exists and is not a symlink"
ln -sfn "$REPO_DIR/work/bin" "$WORK/bin"
enc="$(printf '%s' "$PROJECT" | jq -sRr @uri)"
curl -fsS -H "PRIVATE-TOKEN: $TOKEN" "https://gitlab.$DOMAIN/api/v4/projects/$enc/merge_requests/$IID" \
  | jq '{iid: .iid, repo: "'"$PROJECT"'", title: .title, source_branch: .source_branch, target_branch: .target_branch,
         author: .author.username, reviewers: [.reviewers[].username], assignees: [.assignees[].username], state: .state}' \
  > "$WORK/mr/$IID.json"
say "wrote $WORK/mr/$IID.json: $(jq -r '"\(.source_branch) -> \(.target_branch), reviewers \(.reviewers), assignees \(.assignees)"' "$WORK/mr/$IID.json")"

AIC_WORK="$WORK" bash "$REPO_DIR/quarantine/make-quarantine.sh" "$IID"
say "next, in its own terminal:  AIC_WORK=$WORK ${TMPDIR:-/tmp}/aic-quarantine-$IID/start-session.sh"
