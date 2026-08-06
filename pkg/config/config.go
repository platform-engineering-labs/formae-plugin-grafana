// Package config handles Grafana target configuration and client creation.
package config

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	goapi "github.com/grafana/grafana-openapi-client-go/client"
	"github.com/go-openapi/strfmt"
	httptransport "github.com/go-openapi/runtime/client"
)

// TargetConfig holds Grafana target settings from the forma file.
//
// The Url field receives the resolved value from the formae engine. When the
// PKL config uses a resolvable (e.g., lgtmStack.res.endpoints.at("lgtm:3000")),
// formae resolves it to a plain URL string before passing it to the plugin.
//
// When Username and Password are both non-empty the plugin uses them for basic
// auth directly, without consulting the GRAFANA_AUTH environment variable.
// Either field may originate from a formae-managed secret resolved live by the
// agent (no restart required).
//
// Deprecated: Endpoints and EndpointKey are superseded by collection resolvables
// (MappingResolvable.at()). Use url = stack.res.endpoints.at("key") instead.
// These fields will be removed in a future release.
type TargetConfig struct {
	Type        string            `json:"Type"`
	URL         string            `json:"Url,omitempty"`
	OrgID       *int64            `json:"OrgId,omitempty"`
	Username    string            `json:"Username,omitempty"`
	Password    string            `json:"Password,omitempty"`
	Endpoints   map[string]string `json:"Endpoints,omitempty"`   // Deprecated: use resolvable url instead
	EndpointKey string            `json:"EndpointKey,omitempty"` // Deprecated: use resolvable url instead
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
	return &cfg, nil
}

// NewClient creates a Grafana API client from target config and credentials.
//
// Credential resolution order:
//  1. Config-level basic auth: when both cfg.Username and cfg.Password are
//     non-empty, they are used directly (values arrive pre-resolved from the
//     formae engine — no GRAFANA_AUTH lookup is performed).
//  2. Environment variable fallback: GRAFANA_AUTH is consulted when config
//     credentials are absent. It accepts a service account token (glsa_…),
//     an API key (eyJ…), or "user:password" for basic auth.
//
// An error is returned only when neither config credentials nor GRAFANA_AUTH
// are available.
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

	if cfg.Username != "" && cfg.Password != "" {
		// Config-level basic auth takes priority (sourced from a resolved secret).
		transportCfg.BasicAuth = url.UserPassword(cfg.Username, cfg.Password)
	} else {
		// Fall back to the GRAFANA_AUTH environment variable.
		auth := os.Getenv("GRAFANA_AUTH")
		if auth == "" {
			return nil, fmt.Errorf("no credentials: set username/password in the target Config or the GRAFANA_AUTH environment variable")
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
	}

	if cfg.OrgID != nil {
		transportCfg.OrgID = *cfg.OrgID
	}

	httpClient := &http.Client{Timeout: 30 * time.Second}
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
