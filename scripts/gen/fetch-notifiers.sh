#!/bin/bash
set -euo pipefail

# Regenerate internal/notifiers/alert-notifiers.json from a running Grafana.
# Requires the target Grafana URL as an argument; GRAFANA_AUTH may set
# "user:password" credentials for it.

GRAFANA_URL="${1:-}"
GRAFANA_AUTH="${GRAFANA_AUTH:-}"

if [ -z "$GRAFANA_URL" ]; then
    echo "usage: fetch-notifiers.sh <grafana-url>" >&2
    exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUT_FILE="${SCRIPT_DIR}/../../internal/notifiers/alert-notifiers.json"

echo "fetch-notifiers.sh: fetching notifier metadata from ${GRAFANA_URL}"

if [ -n "$GRAFANA_AUTH" ]; then
    curl -sf -u "${GRAFANA_AUTH}" "${GRAFANA_URL}/api/alert-notifiers" -o "${OUT_FILE}"
else
    curl -sf "${GRAFANA_URL}/api/alert-notifiers" -o "${OUT_FILE}"
fi

echo "fetch-notifiers.sh: wrote ${OUT_FILE}"
