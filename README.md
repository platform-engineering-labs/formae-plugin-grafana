# Grafana Plugin for formae

Manage Grafana instance resources declaratively — dashboards, data sources, folders, alerting, teams, and service accounts. Works with both self-hosted Grafana and Grafana Cloud instances.

## Installation

Requires Go 1.25+ and [Pkl CLI](https://pkl-lang.org/main/current/pkl-cli/index.html).

```bash
git clone https://github.com/platform-engineering-labs/formae-plugin-grafana.git
cd formae-plugin-grafana
make install
```

This builds the plugin binary and installs it to `~/.pel/formae/plugins/grafana/`. The formae agent discovers installed plugins automatically on startup.

## Supported Resources

### Core

| Resource Type | Description | Native ID |
|---|---|---|
| `Grafana::Core::Folder` | Dashboard folders with nested hierarchy support | `uid` |
| `Grafana::Core::Dashboard` | Dashboard definitions (JSON model) | `uid` |
| `Grafana::Core::DataSource` | Data source connections (Prometheus, Loki, etc.) | `uid` |
| `Grafana::Core::Team` | Teams for organizing users and permissions | `id` |
| `Grafana::Core::ServiceAccount` | Service accounts for programmatic API access | `id` |

### Alerting

| Resource Type | Description | Native ID |
|---|---|---|
| `Grafana::Alerting::AlertRule` | Individual alert rules with query conditions | `uid` |
| `Grafana::Alerting::ContactPoint` | Notification channels (Slack, email, PagerDuty, etc.) | `uid` |
| `Grafana::Alerting::NotificationPolicy` | Alert routing tree (singleton per org) | `receiver` |
| `Grafana::Alerting::MuteTiming` | Time windows for suppressing notifications | `name` |
| `Grafana::Alerting::MessageTemplate` | Go templates for notification formatting | `name` |

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

## Development

### Prerequisites

- Go 1.25+
- [Pkl CLI](https://pkl-lang.org/main/current/pkl-cli/index.html)
- Docker (for test infrastructure)

### Building

```bash
make build      # Build plugin binary
make install    # Build + install locally
```

### Testing

```bash
make test-env-up            # Start Grafana on port 3333
make test-integration       # Run integration tests
make conformance-test-crud  # Run CRUD conformance tests
make test-env-down          # Stop Grafana
```

The test infrastructure uses `docker-compose.test.yml` to spin up a Grafana OSS instance on port 3333 with `admin:admin` credentials. Environment variables `GRAFANA_URL` and `GRAFANA_AUTH` are set automatically by the Makefile.

## Licensing

Licensed under FSL-1.1-ALv2. See [LICENSE](LICENSE).
