#!/usr/bin/env bash
# Copies the review-send and review-handle skills into <checkout>/.claude/skills/.
# Usage: skills/install.sh <checkout>
# Idempotent: an existing copy of each skill is replaced, other skills are kept.
set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: $0 <checkout>" >&2
  exit 2
fi

SRC="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEST_ROOT="$1"
if [[ ! -d "$DEST_ROOT" ]]; then
  echo "install.sh: $DEST_ROOT is not a directory" >&2
  exit 1
fi

DEST="$DEST_ROOT/.claude/skills"
mkdir -p "$DEST"
for skill in review-send review-handle; do
  rm -rf "${DEST:?}/$skill"
  cp -R "$SRC/$skill" "$DEST/$skill"
  printf '[skills] installed %s into %s\n' "$skill" "$DEST"
done
