#!/usr/bin/env bash
# Starts a stopped instance. Compose services restart on their own (restart
# policy); allow a few minutes for GitLab.
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
ID="$(instance_id)"
say "starting $ID"
aws ec2 start-instances --region "$(aws_region)" --instance-ids "$ID" --query 'StartingInstances[].CurrentState.Name' --output text
aws ec2 wait instance-running --region "$(aws_region)" --instance-ids "$ID"
say "running at $(host_ip); services need a few minutes"
