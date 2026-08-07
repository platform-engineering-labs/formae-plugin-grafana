# Observability Example

Deploy a full LGTM (Loki, Grafana, Tempo, Mimir) observability stack via Docker Compose, then provision Grafana dashboards to monitor your formae agent - all managed as infrastructure.

This example demonstrates **target resolvables**: the Grafana target automatically resolves its connection endpoint from the Docker Compose stack, so you don't need to hardcode URLs.

## Prerequisites

- formae >= 0.89.0
- [formae-plugin-grafana](https://github.com/platform-engineering-labs/formae-plugin-grafana) installed
- [formae-plugin-compose](https://github.com/platform-engineering-labs/formae-plugin-compose) installed
- Docker with Compose v2 plugin

## Credentials

No environment variable is needed. The Grafana target carries a `BasicAuth`
block with the LGTM stack's default `admin:admin` credentials, so the
credentials live in the forma alongside the stack they belong to.

A literal credential is fine for a throwaway local stack like this one. Anywhere
else, source it from a formae secret so the plaintext stays out of the datastore
and rotations are picked up without an agent restart:

```pkl
auth = new grafana.TokenAuth {
  token = grafanaToken.res.secretValue
}
```

## Usage

```bash
# Start the agent
formae agent start

# Deploy the LGTM stack and Grafana dashboards
formae apply --mode reconcile --watch examples/observability/main.pkl
```

The first apply creates 7 resources: the Docker Compose stack, two targets (docker, grafana), a dashboard folder, and two dashboards.

## Known Limitations

**Agent restart required for metrics**: The formae agent exports OpenTelemetry metrics to the LGTM stack's OTLP collector. If the agent was already running before the collector existed, the gRPC OTLP exporter will not reconnect automatically. Restart the agent after the first apply for all metrics to appear in the dashboards:

```bash
formae agent stop
formae agent start
```

This will be fixed in a future release.

## What's Deployed

| Resource | Type | Target |
|---|---|---|
| LGTM stack | `Docker::Compose::Stack` | docker |
| formae-dashboards | `GRAFANA::Core::Folder` | grafana |
| formae-overview | `GRAFANA::Core::Dashboard` | grafana |
| formae-plugins | `GRAFANA::Core::Dashboard` | grafana |

## Destroy

```bash
formae destroy --yes --watch examples/observability/main.pkl
```

The docker target survives destroy (it has no resolvable dependencies). The grafana target and all its resources are deleted because the target config depends on the compose stack's endpoints.
