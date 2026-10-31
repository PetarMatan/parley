# Cloud host bundle

The services a company would run for the channel, on one host under Docker Compose. Testbed plan: `parley-testbed.md`, section 3. Terraform and the host bootstrap live in `testbed/`.

## What runs

| Service | Image | Networks | Role |
|---|---|---|---|
| caddy | caddy:2.10.2 | edge | TLS from Let's Encrypt; the only published ports (80, 443) |
| keycloak, keycloak-db | quay.io/keycloak/keycloak:26.8.1, postgres:17.6 | edge | Identity provider, realm `parley` imported from `keycloak/realm-parley.json` |
| gitlab | gitlab/gitlab-ce:18.6.0-ce.0 | edge, services, ci | Git host; logs in through Keycloak; config in `gitlab/gitlab.rb` |
| gitlab-runner | gitlab/gitlab-runner:v18.6.0 | ci | CI runner, Docker executor; registered in phase 3 |
| agentgateway | cr.agentgateway.dev/agentgateway:v1.7.0 | edge, services | Validates bearer tokens; config in `gateway/config.yaml` |
| inbox | built from `inbox/Dockerfile` | services | The inbox service; reachable only through the gateway |

Names: `auth.parleylab.dev`, `gitlab.parleylab.dev`, `channel.parleylab.dev`. DNS records come from Terraform.

## Settings

Copy `.env.example` to `.env` and fill every value. Copy `seed/users.env.example` to `seed/users.env` and set a password per realm user. Neither file is committed.

## Start order

```bash
cd deploy/cloud
docker compose up -d --build
# Keycloak answers after about one minute, GitLab after five to ten.
docker compose ps
bash seed/keycloak-users.sh          # sets the realm users' passwords
```

Checks:

```bash
curl -fsS https://auth.parleylab.dev/realms/parley/.well-known/openid-configuration | jq .issuer
curl -fsS https://gitlab.parleylab.dev/-/readiness
curl -fsS https://channel.parleylab.dev/healthz
docker compose logs agentgateway --tail 20
```

Phase 1 is done when `alice` signs in to GitLab through the Keycloak button with the password from `seed/users.env`.

## Phase 3, not in this bundle

Project `parley-lab/orders-service`, user memberships, the inbox's group access token, and the runner registration are created by the seed scripts in `testbed/` once the test service exists.

## Notes

- Keycloak substitutes `${KC_GITLAB_CLIENT_SECRET}` into the realm on import. GitLab reads the same variable, so the two sides always agree.
- The realm file is read on every start with `--import-realm`, but Keycloak only imports a realm that does not exist yet. To apply a changed realm, delete it in the admin console or wipe the `keycloak-db` volume.
- The inbox still uses the file-based merge request provider until the GitLab provider lands (phase 2). Its image carries git for that reason.
