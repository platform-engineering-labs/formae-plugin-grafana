// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package config

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTargetConfig_BasicFields(t *testing.T) {
	raw := json.RawMessage(`{"Type":"Grafana","Url":"https://grafana.example.com","OrgId":2}`)
	cfg, err := ParseTargetConfig(raw)
	require.NoError(t, err)
	assert.Equal(t, "https://grafana.example.com", cfg.URL)
	assert.NotNil(t, cfg.OrgID)
	assert.Equal(t, int64(2), *cfg.OrgID)
}

func TestParseTargetConfig_CredentialFields(t *testing.T) {
	raw := json.RawMessage(`{"Type":"Grafana","Url":"https://grafana.example.com","Username":"admin","Password":"secret"}`)
	cfg, err := ParseTargetConfig(raw)
	require.NoError(t, err)
	assert.Equal(t, "admin", cfg.Username)
	assert.Equal(t, "secret", cfg.Password)
}

func TestParseTargetConfig_MissingUrl(t *testing.T) {
	raw := json.RawMessage(`{"Type":"Grafana"}`)
	_, err := ParseTargetConfig(raw)
	require.Error(t, err)
}

// TestNewClient_ConfigCredentials verifies that when both Username and Password
// are present in the target config the client is constructed using those values
// for basic auth, without consulting GRAFANA_AUTH.
func TestNewClient_ConfigCredentials(t *testing.T) {
	os.Unsetenv("GRAFANA_AUTH")

	cfg := &TargetConfig{
		Type:     "Grafana",
		URL:      "https://grafana.example.com",
		Username: "admin",
		Password: "secret",
	}
	client, err := NewClient(cfg)
	require.NoError(t, err)
	require.NotNil(t, client)
}

// TestNewClient_EnvFallback verifies that when no config credentials are set
// the client falls back to GRAFANA_AUTH for a bearer-token flow.
func TestNewClient_EnvFallback(t *testing.T) {
	t.Setenv("GRAFANA_AUTH", "glsa_someserviceaccounttoken")

	cfg := &TargetConfig{
		Type: "Grafana",
		URL:  "https://grafana.example.com",
	}
	client, err := NewClient(cfg)
	require.NoError(t, err)
	require.NotNil(t, client)
}

// TestNewClient_EnvFallback_BasicAuth verifies the env-var basic-auth path
// (user:password format) still works when no config credentials are set.
func TestNewClient_EnvFallback_BasicAuth(t *testing.T) {
	t.Setenv("GRAFANA_AUTH", "admin:admin")

	cfg := &TargetConfig{
		Type: "Grafana",
		URL:  "https://grafana.example.com",
	}
	client, err := NewClient(cfg)
	require.NoError(t, err)
	require.NotNil(t, client)
}

// TestNewClient_NoCreds verifies that an error is returned when neither config
// credentials nor GRAFANA_AUTH are available.
func TestNewClient_NoCreds(t *testing.T) {
	os.Unsetenv("GRAFANA_AUTH")

	cfg := &TargetConfig{
		Type: "Grafana",
		URL:  "https://grafana.example.com",
	}
	_, err := NewClient(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no credentials")
}

// TestNewClient_PartialConfigCreds verifies that only setting one of Username/
// Password falls through to the env-var path.
func TestNewClient_PartialConfigCreds_FallsBackToEnv(t *testing.T) {
	t.Setenv("GRAFANA_AUTH", "admin:admin")

	cfg := &TargetConfig{
		Type:     "Grafana",
		URL:      "https://grafana.example.com",
		Username: "admin",
		// Password intentionally absent
	}
	client, err := NewClient(cfg)
	require.NoError(t, err)
	require.NotNil(t, client)
}
