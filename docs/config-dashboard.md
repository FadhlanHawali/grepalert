[← Documentation](../README.md#documentation) · Previous: [rules.yaml](config-rules.md) · Next: [Writing rules →](rules.md)

# dashboard.yaml reference

`dashboard.yaml` holds the dashboard's own settings: address, sign-in, where it keeps its data, and the token agents enroll with.

Everything else lives in its data file and is managed in the web UI:
- agents and groups
- group rules and their versions
- local accounts and roles
- silences
- the ops webhook and the agent-down delay

```bash
grepalert-dashboard -config /etc/grepalert/dashboard.yaml
```

**Environment variables:** `$VAR` and `${VAR}` are replaced with environment variables when the file is loaded. Durations are written like `30s` or `12h`.

## Complete example

Every setting, with its default ([examples/dashboard.yaml](../examples/dashboard.yaml)):

```yaml
# grepalert dashboard configuration: every setting, with its default.
# Reference: docs/config-dashboard.md
#
#   grepalert-dashboard -config /etc/grepalert/dashboard.yaml
#
# $VAR and ${VAR} are replaced with environment variables when this file is
# loaded. Agents, groups, rules, users and alert settings are managed in the
# web UI and saved to data_file, not here.

# --- Web server ----------------------------------------------------------
listen: ":9400"                 # default ":9400"
# Serve HTTPS directly, or put the dashboard behind a reverse proxy.
# tls_cert: /etc/grepalert/dashboard.crt
# tls_key: /etc/grepalert/dashboard.key

# --- Sign-in -------------------------------------------------------------
# With no sign-in configured, anyone who can reach the dashboard is an admin
# (fine for a local try-out only). Use any combination of the options below.
auth:
  # A built-in admin account, e.g. for break-glass access when SSO is down.
  # Set both or neither. More local accounts: web UI, Users & access.
  username: admin
  password: ${GREPALERT_DASHBOARD_PASSWORD}

  # Single sign-on with OpenID Connect: Okta, Entra ID, Google, Keycloak...
  # oidc:
  #   name: Okta                                       # sign-in button text; default "single sign-on"
  #   issuer: https://example.okta.com/oauth2/default  # required
  #   client_id: 0oa1b2c3d4                            # required
  #   client_secret: ${OIDC_CLIENT_SECRET}
  #   redirect_url: https://grepalert.example.com/auth/callback   # required: this dashboard's callback
  #   scopes: [openid, email, profile]                 # default
  #   groups_claim: groups                             # ID-token claim with the user's groups (default)
  #   allowed_domains: [example.com]                   # optional: only these email domains
  #   # Only when the dashboard reaches the provider at another address than
  #   # browsers do (Docker, Kubernetes):
  #   # discovery_url: http://keycloak:8080/realms/ops

  # Always admins: emails, usernames, or group:<identity-provider group>.
  # admins: ["group:platform-admins", "alice@example.com"]
  session_ttl: 12h              # how long a sign-in lasts
  # cookie_secure: false        # force the Secure cookie flag (default: on when served over HTTPS)

# --- Data ----------------------------------------------------------------
# Agents, groups, rule versions, users and settings. Holds tokens: it is
# written with 0600 permissions; keep it private and back it up.
data_file: /var/lib/grepalert-dashboard/data.json     # default: dashboard-data.json
# Who changed what (JSON lines, rotated at 20 MB). Default: audit.log next to data_file.
# audit_file: /var/lib/grepalert-dashboard/audit.log

# --- Agents --------------------------------------------------------------
# A fixed token agents can enroll with, for automation (Ansible, Terraform,
# images). Tokens can also be created and revoked in the web UI.
# enrollment_token: ${GREPALERT_ENROLL_TOKEN}
reconcile_interval: 30s         # how often groups re-check their agents and push rules to those that drifted
```

## Settings

### Web server

| Setting | Default | Meaning |
|---|---|---|
| `listen` | `:9400` | Address of the web UI, its API, and the endpoint agents connect to. |
| `tls_cert`, `tls_key` | none | Serve HTTPS with this certificate and key (PEM files). Or put the dashboard behind a reverse proxy that terminates TLS: it honours `X-Forwarded-Proto` for secure cookies. |

### Sign-in

With nothing under `auth` and no local accounts, **anyone who can reach the dashboard is an admin**, and the UI shows a warning. Fine for a try-out on your machine, never for a shared server. Combine the options as you like. See [Sign-in, roles and security](security.md).

| Setting | Default | Meaning |
|---|---|---|
| `auth.username`, `auth.password` | none | A built-in admin account, e.g. for break-glass access when SSO is down. Set both or neither. |
| `auth.admins` | none | Users who are always admins: emails, usernames, or `group:<name>` for a group from your identity provider. Use it to give the first people access. |
| `auth.session_ttl` | `12h` | How long a sign-in lasts. |
| `auth.cookie_secure` | `false` | Always set the session cookie's `Secure` flag. Without it, the flag is set when the request came over HTTPS (directly or via a proxy). |

**Local accounts** (bcrypt-hashed passwords of at least 12 characters) and **roles** (viewer, editor, admin, per group) are managed in the UI, under *Users & access*.

### Single sign-on (OpenID Connect)

Okta, Microsoft Entra ID, Google, Keycloak, Auth0 and any other OpenID Connect provider. Register the dashboard as a web application (confidential client) with the redirect URL `https://<dashboard>/auth/callback`.

| Setting | Default | Meaning |
|---|---|---|
| `auth.oidc.name` | `single sign-on` | Text of the sign-in button: "Sign in with Okta". |
| `auth.oidc.issuer` | required | The provider's issuer URL, as in its tokens, e.g. `https://login.microsoftonline.com/<tenant>/v2.0`. |
| `auth.oidc.client_id` | required | The client ID from the provider. |
| `auth.oidc.client_secret` | none | The client secret, usually `${OIDC_CLIENT_SECRET}`. |
| `auth.oidc.redirect_url` | required | This dashboard's callback: `https://<dashboard>/auth/callback`. |
| `auth.oidc.scopes` | `[openid, email, profile]` | Scopes to request. Some providers need an extra scope to include groups. |
| `auth.oidc.groups_claim` | `groups` | The ID-token claim with the user's groups. Roles can be granted to these groups. |
| `auth.oidc.allowed_domains` | any | Only admit users whose email is in one of these domains. |
| `auth.oidc.discovery_url` | the issuer | Where the dashboard itself reaches the provider, when that differs from the public issuer (Docker, Kubernetes), e.g. `http://keycloak:8080/realms/ops`. |

### Data

| Setting | Default | Meaning |
|---|---|---|
| `data_file` | `dashboard-data.json` | Agents, groups, rule versions, local accounts, roles, silences and settings. Holds tokens, so it is written with mode 0600. Back it up. |
| `audit_file` | `audit.log` next to `data_file` | The audit log: who changed what, as JSON lines. Rotated at 20 MB, keeping 5 old files. |

### Agents

| Setting | Default | Meaning |
|---|---|---|
| `enrollment_token` | none | A fixed token agents can enroll with, for automation (Ansible, Terraform, images). Tokens with an expiry can also be created and revoked in the UI, under *Agent enrollment*. |
| `reconcile_interval` | `30s` | How often each group checks its agents and pushes its rules to those that drifted or came back online. Staged rollouts are judged on the same tick. |

## Command line

| Flag | Meaning |
|---|---|
| `-config <file>` | The dashboard.yaml to use (default `dashboard.yaml`). |
| `-version` | Print the version and exit. |

`GET /healthz` answers `ok` without sign-in, for load balancers and container health checks.
