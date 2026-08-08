// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	goapi "github.com/grafana/grafana-openapi-client-go/client"
	"github.com/grafana/grafana-openapi-client-go/client/provisioning"
	"github.com/platform-engineering-labs/formae-plugin-grafana/pkg/config"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// grafanaClient builds a client against the Grafana test instance, taking its
// credentials from GRAFANA_AUTH the way the plugin does for a target that
// declares no auth block.
func grafanaClient(t *testing.T) *goapi.GrafanaHTTPAPI {
	t.Helper()
	if os.Getenv("GRAFANA_AUTH") == "" {
		t.Skip("GRAFANA_AUTH must be set for integration tests")
	}
	url := os.Getenv("GRAFANA_URL")
	if url == "" {
		url = "http://localhost:3333"
	}
	cfg, err := config.ParseTargetConfig(json.RawMessage(fmt.Sprintf(`{"Type":"Grafana","Url":%q}`, url)))
	require.NoError(t, err)
	client, err := config.NewClient(cfg)
	require.NoError(t, err)
	return client
}

// contactPointProperties builds the properties a contact point is submitted
// with, naming it after its uid so each test owns a distinct one.
func contactPointProperties(t *testing.T, uid, cpType string, settings map[string]any) json.RawMessage {
	t.Helper()
	props, err := json.Marshal(map[string]any{
		"uid":              uid,
		"name":             uid,
		"contactPointType": cpType,
		"settings":         settings,
	})
	require.NoError(t, err)
	return props
}

// createContactPoint submits a contact point through the handler, requiring the
// create to succeed, and removes it when the test ends.
func createContactPoint(t *testing.T, client *goapi.GrafanaHTTPAPI, uid, cpType string, settings map[string]any) *resource.ProgressResult {
	t.Helper()
	deleteWhenDone(t, client, uid)

	result, err := (&ContactPointHandler{}).Create(context.Background(), client,
		contactPointProperties(t, uid, cpType, settings))
	require.NoError(t, err)
	require.Equal(t, resource.OperationStatusSuccess, result.OperationStatus, result.StatusMessage)
	return result
}

// updateContactPoint submits new settings for an existing contact point,
// requiring the update to succeed.
func updateContactPoint(t *testing.T, client *goapi.GrafanaHTTPAPI, uid, cpType string, settings map[string]any) *resource.ProgressResult {
	t.Helper()
	props := contactPointProperties(t, uid, cpType, settings)
	result, err := (&ContactPointHandler{}).Update(context.Background(), client, uid, props, props)
	require.NoError(t, err)
	require.Equal(t, resource.OperationStatusSuccess, result.OperationStatus, result.StatusMessage)
	return result
}

// readContactPoint reads a contact point back through the handler, requiring
// the read to succeed.
func readContactPoint(t *testing.T, client *goapi.GrafanaHTTPAPI, uid string) *resource.ReadResult {
	t.Helper()
	result, err := (&ContactPointHandler{}).Read(context.Background(), client, uid)
	require.NoError(t, err)
	require.Empty(t, result.ErrorCode)
	return result
}

// deleteWhenDone removes a contact point at the end of the test, whether or not
// it was ever written.
func deleteWhenDone(t *testing.T, client *goapi.GrafanaHTTPAPI, uid string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = (&ContactPointHandler{}).Delete(context.Background(), client, uid)
	})
}

// pluginSettings decodes the settings object out of properties the handler
// reported - the view formae records and compares against the declaration.
func pluginSettings(t *testing.T, props []byte) map[string]any {
	t.Helper()
	var out struct {
		Settings map[string]any `json:"settings"`
	}
	require.NoError(t, json.Unmarshal(props, &out))
	return out.Settings
}

// serverSettings returns the settings Grafana itself reports for a contact
// point, before the plugin strips anything from them, and nil when no contact
// point carries the uid.
func serverSettings(t *testing.T, client *goapi.GrafanaHTTPAPI, uid string) map[string]any {
	t.Helper()
	resp, err := client.Provisioning.GetContactpoints(&provisioning.GetContactpointsParams{
		Context: context.Background(),
	})
	require.NoError(t, err)
	for _, cp := range resp.GetPayload() {
		if cp.UID == uid {
			settings, ok := cp.Settings.(map[string]any)
			require.True(t, ok, "settings are %T, want an object", cp.Settings)
			return settings
		}
	}
	return nil
}

// A secret-classified option is stored encrypted and comes back as the redacted
// sentinel, so the plugin declines to report it and formae keeps the declared
// value. Every other option is reported as Grafana holds it, so it stays
// drift-checked individually.
func TestIntegration_ContactPointSecretIsNotReportedWhileOtherKeysAre(t *testing.T) {
	client := grafanaClient(t)
	const uid = "formae-integ-cp-secret"

	created := createContactPoint(t, client, uid, "slack", map[string]any{
		"url":       "https://hooks.slack.com/services/T00000000/B00000000/formaeIntegration",
		"recipient": "#formae-test",
		"username":  "formae",
	})

	reported := pluginSettings(t, created.ResourceProperties)
	assert.NotContains(t, reported, "url", "a secret Grafana redacts must not be reported")
	assert.Equal(t, "#formae-test", reported["recipient"])
	assert.Equal(t, "formae", reported["username"])

	// Grafana does hold the secret - it is the read path that cannot see it.
	assert.Equal(t, "[REDACTED]", serverSettings(t, client, uid)["url"])

	// Read reports the same view as create, so a sync right after an apply has
	// nothing to reconcile.
	assert.Equal(t, reported, pluginSettings(t, []byte(readContactPoint(t, client, uid).Properties)))
}

// Editing a non-secret option re-submits the secret formae kept from the
// declaration, so the secret survives the write and the edited key is the only
// change reported.
func TestIntegration_ContactPointNonSecretEditKeepsTheSecret(t *testing.T) {
	client := grafanaClient(t)
	const uid = "formae-integ-cp-nonsecret-edit"
	const webhookURL = "https://hooks.slack.com/services/T00000000/B00000000/formaeIntegration"

	createContactPoint(t, client, uid, "slack", map[string]any{
		"url":       webhookURL,
		"recipient": "#formae-test",
	})

	updated := updateContactPoint(t, client, uid, "slack", map[string]any{
		"url":       webhookURL,
		"recipient": "#formae-oncall",
	})

	reported := pluginSettings(t, updated.ResourceProperties)
	assert.Equal(t, "#formae-oncall", reported["recipient"])
	assert.NotContains(t, reported, "url")

	// The contact point still carries a secret: the non-secret edit did not
	// clear it.
	assert.Equal(t, "[REDACTED]", serverSettings(t, client, uid)["url"])

	// A read straight after the update reports exactly what the update
	// reported, so the edit settles instead of drifting on the next sync.
	assert.Equal(t, reported, pluginSettings(t, []byte(readContactPoint(t, client, uid).Properties)))
}

// Dropping a secret option from a contact point that carried one is an ordinary
// update: the remaining options are reported and the removed key is reported no
// differently from a secret that is still there. Whether Grafana kept its
// encrypted copy is not observable through the plugin either way - the same
// blindness that makes an out-of-band rotation undetectable.
func TestIntegration_ContactPointSecretRemoved(t *testing.T) {
	client := grafanaClient(t)
	const uid = "formae-integ-cp-secret-removed"

	createContactPoint(t, client, uid, "webhook", map[string]any{
		"url":      "https://example.com/formae-integration",
		"username": "formae",
		"password": "formae-integration-password",
	})

	updated := updateContactPoint(t, client, uid, "webhook", map[string]any{
		"url":      "https://example.com/formae-integration",
		"username": "formae",
	})

	reported := pluginSettings(t, updated.ResourceProperties)
	assert.NotContains(t, reported, "password", "a secret key must stay unreported once removed")
	assert.Equal(t, "https://example.com/formae-integration", reported["url"])
	assert.Equal(t, "formae", reported["username"])

	assert.Equal(t, reported, pluginSettings(t, []byte(readContactPoint(t, client, uid).Properties)))
}

// Grafana stores and echoes back any settings key it is given, including one
// belonging to another notifier type, so a wrong-for-type key would otherwise
// be accepted silently and never reported as drift. The plugin rejects it
// against the target's own notifier vocabulary before the write, so no contact
// point is left behind.
func TestIntegration_ContactPointWrongForTypeKeyIsRejectedBeforeAnyWrite(t *testing.T) {
	client := grafanaClient(t)
	const uid = "formae-integ-cp-wrong-key"
	deleteWhenDone(t, client, uid)

	result, err := (&ContactPointHandler{}).Create(context.Background(), client,
		contactPointProperties(t, uid, "slack", map[string]any{
			"url":            "https://hooks.slack.com/services/T00000000/B00000000/formaeIntegration",
			"recipient":      "#formae-test",
			"integrationKey": "a-pagerduty-key",
		}))
	require.NoError(t, err)

	assert.Equal(t, resource.OperationStatusFailure, result.OperationStatus)
	assert.Equal(t, resource.OperationErrorCodeInvalidRequest, result.ErrorCode)
	assert.Contains(t, result.StatusMessage, `"integrationKey"`)
	assert.Contains(t, result.StatusMessage, `"slack"`, "the rejection names the type the key does not belong to")

	assert.Nil(t, serverSettings(t, client, uid), "the contact point must not have been written")
}

// `url` is secret-classified for every notifier type, because the
// classification is per option name and some type marks it secure - but Grafana
// redacts it only for the types that do. A webhook's `url` is not one of them,
// so Grafana returns the real value, the plugin reports it, and it is
// drift-checked like any other key: an out-of-band edit to a webhook's endpoint
// is detected, where the same key on a Slack contact point is not.
func TestIntegration_ContactPointWebhookURLIsReportedBecauseGrafanaDoesNotRedactIt(t *testing.T) {
	client := grafanaClient(t)
	const uid = "formae-integ-cp-webhook-url"
	const endpoint = "https://example.com/formae-integration-webhook"

	created := createContactPoint(t, client, uid, "webhook", map[string]any{
		"url":      endpoint,
		"username": "formae",
	})

	reported := pluginSettings(t, created.ResourceProperties)
	assert.Equal(t, endpoint, reported["url"], "a webhook url is not redacted, so it is reported")
	assert.Equal(t, endpoint, serverSettings(t, client, uid)["url"])
}
