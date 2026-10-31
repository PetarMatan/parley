#!/usr/bin/env bash
# Destroys every testbed resource: instance, disk, Elastic IP, DNS records,
# security group, key pair. GitLab and Keycloak data are lost. The domain and
# its hosted zone are not managed here and stay.
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

say "this deletes the host and all its data"
read -r -p "type 'destroy' to continue: " answer
[ "$answer" = "destroy" ] || die "aborted"
terraform -chdir="$TF_DIR" destroy -input=false
