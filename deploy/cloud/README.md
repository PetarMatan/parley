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

## Inbox settings

| Variable | Value on this host | Meaning |
|---|---|---|
| `AIC_MR_PROVIDER` | `gitlab` | Merge requests come from the GitLab API; `local` is the file fake of the sandbox |
| `AIC_GITLAB_URL` | `http://gitlab:80` | GitLab over the `services` network; the public URL also works |
| `AIC_GITLAB_TOKEN` | from `.env` | Group access token on `parley-lab`, role Reporter, scope `api` (notes are posted) |
| `AIC_ISSUERS` | Keycloak realm, audience `parley`, identity claim `preferred_username` | The identity string is the GitLab username |
| `AIC_SERVICE_USERS` | `promoter@parleylab.dev` | Senders allowed to post notes on any merge request |

## Start order

```bash
cd deploy/cloud
docker compose up -d --build
# Keycloak answers after about one minute, GitLab after five to ten.
docker compose ps
bash seed/keycloak-users.sh          # sets the realm users' passwords
bash seed/gitlab-settings.sh         # once GitLab is healthy: turns web password sign-in off
```

Checks:

```bash
curl -fsS https://auth.parleylab.dev/realms/parley/.well-known/openid-configuration | jq .issuer
curl -fsS -o /dev/null -w "%{http_code}\n" https://gitlab.parleylab.dev/users/sign_in   # 200; /-/readiness answers only from inside the container
curl -fsS https://channel.parleylab.dev/healthz
docker compose logs agentgateway --tail 20
```

Phase 1 is done when `alice` signs in to GitLab through the Keycloak button with the password from `seed/users.env`.

## Phase 3, not in this bundle

Project `parley-lab/orders-service`, user memberships, the inbox's group access token, and the runner registration are created by the seed scripts in `testbed/` once the test service exists.

## Notes

- Keycloak substitutes `${KC_GITLAB_CLIENT_SECRET}` into the realm on import. GitLab reads the same variable, so the two sides always agree.
- The realm file is read on every start with `--import-realm`, but Keycloak only imports a realm that does not exist yet. To apply a changed realm, delete it in the admin console or wipe the `keycloak-db` volume.
- The inbox reads merge requests from GitLab (`AIC_MR_PROVIDER=gitlab`). Its image still carries git for the local provider.

## Administration

- Keycloak admin console: Caddy answers `/admin` only to private addresses. From your laptop run `make testbed-admin`; it opens a SOCKS tunnel through the host and a separate Chrome profile that uses it. Credentials: `KC_BOOTSTRAP_ADMIN_*` in `.env`.
- GitLab: the sign-in page goes straight to Keycloak, and local password sign-in on the web is off (`seed/gitlab-settings.sh`). Root is used from the host only, for example `docker compose exec gitlab gitlab-rails runner '...'`, or through a personal access token created there.
- A change to `Caddyfile` needs `caddy reload`; `deploy.sh` does it. A change to `gitlab/gitlab.rb` needs `docker compose restart gitlab` (about two minutes). A change to `gateway/config.yaml` needs `docker compose restart agentgateway`.
