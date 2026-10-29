# Project conventions adopted from the original environment

## Logging
- Every JSON log line carries `level` (string) and `intLevel` (integer). Map: FATAL=6, ERROR=5, WARN=4, INFO=3, DEBUG=2, TRACE=1. The field name is `intLevel`, not `level_value`.
- Optional format for entity references in logs: `[<domain>.]<type>:<ID>`, e.g. `com.example.room:1337`.

## Dates
- All instants, dates and calendar weeks in HTTP bodies, SSE event payloads and message schemas use ISO 8601 with a zone. `2007-04-05T14:30Z` and `...T12:30-02:00` pass. Zone-less local time such as `2007-04-05T14:30` fails.

## Authentication
- Scope: all applications under the organization's control.
- Client that cannot keep a secret (browser, local CLI on a user machine): OIDC authorization code flow with PKCE. The local CLI to DEX falls here.
- Server-side client that can keep a secret: code flow with client secret (preferred) or PKCE.
- Resource Owner Password Credentials grant is disallowed. Do not use DEX password grant from the CLI.
- The convention does not cover service-to-service or token-exchange flows (gateway to inbox, MCP to inbox). No rule exists there.

## HTTP API
- Scope of the API guideline: any HTTP API that is not purely internal. Internal APIs are "strongly suggested" to comply. New in-scope APIs must comply from design onward.
- Scope of the spec rules below: HTTP and Kafka APIs that are not domain-internal.
- HTTP API spec: OpenAPI 3.1+ file, in git, in the same repo as the service, versioned with the service.
- README of the service links to the spec file.
- Async API spec (Kafka): AsyncAPI 3.x. SSE is not named explicitly; AsyncAPI 3 is the closest fit for the event stream. Inference, not a rule.
- The actual API guideline rules are not available here. Path casing, pluralisation and error-body format are therefore unchecked. Gap. The error body `{"error":{"code":"snake_case","message":"…"}}` is this project's own choice (`docs/contract.md`).

## PII in schemas
- Every JSON Schema property may carry `"pii-classification"` with one of `no-pii` (default when absent), `indirect-identifier`, `direct-identifier`.
- The annotation lives in the schema only, never in the payload.
- The schema validator must allow this custom keyword (no strict-mode rejection).
- The OpenAPI variant `x-pii-classification` is unsettled; not a convention.

## Gaps
- The API guideline body (paths, error format, pagination) is not available here.
- No convention covers SSE, MCP, JWT claim layout, or gateway auth.
