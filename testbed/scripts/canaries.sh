#!/usr/bin/env bash
# Runs the deterministic canary suite (canaries/run.mjs --mode inbox) against
# the testbed through the real gateway: scenario S12 of parley-testbed.md.
# The canary files keep their sandbox names; the runner's mapping flags put
# them onto the real stack (DECISIONS T43):
#   a@example.com -> alice (reviewer), b@example.com -> bob (author),
#   c@example.com -> dave (project member, not a participant),
#   src/discount.js -> a file of the merge request's diff,
#   origin refs read with `git ls-remote` through the reviewer's clone.
#
#   canaries.sh <iid> <diff-file> [extra run.mjs args]
#   e.g. canaries.sh 2 internal/order/store.go --repeat 3 --only 03,11
# Env: PARLEY_TESTBED_WORK (~/.parley/work), DOMAIN (parleylab.dev),
#      PROJECT (parley-lab/orders-service), REVIEWER_CLONE (~/work/orders-alice)
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
[ $# -ge 2 ] || die "usage: $0 <iid> <diff-file> [run.mjs args]"
IID="$1"; DIFF_FILE="$2"; shift 2
DOMAIN="${DOMAIN:-parleylab.dev}"
PROJECT="${PROJECT:-parley-lab/orders-service}"
WORK="${PARLEY_TESTBED_WORK:-$HOME/.parley/work}"
CLONE="$(cd "${REVIEWER_CLONE:-$HOME/work/orders-alice}" && pwd -P)"
TOKEN_DIR="${AIC_TOKEN_DIR:-$HOME/.parley/tokens}"

[ -f "$WORK/mr/$IID.json" ] || die "$WORK/mr/$IID.json missing; run make testbed-quarantine IID=$IID first (it writes the merge request file)"
# The runner reads <user>.jwt from $AIC_WORK/tokens; the testbed keeps tokens in $TOKEN_DIR.
[ ! -e "$WORK/tokens" ] || [ -L "$WORK/tokens" ] || die "$WORK/tokens exists and is not a symlink"
ln -sfn "$TOKEN_DIR" "$WORK/tokens"

say "refreshing tokens"
node "$TESTBED_DIR/scripts/refresh-tokens.mjs" alice bob dave >&2

say "running canaries against $PROJECT!$IID through https://channel.$DOMAIN"
AIC_WORK="$WORK" AIC_BASE_URL="https://channel.$DOMAIN" node "$REPO_DIR/canaries/run.mjs" --mode inbox \
  --context "$PROJECT!$IID" \
  --sender-map "a@example.com=alice,b@example.com=bob,c@example.com=dave" \
  --file-map "src/discount.js=$DIFF_FILE" \
  --origin-remote "$CLONE" "$@"
