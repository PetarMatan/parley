#!/usr/bin/env bash
# Creates branch scenario/<id> from main in the current orders-service git
# repository and applies the committed patch. The seed script does the same on
# its way to GitLab; this is for a local look at one scenario.
#   bash scenarios/apply.sh S1
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
ID="${1:-}"
[ -n "$ID" ] && [ -f "$HERE/$ID.patch" ] || { echo "usage: $0 <S1|S2|S3|S9|S10>" >&2; exit 2; }
git rev-parse --is-inside-work-tree >/dev/null 2>&1 || { echo "run inside a git checkout of orders-service" >&2; exit 1; }
git checkout -q -B "scenario/$ID" main
git apply --index "$HERE/$ID.patch"
git commit -q -F "$HERE/$ID.message"
echo "branch scenario/$ID ready: $(git log -1 --format=%s)"
