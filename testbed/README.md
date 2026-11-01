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
make testbed-status     # Keycloak discovery 200, GitLab sign-in page 200, gateway 401
```

Run the Make targets from the repository root. GitLab needs about five minutes after `deploy` before `status` passes. Then run the Keycloak user seed described in `../deploy/cloud/README.md`.

`make testbed-deploy` syncs the working tree of this checkout to the host over SSH, so uncommitted changes deploy too and the repository may stay private. Nothing needs to be pushed first.

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

## When your public address changes

SSH on port 22 is open to one address, `admin_cidr` in `terraform/terraform.tfvars`. Home and mobile connections get a new address now and then. The public sites keep working; only `testbed-deploy`, `testbed-ssh`, `testbed-admin` and the container list in `testbed-status` need the rule. To fix it:

```bash
curl -s https://checkip.amazonaws.com          # your current address
# put it into terraform/terraform.tfvars as admin_cidr = "<address>/32"
make testbed-apply                              # changes one rule, nothing else
```

## Administration

- `make testbed-admin` opens the Keycloak admin console through an SSH tunnel; Caddy refuses `/admin` from the Internet.
- GitLab has no web password sign-in. Admin tasks run on the host: `make testbed-ssh`, then `cd /opt/parley/deploy/cloud && docker compose exec gitlab gitlab-rails runner '...'`.
- `make testbed-stop` when nobody is testing. The host then costs about 0.35 USD per day instead of 4.5.

## Phase 3: GitLab seed

Run after the host is up and `alice` has logged in once (phase 1 exit test). Both commands run on your laptop; only the runner registration goes over SSH.

```bash
make testbed-root-token     # root API token, 30 days, into testbed/.gitlab-root-token (ignored by git)
make testbed-seed-gitlab    # everything below; safe to run again
```

What the seed creates, in order:

1. Users `alice`, `bob`, `carol`, `dave`, `erin`, `mallory` with their `@parleylab.dev` emails, so memberships can be set before their first login. GitLab links each Keycloak identity on first login by email.
2. Group `parley-lab` (private) and project `orders-service` (private, default branch `main`, merge only with a green pipeline).
3. Memberships: `alice` and `carol` Maintainer; `bob` and `dave` Developer; `erin` and `mallory` none.
4. A group access token `parley-inbox`, role Reporter, scope `api` (the inbox reads merge requests and posts notes; `read_api` cannot post), 90 days. Printed once as `AIC_GITLAB_TOKEN=...`; put that line into `deploy/cloud/.env` and run `make testbed-deploy`.
5. A project runner, registered on the host with the Docker executor and the `golang:1.27` image on the `ci` network.
6. The `orders-service` code on `main` (the `scenarios/` folder stays out of the project), one branch `scenario/<id>` per planted defect, and one merge request per branch with reviewer and assignee set. Context ids for Parley are `parley-lab/orders-service!<iid>`.

Branch `main` is protected for Maintainers with force push allowed, so a re-run can reset it. The scenario branches are force-pushed on every run.

## First two-session run on one laptop

After the seed, clone the project twice and prepare each clone for one person:

```bash
git clone https://gitlab.parleylab.dev/parley-lab/orders-service.git ~/work/orders-alice
git clone https://gitlab.parleylab.dev/parley-lab/orders-service.git ~/work/orders-bob
make testbed-checkout DIR=~/work/orders-alice USER=alice   # writes .aic/, settings.local.json, logs alice in
make testbed-checkout DIR=~/work/orders-bob   USER=bob
```

Cloning over HTTPS needs a GitLab personal access token per user as the password; each person creates one in GitLab after their first Keycloak login. Then open a Claude Code session in each clone with `PARLEY_ROOT` exported, check `claude mcp list` shows `aic-inbox`, and run scenario S1 from the plan: alice reviews merge request 1 with `review-send`; bob's quarantine session handles it.
