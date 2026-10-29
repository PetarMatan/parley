# Agent inbox channel (aic)

This project is a local proof of concept of an agent-to-agent code review channel. A reviewer's coding agent sends structured findings on a merge request. The author's coding agent receives them in its running session, replies, and proposes patches. A quarantine and a promoter control what reaches the repository.

All parts run on one machine. An inbox service stores messages and streams them to subscribed sessions. A gateway checks every bearer token before a request reaches the inbox. A local OIDC provider stands in for the organization's login. A sandbox script builds a bare origin, two clones and a fake merge request.

## Folders

- `inbox/`: Go inbox service, token minting tool and promoter (standard library only).
- `plugin/`: Node 22 channel plugin: channel server, hooks and OIDC login.
- `quarantine/`: quarantine worktree script and the tool-use guard.
- `canaries/`: adversarial message cases and their runner.
- `schema/`: message schemas and generated test vectors.
- `skills/`: skills for sending and handling reviews.
- `gateway/`: agentgateway config and its validation script.
- `dex/`: config of the local stand-in OIDC provider (DEX).
- `sandbox/`: scripts that build and reset the work directory.
- `docs/`: plan, contract, conventions, runbook and per-stream decisions.

## Run

1. `make setup` builds the sandbox under `work/`.
2. `make up` starts the gateway and the local OIDC provider with docker compose.
3. `make test` runs the Go and Node tests.

The full procedure is in `docs/runbook.md`.

## Identity providers

The login uses standard OIDC discovery, the authorization code flow and PKCE, so any standard OIDC provider works. The sandbox uses DEX in docker compose as the local provider. To use another provider, follow "Using another identity provider" in `docs/runbook.md` section 8.

## Decisions

The decision log is `DECISIONS.md`.
