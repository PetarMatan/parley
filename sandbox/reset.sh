#!/usr/bin/env bash
# Deletes $AIC_WORK (repos, store, keys, tokens, logs) after a confirm prompt.
# Pass --yes to skip the prompt.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
AIC_WORK="${AIC_WORK:-$ROOT/work}"

if [[ ! -e "$AIC_WORK" ]]; then
  echo "nothing to delete: $AIC_WORK does not exist"
  exit 0
fi
AIC_WORK="$(cd "$AIC_WORK" && pwd)"
case "$AIC_WORK" in
  / | "$HOME" | "$ROOT") echo "refusing to delete $AIC_WORK" >&2; exit 1 ;;
esac

if [[ "${1:-}" != "--yes" ]]; then
  read -r -p "Delete $AIC_WORK and everything in it? [y/N] " answer
  [[ "$answer" == y || "$answer" == Y ]] || { echo "aborted"; exit 1; }
fi
rm -rf "$AIC_WORK"
echo "deleted $AIC_WORK"
