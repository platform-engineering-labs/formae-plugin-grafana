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
  settings = new {
    url = "https://hooks.slack.com/services/…"
    recipient = "#alerts"
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

### Contact point settings

A contact point's settings are a single typed `settings` object whose keys are
the options the declared `contactPointType` accepts:

```pkl
new contact_point.ContactPoint {
  label = "slack-alerts"
  name = "Slack Alerts"
  contactPointType = "slack"
  settings = new {
    url = "https://hooks.slack.com/services/xxx/yyy/zzz"
    recipient = "#alerts"
  }
}
```

The vocabulary comes from Grafana's own notifier metadata, so every option any
notifier type declares is available as a property. Nested blocks (a webhook's
`http_config.oauth2.tls_config`, an OpsGenie `responders` entry) are typed
objects of their own, and every free-text option accepts a `formae.Resolvable`,
so a setting can flow in from another resource - even one managed by a
different plugin - in a single apply:

```pkl
settings = new {
  integrationKey = pdIntegration.res.integrationKey
  severity = "critical"
}
```

A key no notifier declares - a misspelled one - is a Pkl type error, caught
before the forma reaches the agent. A key that belongs to a *different*
notifier type than the one declared still evaluates, and the plugin rejects it
when the contact point is submitted, naming the key and the notifier type that
does not accept it. Nothing is written in that case, which matters because
Grafana stores and echoes back any key it is given: an unrejected
wrong-for-type key would sit in the contact point doing nothing, and never be
reported as drift.

#### Secret options

Options Grafana classifies as secret - a Slack `url` or `token`, a PagerDuty
`integrationKey`, a webhook `password` - additionally accept a
`formae.SecretValue` and are hashed at rest. The classification is Grafana's
own, taken from its notifier metadata rather than from a list this plugin
keeps. Such an option can be given at either of two grades:

- A **reference** (`password = mySecret.res.secretValue`) keeps the plaintext
  out of formae's state entirely: state holds the reference, the value is
  resolved per apply, and `formae extract` returns the reference rather than a
  value.
- A **literal** (`password = "hunter2"`) persists as a digest. The plaintext
  still sits in your forma, so prefer a reference anywhere but a throwaway
  local instance.

Grafana stores a secret-classified option encrypted and returns the literal
`[REDACTED]` in its place on every read path. The plugin declines to report a
value it cannot observe, so formae keeps the value you declared rather than
recording the sentinel, and the contact point does not drift on every sync.

That is a deliberate **loss of observability**, not a guarantee that such a
secret never drifts. Because nothing is ever read back for the option, formae
cannot tell whether the secret in Grafana was rotated, replaced or removed out
of band: the divergence is invisible, not absent. Reconciling a suspected
divergence therefore means re-applying the declared value, not syncing
Grafana's. Every non-secret option is reported as Grafana holds it and
drift-checked individually, so an out-of-band edit to one of those is still
reported as a change to that key alone.

The classification is per option name rather than per notifier type, because
that is how Grafana's metadata reads: `url` is secret-classified everywhere
since some types mark it secure. Grafana redacts it only for those types, so a
webhook's `url` comes back in full and stays drift-checked normally.

#### A closed vocabulary

The `settings` type is generated from the notifier metadata of the Grafana the
plugin was built against, so the set of authorable options is closed. An option
added by a newer Grafana cannot be authored until the schema is regenerated and
the plugin republished: the forma fails to evaluate rather than reaching a
Grafana that would have accepted the option. An escape hatch for options newer
than the schema was considered and deliberately deferred - it would reopen the
type, and with it the misspelling check that closing it buys.

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

The Grafana target authenticates with one of two methods:

#### 1. The `auth` Config block (recommended)

`auth` takes one of two strategies, `TokenAuth` or `BasicAuth`. Every credential
field in them accepts either a literal string or a formae resolvable, so the
credential can be sourced from a secret and resolved live at apply time. There is
no credential in the agent environment, and no agent restart when the credential
rotates.

`TokenAuth` carries a Grafana service account token (or a legacy API key), sent
as a bearer token. Prefer it: a service account token has a scoped role and can
be revoked on its own, where the admin password is an instance superuser
credential shared with the interactive login.

```pkl
import "@aws/secretsmanager/secret.pkl" as secretmod

// A secret holding the service account token.
local grafanaToken = new secretmod.Secret {
  label = "grafana-token"
  name = "grafana-agent-token"
}

new formae.Target {
  label = "my-grafana"
  namespace = "GRAFANA"
  config = new grafana.Config {
    url = "https://grafana.example.com"
    auth = new grafana.TokenAuth {
      token = grafanaToken.res.secretValue
    }
  }
}
```

`BasicAuth` carries a username and password:

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
    auth = new grafana.BasicAuth {
      username = grafanaCreds.res.secretValue.json("username")
      password = grafanaCreds.res.secretValue.json("password")
    }
  }
}
```

When a credential is a resolvable, the target config persists only the reference
to the secret; the plaintext value is resolved per plugin call and never lands in
the datastore. Any formae secret works here (for example an AWS Secrets Manager
secret, an Azure Key Vault secret, or a Kubernetes Secret), so the credential can
live alongside the infrastructure it protects.

A credential written as a **literal string** gets no such protection: it is
ordinary target configuration and is stored as given. Use a literal only for a
throwaway local instance, and a resolvable everywhere else.

When `auth` is set it is used as given: `GRAFANA_AUTH` is not consulted, and an
incomplete block (an empty token, a username without a password) is an error
rather than a silent fallback, so a broken secret reference cannot quietly
downgrade the target to whatever credential the agent happens to carry.

#### 2. `GRAFANA_AUTH` environment variable (fallback)

Used when `auth` is not set. Supported formats:

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
