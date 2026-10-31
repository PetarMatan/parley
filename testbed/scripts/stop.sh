#!/usr/bin/env bash
# Stops the instance. Disk and Elastic IP stay, so DNS stays valid and only the
# stopped-instance costs remain.
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
ID="$(instance_id)"
say "stopping $ID"
aws ec2 stop-instances --region "$(aws_region)" --instance-ids "$ID" --query 'StoppingInstances[].CurrentState.Name' --output text
aws ec2 wait instance-stopped --region "$(aws_region)" --instance-ids "$ID"
say "stopped"
