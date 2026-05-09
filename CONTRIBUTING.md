# Contributing

This document covers local development for plugin authors. For user-facing
plugin docs (configuration, supported resources, examples), see
[README.md](README.md).

## Prerequisites

- Go 1.25+
- [Pkl CLI](https://pkl-lang.org/main/current/pkl-cli/index.html)
- Docker (for test infrastructure)

## Local Installation

```bash
git clone https://github.com/platform-engineering-labs/formae-plugin-grafana.git
cd formae-plugin-grafana
make install
```

This builds the plugin binary and installs it to `~/.pel/formae/plugins/grafana/`. The formae agent discovers installed plugins automatically on startup.

## Building

```bash
make build      # Build plugin binary
make install    # Build + install locally
```

## Testing

```bash
make test-env-up            # Start Grafana on port 3333
make test-integration       # Run integration tests
make conformance-test-crud  # Run CRUD conformance tests
make test-env-down          # Stop Grafana
```

The test infrastructure uses `docker-compose.test.yml` to spin up a Grafana OSS instance on port 3333 with `admin:admin` credentials. Environment variables `GRAFANA_URL` and `GRAFANA_AUTH` are set automatically by the Makefile.
