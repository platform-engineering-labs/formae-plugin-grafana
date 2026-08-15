// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package config

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
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
		{name: "IPv6 literal", proxyURL: "http://[::1]:3128"},
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

	assert.Equal(t, "(redacted)", redactProxyURL("://proxy-user:hunter2@nope"))
}

// proxiedTargetHost is the host every proxied target names. The .invalid
// top-level domain is reserved and never resolves, so a request reaching the
// stub Grafana proves the hostname travelled to the proxy unresolved rather
// than being looked up on this machine.
const proxiedTargetHost = "grafana.example.invalid:3000"

// TestNewClient_SOCKS5ProxyCarriesRequestUnresolved verifies that a target with
// a socks5 proxy URL sends its requests through that proxy, asking it for the
// target's hostname rather than for an address resolved locally.
func TestNewClient_SOCKS5ProxyCarriesRequestUnresolved(t *testing.T) {
	t.Setenv("GRAFANA_AUTH", "glsa_proxiedtoken")

	rec := newAuthRecorder(t)
	proxy := newSocksProxy(t, rec.server.Listener.Addr().String())

	cfg, err := hydrate(&TargetConfig{
		Type:     "Grafana",
		URL:      "http://" + proxiedTargetHost,
		ProxyURL: "socks5://" + proxy.addr(),
	})
	require.NoError(t, err)

	client, err := NewClient(cfg)
	require.NoError(t, err)
	rec.call(t, client)

	select {
	case requested := <-proxy.requested:
		assert.Equal(t, proxiedTargetHost, requested,
			"the proxy is asked for the target's hostname and port, unresolved")
	default:
		t.Fatal("the SOCKS5 proxy was never asked to connect anywhere")
	}
}

// TestNewClient_HTTPProxyCarriesRequest verifies that a target with an http
// proxy URL sends its requests to that proxy in the absolute form an HTTP proxy
// is addressed with, naming the target host.
func TestNewClient_HTTPProxyCarriesRequest(t *testing.T) {
	t.Setenv("GRAFANA_AUTH", "glsa_proxiedtoken")

	rec := newAuthRecorder(t)
	proxy := newHTTPProxy(t, rec.server.URL)

	cfg, err := hydrate(&TargetConfig{
		Type:     "Grafana",
		URL:      "http://" + proxiedTargetHost,
		ProxyURL: proxy.server.URL,
	})
	require.NoError(t, err)

	client, err := NewClient(cfg)
	require.NoError(t, err)
	rec.call(t, client)

	select {
	case requestURI := <-proxy.requestURI:
		assert.True(t, strings.HasPrefix(requestURI, "http://"+proxiedTargetHost+"/api/folders"),
			"the proxy receives an absolute-form request URI naming the target host, got %q", requestURI)
	default:
		t.Fatal("the HTTP proxy never received a request")
	}
}

// SOCKS5 wire constants, limited to the no-auth CONNECT exchange these tests
// need.
const (
	socksVersion5       = 0x05
	socksAuthNone       = 0x00
	socksCmdConnect     = 0x01
	socksAddrTypeIPv4   = 0x01
	socksAddrTypeDomain = 0x03
	socksReplySucceeded = 0x00
)

// socksHandshakeTimeout bounds the greeting and connect exchange, and
// socksTunnelTimeout the bytes flowing afterwards, so a client that speaks
// something else fails the test instead of hanging it.
const (
	socksHandshakeTimeout = 10 * time.Second
	socksTunnelTimeout    = 30 * time.Second
)

// socksProxy is a SOCKS5 proxy that records the address it is asked to connect
// to and then connects the caller to a fixed backend, whatever was requested.
// It implements no-auth method negotiation and a CONNECT request carrying a
// domain name, and nothing else: any other exchange is a test failure.
type socksProxy struct {
	listener  net.Listener
	requested chan string

	mu       sync.Mutex
	conns    []net.Conn
	shutdown bool
}

func newSocksProxy(t *testing.T, backendAddr string) *socksProxy {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	proxy := &socksProxy{listener: listener, requested: make(chan string, 1)}

	var served sync.WaitGroup
	served.Add(1)
	go func() {
		defer served.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			if !proxy.track(conn) {
				_ = conn.Close()
				return
			}
			served.Add(1)
			go func() {
				defer served.Done()
				defer func() { _ = conn.Close() }()
				proxy.serve(t, conn, backendAddr)
			}()
		}
	}()

	t.Cleanup(func() {
		proxy.mu.Lock()
		proxy.shutdown = true
		conns := proxy.conns
		proxy.conns = nil
		proxy.mu.Unlock()

		_ = listener.Close()
		for _, conn := range conns {
			_ = conn.Close()
		}
		served.Wait()
	})

	return proxy
}

// addr is the address a proxy URL points at this proxy with.
func (p *socksProxy) addr() string {
	return p.listener.Addr().String()
}

// track registers conn for closing when the test ends, reporting false once the
// proxy is shutting down so the caller closes it straight away instead.
func (p *socksProxy) track(conn net.Conn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.shutdown {
		return false
	}
	p.conns = append(p.conns, conn)
	return true
}

// fail reports a deviation from the expected exchange as a test failure. It is
// called from the proxy's own goroutines, so it uses Errorf rather than Fatalf.
// Failures seen while the proxy is being torn down are expected and dropped.
func (p *socksProxy) fail(t *testing.T, format string, args ...any) {
	p.mu.Lock()
	shutdown := p.shutdown
	p.mu.Unlock()
	if shutdown {
		return
	}
	t.Errorf(format, args...)
}

// serve runs the exchange for one client: method negotiation, a CONNECT request
// whose address is recorded, a success reply, then bytes piped to backendAddr.
// Every fixed-length field is read in full, and any deviation closes the
// connection so the client fails immediately rather than waiting on the proxy.
func (p *socksProxy) serve(t *testing.T, conn net.Conn, backendAddr string) {
	if err := conn.SetDeadline(time.Now().Add(socksHandshakeTimeout)); err != nil {
		p.fail(t, "socks proxy: setting the handshake read and write deadline: %v", err)
		return
	}

	greeting := make([]byte, 2) // version, number of offered methods
	if _, err := io.ReadFull(conn, greeting); err != nil {
		p.fail(t, "socks proxy: reading the greeting header: %v", err)
		return
	}
	if greeting[0] != socksVersion5 {
		p.fail(t, "socks proxy: greeting version = %#x, want %#x", greeting[0], socksVersion5)
		return
	}
	methods := make([]byte, greeting[1])
	if _, err := io.ReadFull(conn, methods); err != nil {
		p.fail(t, "socks proxy: reading the %d offered authentication methods: %v", greeting[1], err)
		return
	}
	if !slices.Contains(methods, byte(socksAuthNone)) {
		p.fail(t, "socks proxy: offered authentication methods %#x, want no-auth (%#x) among them", methods, socksAuthNone)
		return
	}
	if _, err := conn.Write([]byte{socksVersion5, socksAuthNone}); err != nil {
		p.fail(t, "socks proxy: writing the selected authentication method: %v", err)
		return
	}

	request := make([]byte, 4) // version, command, reserved, address type
	if _, err := io.ReadFull(conn, request); err != nil {
		p.fail(t, "socks proxy: reading the request header: %v", err)
		return
	}
	if request[0] != socksVersion5 {
		p.fail(t, "socks proxy: request version = %#x, want %#x", request[0], socksVersion5)
		return
	}
	if request[1] != socksCmdConnect {
		p.fail(t, "socks proxy: request command = %#x, want connect (%#x)", request[1], socksCmdConnect)
		return
	}
	if request[3] != socksAddrTypeDomain {
		p.fail(t, "socks proxy: request address type = %#x, want a domain name (%#x)", request[3], socksAddrTypeDomain)
		return
	}

	hostLen := make([]byte, 1)
	if _, err := io.ReadFull(conn, hostLen); err != nil {
		p.fail(t, "socks proxy: reading the domain name length: %v", err)
		return
	}
	host := make([]byte, hostLen[0])
	if _, err := io.ReadFull(conn, host); err != nil {
		p.fail(t, "socks proxy: reading a %d byte domain name: %v", hostLen[0], err)
		return
	}
	port := make([]byte, 2)
	if _, err := io.ReadFull(conn, port); err != nil {
		p.fail(t, "socks proxy: reading the port: %v", err)
		return
	}
	select {
	case p.requested <- net.JoinHostPort(string(host), strconv.Itoa(int(binary.BigEndian.Uint16(port)))):
	default:
	}

	backend, err := net.Dial("tcp", backendAddr)
	if err != nil {
		p.fail(t, "socks proxy: dialing the stub Grafana at %s: %v", backendAddr, err)
		return
	}
	if !p.track(backend) {
		_ = backend.Close()
		return
	}

	// A success reply, whose bound address the client does not use.
	reply := []byte{socksVersion5, socksReplySucceeded, 0x00, socksAddrTypeIPv4, 0, 0, 0, 0, 0, 0}
	if _, err := conn.Write(reply); err != nil {
		p.fail(t, "socks proxy: writing the success reply: %v", err)
		return
	}

	if err := conn.SetDeadline(time.Now().Add(socksTunnelTimeout)); err != nil {
		p.fail(t, "socks proxy: setting the tunnel read and write deadline: %v", err)
		return
	}
	if err := backend.SetDeadline(time.Now().Add(socksTunnelTimeout)); err != nil {
		p.fail(t, "socks proxy: setting the backend read and write deadline: %v", err)
		return
	}

	// Either direction ending tears down both, so neither copy outlives the
	// exchange.
	closeBoth := func() {
		_ = conn.Close()
		_ = backend.Close()
	}
	var piped sync.WaitGroup
	piped.Add(2)
	go func() {
		defer piped.Done()
		_, _ = io.Copy(backend, conn)
		closeBoth()
	}()
	go func() {
		defer piped.Done()
		_, _ = io.Copy(conn, backend)
		closeBoth()
	}()
	piped.Wait()
}

// httpProxy is an HTTP proxy that records the request URI it was addressed with
// and forwards the request to a fixed backend.
type httpProxy struct {
	server     *httptest.Server
	requestURI chan string
}

func newHTTPProxy(t *testing.T, backendURL string) *httpProxy {
	t.Helper()

	backend, err := url.Parse(backendURL)
	require.NoError(t, err)

	proxy := &httpProxy{requestURI: make(chan string, 1)}
	// A transport of its own, so forwarding never consults the ambient proxy
	// environment variables.
	forwarding := &http.Transport{}
	forwarder := &http.Client{Transport: forwarding}
	t.Cleanup(forwarding.CloseIdleConnections)

	proxy.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case proxy.requestURI <- r.RequestURI:
		default:
		}
		if !strings.HasPrefix(r.RequestURI, "http://") {
			http.Error(w, "a proxy is addressed with an absolute-form request URI", http.StatusBadRequest)
			return
		}

		forwarded := *backend
		forwarded.Path = r.URL.Path
		forwarded.RawQuery = r.URL.RawQuery
		out, err := http.NewRequestWithContext(r.Context(), r.Method, forwarded.String(), r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		out.Header = r.Header.Clone()

		resp, err := forwarder.Do(out)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer func() { _ = resp.Body.Close() }()

		for name, values := range resp.Header {
			for _, value := range values {
				w.Header().Add(name, value)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	t.Cleanup(proxy.server.Close)

	return proxy
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
