# GitLab CE (Omnibus) configuration for the cloud host. Loaded through
# GITLAB_OMNIBUS_CONFIG `from_file` in compose.yaml, so ENV[...] values come
# from the gitlab service environment (deploy/cloud/.env).

external_url 'https://gitlab.parleylab.dev'

# Caddy terminates TLS and forwards plain HTTP on the `edge` network.
nginx['listen_port'] = 80
nginx['listen_https'] = false
nginx['real_ip_header'] = 'X-Forwarded-For'
nginx['real_ip_trusted_addresses'] = ['172.16.0.0/12']
nginx['proxy_set_headers'] = {
  'X-Forwarded-Proto' => 'https',
  'X-Forwarded-Ssl' => 'on',
  'Host' => '$http_host',
  'X-Real-IP' => '$remote_addr',
  'X-Forwarded-For' => '$proxy_add_x_forwarded_for',
}

# Memory tuning for one 16 GB host shared with Keycloak and the gateway.
puma['worker_processes'] = 2
sidekiq['concurrency'] = 10
prometheus_monitoring['enable'] = false

# First boot only: the root password. Later changes go through the UI.
gitlab_rails['initial_root_password'] = ENV['GITLAB_ROOT_PASSWORD']

# Container registry, pages and mail are not part of the testbed.
registry['enable'] = false
gitlab_pages['enable'] = false
gitlab_rails['gitlab_email_enabled'] = false

# Everyone logs in through Keycloak (testbed decision T2). Local sign-up is off.
gitlab_rails['gitlab_signup_enabled'] = false
gitlab_rails['omniauth_enabled'] = true
gitlab_rails['omniauth_allow_single_sign_on'] = ['openid_connect']
gitlab_rails['omniauth_auto_link_user'] = ['openid_connect']
gitlab_rails['omniauth_block_auto_created_users'] = false
gitlab_rails['omniauth_providers'] = [
  {
    name: 'openid_connect',
    label: 'Keycloak',
    args: {
      name: 'openid_connect',
      scope: ['openid', 'profile', 'email'],
      response_type: 'code',
      issuer: 'https://auth.parleylab.dev/realms/parley',
      discovery: true,
      client_auth_method: 'query',
      uid_field: 'sub',
      pkce: true,
      send_scope_to_token_endpoint: 'false',
      client_options: {
        identifier: 'gitlab',
        secret: ENV['KC_GITLAB_CLIENT_SECRET'],
        redirect_uri: 'https://gitlab.parleylab.dev/users/auth/openid_connect/callback',
      },
    },
  },
]
