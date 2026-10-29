| Open point | Choice | Why |
|---|---|---|
| Package layout | `internal/auth`, `internal/mr`, `internal/store`, `internal/route` (pure session pick), `internal/api` (HTTP, SSE, dispatch), `internal/logx` (JSON log lines) | `route` stays a pure function that is easy to test; the HTTP layer owns liveness |
| `AIC_ISSUERS` format | JSON array `[{"issuer","audiences":[…],"jwks_file"|"jwks_url"}]`. It replaces the default list. Default: mock (`$AIC_WORK/keys/jwks.json`, aud `aic-inbox`) and DEX (`http://localhost:5556/dex/keys`, aud `aic-cli`) | One env var covers both kinds of issuer and the audience list |
| JWKS loading | Lazy. A cached set reloads on an unknown `kid`. A failed or unknown-kid reload is retried at most every 10 s for a URL and on every miss for a file | DEX down only fails DEX tokens. The rate limit stops unknown kids from hammering DEX. The file reload picks up a re-mint without a restart |
| Accepted JWT algorithms | RS256 only; `exp` required, `nbf` optional, 60 s skew on both; `aud` string or array | Stops `alg: none` and HS256 key confusion |
| Email case | The `email` claim and the MR file's emails are lower-cased before any comparison | Sender gate must not fail on `A@example.com` |
| Missing / bad token | `401 unauthorized` with `WWW-Authenticate: Bearer`; logged as WARN `"event":"auth_failed"` with the reason, never the token | Contract fixes only the 4xx body shape |
| Status codes the contract leaves open | 400 rule errors, 401 unauthorized, 403 `sender_not_allowed` / `not_addressee` / `not_member` / `session_not_owned`, 404 `mr_not_found` / `task_not_found` / `session_not_found` / `message_not_found`, 409 `duplicate_task` / `round_limit`, 413 `too_large`, 422 `file_not_in_diff` / `blocked_file` | 422 separates "well-formed but not allowed on this MR" from malformed input |
| Order of finding checks | validate → MR exists → sender gate → blocked file → file in diff → duplicate task | Blocked file wins over diff: a finding on `package.json` in the diff still reports `blocked_file` |
| MR lookup | `context_id` repo must equal the MR file's `repo`, else `404 mr_not_found` | Stops a context id from borrowing another repo's MR |
| Bare repo per repo | `git diff` runs on `$AIC_WORK/<repo basename>.git` when it exists, else `$AIC_WORK/origin.git` | setup.sh's `shop.git` link resolves to origin; a second repo (spike check 6) is a second bare repo beside it |
| Session sending a message | Optional request header `Aic-Session-Id` on `POST /v1/messages`. Honoured only for a session the caller owns | Contract reply routing needs "the session that previously sent"; the body shapes have no session field and must stay unchanged |
| Reply routing memory | Each task keeps `sessions[user] = session_id`, set on send (with the header) and on ack | Implements "prefer the addressee's session that previously sent or acked in this task" |
| Delivery tie-break | Preferred session → branch == MR source branch → latest `last_active` → lowest session id | Deterministic choice |
| `last_active` | Updated on PUT, stream open, send with header, ack | "Most recently active" needs a definition |
| When queued messages are dispatched | On a new message, on stream open and on session PUT, for all queued messages of that user. Not on stream close | A stream blip (plugin reconnects after 1 s) must not move a finding from the quarantine session to a normal session |
| Stream replaced | A second stream for the same session id replaces the first. The old stream's unacked pushes go back to queued and come out on the new one | Plugin reconnect may race the old connection's close. At-least-once delivery; consumers dedupe by `message_id` |
| Stream close vs successor | Close-side requeue runs under the store lock and only if this stream is still the live one | Otherwise a late close could requeue messages already pushed to the successor |
| Stream buffer | 256 envelopes per stream; a full buffer leaves the message `queued` | The push never blocks the store lock |
| SSE frames | `event: message`, `id: <message_id>`, `data: <envelope>`; `: open` comment once registered; `: ping` every 15 s | `id` lets a client dedupe; `: open` tells the client the session is live |
| Restart | On start every `pushed` message becomes `queued` | No stream survives a restart |
| Ack of a `queued` message | Allowed; `queued` and `pushed` both become `acked`. A second ack is a no-op `204` | The SessionStart hook fetches and acks queued messages without a stream (contract 9) |
| Ack body `session_id` | Required; must be a registered session of the caller (`404` / `403` otherwise) | It drives reply routing; a foreign session id would misroute |
| Task state on ack | Acking the round-1 finding moves the task `submitted` → `working` | A2A: the addressee picked it up |
| Replies after a terminal state | Allowed after `completed`, `canceled`, `input-required`; refused after `failed` with `409 round_limit` | `fixed` then `accepted` is a normal two-step close; only the round cap stops a task |
| Round cap system envelope | Two envelopes, `from: aic-inbox`, one to each participant, payload a `reply.v1` with `kind: escalated`, `round` = the refused round (6). Task stays `failed`, not `input-required` | `round` must be ≥ 1 and shows which round was refused; the contract says the task becomes `failed` |
| Round cap persistence | The refusal is a 409, but the task state, system envelopes and MR note are saved | The cap must be visible to both sides and to the MR |
| Self-finding | A finding from the MR author (who is also an assignee) is allowed and addressed to the author | Contract allows reviewers or assignees; no rule forbids it. Flag for review |
| Notes sender gate | MR author, reviewers, assignees, and `AIC_SERVICE_USERS` (default `promoter@aic.local`). Others `403 sender_not_allowed`, logged `sender_refused` | Contract 10 has the promoter post notes with its own identity |
| Notes body | Cleaned `note.v1` body (rules package) appended as `---`, `**<sender>** · <ts>`, body. `201 {"context_id","posted_at"}` | Contract says only `201`; the body echoes what happened |
| Context read access | MR members and service users; others `403 not_member` and a WARN `read_refused` | A conversation holds findings that c@ must not read |
| Context view | `{context_id, participants, tasks:[{task_id,state,round,class,sender,addressee,created_at,updated_at}], messages:[envelope]}`; participants = users who sent or received a message | Contract names the parts but not the shape |
| `GET /v1/inbox` `branch` | Accepted and ignored | The contract filters by repo only; branch preference applies to push, not to pull |
| Session id format | `^[A-Za-z0-9._:-]{1,128}$` | Claude session ids are UUIDs; anything else is rejected before it reaches a file or a log |
| Session registration fields | `repo` required; `host`, `branch`, `cwd` optional; each ≤ 1024; unknown fields `400 unknown_field` | Same strictness as the message schemas |
| Request log | One line per request: `ts, level, intLevel, event, method, status, sender, session_id?, context_id?, task_id?, message_id?, outcome (ok/refused/error), error_code?`. INFO for 2xx, WARN for 4xx, ERROR for 5xx. Security refusals add a separate WARN line with `"security":true` | Contract field list plus status for debugging |
| `message_id` | `m-` + 10 Crockford base32 chars of Unix ms + 16 random base32 chars | "ulid-ish": sorts by time, matches `^m-[0-9A-Za-z]+$` |
| `aic-mint --out` | A token directory, default `<work>/tokens` | `tokens/<user>.jwt` is the contract path |
| `aic-mint --expired` | Writes `<user>.expired.jwt` (iat −2 h, exp −1 h) so it never overwrites a working token | Spike check 8 needs both |
| `aic-mint` key reuse | Keeps `keys/signing.pem` and `keys/jwks.json` when present; writes the JWKS only for a new key or a missing file | Re-minting tokens must not change the JWKS under a running gateway |
| Mock token claims | `iss`, `aud` (string `aic-inbox`), `sub` = email, `email`, `name` = local part, `iat`, `exp`. Token files end with a newline | Contract 3 claim list. Readers must trim |
| Store file | `$AIC_WORK/inbox/store.json`, mode 0600 via temp file + fsync + rename in the same directory | Atomic on POSIX |
| `rules` package | Imported unchanged; no bug found | — |
