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
	"time"

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

// TestParseTargetConfig_ProxyURL verifies that the ProxyUrl wire field the
// schema emits binds to the ProxyURL config field.
func TestParseTargetConfig_ProxyURL(t *testing.T) {
	raw := json.RawMessage(`{"Type":"Grafana","Url":"https://grafana.example.com","ProxyUrl":"socks5://proxy.example.com:1080"}`)
	cfg, err := ParseTargetConfig(raw)
	require.NoError(t, err)
	assert.Equal(t, "socks5://proxy.example.com:1080", cfg.ProxyURL)
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

// TestNewHTTPClient_NoProxyURL verifies that a target without a proxy URL gets
// the same client as before the field existed: no explicit transport, so
// net/http's default applies, and a 30 second timeout.
func TestNewHTTPClient_NoProxyURL(t *testing.T) {
	client, err := newHTTPClient(&TargetConfig{Type: "Grafana", URL: "https://grafana.example.com"})
	require.NoError(t, err)
	require.NotNil(t, client)

	assert.Nil(t, client.Transport, "an unset proxy URL leaves the transport to net/http")
	assert.Equal(t, 30*time.Second, client.Timeout)
}

// TestNewHTTPClient_ProxyURLIgnoresEnvironment verifies that a configured proxy
// URL is used for every request regardless of the ambient proxy environment
// variables, and that the transport carrying it keeps the default transport's
// dialing, pooling and HTTP/2 settings.
func TestNewHTTPClient_ProxyURLIgnoresEnvironment(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://environment-proxy.example.com:3128")
	t.Setenv("HTTPS_PROXY", "http://environment-proxy.example.com:3128")
	t.Setenv("NO_PROXY", "*")

	const proxyURL = "socks5://proxy.example.com:1080"
	client, err := newHTTPClient(&TargetConfig{
		Type:     "Grafana",
		URL:      "https://grafana.example.com",
		ProxyURL: proxyURL,
	})
	require.NoError(t, err)
	require.NotNil(t, client)
	assert.Equal(t, 30*time.Second, client.Timeout)

	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok, "a configured proxy URL installs an *http.Transport")
	require.NotNil(t, transport.Proxy)

	for _, target := range []string{
		"http://grafana.example.com/api/folders",
		"https://grafana.example.com/api/folders",
		"http://127.0.0.1:3000/api/folders",
	} {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
		require.NoError(t, err)

		resolved, err := transport.Proxy(req)
		require.NoError(t, err)
		require.NotNil(t, resolved, "%s goes through the configured proxy whatever NO_PROXY says", target)
		assert.Equal(t, proxyURL, resolved.String())
	}

	defaults, ok := http.DefaultTransport.(*http.Transport)
	require.True(t, ok)
	assert.NotSame(t, defaults, transport,
		"the default transport is cloned, not mutated, so one target's proxy cannot capture every other target's requests")
	assert.True(t, transport.ForceAttemptHTTP2, "the clone keeps HTTP/2 negotiation")
	assert.NotZero(t, transport.MaxIdleConns, "the clone keeps an idle connection pool")
	assert.NotZero(t, transport.IdleConnTimeout, "the clone keeps an idle connection timeout")
	assert.NotZero(t, transport.TLSHandshakeTimeout, "the clone keeps a TLS handshake timeout")
	assert.Equal(t, defaults.MaxIdleConns, transport.MaxIdleConns)
	assert.Equal(t, defaults.IdleConnTimeout, transport.IdleConnTimeout)
	assert.Equal(t, defaults.TLSHandshakeTimeout, transport.TLSHandshakeTimeout)
	assert.Equal(t, defaults.ExpectContinueTimeout, transport.ExpectContinueTimeout)
}

// TestNewHTTPClient_AcceptedProxyURLs verifies that every advertised proxy
// scheme is accepted and reaches the transport unchanged.
func TestNewHTTPClient_AcceptedProxyURLs(t *testing.T) {
	cases := []struct {
		name     string
		proxyURL string
	}{
		{name: "socks5", proxyURL: "socks5://proxy.example.com:1080"},
		{name: "socks5h", proxyURL: "socks5h://proxy.example.com:1080"},
		{name: "http", proxyURL: "http://proxy.example.com:3128"},
		{name: "root path", proxyURL: "socks5://proxy.example.com:1080/"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, err := newHTTPClient(&TargetConfig{
				Type:     "Grafana",
				URL:      "https://grafana.example.com",
				ProxyURL: tc.proxyURL,
			})
			require.NoError(t, err)

			transport, ok := client.Transport.(*http.Transport)
			require.True(t, ok)
			require.NotNil(t, transport.Proxy)

			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://grafana.example.com/api/folders", nil)
			require.NoError(t, err)
			resolved, err := transport.Proxy(req)
			require.NoError(t, err)
			require.NotNil(t, resolved)
			assert.Equal(t, tc.proxyURL, resolved.String())
		})
	}
}

// TestNewClient_RejectsInvalidProxyURL verifies that a malformed or unsupported
// proxy URL fails client construction, that the failure names the proxy rather
// than the missing credentials the config also has, and that neither a
// configured proxy password nor the raw configured value reaches the message.
func TestNewClient_RejectsInvalidProxyURL(t *testing.T) {
	const password = "hunter2"
	unparseableWithPassword := "://proxy-user:" + password + "@nope"
	credentialProxyURL := "socks5://proxy-user:" + password + "@proxy.example.com:1080"

	cases := []struct {
		name        string
		proxyURL    string
		contains    []string
		notContains []string
	}{
		{
			name:        "unparseable",
			proxyURL:    "://nope",
			contains:    []string{"proxy"},
			notContains: []string{"://nope"},
		},
		{
			name:        "unparseable carrying credentials",
			proxyURL:    unparseableWithPassword,
			contains:    []string{"proxy"},
			notContains: []string{password, unparseableWithPassword},
		},
		{
			name:     "unsupported scheme ftp",
			proxyURL: "ftp://proxy.example.com:1080",
			contains: []string{"ftp", "socks5", "socks5h", "http"},
		},
		{
			name:     "unsupported scheme https",
			proxyURL: "https://proxy.example.com:1080",
			contains: []string{"https", "socks5", "socks5h", "http"},
		},
		{
			name:     "scheme omitted",
			proxyURL: "proxy.example.com:1055",
			contains: []string{"socks5", "socks5h", "http"},
		},
		{
			name:     "empty host",
			proxyURL: "socks5://",
			contains: []string{"host", "socks5", "socks5h", "http"},
		},
		{
			name:     "empty host with a port",
			proxyURL: "socks5://:1080",
			contains: []string{"host", "socks5", "socks5h", "http"},
		},
		{
			name:     "empty host with a port over http",
			proxyURL: "http://:3128",
			contains: []string{"host", "socks5", "socks5h", "http"},
		},
		{
			name:        "credentials",
			proxyURL:    credentialProxyURL,
			contains:    []string{"credentials"},
			notContains: []string{password, credentialProxyURL},
		},
		{
			name:        "stray path",
			proxyURL:    "socks5://proxy.example.com:1080/socks",
			contains:    []string{"path"},
			notContains: []string{"/socks"},
		},
		{
			name:        "stray query",
			proxyURL:    "socks5://proxy.example.com:1080?resolve=remote",
			contains:    []string{"query"},
			notContains: []string{"resolve=remote"},
		},
		{
			name:        "stray fragment",
			proxyURL:    "socks5://proxy.example.com:1080#socks",
			contains:    []string{"fragment"},
			notContains: []string{"#socks"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GRAFANA_AUTH", "")

			cfg, err := hydrate(&TargetConfig{
				Type:     "Grafana",
				URL:      "https://grafana.example.com",
				ProxyURL: tc.proxyURL,
			})
			require.NoError(t, err)

			_, err = NewClient(cfg)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "no credentials",
				"the proxy URL is validated before credentials, so it is not masked by a missing one")
			for _, want := range tc.contains {
				assert.Contains(t, err.Error(), want)
			}
			for _, unwanted := range tc.notContains {
				assert.NotContains(t, err.Error(), unwanted)
			}
		})
	}
}

// TestRedactProxyURL verifies that redaction reduces a proxy URL to its scheme
// and host, dropping every part that can carry a configured value, and
// withholds a value it cannot parse.
func TestRedactProxyURL(t *testing.T) {
	cases := []struct {
		name     string
		rawURL   string
		expected string
	}{
		{
			name:     "credential-free URL is unchanged",
			rawURL:   "socks5://proxy.example.com:1080",
			expected: "socks5://proxy.example.com:1080",
		},
		{
			name:     "userinfo dropped",
			rawURL:   "socks5://proxy-user:hunter2@proxy.example.com:1080",
			expected: "socks5://proxy.example.com:1080",
		},
		{
			name:     "path dropped",
			rawURL:   "socks5://proxy.example.com:1080/socks",
			expected: "socks5://proxy.example.com:1080",
		},
		{
			name:     "query dropped",
			rawURL:   "http://proxy.example.com:3128?token=hunter2",
			expected: "http://proxy.example.com:3128",
		},
		{
			name:     "fragment dropped",
			rawURL:   "socks5://proxy.example.com:1080#hunter2",
			expected: "socks5://proxy.example.com:1080",
		},
		{
			name:     "every carrier dropped at once",
			rawURL:   "socks5://proxy-user:hunter2@proxy.example.com:1080/socks?token=hunter2#hunter2",
			expected: "socks5://proxy.example.com:1080",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, redactProxyURL(tc.rawURL))
		})
	}

	withheld := redactProxyURL("://proxy-user:hunter2@nope")
	assert.NotContains(t, withheld, "hunter2")
	assert.NotContains(t, withheld, "proxy-user")
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
