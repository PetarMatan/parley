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
| `scripts/` | `deploy.sh`, `status.sh`, `ssh.sh`, `stop.sh`, `start.sh`, `destroy.sh`, `lib.sh`; phase 3: `gitlab-root-token.sh`, `seed-gitlab.sh`, `checkout-setup.sh`, `gitlab-user-token.sh`, `quarantine.sh`, `refresh-tokens.mjs`, `sender-check.sh`, `send-finding.sh`, `send-reply.sh`, `canaries.sh` |
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
6. The `orders-service` code on `main` (the `scenarios/` folder stays out of the project), one branch `scenario/<id>` per planted defect, and one merge request per branch with reviewer and assignee set. Context ids for Parley are `parley-lab/orders-service!<iid>`. The project commits `.aic/policy.json` and nothing else of Parley: the channel server, the hooks and the skills come from the installed plugin (D49, D51).

Branch `main` is protected for Maintainers with force push allowed, so a re-run can reset it. The scenario branches are force-pushed on every run.

## The plugin, once per laptop

Every laptop that runs a session installs the Parley plugin (D49). From a clone of this repository, or from GitHub with read access to it:

```bash
claude plugin marketplace add "$PWD" && claude plugin install parley@parley      # from a local clone: runs the working tree in place
claude plugin install parley --marketplace PetarMatan/parley                      # from GitHub: copies the plugin and installs its dependencies
```

Sessions then start with `claude --dangerously-load-development-channels plugin:parley@parley`. A session in a Parley project that nobody has logged in to says so at start and names the login skill.

## First two-session run on one laptop

After the seed, clone the project twice and log each clone in as one person:

```bash
git clone https://gitlab.parleylab.dev/parley-lab/orders-service.git ~/work/orders-alice
git clone https://gitlab.parleylab.dev/parley-lab/orders-service.git ~/work/orders-bob
make testbed-checkout DIR=~/work/orders-alice USER=alice   # the plugin's login: writes .aic/identity.json, stores the token
make testbed-checkout DIR=~/work/orders-bob   USER=bob
```

Cloning over HTTPS needs a GitLab personal access token per person as the password: `make testbed-user-token USER=alice` issues one with the admin API (D51), or the person creates one in GitLab after their first Keycloak login. Then open a Claude Code session in each clone with the plugin flag above, check `/mcp` shows `aic-inbox`, and run scenario S1 from the plan: alice reviews merge request 1 with `parley:review-send`; bob's quarantine session handles it.

## Second laptop

The second person needs Node 22, git, Claude Code signed in to any Claude account whose organization or plan has Channels switched on (D29: the development flag skips only the allow list; with Channels off, events are dropped silently), and GitHub read access to this repository while it is the marketplace. Parley itself never sees the Claude account: the person is who Keycloak says. You send them, over a private channel:

- the login line: `/parley:login https://channel.parleylab.dev https://auth.parleylab.dev/realms/parley parley-cli preferred_username`
- their user name and Keycloak password (`deploy/cloud/seed/users.env`)
- their GitLab token from `make testbed-user-token USER=<name>`, or an existing one (a token works on any machine)

On the second laptop:

```bash
claude plugin install parley --marketplace PetarMatan/parley
git clone https://gitlab.parleylab.dev/parley-lab/orders-service.git     # user name and the GitLab token as the password
cd orders-service
claude                                                                    # paste the login line; the browser opens on Keycloak
claude --dangerously-load-development-channels plugin:parley@parley      # a new session, with the channel
```

The reviewer side needs nothing else. The author side also runs the quarantine and the promoter, which still come from this repository (`make build`, `make testbed-quarantine`), so the first second-laptop run is the reviewer. While the second laptop runs a person's session, that person's clone on the first laptop must have no session open: the inbox routes to the most active session.

## Scenarios without a session (S4 to S6, S12)

These scenarios need tokens and the inbox, no agent. Each person logs in once (`node plugin/login.mjs --checkout <dir> --no-open`; for a person without a clone, a directory with only `.aic/identity.json` is enough). Keycloak issues access tokens for five minutes, so the scripts refresh every token from its refresh token first. When several people log in from one browser, do not sign the previous person out: Keycloak binds the refresh token to the browser session, and the sign-out revokes it (T46). Open each further person's login link in a private window instead.

```bash
node testbed/scripts/refresh-tokens.mjs alice bob dave             # refreshed | not_due | <reason> per person
bash testbed/scripts/sender-check.sh 2 internal/order/store.go dave erin mallory   # S4 to S6: expects 403 sender_not_allowed each
bash testbed/scripts/canaries.sh 2 internal/order/store.go --repeat 3             # S12: the inbox-mode canaries through the real gateway (T43)
bash testbed/scripts/send-finding.sh 5 alice main.go 14 fact maintainability major "<title>" "<note>"   # a fixed reviewer-side finding (S10, S11)
bash testbed/scripts/send-reply.sh 4 bob f-3363ebec disputed "<note>"                               # a scripted reply, either side (S9: alternate to round max_rounds-1)
```

S9 with a restart (the 2026-11-11 re-run): script the finding and the replies to round `max_rounds - 1` while the author has no session, start the author's quarantine session and let it receive the messages, stop it before it replies (Escape, then exit), start a fresh one and say: "This is a new session. Continue task <task_id> on this merge request. Pull its history from the inbox first." The agent calls `inbox_history`, names the round as "round N of max_rounds", and the person decides the last round.

`canaries.sh` needs `~/.parley/work/mr/<iid>.json`, written by `make testbed-quarantine IID=<iid>`, and reads origin heads through the reviewer's clone (`REVIEWER_CLONE`, default `~/work/orders-alice`). Evidence on the host: `docker compose logs --since <time> inbox` for `sender_refused` and `round_limit` events, `docker compose logs --since <time> agentgateway` for the `sender` field of each request.
