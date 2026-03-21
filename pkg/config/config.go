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
// Contains only the deployment location, NOT credentials.
//
// Supports two modes:
//   - Direct: Url is set directly (e.g., "http://localhost:3000")
//   - Resolved: Endpoints mapping + EndpointKey from a compose stack resolvable
type TargetConfig struct {
	Type        string            `json:"Type"`
	URL         string            `json:"Url,omitempty"`
	OrgID       *int64            `json:"OrgId,omitempty"`
	Endpoints   map[string]string `json:"Endpoints,omitempty"`
	EndpointKey string            `json:"EndpointKey,omitempty"`
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

// NewClient creates a Grafana API client from target config and environment credentials.
// Authentication is read from the GRAFANA_AUTH environment variable:
//   - Service account token (glsa_...)
//   - API key (eyJr...)
//   - Basic auth (user:password)
func NewClient(cfg *TargetConfig) (*goapi.GrafanaHTTPAPI, error) {
	auth := os.Getenv("GRAFANA_AUTH")
	if auth == "" {
		return nil, fmt.Errorf("GRAFANA_AUTH environment variable must be set")
	}

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

	// Detect basic auth (user:password format) vs token
	if strings.Contains(auth, ":") && !strings.HasPrefix(auth, "glsa_") && !strings.HasPrefix(auth, "eyJ") {
		transportCfg.BasicAuth = url.UserPassword(
			auth[:strings.Index(auth, ":")],
			auth[strings.Index(auth, ":")+1:],
		)
	} else {
		transportCfg.APIKey = auth
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
