//go:build integration

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package config

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	goapi "github.com/grafana/grafana-openapi-client-go/client"
	"github.com/grafana/grafana-openapi-client-go/client/folders"
	"github.com/grafana/grafana-openapi-client-go/client/service_accounts"
	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// grafanaURL returns the URL of the Grafana test instance.
func grafanaURL() string {
	if url := os.Getenv("GRAFANA_URL"); url != "" {
		return url
	}
	return "http://localhost:3333"
}

// adminCredentials splits GRAFANA_AUTH into a username and password, skipping
// the test when it does not hold basic-auth credentials.
func adminCredentials(t *testing.T) (string, string) {
	t.Helper()
	user, pass, ok := strings.Cut(os.Getenv("GRAFANA_AUTH"), ":")
	if !ok || user == "" || pass == "" {
		t.Skip("GRAFANA_AUTH must hold user:password credentials for these tests")
	}
	return user, pass
}

// clientForAuth builds a client for a target config carrying the given raw Auth
// block, going through ParseTargetConfig the way the plugin does.
func clientForAuth(t *testing.T, authBlock string) *goapi.GrafanaHTTPAPI {
	t.Helper()
	raw := json.RawMessage(fmt.Sprintf(`{"Type":"Grafana","Url":%q,"Auth":%s}`, grafanaURL(), authBlock))
	cfg, err := ParseTargetConfig(raw)
	require.NoError(t, err)
	client, err := NewClient(cfg)
	require.NoError(t, err)
	return client
}

// listFolders issues an authenticated call, which Grafana rejects with 401 when
// the credentials are missing or wrong.
func listFolders(client *goapi.GrafanaHTTPAPI) error {
	_, err := client.Folders.GetFolders(&folders.GetFoldersParams{Context: context.Background()})
	return err
}

// TestIntegration_BasicAuthBlock verifies that a BasicAuth block authenticates
// against a real Grafana.
func TestIntegration_BasicAuthBlock(t *testing.T) {
	user, pass := adminCredentials(t)

	client := clientForAuth(t, fmt.Sprintf(`{"Type":"Basic","Username":%q,"Password":%q}`, user, pass))
	require.NoError(t, listFolders(client))
}

// TestIntegration_BasicAuthBlock_WrongPassword verifies that the credentials in
// the block are the ones actually presented — a wrong password must be
// rejected rather than papered over by another credential source.
func TestIntegration_BasicAuthBlock_WrongPassword(t *testing.T) {
	user, _ := adminCredentials(t)

	client := clientForAuth(t, fmt.Sprintf(`{"Type":"Basic","Username":%q,"Password":"not-the-password"}`, user))
	require.Error(t, listFolders(client))
}

// TestIntegration_TokenAuthBlock verifies that a service account token supplied
// through the config authenticates against a real Grafana, which is the path
// that previously existed only via the GRAFANA_AUTH environment variable.
func TestIntegration_TokenAuthBlock(t *testing.T) {
	token := mintServiceAccountToken(t, "Viewer")

	client := clientForAuth(t, fmt.Sprintf(`{"Type":"Token","Token":%q}`, token))
	require.NoError(t, listFolders(client))
}

// TestIntegration_TokenAuthBlock_RevokedToken verifies that the token in the
// block is the credential actually presented: once its service account is gone
// the call fails instead of falling back to the environment.
func TestIntegration_TokenAuthBlock_RevokedToken(t *testing.T) {
	adminUser, adminPass := adminCredentials(t)
	t.Setenv("GRAFANA_AUTH", fmt.Sprintf("%s:%s", adminUser, adminPass))

	token, revoke := mintRevocableServiceAccountToken(t, "Viewer")
	client := clientForAuth(t, fmt.Sprintf(`{"Type":"Token","Token":%q}`, token))
	require.NoError(t, listFolders(client))

	revoke()
	assert.Error(t, listFolders(client), "a revoked token must not fall back to GRAFANA_AUTH")
}

// mintServiceAccountToken creates a service account with the given role and
// returns a fresh token for it, removing the account when the test ends.
func mintServiceAccountToken(t *testing.T, role string) string {
	t.Helper()
	token, _ := mintRevocableServiceAccountToken(t, role)
	return token
}

// mintRevocableServiceAccountToken is mintServiceAccountToken plus a revoke
// func that deletes the service account early. Calling revoke twice is safe.
func mintRevocableServiceAccountToken(t *testing.T, role string) (string, func()) {
	t.Helper()
	user, pass := adminCredentials(t)
	admin := clientForAuth(t, fmt.Sprintf(`{"Type":"Basic","Username":%q,"Password":%q}`, user, pass))

	created, err := admin.ServiceAccounts.CreateServiceAccount(&service_accounts.CreateServiceAccountParams{
		Context: context.Background(),
		Body: &models.CreateServiceAccountForm{
			Name: fmt.Sprintf("formae-config-test-%s", t.Name()),
			Role: role,
		},
	})
	require.NoError(t, err)
	accountID := created.GetPayload().ID

	revoked := false
	revoke := func() {
		if revoked {
			return
		}
		revoked = true
		_, err := admin.ServiceAccounts.DeleteServiceAccount(accountID)
		require.NoError(t, err)
	}
	t.Cleanup(revoke)

	tokenResp, err := admin.ServiceAccounts.CreateToken(&service_accounts.CreateTokenParams{
		Context:          context.Background(),
		ServiceAccountID: accountID,
		Body:             &models.AddServiceAccountTokenCommand{Name: "formae-config-test"},
	})
	require.NoError(t, err)
	token := tokenResp.GetPayload().Key
	require.NotEmpty(t, token)

	return token, revoke
}
