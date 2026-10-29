# Agent-to-agent conversation channel: design record

| Field | Value |
|---|---|
| Status | Exploration. Feasibility assessed, direction chosen, nothing built. |
| Date | 28 October 2026 |
| Owner | Petar Matan |
| Location | `~/Desktop/ai-development/agentic-conversation/`, the project folder for this work, next to the proof of concept plan. |

This record keeps everything the exploration produced: the problem, the context we started from, what the industry does, the decisions taken, the architecture, the constraints that shape it, the two risks that decide feasibility and how to contain them, what is still open, and the facts verified along the way. The companion page (not included in this repository) is the short, visual version for discussion. This file is the complete one.

---

## 1. The problem

Two people's coding agents cannot talk to each other. When one person's agent has something for another person's agent, a person carries the message between them: a review finding, a question about a ticket, the result of a deploy check, a point in a design discussion. This record designs a channel that carries typed messages between two people's agents directly. Code review is the first conversation kind. The narrative below uses it as the concrete case.

Developers at the company now do most of their work together with a Claude Code session on their laptop. When one developer reviews another developer's merge request, the reviewer's session reads the change and writes the findings. The reviewer then hands those findings to the author. The author pastes them into their own session, which fixes what it can and asks the author about the rest.

The two sessions never talk to each other. A person carries the messages between them, and that person adds nothing on the way. The review artifact itself is written by a model for a model, so the human in the middle rarely reads it. A sample of the current artifact shape is the untracked `review-points.md` in this repository: YAML front matter, Fixed and Ignored sections, reviewer subagents named, anchors to commits.

We want the review to flow from the reviewer's session to the author's session on its own, and the people to step in only when a decision is really theirs: a product choice, a change of scope, an approval.

Review is the first case because it is concrete and complete. The same path can later carry ticket-readiness questions, deploy-verification results, and a standalone agent application's traffic.

---

## 2. Context we started from

**Existing work.**

- An earlier investigation into automated merge request review recommended one shared GitLab CI template, the review lane, with team-owned instructions.
- The CI review lane work builds that lane: Claude Code headless in a CI job, three instruction levels, model access through the Agent Gateway with a team virtual key, one review thread on the merge request, read-only tools, no shell, a posting token scoped to comments, never pushes code. Status: new idea, blocked by the gateway.
- The Agent Gateway is the platform team's own work, not another team's. It is agentgateway from Solo.io (Apache 2.0, Rust data plane, Linux Foundation project under the Agentic AI Foundation). The gateway maintainer chose it over LiteLLM in an earlier gateway selection for three reasons: configuration as Kubernetes Gateway API resources reconciled by ArgoCD, Prometheus metrics without a licence, and first-class MCP. The accepted risk is maturity: v1.0 shipped in March 2026 with breaking changes across minor versions.
- The gateway proof of concept is deployed in the dev cluster since 5 October 2026. Testing is tracked separately. So far only virtual API keys and team budgets for model traffic have been tested (in review). DNS and reachability are next. An internal ingress is planned by 30 November 2026, and sign-in with the corporate identity is explicitly step two and not yet in place.
- Developers run Claude Code on personal subscriptions. The organization uses an OIDC identity provider (DEX in the local stand-in). The internal knowledge base plugin already proves that a local MCP client can log in to DEX (static client, fixed callback port, no dynamic registration).

**Controls that already exist.**

- An organization's own guard hook can block pipe-to-shell, destructive deletes outside the working directory, force push to protected branches, and reads of secret files.
- An organization can keep that guard's policy in one central file owned by its security function, changed only through an owner-approved merge request, propagated by plugin release.
- An organization can carry a managed floor of denied permissions that a developer cannot override.
- The CI review lane set the precedent for untrusted merge request content: read-only tools, no shell, a minimal-scope token, hooks from the repository checkout ignored.
- The secret-injection hook already skips the tags Claude Code sends by itself, including `<cross-session-message`. A channel message arrives as `<channel source=...>` and will need the same treatment.

**Knowledge base.** The internal knowledge base was not connected during this session, so standing decisions and team lessons were not checked. An earlier investigation found no governing decision record on review infrastructure.

---

## 3. What the industry does

Research covered interoperability protocols, review products, Claude Code's own features, gateways and identity, and human-in-the-loop primitives. The full tables with sources are on the companion page. The conclusions:

**Every product puts the merge request thread between the two agents.** The reviewer is a bot account that comments. A server-side agent, triggered by a webhook or a mention, reads those comments, pushes fixes and replies. GitHub Copilot's cloud agent, GitLab's Resolve with Duo, Claude Code's auto-fix, CodeRabbit, Cursor Bugbot, Greptile and Devin all work this way. No shipping product lets two people's interactive laptop sessions talk to each other.

**Claude Code cannot carry a message between two people's sessions today.** Cross-session messaging is same-account only. Cloud sessions and auto-fix are GitHub-only, and a GitLab repository cannot push back. Channels are the one documented way to push into a running session from outside: a local server, one per session, that emits events the session reads. Channels are a research preview.

**A2A is the only real agent-to-agent standard.** v1.0, March 2026, governed by the Linux Foundation. Its task vocabulary fits a review conversation, including an input-required state for human escalation. It gives no offline delivery, no identity model, and no documented cross-person production use. Claude Code does not speak it. Adopters report building identity and provenance themselves.

**Nobody sells a per-person agent inbox with corporate identity.** The closest are Google's Gemini coworker agents (private preview) and Microsoft's Agent 365 accounts, both locked to their suites.

**Licensing is not a problem.** Anthropic's pages, read directly: subscription traffic through a corporate gateway with only the base URL set is documented and allowed. Company-built headless agents belong on API keys or Bedrock, which the CI review lane already does. The channel moves messages, not model calls.

| Product | Reviewer agent | Who applies fixes | Medium | Human gate | GitLab |
|---|---|---|---|---|---|
| GitHub Copilot review + cloud agent | Copilot | Cloud agent on mention or button | PR comments | Write-access gate, cannot approve or merge, workflows wait | No |
| GitLab Code Review Flow + Resolve with Duo | @GitLabDuo | One click per thread | MR threads | A human clicks | Native, GA 19.3 |
| Claude Code auto-fix PRs | Any | The author's own cloud session | PR events into the session | Asks on ambiguous or architectural requests | GitHub only |
| Claude Code GitLab CI/CD | `claude -p` job | Same job | MR notes via your own webhook | MR approvals | Beta, GitLab-maintained |
| CodeRabbit, Bugbot, Greptile, Devin | Vendor bot | Vendor cloud agent, or hand-off to the author's local agent | PR or MR comments | Human command or review | Partial |

---

## 4. What Claude Code offers today

| Feature | What it does | Scope | Status |
|---|---|---|---|
| Channels | A local stdio MCP server, one per session, declares `claude/channel` and emits `notifications/claude/channel` with content and identifier-keyed meta. Arrives as `<channel source=...>`. Replies go out through an ordinary tool. Optional permission relay to another device. Sender gating is the server's job | Any sender the server admits, so cross-user is possible. Events only while the session is open. No acknowledgement. Busy session receives a batch on the next turn. `-p` sessions can use `--channels`. Not on Bedrock, Vertex or Foundry | Research preview. Team and Enterprise Owners must enable. Custom plugins need the organization allowlist (`allowedChannelPlugins`) or the development flag with a warning dialog on every start. Pro and Max users without an organization get only the development flag. Contract may change |
| Cross-session messaging | Plain-text messages between sessions | Same account only. Same machine over a Unix socket bound to the OS user; other machines through Anthropic servers via Remote Control | Shipped |
| Remote Control | Drive a local session from web or mobile | Own account. Disabled behind any non-Anthropic base URL, so a gateway turns it off | Shipped |
| Cloud sessions, auto-fix, scheduled tasks | An external event starts a new cloud session on the owner's account | Same account. GitHub only | Preview or beta |
| Hooks | Every hook gets `session_id` and `cwd`. `SessionStart` (startup, resume, clear, compact, fork), `UserPromptSubmit` and `Stop` inject context. `SessionEnd` and `CwdChanged` exist. No hook fires on an inbound channel message | Local | Shipped |
| Agent SDK sessions | Resume, fork, and a session store for resuming on another host | A headless responder can carry one conversation across events | Shipped |

---

## 5. Decisions taken in this session

### 5.1 The medium: a private channel through the Agent Gateway

The industry-standard answer is the merge request thread as the medium, with a webhook-triggered agent on the author's side. It is sound and we can always fall back to it. Petar chose the private channel because the merge request thread is not innovative and the company already runs the gateway and DEX. The merge request thread remains the fallback everyone already has and the human-readable record: outcomes and escalations always land there.

### 5.2 The decision map after that choice

| # | Decision | Options | Coupling and recommendation |
|---|---|---|---|
| 1 | What a message is addressed to | The work item (repository plus merge request) · a session · a person | **Load-bearing.** Addressing the merge request dissolves "which of my sessions": the live session with that repository open gets it, otherwise it waits. A session id is unknowable to the sender. A person is ambiguous and would force an orchestrator session per laptop. Recommendation: the work item |
| 2 | Receiver on the laptop | One channel process per session · an orchestrator session per laptop · a laptop daemon · pull only | Collapses under 1. See 5.3 |
| 3 | Broker state | Gateway alone · gateway plus an inbox service | Forced. The gateway stores nothing |
| 4 | Identity | DEX JWT per person with the agent named as actor · a shared service identity | Per person. DEX allows static clients only |
| 5 | Authorization policy | Same organization · reviewers and assignees of that merge request · explicit pairing | Reviewers and assignees, enforced at the gateway through its external-authorization hook |
| 6 | Message protocol | A2A task vocabulary: contextId = merge request, taskId = finding, states including input-required | Adopt the vocabulary so a non-Claude agent can join later |
| 7 | Escalation | The session asks its human on input-required · permission relay to a phone or Slack | Follows 2 |
| 8 | Billing path | Gateway virtual key on Bedrock for headless agents · the developer's subscription for interactive sessions | Follows Anthropic's terms |

### 5.3 Receiver options compared

Claude Code spawns one channel server process per session. That verified fact decides the shape: every session can announce itself, so no session has to orchestrate the others.

| Option | How a message reaches the right session | Keeps | Breaks |
|---|---|---|---|
| A · One channel process per session, routed by work item | Each session's channel server registers (user, host, repository, branch, session id) with the inbox and holds one outbound stream. The inbox delivers to the matching live session, otherwise it queues | Deterministic. Any number of sessions. No model in the routing path. Uses the documented contract | Needs channels enabled. Events drop when the session is closed, so the inbox is mandatory |
| B · One orchestrator session per laptop | Only the orchestrator loads the channel and forwards to working sessions with local cross-session messaging | One gateway connection per laptop | An always-on session on the subscription. A model as router is slow, non-deterministic and costs tokens per message. Local messaging cannot reach a closed session either. Doubles the injection surface. **Rejected** |
| C · Laptop daemon | One non-model process injects into each session through its messaging socket and token | Cheap, deterministic | Undocumented surface, inferred from environment variables. Fallback only |
| D · Pull only | A `SessionStart` hook or a session-scoped scheduled task pulls the inbox through an MCP tool. DEX OAuth for a remote MCP server is proven with the internal knowledge base plugin | Works today without a preview feature | No live push |

**Verdict:** A for live delivery, D as the durable path underneath. Both read the same inbox.

---

## 6. Architecture

### 6.1 Context

```
Reviewer A ──sends findings from their session──▶ ┌──────────────────────────────┐ ◀──delivers into B's session──▶ Author B
                                                  │ Agent conversation channel   │     B's agent replies, disputes
                                                  │ gateway · inbox · plugins    │
                                                  └──────┬───────────┬───────────┘
                                           verifies who  │           │ outcome and escalations
                                                         ▼           ▼
                                                        DEX        GitLab merge request
Model calls from each session go to Anthropic or Bedrock through the gateway's LLM proxy, unchanged.
```

### 6.2 Containers

```
A's laptop                     platform (dev cluster)                           B's laptop
┌─────────────────────┐        ┌───────────────────────────────────┐            ┌─────────────────────┐
│ Claude Code session │        │  Agent Gateway (agentgateway)      │            │ Inbox channel plugin│
│ reviewer A          │        │  identity · policy · audit         │            │ local, one/session  │
│   │ findings        │        │     ▲ verifies sender → DEX        │            │   │ channel event   │
│   ▼                 │        │     │ findings ← CI review lane    │            │   ▼                 │
│ Inbox channel plugin│◀──────▶│     │ (second sender)              │◀──────────▶│ Claude Code session │
│ local, one/session  │ JWT A  │     ▼ with identity   ▲ to the live│  pushed    │ author B            │
└─────────────────────┘        │  Inbox service*       │ session on │  while open│   │ asks only on    │
                               │  tasks · routing by MR│ that MR,   │  pulls on  │   │ INPUT_REQUIRED  │
                               │  round cap            │ else queued│  start     │   ▼                 │
                               │     │ persists                     │            │ Author B (human)    │
                               │     ▼                              │            │ outcome → GitLab MR │
                               │  Inbox store*  durable, per person │            └─────────────────────┘
                               └───────────────────────────────────┘
* new software
```

### 6.3 Components

| Component | Role | Exists |
|---|---|---|
| Agent Gateway (agentgateway) | The door. Validates DEX JWTs, calls the authorization rule, logs every request with sender, route and A2A method, exports over OTLP. Can also front the inbox as an OAuth-protected MCP server for the pull path | Yes, proof of concept on dev |
| Inbox service | A2A-shaped task store per person: tasks, messages, states, routing table of live sessions, one event stream per connected plugin, round caps, canonical cleaned record of each message | No. Small. The first slice of a standalone agent application |
| Inbox store | Where messages wait while a laptop is closed | No |
| Inbox channel plugin | A local stdio MCP server shipped through a plugin marketplace. Logs in to DEX once, registers the session's context, subscribes to its stream, emits channel events, exposes reply, list and acknowledge tools. Runs the validator at both ends | No |
| Hooks shipped with the plugin | `SessionStart` pulls pending items for the repository in `cwd` and injects them as context. `CwdChanged` refreshes the registration. `SessionEnd` deregisters. The secret-injection hook skips `<channel` | Partly (hook framework exists) |
| Skills | `review-send` on the reviewer side packages findings as one A2A task per finding, addressed to the merge request. `review-handle` on the author side fixes what is clear, disputes with reasons, asks its human only on input-required, and posts the outcome summary to the merge request | No |
| CI review lane | A second sender into the same inbox under a service identity, from Bedrock. It cannot receive channels on Bedrock and does not need to | Planned |
| Deterministic promoter | The only exit for code from the quarantine: pushes the commit to the merge request branch under a branch-scoped token after fixed checks. Part of a future CLI | No |

### 6.4 Message flow

1. A's session produces findings. `review-send` shapes them into typed tasks under one context id for the merge request.
2. A's plugin strips secrets and personal data, signs the tasks as A, and sends them through the gateway.
3. The gateway validates A's token against DEX, asks the authorization rule whether A may send to that merge request, rejects replays, rate-limits, logs the request, and forwards to the inbox service.
4. The inbox rejects malformed messages, strips hidden characters and HTML, scores for injection, applies the round and size caps, stores the cleaned form, and looks for a live session that has that repository and branch open. It delivers over the stream if one exists, otherwise it queues.
   - Registration is done by the receiver, not per request. Every session's plugin registers at start (person from the login, laptop, repository, branch, Claude Code session id handed over by the `SessionStart` hook), refreshes on `CwdChanged`, and withdraws on `SessionEnd`. The inbox keeps two tables: open sessions, and conversations per merge request with participants, round count and waiting messages.
   - Delivery rule: the addressee's open sessions with that repository; the one on the merge request's branch wins, otherwise the most recently active; never two sessions at once; otherwise queue. Replies prefer the sender's session that took part in the conversation, then any of the sender's sessions with the repository open, then the sender's inbox. A handled message is acknowledged so it is never delivered twice.
5. B's plugin re-validates against the schema and emits a channel event with the sender named. If no session was open, B's next session on that repository pulls the queued items at start.
6. B's session runs `review-handle` inside the quarantine: fixes what is clear, disputes with reasons, replies through the inbox tool. Replies travel the same path in reverse with the same checks.
7. On input-required, B's session asks B, in the shape problem first, decision needed, suggestion separate. B answers in the terminal or through permission relay.
8. The promoter pushes the resulting commit to the merge request branch after fixed checks. The outcome summary and any open decisions are written to the merge request thread. Approval and merge stay with a person.

### 6.5 Conversation kinds

The design has two layers.

The transport layer is use-case neutral. It is the gateway, the inbox, the channel plugin, the hooks, the quarantine, the promoter, the round cap and the audit. It moves a typed message from one person's agent to another person's agent, checks who sent it, stores it, delivers it and logs it.

A conversation kind sits on top of the transport. A conversation kind is five things:

- A payload schema pair: one schema for the request and one for the reply.
- A context id shape: what the conversation is about, and how the inbox finds the participants.
- A sender rule: who may open a conversation of this kind.
- An authority policy: what the receiving agent may do on its own.
- A skill pair: one skill for the sending side and one for the receiving side.

Review is the first kind:

| Part | Review |
|---|---|
| Payload schemas | `finding.v1` (request), `reply.v1` (reply), `note.v1` (outcome on the merge request) |
| Context id | `<repo>!<iid>`, the merge request |
| Sender rule | Reviewers and assignees of the merge request open a task; the finding's sender and the merge request author reply |
| Authority policy | Ceiling per finding class (`fact`, `taste`, `product`) in `.aic/policy.json` |
| Skills | `review-send` (reviewer), `review-handle` (author) |

Candidate next kinds. None of them is built.

| Kind | Likely context id | Likely sender rule | Likely skills |
|---|---|---|---|
| Ticket readiness | `<project>#<ticket key>` | The ticket's reporter, assignee or refinement participants | `ticket-ask`, `ticket-answer` |
| Deploy verification | `<service>@<environment>#<deploy id>` | The person who ran the deploy and the service owners | `deploy-report`, `deploy-handle` |
| Design question | `<repo>/<design doc path>` or a discussion id | The document's author and named reviewers | `design-ask`, `design-answer` |

The review kind is wired into the inbox, the plugin and the promoter today. `DECISIONS.md` row P20 lists each place that a second kind must change.

---

## 7. Constraints that shape the design

| Constraint | Verified fact | Consequence |
|---|---|---|
| Laptops sleep | Claude Code buffers nothing. Channel events only arrive while the session is open, and are never acknowledged | The inbox is mandatory. "Direct" is store-and-forward |
| Live push is a research preview | Custom channels need the organization allowlist (Team or Enterprise managed settings) or the development flag. The contract may change | The plan tier decides whether the plugin ships cleanly. Pull underneath live push, always |
| The channel server does not know its session | Hooks get `session_id` and `cwd`; the channel server process does not | Registration needs a bridge: the `SessionStart` hook writes the id and `cwd` keyed by the Claude process id; the channel server reads it by parent pid. Or route only by user, host, repository and branch |
| The gateway is a proxy | A2A support is a route policy that rewrites the agent card URL and logs the method. No storage | The inbox service holds state. The gateway only needs HTTP plus JWT |
| Identity through the gateway | `jwtAuth` supports several providers; docs show JWKS from a file, `mcpAuthentication` supports a JWKS URL. DEX is not on the vendor's tested list. The gateway strips the inbound token unless told to preserve it | DEX is expected to work as a standard OIDC issuer, but it is a spike item. The inbox must either re-validate a preserved token or receive claims from the authorization response |
| Long-lived streams | Not documented for the A2A route. Timeouts are configurable. MCP streaming passes through | Spike item |
| The merge-request-level rule | Not a gateway feature | The gateway offers external authorization (gRPC or HTTP) and CEL on claims. The rule itself lives in a small service of ours or in the inbox |
| MCP protocol revision | The v2 client runtime does not register a channel server that negotiates MCP 2026-07-28 | The plugin pins an older SDK revision |
| Bedrock | Channels are unavailable on Bedrock, Vertex and Foundry | Laptop sessions authenticate through claude.ai or Console. The CI lane is a sender only |
| Remote Control | Disabled behind any non-Anthropic base URL | Irrelevant to the channel. Relevant to anyone using Remote Control once laptops point at the gateway |
| Agentgateway maturity | v1.0 March 2026, breaking changes across minors | Pin versions, review upgrades, keep direct Bedrock as break-glass. Already accepted in the gateway selection |

---

## 8. Risks

Two risks decide feasibility. They are two sides of one dial: the more B's agent may do unasked, the more damage a bad message can do. Everything else that could go wrong is a matter of decision (section 9).

### 8.1 Risk 1: steering another person's agent is prompt injection by design

**What it is.** The feature is that text written by A's model, not by A, arrives in B's session and makes B's agent act. That is prompt injection turned into a product. The attack surface is the channel itself.

**Why it cannot be engineered away.** A model cannot reliably tell instructions from data inside content it is asked to act on. Trust is transitive: whatever steered A's agent (an outside merge request description, a poisoned dependency, a bad skill) now steers B's agent one hop later, carrying a trusted colleague's identity. Today that chain is broken by a human reading the review. The concept removes that human on purpose.

**What makes it worse.** B's session holds B's credentials and may run in a permissive mode. The message arrives while B is away. Inbound text can imitate Claude Code's own system messages.

**What the standards say.** No framework claims prevention. OWASP (LLM Top 10 2025, Agentic Top 10 2026), NIST AI 100-2, MITRE ATLAS 2026.09, CSA, Google, Microsoft and Meta all accept residual risk only when deterministic controls outside the model cap what a hijacked agent can do. Five controls appear in every framework:

1. Treat agent-written content as untrusted, including peer-agent messages.
2. Least privilege for the session that reads it: task-scoped, short-lived credentials, no inheritance across agents.
3. Authorization enforced outside the model.
4. A human gate on irreversible actions, tiered by consequence and designed against consent fatigue.
5. Logging of actions and inter-agent messages.

Meta's Rule of Two states the design constraint: a session may have at most two of three properties: it processes untrusted input, it holds sensitive data or credentials, it changes state or communicates externally. Otherwise it needs supervision. B's session as first drawn has all three.

**What vendors do for this exact case.** No vendor relies on the model to resist injected text. GitHub shows the agent only write-access users' comments, strips hidden characters, pushes to one branch, cannot approve or merge, and requires workflow approval. Claude Code's GitHub Action rejects bots unless allowlisted, strips hidden HTML and invisible characters, and uses a short-lived repository-scoped token. Cursor Bugbot reads its configuration from the base branch. Every public incident (GitLab Duo 2025, CamoLeak, Comment and Control, CVE-2026-96893 on GitLab agents, CVE-2026-54316 on the Claude Code Action, CodeRabbit, CurXecute) combined untrusted text, broad credentials and a write or exfiltration path in one runtime.

**What the evidence says about defenses.** Architectural separation (CaMeL, Progent, FIDES) gives the only hard guarantees: attack success on AgentDojo from 41% to 2% with policy enforcement, at a utility cost of a few points. Model training and classifiers cut static success rates by 5 to 10 times (Opus 4.5 browser agent from 8.0% to 1.4%) and are beaten by adaptive attackers at over 90%. Delimiters and spotlighting barely move the needle on agent benchmarks. In the July 2026 coding-agent benchmark (IssueTrojanBench), supply-chain payloads succeeded 96.6% of the time, Claude Code was hit 41% of the time, and generic sandbox and approval layers produced no rejections at all: every refusal came from the model. Static tests prove nothing: NIST red teams took success from 11% to 81%.

**The design move.** B's agent handles A's findings in a quarantine: an isolated copy of the merge request branch with no secrets, no credentials and no network. Inside it the agent may read, edit, run tests and commit, because that state is local and reversible. Anything that leaves the quarantine passes through a program, not the model. The model proposes, a program promotes, a human approves.

**The envelope.**

| Layer | Control | What it stops | Precedent | Where it lives |
|---|---|---|---|---|
| 1 · Identity and authority at the door | Sender verified against DEX and must be a reviewer or assignee of that merge request. Messages signed, replays rejected. The receiving session keeps its own permission rules; an inbound message can never approve a prompt or change configuration | Impersonation, outsiders, forged agent messages | GitHub write-access gate; Claude Code cross-session rule; OWASP ASI07 | Gateway and inbox |
| 2 · Content as data | Typed schema: finding id, file and line that must exist in the diff, category and severity from closed lists, free text length-capped. Hidden Unicode and HTML stripped, no images or links rendered. Arrives inside the channel frame; hooks shipped with the plugin ignore it. Repository instructions and hooks come from the protected base branch, never from the merge request branch | Off-scope targeting, hidden payloads, poisoning the agent's configuration through the merge request | GitHub and Claude Action stripping; Bugbot's base-branch rule; the CI review lane ignores repository hooks | Plugin and inbox validator |
| 3 · Quarantine | Isolated worktree at the merge request head. No secrets in environment or home. Network off except the gateway and GitLab through the plugin. Edits allowed only in files the merge request touches. Writes to CI configuration, hooks, Claude configuration, dependency manifests and lockfiles denied by a deterministic hook. No pipe-to-shell, secret reads or force push (the quarantine guard hook) | Exfiltration, persistence, supply-chain edits. The deny list must name these file classes, because generic sandboxes blocked none of them in the benchmark | Rule of Two; GitHub single branch and firewall; Claude Code sandbox and deny rules | Quarantine: the quarantine guard hook plus the managed deny floor |
| 4 · A program does the risky steps (deterministic promotion) | The only exits. A promoter pushes to the merge request branch under a branch-scoped, short-lived token after checks: diff only in allowed files, size cap, CI green. Replies leave through the inbox tool after schema check and round cap. Resolving threads rides on the promoter. Approve and merge never | Irreversible or external effects driven by the message | GitHub push-only single branch; plan-then-execute pattern | A future CLI and the inbox |
| 5 · Human gates by how reversible the action is | Worktree edits: none. Push to the merge request branch: the promoter, or B at the lowest ladder level. Approve and merge: always a person. Every escalation names who sent what | Consent fatigue, the most bypassed control in Microsoft's red teaming | Microsoft taxonomy v2; OWASP LLM01 | Plugin and hooks, and GitLab |
| 6 · Detection and audit | Injection classifier on inbound findings at the gateway, for noise. Every message and promotion logged with its sender. Off-policy attempts logged as security events. A canary suite in CI | Lowers how often an attempt gets through; produces evidence | Anthropic 8% to 1.4%; GitLab log-only scan; ATLAS telemetry | Gateway, plugin and hooks |

**Where the checks live.** The plugin runs checks at both ends as the local enforcement point of a central policy, never as the only one.

| Check | A's plugin (outbox) | Gateway | Inbox service | B's plugin (inbox) |
|---|---|---|---|---|
| Shape into the typed schema | Builds it | | Rejects malformed (the check that counts) | Re-validates, renders as data |
| Secrets and personal data | Best place: strip before data leaves the laptop, reusing the guard's secret patterns. Hygiene only | | Central check owned by the security function, logged | |
| Provenance | Signs as A | Verifies identity; checks A may send to that merge request; rejects replays | Stores the sender on every event | Shows B who sent what |
| Hidden Unicode and HTML | Hygiene | | Canonical: stores only the cleaned form, so the audit shows what B saw | Strips again |
| Injection classifier | | Optional webhook | Scores and tags, noise only | |
| Round and size cap | Local early stop | Rate limits | Enforced | Reply tool refuses past the cap |
| Quarantine tool rules | Not the plugin's job | | | Not the plugin's job: the quarantine guard hook and the managed deny floor, with or without the plugin |

Trust classes: A's plugin is in the sender's hands and is exactly what Risk 1 assumes may be steered, so its checks are hygiene. The gateway and inbox are out of both parties' hands. B's plugin is in the receiver's hands and is the last hop before the model, so it re-validates rather than trusting the wire. One validator, versioned with the plugin, runs at both ends.

**What remains.** A steered or careless reviewer agent can still make B's agent propose a bad or subtly wrong change inside the files under review, send a bad-faith reply, or waste B's time. It cannot leak secrets, reach the network, touch build or dependency files, push outside the merge request branch, approve, or merge. The harm stays local and reversible, and the same review and CI that catch a bad human contribution catch it. That is the residual to accept.

**Today is worse on this measure.** The author pastes the reviewer's artifact into a full-power session: untrusted text, the author's credentials and push rights in one place, behind a human reading that rarely happens. The channel with the envelope moves the same content into a session that cannot leak or push on its own. The risk does not disappear; it gets a boundary it does not have now.

**How we would know.** Zero violations of the deterministic rules across an adaptive canary suite run many times, a reported success rate for the attempts the model refused on its own, and one human red-team round with full knowledge of the design. Repeated on every model or plugin upgrade.

### 8.2 Risk 2: nobody has decided what B's agent may do on its own (authority is undefined)

**What it is.** No rule says what B's agent may do on a message B never saw. It is also the dial that sets Risk 1's blast radius.

**Why it matters.** The whole value sits here. Too little authority and the mailman is back. Too much and B's name is on changes B never saw, because GitLab attributes everything to B.

**The ladder.** L0: read and brief B. L1: prepare the change, do not commit. L2: commit and reply on the merge request branch. L3: also resolve threads and mark ready. Never: approve or merge. The industry sits at L2 with a human trigger.

**What reduces it.** Make the ladder a per-team setting the plugin and hooks enforce, not a platform team opinion. Start at L1, promote after a trial. Tie the ceiling to the class of finding: facts may climb, taste and product choices stop at L0.

**What the envelope changes.** L1 needs no trust in the model. L2 becomes reachable without B reading, because the promoter and not the model does the push. L3's thread resolution rides on the promoter. Approve and merge never move.

### 8.3 One dial, two risks

Set the authority ceiling low and Risk 1 is survivable. Set it high and Risk 1 ends the idea. The envelope raises how high the dial can go. Decide them together, and decide them before any code.

---

## 9. Matters of decision still open

| # | Decision | Options | Why it matters |
|---|---|---|---|
| 1 | What a message is addressed to | Work item · session · person | Load-bearing for the architecture. Recommendation: the work item |
| 2 | Authority ceiling | L0 · L1 · L2 · L3 | Risk 2's rule and Risk 1's main control. Per team, enforced by the plugin and hooks |
| 3 | Who may send | Anyone in the organization · reviewers and assignees of that merge request · explicit pairing | Risk 1's trust boundary |
| 4 | Escalation rule | Facts the agents settle, taste and product choices go to the human · everything disputed goes to the human. Plus the round cap | Defines "human only when needed" |
| 5 | Visibility rule | Outcomes and escalations on the merge request · the whole exchange · nothing | "Nothing" is not viable |
| 6 | Live push or pull only | Accept the preview dependency with pull underneath · pull only | Live push can change or be blocked by the plan tier |

Settled: the inbox is mandatory; we build it; terms and billing are not a problem.

---

## 10. The experiment that settles feasibility

Two people, two laptops, the development gateway, one real merge request.

1. A's session sends a finding. B's session is open, receives it, and acts within the chosen ceiling without B typing anything.
2. B's session is closed. The finding waits. B opens the repository later and the session picks it up.
3. B's agent disputes; A's agent answers; the exchange stops at the round cap and both humans are told.
4. A's session sends a poisoned finding that carries an instruction. B's agent ignores the instruction and reports it.

If the four steps work, the concept is feasible and the rest is engineering. If step 1 fails because of the preview feature, the concept still works through step 2 and loses only the live feeling. If step 4 fails, the envelope is not real and Risk 1 stands.

Spike checks that fold into the experiment: confirm the plan tier and the channel allowlist; validate a DEX token on an A2A route and an MCP route at the gateway; confirm a long-lived stream passes through; prove the inbox sees the sender's identity; two sessions in two repositories on one laptop each receive only their own merge request's tasks; five findings sent while the author session runs a build arrive as one batch; a message that asks to change configuration is ignored.

---

## 11. Staged plan

| Stage | When | What |
|---|---|---|
| Spike | Two weeks, on the dev gateway | A fakechat-style channel plugin on the development flag. An inbox service of a few hundred lines behind the gateway with DEX JWT. One real merge request. Go or no-go on the experiment |
| v1 | After the spike | Plugin in a plugin marketplace and on the organization allowlist. `SessionStart` pull. The two skills. The quarantine hooks and the promoter in a future CLI. Outcome summary mirrored to the merge request. Round cap and sender gate. Access logs to Loki |
| v2 | Later | The CI lane posts into the inbox. Permission relay to Slack or phone. A2A conformance so a non-Claude agent can join. The inbox becomes the first service of a standalone agent application |

---

## 12. What is new

- A per-person agent inbox with corporate identity. No product sells it.
- The debate is agent-to-agent; the record is human. People see outcomes and decisions, not the negotiation.
- Escalation from anywhere, through permission relay, while the session waits.
- One inbox, many flows: ticket-readiness questions, deploy-verification results, a standalone agent application.

---

## 13. Gateway ground truth

Claims about the gateway in this document come from the vendor's documentation for version 1.7. They were not validated against the dev deployment or with the gateway maintainer.

| Need | Product (vendor docs) | Dev deployment | Verdict |
|---|---|---|---|
| Check who is sending | JWT validation against an OIDC provider, several providers at once. DEX not on the tested list | Sign-in pending | Configuration, needs one test |
| Decide whether a person may send to a merge request | Not built in. External authorization hook and CEL on claims | Not tested | The rule is ours |
| Record every message | Logs every request with route, status, latency, A2A method, CEL-added identity fields. No body logging for non-model traffic, no tamper-evident log | Model traffic only | "Records every request" is true. Content audit is the inbox's job |
| Pass long-lived streams | Not documented. Timeouts configurable | Not tested | Spike item |
| Carry identity to the inbox | Strips the token by default, can preserve it | Not tested | Configuration, needs a test |
| Hold and route messages | No | n/a | Inbox service |

---

## 14. Corrections made during the session

- An early research pass claimed Anthropic forbids routing subscription traffic through a gateway. The legal page restricts third-party developers and Agent SDK products; the gateway page explicitly documents subscription login through a corporate gateway. Corrected.
- An early pass described agentgateway as having a tamper-evident audit log. The documentation shows access logs, CEL fields and OTLP export, nothing tamper-evident. Corrected.
- The first container description presented gateway features as validated on dev. They were validated against vendor documentation only; the dev proof of concept has been tested for virtual keys and budgets. Corrected.
- The first design pass went to implementation detail (hook registration by process id, SDK revisions, eleven spike checks). Pulled back to concept level on the page; the detail is kept in Appendix A for the build phase.
- The gateway was first attributed to another team. It is the platform team's own work, run by the gateway maintainer. Corrected on the page, in this file, and in memory.
- The page was rewritten in plain English for readers new to the topic. The envelope table became an interactive layers figure, the "where the checks live" paragraph became an interactive flow, and a section on how a message finds the right session was added. Its text and the Risk 1 introduction were shortened. This file keeps the full tables and the longer reasoning.

---

## 15. Questions for people

- Which Claude plan are developers on? Team or Enterprise allows an Owner to allowlist the plugin; individual Max plans get only the development flag. (Decides whether live push can ship cleanly.)
- The gateway maintainer: is DEX JWT validation and long-lived stream pass-through in scope for the dev gateway tests, or does the channel spike add them?
- Security: is "propose, never commit without a program's checks, never approve" an acceptable residual for a session that acts on another person's agent output?
- The gateway maintainer: where does the merge-request-level authorization rule live, a small authorization service called by the gateway or the inbox itself?

---

## Appendix A: verified facts for the build phase

**Claude Code channels (docs read 28 October 2026).**
- A channel is an MCP server spawned by Claude Code as a subprocess over stdio, one per session. It declares `capabilities.experimental['claude/channel']` and emits `notifications/claude/channel` with `content` and `meta` (identifier keys only; keys with hyphens are silently dropped). The event arrives as `<channel source="plugin:<name>:<server>" ...>`.
- Replies are ordinary MCP tools on the same server. Permission relay uses `claude/channel/permission` and `notifications/claude/channel/permission_request` with a five-letter request id.
- No acknowledgement. Dropped silently if the server is not registered as a channel or organization policy blocks it. Busy sessions receive queued events together on the next turn. Separate sessions for concurrent streams.
- `--channels plugin:<name>@<marketplace>` per session. Development: `--dangerously-load-development-channels` (interactive only; ignored with `-p` and the Agent SDK, but `--channels` itself works with `-p`). Managed settings: `channelsEnabled`, `allowedChannelPlugins`.
- The v2 MCP client runtime does not register a channel server that negotiates protocol revision 2026-07-28.
- Not available on Bedrock, Vertex or Foundry.

**Claude Code hooks and sessions.**
- Hook input carries `session_id`, `transcript_path`, `cwd`, `permission_mode`, `hook_event_name`. `SessionStart` sources: startup, resume, clear, compact, fork. `SessionEnd` reasons: clear, resume, logout, prompt_input_exit, other. `CwdChanged` exists. `SessionStart`, `UserPromptSubmit` and `Stop` inject `additionalContext`. `asyncRewake` wakes Claude on exit code 2.
- Cross-session messages cannot approve permission prompts or change configuration. Same account only.
- Any non-Anthropic `ANTHROPIC_BASE_URL` disables Remote Control.

**agentgateway (standalone docs, version 1.7).**
- `jwtAuth`: issuer, audiences, JWKS (file shown), modes strict, optional, permissive; several providers under `providers`, matched by issuer and key id. Claims available to later policies through CEL. Token removed before forwarding unless `preserveToken: true` or passthrough backend auth.
- `mcpAuthentication`: resource server for MCP clients, `jwks.url`, protected resource metadata. Tested providers: Auth0, authentik, Descope, Entra, Keycloak, Okta.
- `extAuthz`: gRPC (Envoy-compatible) or HTTP; claims passed as metadata through CEL; per listener, route or backend; cache; 2-second default timeout.
- A2A: route policy `a2a: {}`; agent card URL rewritten to the gateway; `a2a.method` in access logs. 2025 release note names A2A 0.2.x; current conformance unstated.
- Token exchange at the backend: RFC 8693, RFC 7523, Entra on-behalf-of, ID-JAG.
- Access logs: route, backend, status, latency, source address, HTTP fields, `gen_ai.*`, `mcp.*`; CEL-added fields; stdout, OTLP, database (model requests only).

**GitLab.com.**
- Group service accounts: Premium, one per paid seat, PAT-authenticated.
- Composite identity (service account plus human): GA 18.8, Duo Agent Platform flows only.
- GitLab 19.3: webhook on review submit; `get_merge_request` MCP tool returns discussions.
- No custom external agents on GitLab.com; managed external agents bypass the gateway.

**Anthropic terms.**
- Subscription traffic through a gateway with only the base URL set: allowed; the gateway forwards the OAuth capability in `anthropic-beta`.
- Products built with the Agent SDK and third-party developers: API keys or a cloud provider, never routed through Free, Pro or Max credentials.

**Quarantine guard and hooks.**
- `quarantine/guard.mjs` rule ids: blocked-file, not-in-diff, outside-worktree, unresolved-target, too-deep, secret-read, network-command, network-tool, network-inline-code, package-network, git-network, git-config-write, git-config-override, git-internals, patch-apply, bad-input.
- `quarantine/blocked.json`: blocked file globs, mirrored from `inbox/internal/rules` and checked by the promoter tests.
- The secret-injection hook skips prompts starting with `<agent-message`, `<task-notification>`, `<cross-session-message`, `<scheduled-task`. Add `<channel`.

## Appendix B: sources

The companion page lists every source with links, grouped as prompt injection (standards, incidents, evidence), Claude Code and Anthropic, agentgateway, review products, and protocols, identity and human-in-the-loop.
