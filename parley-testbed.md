# Agent-to-agent conversation channel: real-architecture testbed plan

| Field | Value |
|---|---|
| Status | Plan only. Nothing bought, provisioned or built. |
| Date | 30 October 2026 |
| Owner | Petar Matan |
| Design record | `parley-design.md` in this folder |
| Proof of concept | `parley-poc.md` in this folder |
| Decisions | `DECISIONS.md`, section "testbed", rows T1 to T14 |

The proof of concept proved the message path on one machine with fakes. This plan replaces every fake with the component a company would run, so the solution can be tested, and shown, in a real architecture with a real identity provider, a real git host, real network paths and real people. The review conversation kind is the first exercised because it is the most complete one. The testbed is built so that a second kind runs on it without change.

---

## 1. What the proof of concept fakes

| Fake today | Where | Real replacement |
|---|---|---|
| Merge requests are a JSON file | `inbox/internal/mr` (the `Local` provider) | Self-hosted GitLab CE, read through its API |
| Identity is a DEX container with three static users | `deploy/local/dex/` | Keycloak with a committed realm: users, groups, roles, session policy |
| Only the `email` claim exists; no groups, no roles | plugin, inbox, gateway | `email` stays the identity; a `groups` claim becomes an optional second layer |
| A token is written once at login and never refreshed | `plugin/login.mjs` | Refresh tokens; the channel server refreshes in-process |
| The plugin sends the ID token | `plugin/lib/oidc.mjs` default `token: id` | The access token with audience `parley` (T4) |
| One machine, no network segmentation | `deploy/local/compose.yaml` has ports only | One cloud host with three Docker networks; only the proxy is exposed |
| Plain HTTP, `localhost` names | everywhere | TLS from Let's Encrypt, real DNS names |
| Sessions are driven by a person at one keyboard | runbook | Two real laptops, two real people; headless containers in phase 4 |
| Any repository with any content | sandbox fixture | A small Go service with tests and a CI pipeline |

---

## 2. Decisions in one table

| Id | Decision | Choice |
|---|---|---|
| T1 | Testbed shape | Real laptops driven by people, shared services on one cloud host. Headless agent containers come later as a second shape (T13) |
| T2 | Git host | Self-hosted GitLab CE on the cloud host, logging in through Keycloak |
| T3 | Identity provider | Keycloak, realm committed as a file |
| T4 | Token the plugin sends | Access token, audience `parley`. ID token stays selectable for the local DEX sandbox |
| T5 | How the inbox reads GitLab | A GitLab group access token with `read_api` scope, held only by the inbox |
| T6 | Who may send | Unchanged: reviewers and assignees of the merge request. GitLab membership is the source of truth |
| T7 | Groups and roles | Optional second layer. A `groups` claim in the token; a policy may require a group at the gateway or per finding class in the inbox. Off by default |
| T8 | Token lifetime | Keycloak refresh tokens; realm session idle 8 h, maximum 12 h; the channel server refreshes before expiry and rewrites the token file |
| T9 | Hosting | One EC2 instance under Docker Compose. Elastic IP. Segmentation by Docker networks, not by machines |
| T10 | DNS and TLS | A bought domain, zone in Route 53, records from Terraform; Let's Encrypt through the reverse proxy |
| T11 | Infrastructure tool | Terraform for instance, address, DNS, security group; cloud-init installs Docker and starts Compose |
| T12 | Repository layout | `deploy/local/` (DEX sandbox), `deploy/cloud/` (the host bundle), `testbed/` (Terraform, seed, test service, scenarios) |
| T13 | Headless agents | Phase 4. Device authorization grant on a test-only Keycloak client |
| T14 | Test service | A small Go HTTP service with tests, a Makefile and a GitLab CI pipeline |

Reasons are in `DECISIONS.md`.

---

## 3. Topology

```mermaid
flowchart LR
  subgraph LA[Laptop A, reviewer]
    CA[Claude Code + Parley plugin]
  end
  subgraph LB[Laptop B, author]
    CB[Claude Code + Parley plugin, quarantine worktree]
  end
  subgraph EC2[Cloud host, Docker Compose]
    RP[Reverse proxy :443, TLS]
    KC[Keycloak]
    GL[GitLab CE + runner]
    GW[agentgateway]
    IN[Inbox]
    RP --> KC
    RP --> GL
    RP --> GW
    GW --> IN
    IN -->|merge request API, read only| GL
    GL -->|OIDC login| KC
  end
  CA -->|OIDC PKCE, bearer JWT| RP
  CB -->|OIDC PKCE, bearer JWT| RP
  CB -->|git push by the promoter| RP
```

Names, with `<domain>` = `parleylab.dev`, registered in Route 53 on 30 October 2026 (T10):

| Name | Service | Exposed |
|---|---|---|
| `auth.<domain>` | Keycloak | yes, 443 |
| `gitlab.<domain>` | GitLab CE web, API, git over HTTPS | yes, 443 |
| `channel.<domain>` | agentgateway, which fronts the inbox | yes, 443 |
| `inbox` | inbox service | no; Docker network `services` only |

Docker networks: `edge` (proxy, Keycloak, GitLab, gateway), `services` (gateway, inbox, GitLab API side), `ci` (GitLab, runner). The inbox joins `services` only. The security group opens 443 to the world and 22 to the owner's address.

---

## 4. Identity: the Keycloak realm `parley`

| Item | Value |
|---|---|
| Clients | `parley-cli`: public, PKCE S256 required, standard flow, refresh tokens on, redirect `http://127.0.0.1:5557/callback`. `gitlab`: confidential, standard flow, GitLab's callback. `parley-headless` (phase 4): public, device flow only |
| Mappers on `parley-cli` | Audience mapper adds `parley` to the access token `aud`. Group membership mapper adds `groups` as a list of names. `email` and `email_verified` as usual |
| Groups | `parley-users` (may use the channel), `reviewers`, `maintainers`, `security` |
| Users | `alice` (reviewer, in `reviewers`), `bob` (author), `carol` (second reviewer, in `reviewers` and `security`), `dave` (project member, not a participant), `erin` (no project access, in `parley-users` only), `mallory` (valid account, not in `parley-users`), `ci-bot` (service account, phase 4) |
| Sessions | SSO idle 8 h, SSO maximum 12 h, access token 5 min, refresh token rotation on |
| Realm export | `deploy/cloud/keycloak/realm-parley.json`, imported at start. Test passwords are set by the seed script from an untracked file, never committed |

GitLab logs in with the `openid_connect` provider against the same realm. GitLab's user email is the Keycloak email, so the token's `email` claim and the merge request participant are the same string with no mapping table (T2).

---

## 5. GitLab

| Item | Value |
|---|---|
| Edition | GitLab CE, the current stable image, with the Omnibus configuration committed as `deploy/cloud/gitlab/gitlab.rb` |
| Runner | One Docker-executor runner on network `ci`, registered by the seed script |
| Group and project | Group `parley-lab`, project `orders-service` (T14) |
| Memberships | `alice`, `carol`: Maintainer. `bob`, `dave`: Developer. `erin`, `mallory`: none |
| Inbox credential | Group access token on `parley-lab`, role Reporter, scope `read_api`, stored only on the host as an inbox environment variable (T5) |
| Merge requests | Created by the seed script from branches with planted defects, one per scenario in section 9. Reviewer and assignee set per scenario |

---

## 6. Changes to the solution

These are the only code changes the testbed needs. Everything else is configuration.

| Component | Change | Why |
|---|---|---|
| inbox | A `GitLab` provider behind the existing `mr` interface: merge request by `<repo>!<iid>`, author, reviewers, assignees, source and target branch, diff paths; visibility check by the project membership of the sender's email | Replaces the file fake (T2, T5, T6) |
| inbox | Issuer entry for Keycloak: discovery, audience `parley`, `identity_claim` `email` | T3, T4 |
| inbox | Optional group policy: a required group per finding class in `.aic/policy.json`, checked against the `groups` claim | T7, off by default |
| plugin | Default `token` becomes `access`; the login stores the refresh token next to the access token with mode 0600 | T4, T8 |
| plugin | The channel server refreshes the access token two minutes before `exp` and rewrites the token file; hooks keep reading the file | T8 |
| gateway | Provider entry for the Keycloak issuer with audience `parley`; optional claim rule `groups` contains `parley-users` | T3, T7 |
| quarantine, promoter | None expected. The promoter pushes to `gitlab.<domain>` over HTTPS with the person's git credential | |
| repository | The local compose file and DEX config now live in `deploy/local/` (done in phase 1); add `deploy/cloud/` and `testbed/` | T12 |

The refresh happens in the channel server because it is the one long-lived process per session. Hooks are short-lived and already read the token file on every call (`plugin/lib/identity.mjs`, `readToken`).

---

## 7. Infrastructure

| Resource | Choice | Reason |
|---|---|---|
| Instance | `t3.xlarge`, 4 vCPU, 16 GB, 80 GB gp3 | GitLab CE alone needs 4 GB; Keycloak, the runner and builds need headroom |
| Address | Elastic IP | DNS records survive stop and start |
| DNS | Route 53 hosted zone for `<domain>`; `A` records for `auth`, `gitlab`, `channel` | Managed from Terraform (T10, T11) |
| Certificates | Caddy as the reverse proxy; Let's Encrypt with HTTP-01 | Zero certificate handling in the other services |
| Boot | cloud-init: Docker, Compose, clone of the `deploy/cloud/` bundle, `docker compose up -d` | One command from Terraform to a running host |
| Secrets | A `.env` on the host written by the seed step from an untracked local file; never committed | |
| State | Terraform state local at first; move to an S3 bucket if a second person needs to apply | |

Region: `eu-north-1` (Stockholm). The AWS account is a member of an organization created by the new projects console, and a service control policy denies EC2 in every other region. Route 53 is global and unaffected.

Expected cost, on demand, Stockholm region, October 2026 list prices. Check before buying.

| Item | Approximate |
|---|---|
| `t3.xlarge` running 24 h | about 5 USD per day |
| `t3.xlarge` stopped, 80 GB disk kept | about 0.25 USD per day |
| Elastic IP while the instance is stopped | about 0.12 USD per day |
| Route 53 hosted zone | 0.50 USD per month |
| Domain, `.dev` or `.com` | 12 to 15 USD per year |

Stop the instance between sessions. A `make testbed-stop` and `make testbed-start` wrap the two AWS calls.

---

## 8. Test service `orders-service`

A Go HTTP service with the standard library only, ten to fifteen files: an in-memory order store, create, get, list, cancel, a price calculation with discounts, input validation, JSON handlers, a `Makefile` with `test`, `vet`, `lint`, and a `.gitlab-ci.yml` that runs them on every merge request. Small enough to read in ten minutes. Large enough that a review finding needs context.

The planted defects live on branches, one per scenario. Each branch has one merge request. The seed script is idempotent: running it again resets the branches and merge requests to the committed state.

---

## 9. Scenario catalogue

| Id | Scenario | Sender | Expected outcome |
|---|---|---|---|
| S1 | Happy path. One `fix` class finding on a real defect | `alice` on MR 1 | Author's agent fixes, promoter pushes, CI passes, reply `done` |
| S2 | Dispute. A finding that is wrong | `alice` on MR 2 | Author's agent disputes with a reason; no push |
| S3 | Escalation. A `product` class finding | `carol` on MR 3 | Author's agent asks its person; no push until the person answers |
| S4 | Non-participant sender | `dave` on MR 1 | Inbox rejects: sender is a project member but not a reviewer or assignee |
| S5 | No project access | `erin` on MR 1 | Inbox rejects: sender cannot see the merge request |
| S6 | Outside the channel group | `mallory` on MR 1 | Gateway rejects on the `groups` claim when T7 is enabled; inbox rejects on visibility when it is not |
| S7 | Expired token | `alice`, token aged past `exp` | Gateway rejects; the plugin refreshes and retries once |
| S8 | Revoked user | `bob` disabled in Keycloak mid-session | Refresh fails; the session reports it; no message is accepted from `bob` |
| S9 | Round cap | `alice` and `bob` on MR 4, a defect with two valid fixes | The fifth round ends the task; both people are told |
| S10 | Promoter rejection | `bob` edits a lock file on MR 5 | Guard denies the edit; the promoter refuses the push |
| S11 | Laptop closed | `bob` offline when S1 is sent | Message queues; delivered at the next session start |
| S12 | Canaries | the twelve poisoned findings | Same results as the local suite, through the real gateway |
| S13 | Group policy | `alice` sends a `security` class finding, T7 on | Rejected: only `security` group members may send that class |
| S14 | Second conversation kind | to be defined | Proves the transport is kind-neutral on the real stack |

---

## 10. Phases

| Phase | Deliverable | Done when |
|---|---|---|
| 0 | Domain bought, Route 53 zone live, AWS account and credentials ready. Done 2026-10-30: `parleylab.dev` registered from the CLI, profile `parley` (IAM user `parley-admin`, region `eu-north-1`, see section 7) | `dig auth.<domain>` returns the Elastic IP |
| 1 | `testbed/terraform` applies; `deploy/cloud/` runs; Keycloak and GitLab reachable on HTTPS; GitLab logs in through Keycloak; realm and users seeded | `alice` logs in to GitLab with her Keycloak password |
| 2 | Code changes of section 6; local test suites green; the DEX sandbox still runs from `deploy/local/` | `make test` green; a login against `auth.<domain>` writes an access token with `aud` `parley` |
| 3 | `orders-service` seeded with its merge requests; S1 to S12 run from two laptops | Each scenario's outcome recorded in the runbook with the message ids from the inbox log |
| 4 | Headless agent containers with the device flow; S1 to S12 scripted | `make e2e` runs the catalogue without a person |

---

## 11. Open points

- Whether the promoter should push with the person's credential or with a per-task deploy token minted by the inbox. The person's credential is the proof of concept behaviour and stays for phase 3.
- Whether GitLab group membership should be synced from Keycloak groups. Not in CE in a way this plan relies on; memberships are set by the seed script.
- The content and id shape of the first non-review conversation kind for S14.
