# Testbed on AWS

The AWS side of the real-architecture testbed (plan: `../parley-testbed.md`). One EC2 host runs the shared services from `../deploy/cloud/` under Docker Compose. Two real laptops talk to it. Terraform creates the host, its address, DNS records, firewall and SSH key. Scripts copy the secrets, start the bundle, and stop or start the host.

## Prerequisites

| Item | Where |
|---|---|
| AWS CLI profile `parley` | `aws configure --profile parley`; an IAM user with `AdministratorAccess`. Never a work profile |
| Region | `eu-north-1` (Stockholm). The account's service control policy allows EC2 there only |
| Terraform 1.12 or newer | `brew install terraform` |
| SSH key | `~/.ssh/id_ed25519.pub`, or set `admin_public_key_path` |
| Domain | `parleylab.dev`, hosted zone in Route 53 (done 2026-10-30) |
| Secrets | `../deploy/cloud/.env` and `../deploy/cloud/seed/users.env`, copied from their `.example` files and filled in. Both are ignored by git |

## From nothing to a running host

```bash
cd testbed/terraform
cp terraform.tfvars.example terraform.tfvars      # set admin_cidr to your public IP /32
curl -s https://checkip.amazonaws.com             # that address

make testbed-init       # downloads the AWS provider
make testbed-plan       # 15 resources, nothing created yet
make testbed-apply      # creates the host; answer yes
make testbed-deploy     # waits for first boot, copies secrets, starts the bundle
make testbed-status     # Keycloak discovery 200, GitLab readiness 200, gateway 401
```

Run the Make targets from the repository root. GitLab needs about five minutes after `deploy` before `status` passes. Then run the Keycloak user seed described in `../deploy/cloud/README.md`.

`make testbed-deploy REF=<branch>` deploys another branch. Without `REF` it deploys the branch checked out here; the host pulls it from GitHub, so push first.

## Day to day

| Command | Effect |
|---|---|
| `make testbed-stop` | Stops the instance. Disk and address stay; DNS stays valid |
| `make testbed-start` | Starts it again; services come back on their own in a few minutes |
| `make testbed-ssh` | Shell on the host as `ubuntu`; the checkout is `/opt/parley` |
| `make testbed-status` | The three public checks plus `docker compose ps` |

## Cost

| State | Approximate |
|---|---|
| Running, `t3.xlarge` | about 5 USD per day |
| Stopped, 80 GB disk kept | about 0.25 USD per day |
| Elastic IP while stopped | about 0.12 USD per day |

Stop the host whenever nobody is testing.

## Tear down

```bash
make testbed-destroy    # asks for the word "destroy"
```

Removes the host, its disk, the address, the DNS records, the firewall and the key pair. GitLab and Keycloak data are gone. The domain and its hosted zone are not managed here and stay.

## Files

| Path | Contents |
|---|---|
| `terraform/` | `versions.tf`, `variables.tf`, `main.tf`, `dns.tf`, `outputs.tf`, `cloud-init.yaml`, `terraform.tfvars.example` |
| `scripts/` | `deploy.sh`, `status.sh`, `ssh.sh`, `stop.sh`, `start.sh`, `destroy.sh`, `lib.sh` |
| `Makefile` | The `testbed-*` targets, included from the repository Makefile |

Terraform state is local (`terraform.tfstate`, ignored by git). Move it to an S3 backend if a second person needs to apply.
