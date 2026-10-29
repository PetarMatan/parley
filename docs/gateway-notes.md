# Gateway and DEX notes

Covers `gateway/config.yaml`, `dex/config.yaml` and `docker-compose.yml`, and how spike checks 7, 8 and 9 run against them. Nothing here was run against a live inbox. Every command below is for the PoC run, not for the build.

## Topology

```
host                                      docker network "aic_default"
─────────────────────────────────────     ──────────────────────────────────────
plugin / curl ──► localhost:3000 ───────► agentgateway :3000
                                            │ jwtAuth strict, preserveToken
                                            │ mock JWKS: file /work/keys/jwks.json
                                            │ DEX JWKS:  http://dex:5556/dex/keys ──► dex :5556
aic-inbox 127.0.0.1:7070 ◄── host.docker.internal:7070
login.mjs / browser ──► localhost:5556 ─────────────────────────────────────► dex :5556
aic-inbox ──► localhost:5556/dex/keys (re-validation) ──────────────────────► dex :5556
```

| Item | Value |
|---|---|
| agentgateway image | `cr.agentgateway.dev/agentgateway:v1.7.0` (digest `sha256:60216aaf…c45b`, source revision `26f5aebe`) |
| Local OIDC provider (DEX) image | `ghcr.io/dexidp/dex:v2.46.1` (released 2026-10-21) |
| Published ports | `127.0.0.1:3000` (gateway), `127.0.0.1:5556` (local OIDC provider, DEX). Loopback only |
| Unauthenticated route | `GET /healthz`, exact match. Every other path needs a valid token |
| Mock provider | issuer `https://aic.local/mock`, audience `aic-inbox`, JWKS file `/work/keys/jwks.json` = `$AIC_WORK/keys/jwks.json` |
| Local OIDC provider (DEX) | issuer `http://localhost:5556/dex`, audience `aic-cli`, JWKS URL `http://dex:5556/dex/keys` |
| Local OIDC provider (DEX) users | `a@example.com`, `b@example.com`, `c@example.com` |
| Local OIDC provider (DEX) password | `aic-poc-only` for all three. **PoC only.** It is in this repository, so it protects nothing |
| Local OIDC provider (DEX) client | `aic-cli`, public, redirect `http://127.0.0.1:5557/callback`, PKCE S256 enforced, grants `authorization_code` and `refresh_token` only |

## Start order and restarts

1. `make setup`. This writes `$AIC_WORK/keys/jwks.json` and the mock tokens.
2. Start the inbox on the host: `$AIC_WORK/bin/aic-inbox` (listens on `127.0.0.1:7070`).
3. `make up`. Compose starts DEX, waits until `/dex/keys` answers, then starts the gateway.

The gateway loads both JWKS while it loads its config. If the DEX fetch fails, the gateway exits. Compose prevents this with `depends_on: condition: service_healthy`.

Restart the gateway after any of these events, because it may keep the JWKS it loaded at start:

- `AIC_REMINT=1 make setup` wrote a new mock key: `docker compose restart agentgateway`.
- DEX was recreated (`docker compose up -d --force-recreate dex`, or `make down`), so its sqlite store and signing key are new: `docker compose restart agentgateway`.

`AIC_WORK` must be under a folder that Docker Desktop shares (`/Users/...`). A path under `/private/tmp` mounts as an empty folder, and the gateway then fails with `failed to load JWKS: read resource file /work/keys/jwks.json`.

Validate both configs without starting the PoC: `bash gateway/validate.sh`.

## Shared shell setup for the checks

```bash
cd "$HOME/Desktop/ai-development/agentic-conversation"
export AIC_WORK="$PWD/work"
G=http://localhost:3000
TA="$(cat "$AIC_WORK/tokens/a@example.com.jwt")"
TB="$(cat "$AIC_WORK/tokens/b@example.com.jwt")"
# gateway access log, one JSON object per line
docker compose logs -f --no-log-prefix agentgateway
```

The gateway adds these fields to every access log line: `sender` (`jwt.email`), `sub` (`jwt.sub`), `iss` (`jwt.iss`) and `intLevel` (`3`). A field is absent when the request had no validated token.

## Spike check 7: event stream through the gateway

What makes it pass in this config:

- `timeout.requestTimeout` ends when response headers arrive. It does not limit an SSE body. It is set to `24h` in case the inbox sends the stream headers late.
- `timeout.responseIdleTimeout` is not set. Unset means disabled. This is the only agentgateway timeout that limits gaps in a response body.
- The inbox sends `: ping` every 15 s, so the stream is never idle for long.
- `frontendPolicies.http.http1IdleTimeout` is `2h` (default `10m`). It applies to idle keep-alive connections.

Commands (three terminals):

```bash
# terminal 1: register B's session and hold the stream open through the gateway
SID="spike7-$(date +%s)"; echo "$SID"
curl -sS -X PUT "$G/v1/sessions/$SID" \
  -H "Authorization: Bearer $TB" -H 'content-type: application/json' \
  -d "{\"host\":\"$(hostname)\",\"repo\":\"local/shop\",\"branch\":\"feature/discount\",\"cwd\":\"$AIC_WORK/clone-b\"}"
curl -sN "$G/v1/sessions/$SID/events" -H "Authorization: Bearer $TB" \
  | while IFS= read -r l; do printf '%s %s\n' "$(date -u +%FT%TZ)" "$l"; done

# terminal 2: wait at least 10 minutes, then send A's finding through the gateway
sleep 660
curl -sS -X POST "$G/v1/messages" \
  -H "Authorization: Bearer $TA" -H 'content-type: application/json' \
  -d '{"schema":"finding.v1","context_id":"local/shop!1","task_id":"f-0000a007",
       "file":"src/discount.js","line":1,"category":"bug","severity":"major","class":"fact",
       "title":"spike 7: stream survives 10 idle minutes","note":"Sent after a 10 minute wait."}'
```

Pass: the reply says `"delivered":"pushed"`, and terminal 1 prints `event: message` and the envelope within seconds. The ping lines show the stream stayed open for the whole wait.

Fail and retry: if terminal 1 ends before the send, note the time it ended. Set `responseIdleTimeout: 0s` explicitly, or raise `http1IdleTimeout`, then repeat. The access log line for the stream may appear only when the stream closes.

## Spike check 8: DEX token at the gateway

```bash
# 1. real login as b (browser opens; user b@example.com, password aic-poc-only)
node plugin/login.mjs --checkout "$AIC_WORK/clone-b"
TD="$(cat "$(node -e 'console.log(JSON.parse(require("fs").readFileSync(process.argv[1])).token_file)' "$AIC_WORK/clone-b/.aic/identity.json")")"

# 2. the gateway accepts the DEX token
curl -sS -o /dev/null -w '%{http_code}\n' "$G/v1/inbox?repo=local/shop&branch=feature/discount" -H "Authorization: Bearer $TD"
#    expect 200 (the gateway passed it and the inbox accepted it)

# 3. the gateway rejects an expired mock token
"$AIC_WORK/bin/aic-mint" --work "$AIC_WORK" --users b@example.com --expired
curl -sS -i "$G/v1/inbox?repo=local/shop" -H "Authorization: Bearer $(cat "$AIC_WORK/tokens/b@example.com.expired.jwt")" | head -1
#    expect HTTP 401 from the gateway; the inbox log has no line for this request

# 4. the gateway rejects no token and a wrong audience
curl -sS -o /dev/null -w '%{http_code}\n' "$G/v1/inbox?repo=local/shop"                       # expect 401
curl -sS -o /dev/null -w '%{http_code}\n' "$G/healthz"                                        # expect 200, no token needed
```

Pass: step 2 gives `200`, and the gateway access log line has `"iss":"http://localhost:5556/dex"` and `"sender":"b@example.com"`. Steps 3 and 4 give `401`, and those requests never reach the inbox log.

Check before the run: the inbox must accept DEX tokens with audience `aic-cli` from `http://localhost:5556/dex/keys` (contract section 3). Also check that DEX puts `email` into the ID token. `login.mjs` asks for scope `openid email profile`, so it should.

Expected 401 status: the vendor docs say `strict` rejects a missing or invalid token. They do not state the status code. Record what the gateway actually returns.

## Spike check 9: sender identity reaches the inbox

What makes it pass in this config: `jwtAuth.preserveToken: true`. The default is `false`, and then the gateway removes the `Authorization` header after it validates the token. The inbox re-validates the same token and takes `email` as the sender (DECISIONS D7). No identity header is added.

```bash
curl -sS -X POST "$G/v1/messages" \
  -H "Authorization: Bearer $TA" -H 'content-type: application/json' \
  -d '{"schema":"finding.v1","context_id":"local/shop!1","task_id":"f-0000a009",
       "file":"src/discount.js","line":1,"category":"bug","severity":"minor","class":"fact",
       "title":"spike 9: sender reaches the inbox","note":"Check the sender field."}'
grep '"f-0000a009"' "$AIC_WORK/logs/inbox.jsonl" | tail -1      # expect "sender":"a@example.com"
docker compose logs --no-log-prefix agentgateway | grep '/v1/messages' | tail -1   # expect "sender":"a@example.com"
```

Pass: the inbox log line and the gateway access log line both show `a@example.com`.

Negative control (optional, proves the token travels): set `preserveToken: false`, run `docker compose restart agentgateway`, and repeat. The gateway accepts the request, and the inbox answers `401` because the header is gone. Set it back to `true` afterwards.

## What was validated, and how

| What | How | Result |
|---|---|---|
| Field names in `gateway/config.yaml` | Compared against `schema/config.json` and `schema/config.md` from the agentgateway repository at the image's source revision `26f5aebe`, and against the vendor pages for JWT auth and timeouts | All fields exist. `binds` is deprecated, so the config uses `gateways` and `routes` |
| `gateway/config.yaml` as a whole | `agentgateway -f … --validate-only` in the pinned image, see `gateway/validate.sh` | `Configuration is valid!` |
| That `--validate-only` is strict | The same run with a misspelled field (`preserveTokn`), a bad enum (`strictt`) and a broken CEL expression | All three rejected with an error that names the field |
| DEX JWKS fetch during validation | `--validate-only` fetches the JWKS URL. The script answers it with a throwaway `nc` responder named `dex` on an internal network, which serves `gateway/testdata/jwks.json` | Passes. Without the responder, validation fails with `failed to load JWKS: fetch http://dex:5556/dex/keys` |
| Field names in `dex/config.yaml` | Compared against `cmd/dex/config.go`, `cmd/dex/serve.go` and `storage/storage.go` at tag `v2.46.1` | All fields exist. DEX's default grant list includes `password`, so the config sets `grantTypes` explicitly |
| `dex/config.yaml` as a whole | `dex serve --web-http-addr 256.0.0.1:1` with no network. DEX parses the config, opens storage, loads the client and the passwords, creates a key, then fails at listen | Accepted. DEX ignores unknown fields, so this check is weaker than the gateway check |
| bcrypt hashes | `htpasswd -vb` against `aic-poc-only` | All three match |
| `docker-compose.yml` | `docker compose config --quiet` | Valid |
| Container to host loopback | A container with `host.docker.internal:host-gateway` fetched from an `nc` listener on host `127.0.0.1:7071` | Works on this Docker Desktop |
| Gateway user reads a `0700` keys folder | `--validate-only` with the JWKS in a `0700` folder | Works on Docker Desktop (virtiofs). On Linux Docker, uid 65532 may be refused |

Not validated: anything at runtime. Token acceptance, the 401 status, SSE behaviour, access log field layout, and whether the gateway reloads a changed JWKS are all open until the PoC runs.
