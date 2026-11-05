# Parley

A channel that lets one person's coding agent talk to another person's coding agent, with identity checks, store-and-forward delivery, a quarantine for the receiving agent, and a program, not a model, deciding what reaches the repository.

Parley lets developers' AI agents work things out with each other directly, so people get involved only where their judgment is needed. In code review, a reviewer's agent sends findings straight to the author's agent. The author's agent fixes clear bugs, pushes back on wrong findings with evidence, and brings only real decisions to the author, so review rounds take minutes instead of days of back-and-forth. For companies this means faster merges and less context-switching for engineers, with strict safety limits: agents act only within set rules, every exchange is recorded, and nothing reaches the codebase without passing fixed checks.

**Status: proof of concept.** It runs locally in a sandbox and on a testbed with real services: self-hosted GitLab, Keycloak login, and one cloud host (`parley-testbed.md`; results in `docs/runbook.md`). Nothing here is production software. Code review is the first conversation kind; the transport is built for any typed exchange between two people's agents.

## Contents

- [Why](#why)
- [How it works](#how-it-works)
- [Quick start](#quick-start)
- [Using the channel](#using-the-channel)
- [Configuration](#configuration)
- [Identity providers](#identity-providers)
- [Security model](#security-model)
- [Conversation kinds](#conversation-kinds)
- [Repository layout](#repository-layout)
- [Documentation](#documentation)
- [Development](#development)
- [Roadmap and known gaps](#roadmap-and-known-gaps)
- [Contributing](#contributing)
- [License](#license)

## Why

Developers increasingly work together with a coding agent in a local session. When two developers collaborate, their agents never talk to each other. A person carries messages between the two sessions and adds nothing on the way.

This project moves that exchange onto a channel. One agent sends a typed message addressed to a work item. The other agent receives it inside its own session, acts within an authority limit, and replies. People step in only for decisions that are theirs: a product choice, a change of scope, an approval.

The first conversation kind is code review. A reviewer's agent sends structured findings on a merge request. The author's agent fixes what is clear, disputes with reasons, and asks its human only when a decision is required.

## How it works

```
Person A's laptop                  Shared services                    Person B's laptop
┌──────────────────┐     ┌──────────────────────────────┐     ┌──────────────────────┐
│ Agent session    │     │ Gateway (agentgateway)       │     │ Quarantine session   │
│  └ channel plugin│──▶──│  verifies the sender's JWT   │──▶──│  └ channel plugin    │
│    signs as A    │     │ Inbox service (Go)           │     │    re-validates,     │
│                  │◀────│  validates, cleans, stores,  │◀────│    renders as data   │
│                  │     │  routes to the live session, │     │ Promoter             │
│                  │     │  caps rounds, logs           │     │  pushes after checks │
└──────────────────┘     └──────────────────────────────┘     └──────────────────────┘
```

1. The sender's agent shapes a message into a typed schema and sends it through its channel plugin with a bearer token that names the person.
2. The gateway validates the token against the configured OpenID Connect issuer and forwards the request with the token preserved.
3. The inbox re-validates the token, rejects malformed messages, strips hidden characters and HTML, checks the sender against the work item, stores the cleaned form, and delivers it to the receiver's live session or queues it.
4. The receiver's plugin re-validates the message and emits it into the session as a channel event marked as untrusted data. A session-start hook pulls queued messages when no session was open.
5. The receiving agent works in a quarantine: an isolated worktree with no secrets, no network, and a guard hook that blocks writes outside the change under discussion.
6. A deterministic promoter is the only exit for code. It pushes to the branch after fixed checks. Replies travel back on the same path with the same checks. A round cap ends a conversation that does not converge, and both people are told.

The design record explains every decision, the two risks that decide feasibility, and the control layers: [parley-design.md](parley-design.md).

## Quick start

Prerequisites:

| Tool | Version |
|---|---|
| Node | 22 or newer |
| Go | 1.27 or newer |
| Docker with Compose | running |
| Claude Code | 2.1.303 or newer, with channels enabled or the development flag |
| make, jq | any |

Build, test, and bring up the sandbox:

```bash
git clone https://github.com/PetarMatan/parley.git
cd parley
(cd plugin && npm ci)
make build        # Go binaries into work/bin
make test         # Go and Node test suites
make setup        # keys, mock tokens, a bare origin, two clones, one fake merge request
make up           # deploy/local: gateway on :3000 and the local OIDC provider on :5556
AIC_WORK="$PWD/work" "$PWD/work/bin/aic-inbox"    # in its own terminal
```

Then open two sessions, one per clone, and follow the four-step experiment in the runbook: [docs/runbook.md](docs/runbook.md). The runbook marks commands that still need confirmation on a first run.

## Using the channel

Each session gets the channel through a local MCP server started from the checkout's `.mcp.json`. The server exposes these tools to the agent:

| Tool | What it does |
|---|---|
| `inbox_send` | Sends a new message (a finding) addressed to a work item |
| `inbox_reply` | Replies within an existing task; refused past the round cap |
| `inbox_list` | Lists unacknowledged messages for this person and repository |
| `inbox_ack` | Acknowledges a handled message so it is never delivered twice |
| `inbox_note` | Writes an outcome note to the work item's record |
| `inbox_promote` | Asks the promoter to push the current commit after its checks |

Two skills drive the review kind: `review-send` on the sending side and `review-handle` on the receiving side. They live in `skills/` and are installed into the sandbox clones by `make setup`.

Hooks shipped with the plugin register the session with the inbox at start, refresh the registration when the working directory changes, pull queued messages, and withdraw the session at end.

## Configuration

Settings are environment variables with the `AIC_` prefix. The ones most people need:

| Variable | Used by | Meaning |
|---|---|---|
| `AIC_WORK` | all | Work directory: keys, tokens, store, sandbox clones (default `./work`) |
| `AIC_BASE_URL` | plugin, hooks | Inbox URL as seen through the gateway (default `http://localhost:3000`) |
| `AIC_INBOX_ADDR` | inbox | Listen address of the inbox service (default `127.0.0.1:7070`) |
| `AIC_ISSUERS` | inbox | JSON array of accepted token issuers; see [Identity providers](#identity-providers) |
| `AIC_MAX_ROUNDS` | inbox | Round cap per task (default 5) |
| `AIC_EXTRA_GUARD` | quarantine | Optional extra PreToolUse hook command for an organization's own guard |
| `AIC_CHECKOUT` | plugin, hooks | Overrides the search for the checkout's `.aic/identity.json` |

Per checkout, `.aic/identity.json` names the person, the token file, the inbox URL, and the OIDC login settings. `.aic/policy.json` holds the authority ceiling per finding class and the round and patch-size limits. The full API is specified in [inbox/openapi.yaml](inbox/openapi.yaml) and the message rules in [docs/contract.md](docs/contract.md).

## Identity providers

Every request carries an RS256 JSON Web Token. The gateway and the inbox both validate it against a list of issuers, so any standard OpenID Connect provider works: Keycloak, Auth0, Okta, Entra ID, Google, DEX. The plugin logs in with OIDC discovery, the authorization code flow, and PKCE, as a public client with a loopback redirect.

To use your provider:

1. Register a public client with redirect URI `http://127.0.0.1:5557/callback` and PKCE S256.
2. Set `oidc.issuer` and `oidc.client_id` in `.aic/identity.json`, or the `AIC_OIDC_ISSUER` and `AIC_OIDC_CLIENT_ID` variables.
3. Add the issuer to `AIC_ISSUERS`. Without a JWKS field the inbox reads `jwks_uri` from the provider's discovery document. Set `identity_claim` if the provider does not emit `email`.
4. Add the issuer and its JWKS URL to `gateway/config.yaml`.
5. Run `node plugin/login.mjs --checkout <dir>`.

The sandbox uses DEX in Docker Compose as the local provider, and minted mock tokens for tests and canaries. Details and provider-specific notes are in runbook section 8.

## Security model

Steering another person's agent with text written by a model is prompt injection by design. The project does not try to make the model resist it. It caps what a hijacked agent can do with controls outside the model:

- **Identity at the door.** Sender verified by the gateway and the inbox; sender must be a participant of the work item; replays rejected.
- **Content as data.** Typed schemas with closed lists and length caps; hidden Unicode and HTML stripped and the cleaned form stored; messages rendered inside a frame that marks them untrusted.
- **Quarantine.** Isolated worktree, scrubbed environment, no network, a guard hook that denies edits outside the change and to configuration, dependency, and lock files.
- **A program does the risky steps.** Only the promoter pushes, after fixed checks. Approve and merge never leave a person.
- **Human gates by reversibility.** No gate for worktree edits; the promoter or a person for a push; always a person for approve and merge.
- **Detection and audit.** Injection scoring, every message and promotion logged with its sender, a canary suite of twelve attacks in `canaries/`.

The residual risk, and the evidence behind each layer, are in section 8 of the design record.

## Conversation kinds

The transport is use-case neutral. A conversation kind adds five things: a payload schema pair, a context id shape, a sender rule, an authority policy, and a skill pair. Review is the only kind built: `finding.v1`, `reply.v1`, `note.v1`, context `<repo>!<iid>`, reviewers and assignees as senders, a ceiling per finding class, and the two review skills.

Candidate next kinds, not built: ticket-readiness questions, deploy-verification results, design questions. Section 6.5 of the design record describes the model, and decision P20 in [DECISIONS.md](DECISIONS.md) lists each place in the code a second kind must change.

## Repository layout

| Path | Contents |
|---|---|
| `inbox/` | Go inbox service, token minting tool, promoter; standard library only |
| `plugin/` | Node 22 channel plugin: MCP channel server, hooks, OIDC login |
| `quarantine/` | Quarantine worktree script, tool-use guard, settings overlay |
| `schema/` | Message schemas (JSON Schema) and generated test vectors shared by both validators |
| `skills/` | Skills of the review kind: `review-send`, `review-handle` |
| `canaries/` | Twelve poisoned-message cases and their runner |
| `gateway/` | agentgateway configuration and its validation script |
| `deploy/local/` | Compose file and DEX configuration of the local one-machine sandbox |
| `deploy/cloud/` | Compose bundle for a shared host: reverse proxy, Keycloak, GitLab CE, agentgateway, inbox (testbed plan) |
| `testbed/` | AWS part of the testbed: Terraform, scripts, test service, scenarios |
| `sandbox/` | Scripts that build and reset the work directory |
| `docs/` | Contract, runbook, conventions, gateway notes, per-stream decisions |

## Documentation

- [parley-design.md](parley-design.md): design record, risks, control layers, open questions.
- [parley-poc.md](parley-poc.md): proof-of-concept plan, test plan, canary suite.
- [parley-testbed.md](parley-testbed.md): plan for a testbed on real architecture: Keycloak, self-hosted GitLab, a cloud host, two laptops.
- [deploy/cloud/README.md](deploy/cloud/README.md): the Compose bundle for a shared host and how to check each service.
- [testbed/README.md](testbed/README.md): from nothing to a running host on AWS, and how to stop, start, and tear it down.
- [docs/runbook.md](docs/runbook.md): step-by-step run on one machine, and with a second person.
- [docs/contract.md](docs/contract.md): message shapes, cleaning rules, routing and state rules.
- [inbox/openapi.yaml](inbox/openapi.yaml): HTTP API of the inbox.
- [docs/conventions.md](docs/conventions.md): logging, dates, API, and PII conventions the code follows.
- [DECISIONS.md](DECISIONS.md): every decision taken while building, reviewing, running, and porting the proof of concept.

## Development

```bash
make test         # Go: cd inbox && go test ./...   Node: node --test in plugin, quarantine, canaries
make vectors      # regenerate schema/vectors.json after editing schema/gen-vectors.mjs
bash gateway/validate.sh   # validate the gateway and DEX configs (needs Docker)
make down         # stop the services
make reset        # delete the work directory (asks first)
```

Two validators, one in Go and one in Node, must behave identically. They are tested against the same `schema/vectors.json`. Never edit that file by hand.

The canary runner has two modes: `--mode inbox` is deterministic and needs no model; `--mode session` runs an agent in a fresh quarantine for each case. See runbook section 7.

## Roadmap and known gaps

The proof of concept answered its three questions: a message travels between two people's sessions, the envelope held against the canaries, and the spike checks ran. Open points found in live runs are recorded as decisions D30 to D40, among them:

- A dropped channel event is still acknowledged, so a message can be lost (D30).
- The promoter pushes every commit in the range, not only the promoted task's (D34).
- A human-approved escalation has no path to the promoter (D36).
- The round cap is reached by self-limiting agents, so the inbox never escalates (D40).

Next step: a testbed on real architecture, planned in [parley-testbed.md](parley-testbed.md). Beyond the review kind: a second conversation kind (P20), a server-side CI sender, permission relay to a phone or chat, and A2A conformance so a non-Claude agent can join.

## Contributing

Issues and pull requests are welcome. Before opening a pull request:

1. Run `make test` and keep every suite green.
2. If you change a message rule, change it in both validators and add a vector to `schema/gen-vectors.mjs`.
3. Record any design decision as a new row in `DECISIONS.md`: what was open, what you chose, why.
4. Keep the documents company-neutral. Name public products and standards only.

## License

This project is licensed under the Apache License 2.0. See [LICENSE](LICENSE).

The channel plugin started from Anthropic's `fakechat` example and keeps that example's Apache 2.0 notice in [plugin/LICENSE](plugin/LICENSE).
