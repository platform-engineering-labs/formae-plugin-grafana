# Grafana Plugin for formae

[![CI](https://github.com/platform-engineering-labs/formae-plugin-grafana/actions/workflows/ci.yml/badge.svg)](https://github.com/platform-engineering-labs/formae-plugin-grafana/actions/workflows/ci.yml)
[![Nightly](https://github.com/platform-engineering-labs/formae-plugin-grafana/actions/workflows/nightly.yml/badge.svg)](https://github.com/platform-engineering-labs/formae-plugin-grafana/actions/workflows/nightly.yml)

Manage Grafana instance resources declaratively — dashboards, data sources, folders, alerting, teams, and service accounts. Works with both self-hosted Grafana and Grafana Cloud instances.

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

Set the `GRAFANA_AUTH` environment variable. Supported formats:

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

**Basic** — folder, data source, dashboard:
```bash
formae apply --mode reconcile --watch examples/basic/main.pkl
```

**Alerting** — contact points, mute timings, templates:
```bash
formae apply --mode reconcile --watch examples/alerting/main.pkl
```

**Observability** — LGTM stack via Docker Compose with Grafana dashboards provisioned through a target resolvable (requires formae >= 0.83.0 and formae-plugin-compose):
```bash
formae apply --mode reconcile --watch examples/observability/main.pkl
```

## Licensing

Licensed under FSL-1.1-ALv2. See [LICENSE](LICENSE).
