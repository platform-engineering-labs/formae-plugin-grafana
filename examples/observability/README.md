# Observability Example

Deploy a full LGTM (Loki, Grafana, Tempo, Mimir) observability stack via Docker Compose, then provision Grafana dashboards to monitor your formae agent — all managed as infrastructure.

This example demonstrates **target resolvables**: the Grafana target automatically resolves its connection endpoint from the Docker Compose stack, so you don't need to hardcode URLs.

## Prerequisites

- formae >= 0.83.0
- [formae-plugin-grafana](https://github.com/platform-engineering-labs/formae-plugin-grafana) installed
- [formae-plugin-compose](https://github.com/platform-engineering-labs/formae-plugin-compose) installed
- Docker with Compose v2 plugin

## Environment Variables

| Variable | Value | Description |
|---|---|---|
| `GRAFANA_AUTH` | `admin:admin` | The LGTM stack uses default admin credentials. Must be set **before** starting the agent. |

## Usage

```bash
# Set credentials and start the agent
export GRAFANA_AUTH=admin:admin
formae agent start

# Deploy the LGTM stack and Grafana dashboards
formae apply --mode reconcile --watch examples/observability/main.pkl
```

The first apply creates 7 resources: the Docker Compose stack, two targets (docker, grafana), a dashboard folder, and two dashboards.

## Known Limitations

**Agent restart required for metrics**: The formae agent exports OpenTelemetry metrics to the LGTM stack's OTLP collector. If the agent was already running before the collector existed, the gRPC OTLP exporter will not reconnect automatically. Restart the agent after the first apply for all metrics to appear in the dashboards:

```bash
formae agent stop
GRAFANA_AUTH=admin:admin formae agent start
```

This will be fixed in a future release.

## What's Deployed

| Resource | Type | Target |
|---|---|---|
| LGTM stack | `Docker::Compose::Stack` | docker |
| formae-dashboards | `Grafana::Core::Folder` | grafana |
| formae-overview | `Grafana::Core::Dashboard` | grafana |
| formae-plugins | `Grafana::Core::Dashboard` | grafana |

## Destroy

```bash
formae destroy --yes --watch examples/observability/main.pkl
```

The docker target survives destroy (it has no resolvable dependencies). The grafana target and all its resources are deleted because the target config depends on the compose stack's endpoints.
