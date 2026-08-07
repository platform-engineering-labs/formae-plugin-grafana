# Grafana Plugin for formae

[![CI](https://github.com/platform-engineering-labs/formae-plugin-grafana/actions/workflows/ci.yml/badge.svg)](https://github.com/platform-engineering-labs/formae-plugin-grafana/actions/workflows/ci.yml)
[![Nightly](https://github.com/platform-engineering-labs/formae-plugin-grafana/actions/workflows/nightly.yml/badge.svg)](https://github.com/platform-engineering-labs/formae-plugin-grafana/actions/workflows/nightly.yml)

Manage Grafana instance resources declaratively - dashboards, data sources, folders, alerting, teams, and service accounts. Works with both self-hosted Grafana and Grafana Cloud instances.

## Supported Resources

### Core

| Resource Type | Description | Native ID |
|---|---|---|
| `GRAFANA::Core::Folder` | Dashboard folders with nested hierarchy support | `uid` |
| `GRAFANA::Core::Dashboard` | Dashboard definitions (JSON model) | `uid` |
| `GRAFANA::Core::DataSource` | Data source connections (Prometheus, Loki, etc.) | `uid` |
| `GRAFANA::Core::Team` | Teams for organizing users and permissions | `id` |
| `GRAFANA::Core::ServiceAccount` | Service accounts for programmatic API access | `id` |

### Alerting

| Resource Type | Description | Native ID |
|---|---|---|
| `GRAFANA::Alerting::AlertRule` | Individual alert rules with query conditions | `uid` |
| `GRAFANA::Alerting::ContactPoint` | Notification channels (Slack, email, PagerDuty, etc.) | `uid` |
| `GRAFANA::Alerting::NotificationPolicy` | Alert routing tree (singleton per org) | `receiver` |
| `GRAFANA::Alerting::MuteTiming` | Time windows for suppressing notifications | `name` |
| `GRAFANA::Alerting::MessageTemplate` | Go templates for notification formatting | `name` |

A notification policy names the contact point it routes to. Point `receiver` at
the contact point's `name` resolvable rather than repeating the name as a
string:

```pkl
local onCall = new contact_point.ContactPoint {
  label = "on-call"
  name = "on-call"
  contactPointType = "slack"
  settingsMap = new Mapping {
    ["url"] = "https://hooks.slack.com/services/…"
    ["recipient"] = "#alerts"
  }
}
onCall  // a `local` binding is not emitted on its own; name it to add it to the forma

new notification_policy.NotificationPolicy {
  label = "default-routing"
  receiver = onCall.res.name
}
```

Grafana rejects a policy tree that names a receiver which does not exist, so the
ordering matters. The resolvable is what tells formae to create the contact
point before the policy, and to reset the policy before deleting the contact
point on teardown. Receivers named inside `routes` are part of an opaque JSON
string and carry no such edge.

### Contact point settings: use `settingsMap` for secret-bearing values

A contact point's settings can be given as `settingsMap` (a key/value mapping)
or as `settings` (a JSON string). Use `settingsMap` whenever any value is one
Grafana treats as a secret, for example a Slack `url` or `token`, a PagerDuty
`integrationKey`, or a webhook `password`.

Grafana stores those fields encrypted and returns the literal `[REDACTED]` in
their place on every read. With the `settings` JSON string, the plaintext you
declared and the `[REDACTED]` Grafana returns occupy the same field, so they
never compare equal and the contact point reports drift on every sync forever.
`settingsMap` is a submission-only form that is never read back, so no such
comparison happens.

For settings with no secret values, either form works.

## Configuration

### Target

Configure a Grafana target in your forma file:

```pkl
import "@grafana/grafana.pkl"

new formae.Target {
  label = "my-grafana"
  namespace = "GRAFANA"
  config = new grafana.Config {
    url = "https://grafana.example.com"
    // orgId = 1  // optional, defaults to token's org
  }
}
```

### Credentials

The Grafana target authenticates with one of two methods, in priority order:

#### 1. `username` and `password` Config fields (recommended)

Both fields accept either a literal string or a formae resolvable, so the
credentials can be sourced from a secret and resolved live at apply time. There
is no credential in the agent environment, and no agent restart when the
credential rotates. When both are set they take priority over `GRAFANA_AUTH`.

```pkl
import "@aws/secretsmanager/secret.pkl" as secretmod

// A secret holding {"username": "...", "password": "..."} as JSON.
local grafanaCreds = new secretmod.Secret {
  label = "grafana-admin"
  name = "grafana-admin-creds"
}

new formae.Target {
  label = "my-grafana"
  namespace = "GRAFANA"
  config = new grafana.Config {
    url = "https://grafana.example.com"
    username = grafanaCreds.res.secretValue.json("username")
    password = grafanaCreds.res.secretValue.json("password")
  }
}
```

The target config persists only a reference to the secret; the plaintext
credential never lands in the datastore. Any formae secret works here (for
example an AWS Secrets Manager secret, an Azure Key Vault secret, or a Kubernetes
Secret), so the credential can live alongside the infrastructure it protects.

#### 2. `GRAFANA_AUTH` environment variable (fallback)

Used when `username` and `password` are not both set. This is the right choice
for a service account token or API key (the resolvable fields above cover basic
auth). Supported formats:

| Format | Example |
|---|---|
| Service account token | `glsa_xxxxxxxxxxxx` |
| API key (legacy) | `eyJrIjoi...` |
| Basic auth | `admin:password` |

```bash
export GRAFANA_AUTH="glsa_your_service_account_token"
```

## Examples

See the [examples/](examples/) directory.

**Basic** - folder, data source, dashboard:
```bash
formae apply --mode reconcile --watch examples/basic/main.pkl
```

**Alerting** - contact points, mute timings, templates:
```bash
formae apply --mode reconcile --watch examples/alerting/main.pkl
```

**Observability** - LGTM stack via Docker Compose with Grafana dashboards provisioned through a target resolvable (requires formae >= 0.83.0 and formae-plugin-compose):
```bash
formae apply --mode reconcile --watch examples/observability/main.pkl
```

## Licensing

Licensed under FSL-1.1-ALv2. See [LICENSE](LICENSE).
