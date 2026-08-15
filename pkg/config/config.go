// Package config handles Grafana target configuration and client creation.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	goapi "github.com/grafana/grafana-openapi-client-go/client"
	"github.com/go-openapi/strfmt"
	httptransport "github.com/go-openapi/runtime/client"
)

// Auth strategy discriminators, matching the Type of each Auth subclass in
// schema/pkl/grafana.pkl.
const (
	AuthTypeToken = "Token"
	AuthTypeBasic = "Basic"
)

// TargetConfig holds Grafana target settings from the forma file.
//
// The Url field receives the resolved value from the formae engine. When the
// PKL config uses a resolvable (e.g., lgtmStack.res.endpoints.at("lgtm:3000")),
// formae resolves it to a plain URL string before passing it to the plugin.
//
// Auth carries the credential block, kept raw so it can be dispatched on its
// Type discriminator. Every credential inside it may originate from a
// formae-managed secret resolved live by the agent (no restart required). When
// Auth is absent the plugin falls back to the GRAFANA_AUTH environment
// variable.
//
// ProxyURL, when set, sends this target's HTTP traffic through the named
// socks5, socks5h or http proxy, and the ambient HTTP_PROXY, HTTPS_PROXY and
// NO_PROXY variables are ignored for it. When it is unset those variables are
// honoured as before.
//
// Deprecated: Endpoints and EndpointKey are superseded by collection resolvables
// (MappingResolvable.at()). Use url = stack.res.endpoints.at("key") instead.
// These fields will be removed in a future release.
type TargetConfig struct {
	Type        string            `json:"Type"`
	URL         string            `json:"Url,omitempty"`
	OrgID       *int64            `json:"OrgId,omitempty"`
	Auth        json.RawMessage   `json:"Auth,omitempty"`
	ProxyURL    string            `json:"ProxyUrl,omitempty"`
	Endpoints   map[string]string `json:"Endpoints,omitempty"`   // Deprecated: use resolvable url instead
	EndpointKey string            `json:"EndpointKey,omitempty"` // Deprecated: use resolvable url instead
}

// authHeader extracts just the discriminator from an Auth block.
type authHeader struct {
	Type string `json:"Type"`
}

// tokenAuthConfig holds the fields of a Token auth block: a Grafana service
// account token or a legacy API key, sent as a bearer token.
type tokenAuthConfig struct {
	Token string `json:"Token"`
}

// basicAuthConfig holds the fields of a Basic auth block.
type basicAuthConfig struct {
	Username string `json:"Username"`
	Password string `json:"Password"`
}

// HasAuth reports whether the target config carries a credential block.
func (c *TargetConfig) HasAuth() bool {
	return len(c.Auth) > 0 && string(c.Auth) != "null"
}

// AuthType returns the auth strategy discriminator, or "" when the config has
// no Auth block (or one whose Type is absent or unreadable).
func (c *TargetConfig) AuthType() string {
	if !c.HasAuth() {
		return ""
	}
	var header authHeader
	if err := json.Unmarshal(c.Auth, &header); err != nil {
		return ""
	}
	return header.Type
}

// ParseTargetConfig deserializes target configuration from JSON.
func ParseTargetConfig(data json.RawMessage) (*TargetConfig, error) {
	var cfg TargetConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid target config: %w", err)
	}

	// Resolve URL from endpoints mapping if direct URL not set
	if cfg.URL == "" && cfg.Endpoints != nil && cfg.EndpointKey != "" {
		if endpoint, ok := cfg.Endpoints[cfg.EndpointKey]; ok {
			cfg.URL = endpoint
		}
	}

	if cfg.URL == "" {
		return nil, fmt.Errorf("target config missing 'Url' (or 'Endpoints'+'EndpointKey')")
	}

	if cfg.HasAuth() {
		var header authHeader
		if err := json.Unmarshal(cfg.Auth, &header); err != nil {
			return nil, fmt.Errorf("invalid target config Auth block: %w", err)
		}
		if header.Type == "" {
			return nil, fmt.Errorf("target config Auth block missing required Type field")
		}
	}
	return &cfg, nil
}

// NewClient creates a Grafana API client from target config and credentials.
//
// Credential resolution:
//  1. Config-level Auth block, when present. A Token block becomes a bearer
//     token, a Basic block becomes basic auth. Values arrive pre-resolved from
//     the formae engine, so either may be sourced from a secret. An incomplete
//     or unrecognized block is an error — it never falls through to the
//     environment, so a broken secret reference cannot silently downgrade the
//     target to whatever credential the agent happens to carry.
//  2. Environment variable fallback: GRAFANA_AUTH is consulted when the config
//     has no Auth block. It accepts a service account token (glsa_…), an API
//     key (eyJ…), or "user:password" for basic auth.
//
// An error is returned when the config has no Auth block and GRAFANA_AUTH is
// unset, or when ProxyURL is not a usable proxy URL — the proxy is validated
// first, so a broken one is reported as such rather than as a credential
// problem.
func NewClient(cfg *TargetConfig) (*goapi.GrafanaHTTPAPI, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid Grafana URL %q: %w", cfg.URL, err)
	}

	host := u.Host
	scheme := u.Scheme
	if scheme == "" {
		scheme = "https"
	}

	basePath := u.Path
	if basePath == "" || basePath == "/" {
		basePath = "/api"
	} else {
		basePath = strings.TrimSuffix(basePath, "/") + "/api"
	}

	transportCfg := &goapi.TransportConfig{
		Host:     host,
		BasePath: basePath,
		Schemes:  []string{scheme},
	}

	httpClient, err := newHTTPClient(cfg)
	if err != nil {
		return nil, err
	}

	if err := applyCredentials(cfg, transportCfg); err != nil {
		return nil, err
	}

	if cfg.OrgID != nil {
		transportCfg.OrgID = *cfg.OrgID
	}

	transport := httptransport.NewWithClient(transportCfg.Host, transportCfg.BasePath, transportCfg.Schemes, httpClient)
	if transportCfg.BasicAuth != nil {
		password, _ := transportCfg.BasicAuth.Password()
		transport.DefaultAuthentication = httptransport.BasicAuth(transportCfg.BasicAuth.Username(), password)
	} else if transportCfg.APIKey != "" {
		transport.DefaultAuthentication = httptransport.BearerToken(transportCfg.APIKey)
	}
	if transportCfg.OrgID > 0 {
		transport.DefaultMediaType = "application/json"
	}
	client := goapi.New(transport, transportCfg, strfmt.Default)
	return client, nil
}

// newHTTPClient builds the HTTP client the Grafana API client runs on.
//
// Without a proxy URL the client carries no explicit transport, so net/http's
// default is used — including its reading of HTTP_PROXY, HTTPS_PROXY and
// NO_PROXY. With one, an explicit transport pins every request of this target
// to that proxy instead.
func newHTTPClient(cfg *TargetConfig) (*http.Client, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	if cfg.ProxyURL == "" {
		return client, nil
	}

	transport, err := proxyTransport(cfg.ProxyURL)
	if err != nil {
		return nil, err
	}
	client.Transport = transport
	return client, nil
}

// proxySchemes are the URL schemes accepted for a proxy URL. socks5 and socks5h
// are aliases in Go: the hostname always reaches the proxy unresolved.
var proxySchemes = []string{"socks5", "socks5h", "http"}

// proxyTransport returns a transport routing every request through rawURL,
// which must name one of proxySchemes plus a host and carry nothing else.
func proxyTransport(rawURL string) (*http.Transport, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		// url.Parse embeds its input in the error, and that input may carry a
		// credential, so only the cause is reported.
		var parseErr *url.Error
		if errors.As(err, &parseErr) {
			err = parseErr.Err
		}
		return nil, fmt.Errorf("invalid proxy URL: %w", err)
	}
	if !slices.Contains(proxySchemes, u.Scheme) {
		return nil, fmt.Errorf("unsupported proxy URL scheme %q: expected one of %s",
			u.Scheme, strings.Join(proxySchemes, ", "))
	}
	if u.Host == "" {
		return nil, fmt.Errorf("proxy URL must include a host: expected one of %s followed by a host and port",
			strings.Join(proxySchemes, ", "))
	}
	if u.User != nil {
		return nil, fmt.Errorf("proxy URL must not carry credentials: %s", redactProxyURL(rawURL))
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("proxy URL must not carry a path, query or fragment: %s", redactProxyURL(rawURL))
	}

	// Clone the default transport so a proxied target keeps its dial,
	// keep-alive, TLS handshake and idle-pool settings; fall back to a bare
	// transport if something has replaced the default with another type.
	transport := &http.Transport{}
	if defaults, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = defaults.Clone()
	}
	transport.Proxy = http.ProxyURL(u)
	return transport, nil
}

// redactProxyURL returns rawURL with any userinfo removed, so a configured
// proxy credential cannot reach an error message, a log or recorded state. A
// value that does not parse is withheld entirely, since its userinfo cannot be
// located.
func redactProxyURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "(redacted)"
	}
	u.User = nil
	return u.String()
}

// applyCredentials sets the credential fields on transportCfg from the target
// config's Auth block, or from GRAFANA_AUTH when there is no Auth block.
func applyCredentials(cfg *TargetConfig, transportCfg *goapi.TransportConfig) error {
	if !cfg.HasAuth() {
		return applyEnvCredentials(transportCfg)
	}

	switch authType := cfg.AuthType(); authType {
	case AuthTypeToken:
		var tokenAuth tokenAuthConfig
		if err := json.Unmarshal(cfg.Auth, &tokenAuth); err != nil {
			return fmt.Errorf("invalid %s auth block: %w", AuthTypeToken, err)
		}
		if tokenAuth.Token == "" {
			return fmt.Errorf("%s auth block has an empty Token", AuthTypeToken)
		}
		transportCfg.APIKey = tokenAuth.Token
	case AuthTypeBasic:
		var basic basicAuthConfig
		if err := json.Unmarshal(cfg.Auth, &basic); err != nil {
			return fmt.Errorf("invalid %s auth block: %w", AuthTypeBasic, err)
		}
		if basic.Username == "" {
			return fmt.Errorf("%s auth block has an empty Username", AuthTypeBasic)
		}
		if basic.Password == "" {
			return fmt.Errorf("%s auth block has an empty Password", AuthTypeBasic)
		}
		transportCfg.BasicAuth = url.UserPassword(basic.Username, basic.Password)
	default:
		return fmt.Errorf("unsupported auth Type %q: expected %q or %q", authType, AuthTypeToken, AuthTypeBasic)
	}
	return nil
}

// applyEnvCredentials reads GRAFANA_AUTH, which accepts a service account token,
// an API key, or "user:password" for basic auth.
func applyEnvCredentials(transportCfg *goapi.TransportConfig) error {
	auth := os.Getenv("GRAFANA_AUTH")
	if auth == "" {
		return fmt.Errorf("no credentials: set an auth block in the target Config or the GRAFANA_AUTH environment variable")
	}
	// Detect basic auth (user:password format) vs token.
	if strings.Contains(auth, ":") && !strings.HasPrefix(auth, "glsa_") && !strings.HasPrefix(auth, "eyJ") {
		transportCfg.BasicAuth = url.UserPassword(
			auth[:strings.Index(auth, ":")],
			auth[strings.Index(auth, ":")+1:],
		)
	} else {
		transportCfg.APIKey = auth
	}
	return nil
}
