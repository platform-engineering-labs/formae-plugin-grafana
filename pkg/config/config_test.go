// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package config

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	goapi "github.com/grafana/grafana-openapi-client-go/client"
	"github.com/grafana/grafana-openapi-client-go/client/folders"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// authRecorder is a stub Grafana that records how the client authenticated on
// the request it received, so a test can assert on the wire format rather than
// on the client object being non-nil.
type authRecorder struct {
	server *httptest.Server

	authorization string
	username      string
	password      string
	hasBasicAuth  bool
}

func newAuthRecorder(t *testing.T) *authRecorder {
	t.Helper()
	rec := &authRecorder{}
	rec.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.authorization = r.Header.Get("Authorization")
		rec.username, rec.password, rec.hasBasicAuth = r.BasicAuth()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(rec.server.Close)
	return rec
}

// call issues one request through the client so the recorder observes the
// credentials the transport attached.
func (rec *authRecorder) call(t *testing.T, client *goapi.GrafanaHTTPAPI) {
	t.Helper()
	_, err := client.Folders.GetFolders(&folders.GetFoldersParams{Context: context.Background()})
	require.NoError(t, err)
}

func TestParseTargetConfig_BasicFields(t *testing.T) {
	raw := json.RawMessage(`{"Type":"Grafana","Url":"https://grafana.example.com","OrgId":2}`)
	cfg, err := ParseTargetConfig(raw)
	require.NoError(t, err)
	assert.Equal(t, "https://grafana.example.com", cfg.URL)
	assert.NotNil(t, cfg.OrgID)
	assert.Equal(t, int64(2), *cfg.OrgID)
	assert.Empty(t, cfg.AuthType(), "a config without an Auth block has no auth strategy")
}

func TestParseTargetConfig_TokenAuth(t *testing.T) {
	raw := json.RawMessage(`{"Type":"Grafana","Url":"https://grafana.example.com","Auth":{"Type":"Token","Token":"glsa_token"}}`)
	cfg, err := ParseTargetConfig(raw)
	require.NoError(t, err)
	assert.Equal(t, "Token", cfg.AuthType())
}

func TestParseTargetConfig_BasicAuth(t *testing.T) {
	raw := json.RawMessage(`{"Type":"Grafana","Url":"https://grafana.example.com","Auth":{"Type":"Basic","Username":"admin","Password":"secret"}}`)
	cfg, err := ParseTargetConfig(raw)
	require.NoError(t, err)
	assert.Equal(t, "Basic", cfg.AuthType())
}

func TestParseTargetConfig_AuthWithoutType(t *testing.T) {
	raw := json.RawMessage(`{"Type":"Grafana","Url":"https://grafana.example.com","Auth":{"Token":"glsa_token"}}`)
	_, err := ParseTargetConfig(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Type")
}

func TestParseTargetConfig_MissingUrl(t *testing.T) {
	raw := json.RawMessage(`{"Type":"Grafana"}`)
	_, err := ParseTargetConfig(raw)
	require.Error(t, err)
}

// TestNewClient_TokenAuth verifies that a Token auth block authenticates with a
// bearer token, which is what a Grafana service account token requires.
func TestNewClient_TokenAuth(t *testing.T) {
	t.Setenv("GRAFANA_AUTH", "")
	rec := newAuthRecorder(t)

	cfg := &TargetConfig{
		Type: "Grafana",
		URL:  rec.server.URL,
		Auth: json.RawMessage(`{"Type":"Token","Token":"glsa_serviceaccounttoken"}`),
	}
	cfg, err := hydrate(cfg)
	require.NoError(t, err)

	client, err := NewClient(cfg)
	require.NoError(t, err)
	rec.call(t, client)

	assert.Equal(t, "Bearer glsa_serviceaccounttoken", rec.authorization)
}

// TestNewClient_BasicAuth verifies that a Basic auth block authenticates with
// the configured username and password.
func TestNewClient_BasicAuth(t *testing.T) {
	t.Setenv("GRAFANA_AUTH", "")
	rec := newAuthRecorder(t)

	cfg := &TargetConfig{
		Type: "Grafana",
		URL:  rec.server.URL,
		Auth: json.RawMessage(`{"Type":"Basic","Username":"admin","Password":"secret"}`),
	}
	cfg, err := hydrate(cfg)
	require.NoError(t, err)

	client, err := NewClient(cfg)
	require.NoError(t, err)
	rec.call(t, client)

	require.True(t, rec.hasBasicAuth)
	assert.Equal(t, "admin", rec.username)
	assert.Equal(t, "secret", rec.password)
}

// TestNewClient_AuthBlockTakesPriorityOverEnv verifies that a configured auth
// block is used even when GRAFANA_AUTH is set, so a target that sources its
// credential from a secret is never silently overridden by the environment.
func TestNewClient_AuthBlockTakesPriorityOverEnv(t *testing.T) {
	t.Setenv("GRAFANA_AUTH", "glsa_envtoken")
	rec := newAuthRecorder(t)

	cfg := &TargetConfig{
		Type: "Grafana",
		URL:  rec.server.URL,
		Auth: json.RawMessage(`{"Type":"Token","Token":"glsa_configtoken"}`),
	}
	cfg, err := hydrate(cfg)
	require.NoError(t, err)

	client, err := NewClient(cfg)
	require.NoError(t, err)
	rec.call(t, client)

	assert.Equal(t, "Bearer glsa_configtoken", rec.authorization)
}

// TestNewClient_TokenAuth_EmptyToken verifies that an explicit Token block with
// no token is a configuration error rather than a silent fall-through to the
// environment.
func TestNewClient_TokenAuth_EmptyToken(t *testing.T) {
	t.Setenv("GRAFANA_AUTH", "glsa_envtoken")

	cfg := &TargetConfig{
		Type: "Grafana",
		URL:  "https://grafana.example.com",
		Auth: json.RawMessage(`{"Type":"Token","Token":""}`),
	}
	cfg, err := hydrate(cfg)
	require.NoError(t, err)

	_, err = NewClient(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Token")
}

// TestNewClient_BasicAuth_MissingPassword verifies that an explicit Basic block
// missing one half of the credential is a configuration error rather than a
// silent fall-through to the environment.
func TestNewClient_BasicAuth_MissingPassword(t *testing.T) {
	t.Setenv("GRAFANA_AUTH", "admin:admin")

	cfg := &TargetConfig{
		Type: "Grafana",
		URL:  "https://grafana.example.com",
		Auth: json.RawMessage(`{"Type":"Basic","Username":"admin"}`),
	}
	cfg, err := hydrate(cfg)
	require.NoError(t, err)

	_, err = NewClient(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Password")
}

// TestNewClient_BasicAuth_MissingUsername verifies that an explicit Basic block
// with no username is a configuration error rather than a silent fall-through
// to the environment.
func TestNewClient_BasicAuth_MissingUsername(t *testing.T) {
	t.Setenv("GRAFANA_AUTH", "admin:admin")

	cfg := &TargetConfig{
		Type: "Grafana",
		URL:  "https://grafana.example.com",
		Auth: json.RawMessage(`{"Type":"Basic","Password":"secret"}`),
	}
	cfg, err := hydrate(cfg)
	require.NoError(t, err)

	_, err = NewClient(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Username")
}

// TestNewClient_UnknownAuthType verifies that an unrecognized auth strategy is
// rejected instead of falling back to the environment.
func TestNewClient_UnknownAuthType(t *testing.T) {
	t.Setenv("GRAFANA_AUTH", "glsa_envtoken")

	cfg := &TargetConfig{
		Type: "Grafana",
		URL:  "https://grafana.example.com",
		Auth: json.RawMessage(`{"Type":"Mtls"}`),
	}
	cfg, err := hydrate(cfg)
	require.NoError(t, err)

	_, err = NewClient(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Mtls")
}

// TestNewClient_EnvFallback_Token verifies that a config without an auth block
// still authenticates with a bearer token from GRAFANA_AUTH.
func TestNewClient_EnvFallback_Token(t *testing.T) {
	t.Setenv("GRAFANA_AUTH", "glsa_someserviceaccounttoken")
	rec := newAuthRecorder(t)

	cfg := &TargetConfig{
		Type: "Grafana",
		URL:  rec.server.URL,
	}
	client, err := NewClient(cfg)
	require.NoError(t, err)
	rec.call(t, client)

	assert.Equal(t, "Bearer glsa_someserviceaccounttoken", rec.authorization)
}

// TestNewClient_EnvFallback_BasicAuth verifies the env-var basic-auth path
// (user:password format) when no auth block is set.
func TestNewClient_EnvFallback_BasicAuth(t *testing.T) {
	t.Setenv("GRAFANA_AUTH", "admin:admin")
	rec := newAuthRecorder(t)

	cfg := &TargetConfig{
		Type: "Grafana",
		URL:  rec.server.URL,
	}
	client, err := NewClient(cfg)
	require.NoError(t, err)
	rec.call(t, client)

	require.True(t, rec.hasBasicAuth)
	assert.Equal(t, "admin", rec.username)
	assert.Equal(t, "admin", rec.password)
}

// TestNewClient_NoCreds verifies that an error is returned when neither an auth
// block nor GRAFANA_AUTH is available.
func TestNewClient_NoCreds(t *testing.T) {
	t.Setenv("GRAFANA_AUTH", "")

	cfg := &TargetConfig{
		Type: "Grafana",
		URL:  "https://grafana.example.com",
	}
	_, err := NewClient(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no credentials")
}

// hydrate round-trips a hand-built TargetConfig through ParseTargetConfig so it
// carries the parsed auth discriminator, the way a config from the engine does.
func hydrate(cfg *TargetConfig) (*TargetConfig, error) {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	return ParseTargetConfig(raw)
}
