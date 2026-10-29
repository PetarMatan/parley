#!/usr/bin/env bash
# Validate gateway/config.yaml and dex/config.yaml with the pinned images.
# Leaves nothing running. Needs Docker; the repo must be under a path Docker
# Desktop shares (e.g. /Users).
#
# agentgateway fetches the DEX JWKS URL while loading its config, even with
# --validate-only. A throwaway container answers that fetch on a private,
# internal network under the name "dex" with testdata/jwks.json. It is not DEX.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
AGW=cr.agentgateway.dev/agentgateway:v1.7.0
DEX=ghcr.io/dexidp/dex:v2.46.1
NET=aic-validate-$$
FIX=aic-jwks-fixture-$$

cleanup() {
  docker rm -f "$FIX" >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "== agentgateway --validate-only"
docker network create --internal "$NET" >/dev/null
docker run -d --name "$FIX" --network "$NET" --network-alias dex \
  -v "$ROOT/gateway/testdata:/srv:ro" --entrypoint sh "$DEX" \
  -c 'while true; do nc -l -p 5556 -e /srv/jwks-fixture.sh; done' >/dev/null
sleep 1
docker run --rm --network "$NET" \
  -v "$ROOT/gateway/config.yaml:/config/config.yaml:ro" \
  -v "$ROOT/gateway/testdata:/work/keys:ro" \
  "$AGW" -f /config/config.yaml --validate-only

# DEX has no validate-only mode. An unusable listen address makes it exit right
# after it has parsed the config, opened storage, loaded clients and passwords
# and created a signing key, and before it serves anything. DEX ignores unknown
# YAML fields, so this proves less than the gateway check.
echo "== dex serve (stops at listen)"
out="$(docker run --rm --network none \
  -v "$ROOT/dex/config.yaml:/etc/dex/aic.yaml:ro" \
  "$DEX" dex serve --web-http-addr 256.0.0.1:1 /etc/dex/aic.yaml 2>&1 || true)"
if grep -q 'listening (http) on 256.0.0.1:1' <<<"$out"; then
  echo "DEX config accepted (stopped at listen as intended)"
else
  echo "$out" | tail -5
  echo "DEX config rejected" >&2
  exit 1
fi

echo "== docker compose config"
docker compose -f "$ROOT/docker-compose.yml" config --quiet && echo "compose file valid"
