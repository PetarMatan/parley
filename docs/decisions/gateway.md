| Open point | Choice | Why |
|---|---|---|
| Config style for agentgateway v1.7 | Top-level `gateways` + `routes`, not `binds` | The v1.7.0 schema marks `binds` as deprecated. The vendor docs use `gateways` |
| Where `jwtAuth` sits | Route policy on the `/` route, not on the gateway | Contract section 6 exempts `/healthz` from the bearer token. A gateway-level policy would cover it too |
| `/healthz` through the gateway | Separate route, exact match, no `jwtAuth` | Same reason. Liveness checks need no token |
| Field that forwards the token | `jwtAuth.preserveToken: true` | Verified in the v1.7.0 schema (default `false`, "Keep a successfully validated JWT in its original location"). The inbox re-validates it (D7) |
| DEX issuer vs container hostname | Issuer stays `http://localhost:5556/dex`. The gateway fetches keys from `http://dex:5556/dex/keys` on the compose network | The gateway compares only the `iss` string. Host-side clients (login, inbox) use localhost, so a single issuer value works everywhere without `/etc/hosts` changes |
| Gateway startup depends on DEX | `depends_on: dex: condition: service_healthy`, health check `wget /dex/keys` | agentgateway fetches the JWKS URL while loading config and exits on failure (seen with `--validate-only`) |
| SSE timeouts | `requestTimeout` and `backendRequestTimeout` `24h`. `responseIdleTimeout` unset. `http1IdleTimeout` `2h` | Schema and vendor docs: only `responseIdleTimeout` limits gaps in a streamed body, and unset means disabled. The others stop at response headers. `24h` covers an inbox that delays stream headers |
| Access log identity fields | `frontendPolicies.accessLog.add`: `sender`=`jwt.email`, `sub`=`jwt.sub`, `iss`=`jwt.iss` | Contract says the sender is the `email` claim. `iss` shows which provider accepted the token (spike 8) |
| Log convention on gateway logs | `config.logging.format: json` and a constant `intLevel: "3"` on access log lines. The gateway's own `level` field and non-access log lines are not changed | agentgateway owns its log format. Access lines are INFO by nature. The YAML value `"3"` is the CEL integer literal `3`; that it renders as a JSON number is unchecked until the first run |
| Ports exposed on the host | `127.0.0.1:3000` and `127.0.0.1:5556` only | PoC topology A is one laptop. Topology C (day 8) must widen this |
| DEX image | `ghcr.io/dexidp/dex:v2.46.1` | Latest release (2026-10-21). Patch release on top of v2.46.0 |
| DEX storage | `sqlite3` at `/var/dex/dex.db` inside the container (no volume) | Survives a container restart, so the gateway's cached JWKS stays valid. Gone on recreate, which is fine for a PoC |
| DEX key rotation | `keysRotationPeriod: 720h` | The gateway loads the DEX JWKS at start. Whether it refreshes later is unknown; a long rotation removes that risk for the PoC |
| Password grant (PKCE convention) | `oauth2.grantTypes: [authorization_code, refresh_token]`, no `passwordConnector` | DEX v2.46.1's default grant list includes `password` and `implicit` (`cmd/dex/serve.go`). Leaving the list empty would enable them |
| PKCE | `oauth2.pkce.enforce: true`, `codeChallengeMethodsSupported: [S256]` | The PKCE convention makes PKCE mandatory for a public CLI client. DEX supports it by default but does not require it |
| DEX approval screen | `skipApprovalScreen: true` | One fewer click per login. Only one client exists |
| DEX token lifetime | ID token `1h` | Short enough to see expiry during the PoC, long enough for a spike run |
| DEX password | One password, `aic-poc-only`, for all three users, bcrypt cost 10 | PoC only. Documented in `docs/gateway-notes.md` |
| DEX userIDs | Fixed UUID-shaped strings | Stable `sub` values across DEX restarts |
| Config validation fixture | `gateway/testdata/jwks.json` (public key only, private key discarded) and `gateway/testdata/jwks-fixture.sh`, used by `gateway/validate.sh` | `--validate-only` must reach the DEX JWKS URL. A throwaway `nc` responder on an internal Docker network answers it, so DEX itself is never started |
| Mock JWKS location | `$AIC_WORK/keys/jwks.json` mounted at `/work/keys`, not `gateway/jwks/` | Contract section 2 names `$AIC_WORK/keys/jwks.json`. Contract section 1 still mentions `gateway/jwks/ (generated)`; nothing is generated there |
