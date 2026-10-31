#!/usr/bin/env bash
# Opens a shell on the host, or runs the given command there.
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
exec ssh "${SSH_OPTS[@]}" "$SSH_USER@$(host_ip)" "$@"
