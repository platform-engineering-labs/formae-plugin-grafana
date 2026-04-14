package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	goapi "github.com/grafana/grafana-openapi-client-go/client"
	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"

	"github.com/platform-engineering-labs/formae-plugin-grafana/pkg/config"
	"github.com/platform-engineering-labs/formae-plugin-grafana/pkg/handler"
)

// Plugin implements the Formae ResourcePlugin interface for Grafana.
type Plugin struct {
	mu      sync.Mutex
	clients map[string]*goapi.GrafanaHTTPAPI
}

var _ plugin.ResourcePlugin = &Plugin{}

// getClient returns a cached Grafana API client for the given target config.
func (p *Plugin) getClient(targetConfig json.RawMessage) (*goapi.GrafanaHTTPAPI, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.clients == nil {
		p.clients = make(map[string]*goapi.GrafanaHTTPAPI)
	}

	key := string(targetConfig)
	if client, ok := p.clients[key]; ok {
		return client, nil
	}

	cfg, err := config.ParseTargetConfig(targetConfig)
	if err != nil {
		return nil, err
	}

	client, err := config.NewClient(cfg)
	if err != nil {
		return nil, err
	}

	p.clients[key] = client
	return client, nil
}

// RateLimit returns the rate limiting configuration.
// Grafana Cloud has rate limits; self-hosted may not, but we set a reasonable default.
func (p *Plugin) RateLimit() model.RateLimitConfig {
	return model.RateLimitConfig{
		Scope:                            model.RateLimitScopeNamespace,
		MaxRequestsPerSecondForNamespace: 10,
	}
}

// DiscoveryFilters returns filters to exclude resources from discovery.
func (p *Plugin) DiscoveryFilters() []model.MatchFilter {
	return nil
}

// LabelConfig returns the configuration for extracting human-readable labels.
func (p *Plugin) LabelConfig() model.LabelConfig {
	return model.LabelConfig{
		DefaultQuery: "$.title",
		ResourceOverrides: map[string]string{
			"Grafana::Core::DataSource":             "$.name",
			"Grafana::Core::Team":                   "$.name",
			"Grafana::Core::ServiceAccount":         "$.name",
			"Grafana::Alerting::AlertRule":           "$.title",
			"Grafana::Alerting::ContactPoint":        "$.name",
			"Grafana::Alerting::NotificationPolicy":  "$.receiver",
			"Grafana::Alerting::MuteTiming":          "$.name",
			"Grafana::Alerting::MessageTemplate":     "$.name",
		},
	}
}

// Create provisions a new Grafana resource.
func (p *Plugin) Create(ctx context.Context, req *resource.CreateRequest) (*resource.CreateResult, error) {
	h, err := handler.Get(req.ResourceType)
	if err != nil {
		return &resource.CreateResult{
			ProgressResult: handler.FailResult(resource.OperationCreate, resource.OperationErrorCodeInvalidRequest, err.Error()),
		}, nil
	}

	client, err := p.getClient(req.TargetConfig)
	if err != nil {
		return &resource.CreateResult{
			ProgressResult: handler.FailResult(resource.OperationCreate, resource.OperationErrorCodeNetworkFailure, fmt.Sprintf("failed to create Grafana client: %v", err)),
		}, nil
	}

	result, err := h.Create(ctx, client, req.Properties)
	if err != nil {
		return &resource.CreateResult{
			ProgressResult: handler.FailResult(resource.OperationCreate, resource.OperationErrorCodeInternalFailure, err.Error()),
		}, nil
	}
	return &resource.CreateResult{ProgressResult: result}, nil
}

// Read retrieves the current state of a Grafana resource.
func (p *Plugin) Read(ctx context.Context, req *resource.ReadRequest) (*resource.ReadResult, error) {
	h, err := handler.Get(req.ResourceType)
	if err != nil {
		return &resource.ReadResult{
			ResourceType: req.ResourceType,
			ErrorCode:    resource.OperationErrorCodeInvalidRequest,
		}, nil
	}

	client, err := p.getClient(req.TargetConfig)
	if err != nil {
		return &resource.ReadResult{
			ResourceType: req.ResourceType,
			ErrorCode:    resource.OperationErrorCodeNetworkFailure,
		}, nil
	}

	return h.Read(ctx, client, req.NativeID)
}

// Update modifies an existing Grafana resource.
func (p *Plugin) Update(ctx context.Context, req *resource.UpdateRequest) (*resource.UpdateResult, error) {
	h, err := handler.Get(req.ResourceType)
	if err != nil {
		return &resource.UpdateResult{
			ProgressResult: handler.FailResult(resource.OperationUpdate, resource.OperationErrorCodeInvalidRequest, err.Error()),
		}, nil
	}

	client, err := p.getClient(req.TargetConfig)
	if err != nil {
		return &resource.UpdateResult{
			ProgressResult: handler.FailResult(resource.OperationUpdate, resource.OperationErrorCodeNetworkFailure, fmt.Sprintf("failed to create Grafana client: %v", err)),
		}, nil
	}

	result, err := h.Update(ctx, client, req.NativeID, req.PriorProperties, req.DesiredProperties)
	if err != nil {
		return &resource.UpdateResult{
			ProgressResult: handler.FailResult(resource.OperationUpdate, resource.OperationErrorCodeInternalFailure, err.Error()),
		}, nil
	}
	return &resource.UpdateResult{ProgressResult: result}, nil
}

// Delete removes a Grafana resource.
func (p *Plugin) Delete(ctx context.Context, req *resource.DeleteRequest) (*resource.DeleteResult, error) {
	h, err := handler.Get(req.ResourceType)
	if err != nil {
		return &resource.DeleteResult{
			ProgressResult: handler.FailResult(resource.OperationDelete, resource.OperationErrorCodeInvalidRequest, err.Error()),
		}, nil
	}

	client, err := p.getClient(req.TargetConfig)
	if err != nil {
		return &resource.DeleteResult{
			ProgressResult: handler.FailResult(resource.OperationDelete, resource.OperationErrorCodeNetworkFailure, fmt.Sprintf("failed to create Grafana client: %v", err)),
		}, nil
	}

	result, err := h.Delete(ctx, client, req.NativeID)
	if err != nil {
		return &resource.DeleteResult{
			ProgressResult: handler.FailResult(resource.OperationDelete, resource.OperationErrorCodeInternalFailure, err.Error()),
		}, nil
	}
	return &resource.DeleteResult{ProgressResult: result}, nil
}

// Status checks the progress of an async operation.
// All Grafana API operations are synchronous, so this always returns Success.
func (p *Plugin) Status(ctx context.Context, req *resource.StatusRequest) (*resource.StatusResult, error) {
	return &resource.StatusResult{
		ProgressResult: &resource.ProgressResult{
			Operation:       resource.OperationCheckStatus,
			OperationStatus: resource.OperationStatusSuccess,
			RequestID:       req.RequestID,
		},
	}, nil
}

// List returns all resource identifiers of a given type for discovery.
func (p *Plugin) List(ctx context.Context, req *resource.ListRequest) (*resource.ListResult, error) {
	h, err := handler.Get(req.ResourceType)
	if err != nil {
		return &resource.ListResult{NativeIDs: []string{}}, nil
	}

	client, err := p.getClient(req.TargetConfig)
	if err != nil {
		return &resource.ListResult{NativeIDs: []string{}}, nil
	}

	return h.List(ctx, client, req.PageSize, req.PageToken)
}
