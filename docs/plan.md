# Implementation plan

Scope: PoC plan days 1–7, fully local, no GitLab (DECISIONS D1, D2). Interface: `docs/contract.md`. Decisions: `DECISIONS.md` plus `docs/decisions/<stream>.md`.

## Build streams

```
Foundation (sequential)
  schema/*.json + vectors.json · inbox/internal/rules · sandbox/setup.sh · Makefile
        │
        ├── inbox       Go service, aic-mint, openapi.yaml           (PoC days 1, 3)
        ├── plugin      channel server, hooks, DEX PKCE login         (days 1, 2, 7)
        ├── gateway     agentgateway + DEX config, compose            (days 3, 7)
        ├── quarantine  worktree script, guard hook, aic-promote      (day 4)
        └── skills      review-send, review-handle, canaries, runbook (days 2, 5, 6)
        │
Review (3 lenses in parallel): integration seams · security envelope · code correctness
        │
Fix: apply confirmed findings, make build + make test green, commit
```

The foundation runs first because the two validators and the promoter share its rule set and test vectors. The five streams own disjoint folders, so they run in parallel without worktrees.

## What each PoC question maps to

| PoC question | Built by | Evidence when run |
|---|---|---|
| Live delivery (step 1) | plugin, inbox | B's quarantine session shows the finding within seconds |
| Closed session (step 2, spike 2) | hooks, inbox queue | `SessionStart` context contains the finding; message state `acked` |
| Dispute and round limit (step 3, spike 11) | inbox round cap, skills | 6th message `409 round_limit`, MR notes file says so |
| Poisoned finding (step 4, canaries) | rules, guard, promoter, skills | `guard.jsonl`, `promoter.jsonl`, origin branch sha |
| Gateway identity and streams (spikes 7–9) | gateway, inbox auth | gateway access log, inbox log `sender` field |
| Sender gate (spike 10) | inbox | `403 sender_not_allowed` security event |
| Session identity (spike 6) | hooks bridge, inbox routing | two registrations, each receives only its own |
| DEX (spike 8, day 7) | dex, login.mjs, gateway | gateway accepts DEX token, rejects expired mock token |

## Out of scope

Day 8 topology C (runbook section only), GitLab, rate limiting, injection classifier beyond a keyword score, the CI review lane, permission relay.
