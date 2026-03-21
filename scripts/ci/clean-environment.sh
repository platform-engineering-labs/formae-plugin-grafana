#!/bin/bash
set -euo pipefail

# Clean up test resources in Grafana
# Requires GRAFANA_URL and GRAFANA_AUTH environment variables

GRAFANA_URL="${GRAFANA_URL:-http://localhost:3000}"
GRAFANA_AUTH="${GRAFANA_AUTH:-}"

if [ -z "$GRAFANA_AUTH" ]; then
    echo "clean-environment.sh: GRAFANA_AUTH not set, skipping cleanup"
    exit 0
fi

echo "clean-environment.sh: Cleaning up test resources in ${GRAFANA_URL}"

# Helper: filter JSON array items where a field starts with a test prefix, printing that field's value.
# Usage: filter_by_prefix <field>
filter_by_prefix() {
    local field="$1"
    python3 -c "
import json, sys
try:
    items = json.load(sys.stdin)
    for item in items:
        val = item.get('${field}', '')
        if val.startswith('formae-test-') or val.startswith('formae-integ-test-'):
            print(val)
except:
    pass
" 2>/dev/null
}

# 1. Alert rules (depend on folders + datasources, delete first)
ALERT_RULES=$(curl -s -u "${GRAFANA_AUTH}" "${GRAFANA_URL}/api/v1/provisioning/alert-rules" 2>/dev/null || echo "[]")
echo "$ALERT_RULES" | filter_by_prefix uid | while read -r uid; do
    echo "  Deleting alert rule: $uid"
    curl -s -X DELETE -u "${GRAFANA_AUTH}" "${GRAFANA_URL}/api/v1/provisioning/alert-rules/${uid}" >/dev/null 2>&1 || true
done

# 2. Dashboards (depend on folders)
DASHBOARDS=$(curl -s -u "${GRAFANA_AUTH}" "${GRAFANA_URL}/api/search?type=dash-db" 2>/dev/null || echo "[]")
echo "$DASHBOARDS" | filter_by_prefix uid | while read -r uid; do
    echo "  Deleting dashboard: $uid"
    curl -s -X DELETE -u "${GRAFANA_AUTH}" "${GRAFANA_URL}/api/dashboards/uid/${uid}" >/dev/null 2>&1 || true
done

# 3. Contact points
CONTACT_POINTS=$(curl -s -u "${GRAFANA_AUTH}" "${GRAFANA_URL}/api/v1/provisioning/contact-points" 2>/dev/null || echo "[]")
echo "$CONTACT_POINTS" | filter_by_prefix uid | while read -r uid; do
    echo "  Deleting contact point: $uid"
    curl -s -X DELETE -u "${GRAFANA_AUTH}" "${GRAFANA_URL}/api/v1/provisioning/contact-points/${uid}" >/dev/null 2>&1 || true
done

# 4. Mute timings
MUTE_TIMINGS=$(curl -s -u "${GRAFANA_AUTH}" "${GRAFANA_URL}/api/v1/provisioning/mute-timings" 2>/dev/null || echo "[]")
echo "$MUTE_TIMINGS" | filter_by_prefix name | while read -r name; do
    echo "  Deleting mute timing: $name"
    curl -s -X DELETE -u "${GRAFANA_AUTH}" "${GRAFANA_URL}/api/v1/provisioning/mute-timings/${name}" >/dev/null 2>&1 || true
done

# 5. Message templates
TEMPLATES=$(curl -s -u "${GRAFANA_AUTH}" "${GRAFANA_URL}/api/v1/provisioning/templates" 2>/dev/null || echo "[]")
echo "$TEMPLATES" | filter_by_prefix name | while read -r name; do
    echo "  Deleting message template: $name"
    curl -s -X DELETE -u "${GRAFANA_AUTH}" "${GRAFANA_URL}/api/v1/provisioning/templates/${name}" >/dev/null 2>&1 || true
done

# 6. Service accounts
SERVICE_ACCOUNTS=$(curl -s -u "${GRAFANA_AUTH}" "${GRAFANA_URL}/api/serviceaccounts/search?perpage=1000" 2>/dev/null || echo '{"serviceAccounts":[]}')
echo "$SERVICE_ACCOUNTS" | python3 -c "
import json, sys
try:
    data = json.load(sys.stdin)
    for sa in data.get('serviceAccounts', []):
        name = sa.get('name', '')
        if name.startswith('formae-test-') or name.startswith('formae-integ-test-'):
            print(sa.get('id', ''))
except:
    pass
" 2>/dev/null | while read -r id; do
    echo "  Deleting service account: $id"
    curl -s -X DELETE -u "${GRAFANA_AUTH}" "${GRAFANA_URL}/api/serviceaccounts/${id}" >/dev/null 2>&1 || true
done

# 7. Teams
TEAMS=$(curl -s -u "${GRAFANA_AUTH}" "${GRAFANA_URL}/api/teams/search?perpage=1000" 2>/dev/null || echo '{"teams":[]}')
echo "$TEAMS" | python3 -c "
import json, sys
try:
    data = json.load(sys.stdin)
    for team in data.get('teams', []):
        name = team.get('name', '')
        if name.startswith('formae-test-') or name.startswith('formae-integ-test-'):
            print(team.get('id', ''))
except:
    pass
" 2>/dev/null | while read -r id; do
    echo "  Deleting team: $id"
    curl -s -X DELETE -u "${GRAFANA_AUTH}" "${GRAFANA_URL}/api/teams/${id}" >/dev/null 2>&1 || true
done

# 8. Data sources
DATASOURCES=$(curl -s -u "${GRAFANA_AUTH}" "${GRAFANA_URL}/api/datasources" 2>/dev/null || echo "[]")
echo "$DATASOURCES" | filter_by_prefix uid | while read -r uid; do
    echo "  Deleting data source: $uid"
    curl -s -X DELETE -u "${GRAFANA_AUTH}" "${GRAFANA_URL}/api/datasources/uid/${uid}" >/dev/null 2>&1 || true
done

# 9. Folders (delete last since dashboards and alert rules depend on them)
FOLDERS=$(curl -s -u "${GRAFANA_AUTH}" "${GRAFANA_URL}/api/folders" 2>/dev/null || echo "[]")
echo "$FOLDERS" | filter_by_prefix uid | while read -r uid; do
    echo "  Deleting folder: $uid"
    curl -s -X DELETE -u "${GRAFANA_AUTH}" "${GRAFANA_URL}/api/folders/${uid}" >/dev/null 2>&1 || true
done

echo "clean-environment.sh: Cleanup complete"
