#!/usr/bin/env bash
# Seeds the testbed GitLab (phase 3): users, group, project, memberships, the
# inbox's group access token, a project runner, the orders-service code, one
# branch and one merge request per review scenario. Idempotent: every step
# checks what exists; branches are reset to the committed patches.
#
# Needs: testbed/.gitlab-root-token (from gitlab-root-token.sh), curl, jq, git.
# Runs from the laptop against the public API; the runner registration and
# nothing else goes over SSH.
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

DOMAIN="${DOMAIN:-parleylab.dev}"
GL="https://gitlab.$DOMAIN"
API="$GL/api/v4"
TOKEN_FILE="${GITLAB_ROOT_TOKEN_FILE:-$TESTBED_DIR/.gitlab-root-token}"
GROUP_PATH="${GROUP_PATH:-parley-lab}"
PROJECT_PATH="${PROJECT_PATH:-orders-service}"
# The inbox reads merge requests and posts outcome notes, so it needs `api`
# (read_api cannot write notes). Role Reporter keeps it out of code and settings.
INBOX_TOKEN_SCOPE="${INBOX_TOKEN_SCOPE:-api}"
INBOX_TOKEN_DAYS="${INBOX_TOKEN_DAYS:-90}"
RUNNER_DESC="${RUNNER_DESC:-parley-testbed-runner}"
SERVICE_DIR="$TESTBED_DIR/orders-service"
# Fixed dates make the main commit reproducible, so a re-run pushes nothing new.
export GIT_AUTHOR_DATE="2026-11-01T12:00:00+00:00" GIT_COMMITTER_DATE="2026-11-01T12:00:00+00:00"
export GIT_AUTHOR_NAME="Parley seed" GIT_AUTHOR_EMAIL="seed@$DOMAIN" GIT_COMMITTER_NAME="Parley seed" GIT_COMMITTER_EMAIL="seed@$DOMAIN"

[ -f "$TOKEN_FILE" ] || die "$TOKEN_FILE missing; run: bash testbed/scripts/gitlab-root-token.sh > $TOKEN_FILE && chmod 600 $TOKEN_FILE"
TOKEN="$(tr -d '[:space:]' < "$TOKEN_FILE")"
command -v jq >/dev/null || die "jq is required"

gl() { # method path [curl args...]
  local method="$1" path="$2"; shift 2
  curl -fsS -X "$method" -H "PRIVATE-TOKEN: $TOKEN" "$API$path" "$@"
}
gl_quiet() { gl "$@" 2>/dev/null; }
enc() { jq -rn --arg v "$1" '$v|@uri'; }

# ---- users ----------------------------------------------------------------
# GitLab creates a user on first OIDC login, but memberships need the user to
# exist before that. The users are created here by email and username; GitLab
# links the Keycloak identity on first login (omniauth_auto_link_user, by
# email). An extern_uid cannot be set in advance because it is Keycloak's
# opaque subject id.
user_id() { gl GET "/users?username=$(enc "$1")" | jq -r '.[0].id // empty'; }
ensure_user() {
  local name="$1" id
  id="$(user_id "$name")"
  if [ -z "$id" ]; then
    id="$(gl POST /users --data-urlencode "username=$name" --data-urlencode "email=$name@$DOMAIN" \
      --data-urlencode "name=$(printf '%s' "$name" | awk '{print toupper(substr($0,1,1)) substr($0,2)}')" --data "skip_confirmation=true" --data "force_random_password=true" | jq -r .id)"
    say "user $name created (id $id)"
  else
    say "user $name exists (id $id)"
  fi
  echo "$id"
}
# macOS ships bash 3.2 (no associative arrays): one plain variable per user.
for u in alice bob carol dave erin mallory; do eval "UID_$u=\"\$(ensure_user "$u")\""; done
uid_of() { eval "printf '%s' \"\$UID_$1\""; }

# ---- group and project ------------------------------------------------------
GROUP_ID="$(gl_quiet GET "/groups/$(enc "$GROUP_PATH")" | jq -r '.id // empty' || true)"
if [ -z "$GROUP_ID" ]; then
  GROUP_ID="$(gl POST /groups --data-urlencode "name=$GROUP_PATH" --data-urlencode "path=$GROUP_PATH" --data "visibility=private" | jq -r .id)"
  say "group $GROUP_PATH created (id $GROUP_ID)"
else
  say "group $GROUP_PATH exists (id $GROUP_ID)"
fi
FULL="$GROUP_PATH/$PROJECT_PATH"
PROJECT_ID="$(gl_quiet GET "/projects/$(enc "$FULL")" | jq -r '.id // empty' || true)"
if [ -z "$PROJECT_ID" ]; then
  PROJECT_ID="$(gl POST /projects --data-urlencode "name=$PROJECT_PATH" --data-urlencode "path=$PROJECT_PATH" \
    --data "namespace_id=$GROUP_ID" --data "visibility=private" --data "default_branch=main" \
    --data "initialize_with_readme=false" --data "only_allow_merge_if_pipeline_succeeds=true" | jq -r .id)"
  say "project $FULL created (id $PROJECT_ID)"
else
  say "project $FULL exists (id $PROJECT_ID)"
fi

# ---- memberships --------------------------------------------------------------
ensure_member() { # user access_level
  local id level; id="$(uid_of "$1")"; level="$2"
  if gl_quiet GET "/projects/$PROJECT_ID/members/$id" >/dev/null; then
    gl PUT "/projects/$PROJECT_ID/members/$id" --data "access_level=$level" >/dev/null
  else
    gl POST "/projects/$PROJECT_ID/members" --data "user_id=$id" --data "access_level=$level" >/dev/null
  fi
  say "member $1: access level $level"
}
ensure_member alice 40; ensure_member carol 40   # Maintainer
ensure_member bob 30;   ensure_member dave 30    # Developer
# erin and mallory: no membership on purpose (scenarios S5, S6)

# ---- inbox group access token --------------------------------------------------
if gl GET "/groups/$GROUP_ID/access_tokens" | jq -e '.[] | select(.name=="parley-inbox" and .revoked==false)' >/dev/null; then
  say "group access token parley-inbox exists; not shown again (revoke it in GitLab to get a new one)"
else
  expires="$(date -u -v+"${INBOX_TOKEN_DAYS}"d +%F 2>/dev/null || date -u -d "+${INBOX_TOKEN_DAYS} days" +%F)"
  tok="$(gl POST "/groups/$GROUP_ID/access_tokens" --data "name=parley-inbox" --data "scopes[]=$INBOX_TOKEN_SCOPE" \
    --data "access_level=20" --data "expires_at=$expires" | jq -r .token)"
  say "group access token for the inbox (shown once). Put this line into deploy/cloud/.env and run make testbed-deploy:"
  printf 'AIC_GITLAB_TOKEN=%s\n' "$tok"
fi

# ---- runner ---------------------------------------------------------------------
# Inside the Docker networks the GitLab container's hostname resolves to the
# container itself, which serves plain HTTP on port 80 only. The runner and its
# job containers therefore talk to GitLab over HTTP on the internal name; the
# public HTTPS name is for people and laptops.
RUNNER_URL="${RUNNER_URL:-http://gitlab.$DOMAIN}"
runner_id="$(gl GET "/projects/$PROJECT_ID/runners" | jq -r ".[] | select(.description==\"$RUNNER_DESC\") | .id" | head -1)"
on_host=0
if remote "cd $REMOTE_REPO/deploy/cloud && docker compose exec -T gitlab-runner gitlab-runner list 2>&1 | grep -q \"$RUNNER_DESC\"" </dev/null; then on_host=1; fi
if [ -n "$runner_id" ] && [ "$on_host" = 1 ]; then
  say "runner $RUNNER_DESC is registered"
else
  if [ -n "$runner_id" ]; then
    say "runner $RUNNER_DESC exists in GitLab but not on the host; removing it"
    gl DELETE "/runners/$runner_id" >/dev/null
  fi
  rtoken="$(gl POST /user/runners --data "runner_type=project_type" --data "project_id=$PROJECT_ID" \
    --data-urlencode "description=$RUNNER_DESC" --data "run_untagged=true" | jq -r .token)"
  say "registering runner on the host"
  remote "cd $REMOTE_REPO/deploy/cloud && docker compose exec -T gitlab-runner gitlab-runner register --non-interactive \
    --url $RUNNER_URL --clone-url $RUNNER_URL --token $rtoken --executor docker --docker-image golang:1.27 \
    --docker-network-mode parley_ci --description $RUNNER_DESC" </dev/null >/dev/null
  say "runner $RUNNER_DESC registered"
fi

# ---- protected main allows the seed to reset it -----------------------------------
if ! gl GET "/projects/$PROJECT_ID/protected_branches" | jq -e '.[] | select(.name=="main" and .allow_force_push==true)' >/dev/null; then
  gl_quiet DELETE "/projects/$PROJECT_ID/protected_branches/main" >/dev/null || true
  gl POST "/projects/$PROJECT_ID/protected_branches" --data "name=main" --data "push_access_level=40" \
    --data "merge_access_level=40" --data "allow_force_push=true" >/dev/null
  say "branch main protected (Maintainers, force push allowed for the seed)"
fi

# ---- code: main and one branch per scenario -----------------------------------------
WORK="$(mktemp -d)"; trap 'rm -rf "$WORK"' EXIT
REMOTE_URL="https://root:$TOKEN@gitlab.$DOMAIN/$FULL.git"
rsync -a --exclude scenarios --exclude '.go' "$SERVICE_DIR/" "$WORK/"
# The review skills travel with the project on main: the quarantine restores
# .claude/ from the target branch, so untracked copies would never reach it.
bash "$REPO_DIR/skills/install.sh" "$WORK" >/dev/null
git -C "$WORK" init -q -b main
git -C "$WORK" add -A
git -C "$WORK" commit -q -m "orders-service: initial import"
git -C "$WORK" remote add origin "$REMOTE_URL"
git -C "$WORK" push -q -f origin main
say "main pushed"

mr_create() { # scenario title assignee reviewer description
  local id="$1" title="$2" assignee="$3" reviewer="$4" desc="$5" branch="scenario/$1"
  if gl GET "/projects/$PROJECT_ID/merge_requests?source_branch=$branch&state=opened" | jq -e '.[0]' >/dev/null; then
    say "merge request for $branch exists"
    return
  fi
  # Opened as the assignee (Sudo needs the admin token's sudo scope): the inbox
  # addresses findings to the merge request author, as in real use where the
  # author opens their own merge request.
  gl POST "/projects/$PROJECT_ID/merge_requests" -H "Sudo: $assignee" --data-urlencode "source_branch=$branch" --data "target_branch=main" \
    --data-urlencode "title=$title" --data-urlencode "description=$desc" \
    --data "assignee_ids[]=$(uid_of "$assignee")" --data "reviewer_ids[]=$(uid_of "$reviewer")" --data "remove_source_branch=true" \
    | jq -r '"merge request !\(.iid) opened: \(.title)"' >&2
}

for sc in S1 S2 S3 S9 S10; do
  git -C "$WORK" checkout -q -B "scenario/$sc" main
  git -C "$WORK" apply --index "$SERVICE_DIR/scenarios/$sc.patch"
  git -C "$WORK" commit -q -F "$SERVICE_DIR/scenarios/$sc.message"
  git -C "$WORK" push -q -f origin "scenario/$sc"
done
git -C "$WORK" checkout -q main
say "scenario branches pushed"

mr_create S1 "Pricing: apply the small discount tier only above 10 units"            bob alice "$(cat "$SERVICE_DIR/scenarios/S1.message")"
mr_create S2 "Cancel: make a repeated cancel idempotent"                                bob alice "$(cat "$SERVICE_DIR/scenarios/S2.message")"
mr_create S3 "Validation: clamp quantities above the limit instead of rejecting"       bob carol "$(cat "$SERVICE_DIR/scenarios/S3.message")"
mr_create S9 "List: add a limit query parameter"                                        bob alice "$(cat "$SERVICE_DIR/scenarios/S9.message")"
mr_create S10 "Build: pin the Go toolchain and report the version"                      bob alice "$(cat "$SERVICE_DIR/scenarios/S10.message")"

say "done. Context ids are $FULL!<iid>; see the merge request list in GitLab."
